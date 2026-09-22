package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/LeibNici/providerapi/internal/protocol"
	sdkplugin "github.com/LeibNici/providerapi/sdk/plugin"
)

type Compat struct {
	server *sdkplugin.Server
	cfg    InstanceConfig
	http   *http.Client
}

type InstanceConfig struct {
	BaseURL      string                     `json:"base_url"`
	ExtraHeaders map[string]string          `json:"extra_headers"`
	ReasoningMap map[string]json.RawMessage `json:"reasoning_map"`
}

func main() {
	// plugin.ListenAndServe creates Server internally; we need Audit from that server.
	socket := ""
	for i, a := range os.Args {
		if a == "--socket" && i+1 < len(os.Args) {
			socket = os.Args[i+1]
		}
		if strings.HasPrefix(a, "--socket=") {
			socket = strings.TrimPrefix(a, "--socket=")
		}
	}
	if socket == "" {
		fmt.Fprintln(os.Stderr, "--socket is required")
		os.Exit(1)
	}
	c := &Compat{http: newHTTPClient()}
	if err := listen(socket, c); err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		os.Exit(1)
	}
}

func listen(socket string, c *Compat) error {
	h := c
	return sdkplugin.ListenAndServeWithHook(socket, h, func(s *sdkplugin.Server) { c.server = s })
}

func (c *Compat) Handshake(ctx context.Context, req protocol.HandshakeRequest) (*protocol.HandshakeResult, error) {
	var cfg InstanceConfig
	if len(req.Config) > 0 {
		if err := json.Unmarshal(req.Config, &cfg); err != nil {
			return nil, fmt.Errorf("invalid plugin config: %w", err)
		}
	}
	if cfg.BaseURL == "" {
		return nil, fmt.Errorf("config.base_url is required")
	}
	if cfg.ReasoningMap == nil {
		cfg.ReasoningMap = map[string]json.RawMessage{}
	}
	c.cfg = cfg
	return &protocol.HandshakeResult{Manifest: protocol.Manifest{
		ProtocolVersion: sdkplugin.ProtocolVersion,
		ID:              "openai-compat",
		Version:         "0.1.0",
		Capabilities:    protocol.Capabilities{Stream: true, Tools: true, Reasoning: true},
	}}, nil
}

func (c *Compat) Health(ctx context.Context) error { return nil }

func (c *Compat) Models(ctx context.Context) ([]protocol.ModelInfo, error) {
	return []protocol.ModelInfo{}, nil
}

func (c *Compat) Complete(ctx context.Context, req *protocol.CompletionRequest) (*protocol.CompletionResponse, error) {
	body, err := c.buildBody(req, false)
	if err != nil {
		return nil, err
	}
	httpReq, url, headers, err := c.newRequest(ctx, body)
	if err != nil {
		return nil, err
	}
	c.audit().ProviderRequest(req.RequestID, http.MethodPost, url, headers, body)
	resp, err := c.http.Do(httpReq)
	if err != nil {
		pe := mapTransport(err)
		c.audit().ProviderStreamEvent(req.RequestID, "provider_error", map[string]any{"message": pe.Message})
		return nil, pe
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	respHeaders := headerMap(resp.Header)
	c.audit().ProviderResponse(req.RequestID, http.MethodPost, url, resp.StatusCode, respHeaders, json.RawMessage(trimJSON(raw)))
	if resp.StatusCode >= 400 {
		return nil, parseUpstreamError(resp.StatusCode, raw, resp.Header.Get("x-request-id"))
	}
	return parseCompletion(req, raw)
}

func (c *Compat) Stream(ctx context.Context, req *protocol.CompletionRequest) (<-chan protocol.StreamEvent, error) {
	body, err := c.buildBody(req, true)
	if err != nil {
		return nil, err
	}
	httpReq, url, headers, err := c.newRequest(ctx, body)
	if err != nil {
		return nil, err
	}
	c.audit().ProviderRequest(req.RequestID, http.MethodPost, url, headers, body)
	resp, err := c.http.Do(httpReq)
	if err != nil {
		pe := mapTransport(err)
		c.audit().ProviderStreamEvent(req.RequestID, "provider_error", map[string]any{"message": pe.Message})
		return nil, pe
	}
	if resp.StatusCode >= 400 {
		raw, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		c.audit().ProviderResponse(req.RequestID, http.MethodPost, url, resp.StatusCode, headerMap(resp.Header), json.RawMessage(trimJSON(raw)))
		return nil, parseUpstreamError(resp.StatusCode, raw, resp.Header.Get("x-request-id"))
	}
	c.audit().ProviderResponse(req.RequestID, http.MethodPost, url, resp.StatusCode, headerMap(resp.Header), nil)

	ch := make(chan protocol.StreamEvent, 32)
	go func() {
		defer close(ch)
		defer resp.Body.Close()
		c.audit().ProviderStreamEvent(req.RequestID, "provider_stream_started", map[string]any{"status": resp.StatusCode})
		ch <- protocol.StreamEvent{Type: protocol.EventStreamStart}
		first := true
		finish := ""
		var usage *protocol.Usage
		sc := bufio.NewScanner(resp.Body)
		sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
		for sc.Scan() {
			if ctx.Err() != nil {
				return
			}
			line := sc.Text()
			if !strings.HasPrefix(line, "data:") {
				continue
			}
			data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			if data == "" {
				continue
			}
			if data == "[DONE]" {
				break
			}
			events, f, u, err := parseStreamChunk(data)
			if err != nil {
				ch <- protocol.StreamEvent{Type: protocol.EventError, Error: protocol.NewProviderError(502, "malformed_sse", err.Error())}
				return
			}
			for _, ev := range events {
				if first && ev.Type == protocol.EventTextDelta {
					c.audit().ProviderStreamEvent(req.RequestID, "provider_first_event", map[string]any{})
					first = false
				}
				ch <- ev
			}
			if f != "" {
				finish = f
			}
			if u != nil {
				usage = u
			}
		}
		if err := sc.Err(); err != nil && ctx.Err() == nil {
			ch <- protocol.StreamEvent{Type: protocol.EventError, Error: protocol.NewProviderError(502, "malformed_sse", err.Error())}
			return
		}
		if usage != nil {
			ch <- protocol.StreamEvent{Type: protocol.EventUsage, Usage: usage}
		}
		if finish == "" {
			finish = "stop"
		}
		c.audit().ProviderStreamEvent(req.RequestID, "provider_stream_finished", map[string]any{"finish_reason": finish})
		ch <- protocol.StreamEvent{Type: protocol.EventStreamEnd, FinishReason: finish, Usage: usage}
	}()
	return ch, nil
}

func (c *Compat) audit() *sdkplugin.AuditEmitter {
	if c.server != nil {
		return c.server.Audit()
	}
	return &sdkplugin.AuditEmitter{}
}

func (c *Compat) buildBody(req *protocol.CompletionRequest, stream bool) (json.RawMessage, error) {
	msg := make([]any, 0, len(req.Messages))
	for _, m := range req.Messages {
		item := map[string]any{"role": m.Role, "content": m.Content}
		if m.Name != "" {
			item["name"] = m.Name
		}
		if m.ToolCallID != "" {
			item["tool_call_id"] = m.ToolCallID
		}
		if len(m.ToolCalls) > 0 {
			item["tool_calls"] = m.ToolCalls
		}
		msg = append(msg, item)
	}
	body := map[string]any{
		"model":    req.Model,
		"messages": msg,
		"stream":   stream,
	}
	if stream {
		body["stream_options"] = map[string]any{"include_usage": true}
	}
	if req.Temperature != nil {
		body["temperature"] = *req.Temperature
	}
	if req.MaxTokens != nil {
		body["max_tokens"] = *req.MaxTokens
	}
	if len(req.Tools) > 0 {
		tools := make([]any, 0, len(req.Tools))
		for _, t := range req.Tools {
			tools = append(tools, t.UpstreamObject())
		}
		body["tools"] = tools
	}
	if len(req.ToolChoice) > 0 {
		var tc any
		_ = json.Unmarshal(req.ToolChoice, &tc)
		body["tool_choice"] = tc
	}

	level := string(req.Reasoning.Level)
	if level == "" {
		level = string(protocol.ReasoningDefault)
	}
	patch, ok := c.cfg.ReasoningMap[level]
	if !ok && level != string(protocol.ReasoningDefault) {
		return nil, protocol.NewProviderError(400, "reasoning_unsupported",
			fmt.Sprintf("reasoning level %q is not mapped for this provider instance", level))
	}
	if ok && len(patch) > 0 && string(patch) != "null" && string(patch) != "{}" {
		var src map[string]any
		if err := json.Unmarshal(patch, &src); err != nil {
			return nil, protocol.InvalidRequest("invalid reasoning_map entry")
		}
		deepMerge(body, src, map[string]bool{"model": true, "messages": true, "tools": true, "stream": true})
	}
	return json.Marshal(body)
}

func (c *Compat) newRequest(ctx context.Context, body json.RawMessage) (*http.Request, string, map[string]string, error) {
	url := strings.TrimRight(c.cfg.BaseURL, "/") + "/chat/completions"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, "", nil, err
	}
	headers := map[string]string{"Content-Type": "application/json"}
	if key := bearerToken(); key != "" {
		headers["Authorization"] = "Bearer " + key
	}
	for k, v := range c.cfg.ExtraHeaders {
		headers[k] = v
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	return req, url, headers, nil
}

func parseCompletion(req *protocol.CompletionRequest, raw []byte) (*protocol.CompletionResponse, error) {
	var env struct {
		ID      string `json:"id"`
		Model   string `json:"model"`
		Choices []struct {
			FinishReason string `json:"finish_reason"`
			Message      struct {
				Role      string              `json:"role"`
				Content   protocol.Content    `json:"content"`
				ToolCalls []protocol.ToolCall `json:"tool_calls"`
			} `json:"message"`
		} `json:"choices"`
		Usage *struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, protocol.NewProviderError(502, "malformed_response", err.Error())
	}
	if len(env.Choices) == 0 {
		return nil, protocol.NewProviderError(502, "malformed_response", "no choices")
	}
	ch := env.Choices[0]
	out := &protocol.CompletionResponse{
		ID:           env.ID,
		Model:        req.Model,
		Message:      protocol.Message{Role: ch.Message.Role, Content: ch.Message.Content, ToolCalls: ch.Message.ToolCalls},
		FinishReason: ch.FinishReason,
	}
	if env.Usage != nil {
		out.Usage = &protocol.Usage{
			InputTokens:  env.Usage.PromptTokens,
			OutputTokens: env.Usage.CompletionTokens,
			TotalTokens:  env.Usage.PromptTokens + env.Usage.CompletionTokens,
		}
	}
	return out, nil
}

func newHTTPClient() *http.Client {
	return &http.Client{
		Timeout: 10 * time.Minute,
		Transport: &http.Transport{
			ResponseHeaderTimeout: 30 * time.Second,
		},
	}
}

func bearerToken() string {
	if key := os.Getenv("PROVIDERAPI_CREDENTIAL"); key != "" {
		return key
	}
	return os.Getenv("OPENAI_COMPAT_API_KEY")
}

func parseStreamChunk(data string) ([]protocol.StreamEvent, string, *protocol.Usage, error) {
	var env struct {
		Error *struct {
			Message string `json:"message"`
			Type    string `json:"type"`
			Code    string `json:"code"`
		} `json:"error"`
		Choices []struct {
			FinishReason string `json:"finish_reason"`
			Delta        struct {
				Content   *string `json:"content"`
				ToolCalls []struct {
					Index    int    `json:"index"`
					ID       string `json:"id"`
					Type     string `json:"type"`
					Function struct {
						Name      string `json:"name"`
						Arguments string `json:"arguments"`
					} `json:"function"`
				} `json:"tool_calls"`
			} `json:"delta"`
		} `json:"choices"`
		Usage *struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal([]byte(data), &env); err != nil {
		return nil, "", nil, err
	}
	if env.Error != nil {
		return []protocol.StreamEvent{{Type: protocol.EventError, Error: protocol.NewProviderError(502, "provider_error", env.Error.Message)}}, "", nil, nil
	}
	var usage *protocol.Usage
	if env.Usage != nil {
		usage = &protocol.Usage{InputTokens: env.Usage.PromptTokens, OutputTokens: env.Usage.CompletionTokens}
	}
	if len(env.Choices) == 0 {
		return nil, "", usage, nil
	}
	ch := env.Choices[0]
	finish := ch.FinishReason
	var events []protocol.StreamEvent
	if ch.Delta.Content != nil && *ch.Delta.Content != "" {
		events = append(events, protocol.StreamEvent{Type: protocol.EventTextDelta, Text: ch.Delta.Content, FinishReason: finish})
	}
	for _, tc := range ch.Delta.ToolCalls {
		typ := protocol.EventToolCallDelta
		if tc.ID != "" || tc.Function.Name != "" {
			typ = protocol.EventToolCallStart
		}
		events = append(events, protocol.StreamEvent{
			Type: typ,
			ToolCallDelta: &protocol.ToolCallDelta{
				Index: tc.Index, ID: tc.ID, Type: tc.Type, Name: tc.Function.Name, Arguments: tc.Function.Arguments,
			},
			FinishReason: finish,
		})
	}
	return events, finish, usage, nil
}

func parseUpstreamError(status int, raw []byte, reqID string) *protocol.ProviderError {
	var env struct {
		Error struct {
			Message string `json:"message"`
			Code    any    `json:"code"`
			Type    string `json:"type"`
		} `json:"error"`
	}
	msg := strings.TrimSpace(string(raw))
	code := ""
	if json.Unmarshal(raw, &env) == nil && env.Error.Message != "" {
		msg = env.Error.Message
		code = fmt.Sprint(env.Error.Code)
	}
	pe := protocol.MapProviderStatus(status, msg, code, reqID)
	return pe
}

func mapTransport(err error) *protocol.ProviderError {
	if err == nil {
		return protocol.NewProviderError(502, "provider_error", "unknown transport error")
	}
	if os.IsTimeout(err) || strings.Contains(strings.ToLower(err.Error()), "timeout") {
		return protocol.Timeout(err.Error())
	}
	if strings.Contains(strings.ToLower(err.Error()), "connection reset") {
		return protocol.NewProviderError(502, "connection_reset", err.Error())
	}
	return protocol.NewProviderError(502, "provider_error", err.Error())
}

func deepMerge(dst, src map[string]any, protected map[string]bool) {
	for k, v := range src {
		if protected[k] {
			continue
		}
		if existing, ok := dst[k].(map[string]any); ok {
			if nested, ok := v.(map[string]any); ok {
				deepMerge(existing, nested, nil)
				continue
			}
		}
		dst[k] = v
	}
}

func headerMap(h http.Header) map[string]string {
	out := map[string]string{}
	for k, vs := range h {
		if len(vs) > 0 {
			out[k] = vs[0]
		}
	}
	return out
}

func trimJSON(raw []byte) []byte {
	s := bytes.TrimSpace(raw)
	if len(s) == 0 {
		return []byte(`{}`)
	}
	if s[0] == '{' || s[0] == '[' {
		return s
	}
	b, _ := json.Marshal(string(s))
	return b
}
