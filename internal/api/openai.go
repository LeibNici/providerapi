package api

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/LeibNici/providerapi/internal/config"
	"github.com/LeibNici/providerapi/internal/idgen"
	"github.com/LeibNici/providerapi/internal/model"
	"github.com/LeibNici/providerapi/internal/observability"
	"github.com/LeibNici/providerapi/internal/plugin"
	"github.com/LeibNici/providerapi/internal/protocol"
	"github.com/LeibNici/providerapi/internal/trace"
	"github.com/go-chi/chi/v5"
)

type Public struct {
	Cfg     *config.Config
	Plugins *plugin.Manager
	Trace   *trace.Service
	Metrics *observability.Metrics
	Log     *slog.Logger
}

func (p *Public) Routes() http.Handler {
	r := chi.NewRouter()
	r.Get("/health", p.health)
	r.Get("/v1/models", p.models)
	r.Post("/v1/chat/completions", p.chatCompletions)
	return r
}

func (p *Public) health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (p *Public) models(w http.ResponseWriter, r *http.Request) {
	aliases := model.ListAliases(p.Cfg)
	writeJSON(w, http.StatusOK, map[string]any{
		"object": "list",
		"data":   aliases,
	})
}

type streamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

func (p *Public) chatCompletions(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	reqID := idgen.RequestID()
	clientReqID := r.Header.Get("X-Request-ID")
	w.Header().Set("X-Request-ID", reqID)

	raw, err := io.ReadAll(r.Body)
	if err != nil {
		p.writeError(w, reqID, protocol.InvalidRequest("failed to read body"), false)
		return
	}

	sess := p.Trace.Start(reqID, clientReqID)
	sess.ClientRaw(json.RawMessage(raw))

	in, extra, err := parseChatRequest(raw)
	if err != nil {
		pe := protocol.InvalidRequest(err.Error())
		sess.Finish(pe.HTTPStatus, "error", "", 0, 0, pe.Type, pe.Code, pe.Message)
		p.writeError(w, reqID, pe, false)
		return
	}
	if in.Model == "" {
		pe := protocol.InvalidRequest("model is required")
		sess.Finish(pe.HTTPStatus, "error", "", 0, 0, pe.Type, pe.Code, pe.Message)
		p.writeError(w, reqID, pe, false)
		return
	}

	reqReasoning := protocol.ReasoningDefault
	if in.ReasoningEffort != "" {
		lvl, ok := protocol.ParseReasoningLevel(in.ReasoningEffort)
		if !ok {
			pe := protocol.InvalidRequest("invalid reasoning_effort")
			sess.Finish(pe.HTTPStatus, "error", "", 0, 0, pe.Type, pe.Code, pe.Message)
			p.writeError(w, reqID, pe, false)
			return
		}
		reqReasoning = lvl
	}
	// When both are set, reasoning.effort wins over reasoning_effort.
	if len(in.Reasoning) > 0 && string(in.Reasoning) != "null" {
		var rs struct {
			Effort string `json:"effort"`
		}
		if err := json.Unmarshal(in.Reasoning, &rs); err == nil && rs.Effort != "" {
			lvl, ok := protocol.ParseReasoningLevel(rs.Effort)
			if !ok {
				pe := protocol.InvalidRequest("invalid reasoning.effort")
				sess.Finish(pe.HTTPStatus, "error", "", 0, 0, pe.Type, pe.Code, pe.Message)
				p.writeError(w, reqID, pe, false)
				return
			}
			reqReasoning = lvl
		}
	}

	resolved, err := model.Resolve(p.Cfg, in.Model, reqReasoning)
	if err != nil {
		pe := protocol.AsProviderError(err)
		sess.Finish(pe.HTTPStatus, "error", "", 0, 0, pe.Type, pe.Code, pe.Message)
		p.writeError(w, reqID, pe, false)
		return
	}

	inst, err := p.Plugins.Get(resolved.Provider)
	if err != nil {
		pe := protocol.NewProviderError(502, "plugin_unavailable", err.Error())
		sess.Finish(pe.HTTPStatus, "error", "", 0, 0, pe.Type, pe.Code, pe.Message)
		p.writeError(w, reqID, pe, false)
		return
	}
	sess.SetRouting(resolved.ClientModel, resolved.Provider, resolved.UpstreamModel, string(resolved.Reasoning), inst.Manifest.ID, inst.Manifest.Version)

	canon := &protocol.CompletionRequest{
		RequestID:   reqID,
		Model:       resolved.UpstreamModel,
		Messages:    in.Messages,
		Tools:       in.Tools,
		ToolChoice:  in.ToolChoice,
		Stream:      in.Stream,
		Temperature: in.Temperature,
		MaxTokens:   in.MaxTokens,
		Reasoning:   protocol.ReasoningConfig{Level: resolved.Reasoning},
		Extra:       extra,
	}
	sess.Normalized(canon)
	sess.PluginRequest(canon)

	inst.SetTraceHandler(reqID, func(raw json.RawMessage) { sess.ProviderAudit(raw) })
	defer inst.SetTraceHandler(reqID, nil)

	log := p.Log.With(
		slog.String("request_id", reqID),
		slog.String("provider", resolved.Provider),
		slog.String("model", resolved.ClientModel),
	)

	if in.Stream {
		p.handleStream(w, r, start, reqID, resolved, inst, canon, sess, in.StreamOptions, log)
		return
	}

	resp, err := inst.Complete(r.Context(), canon)
	if err != nil {
		pe := protocol.AsProviderError(err)
		p.Metrics.ProviderErrors.WithLabelValues(resolved.Provider, pe.Code).Inc()
		p.Metrics.RequestsTotal.WithLabelValues(resolved.Provider, resolved.ClientModel, "error").Inc()
		p.Metrics.RequestDuration.WithLabelValues(resolved.Provider, resolved.ClientModel).Observe(time.Since(start).Seconds())
		sess.Finish(statusOr(pe, 502), "error", "", 0, 0, pe.Type, pe.Code, pe.Message)
		log.Error("request failed", "error", pe.Message)
		p.writeError(w, reqID, pe, false)
		return
	}
	out := toOpenAIResponse(reqID, resolved.ClientModel, resp)
	sess.ClientResponse(out)
	inTok, outTok := 0, 0
	if resp.Usage != nil {
		inTok, outTok = resp.Usage.InputTokens, resp.Usage.OutputTokens
		p.Metrics.InputTokens.WithLabelValues(resolved.Provider, resolved.ClientModel).Add(float64(inTok))
		p.Metrics.OutputTokens.WithLabelValues(resolved.Provider, resolved.ClientModel).Add(float64(outTok))
	}
	p.Metrics.RequestsTotal.WithLabelValues(resolved.Provider, resolved.ClientModel, "ok").Inc()
	p.Metrics.RequestDuration.WithLabelValues(resolved.Provider, resolved.ClientModel).Observe(time.Since(start).Seconds())
	sess.Finish(200, "ok", resp.FinishReason, inTok, outTok, "", "", "")
	log.Info("request completed")
	writeJSON(w, http.StatusOK, out)
}

func (p *Public) handleStream(w http.ResponseWriter, r *http.Request, start time.Time, reqID string, resolved *model.Resolved, inst *plugin.Instance, canon *protocol.CompletionRequest, sess *trace.Session, opts *streamOptions, log *slog.Logger) {
	p.Metrics.ActiveStreams.Inc()
	defer p.Metrics.ActiveStreams.Dec()

	ch, handle, err := inst.Stream(r.Context(), canon)
	if err != nil {
		pe := protocol.AsProviderError(err)
		p.Metrics.ProviderErrors.WithLabelValues(resolved.Provider, pe.Code).Inc()
		p.Metrics.RequestsTotal.WithLabelValues(resolved.Provider, resolved.ClientModel, "error").Inc()
		sess.Finish(statusOr(pe, 502), "error", "", 0, 0, pe.Type, pe.Code, pe.Message)
		p.writeError(w, reqID, pe, false)
		return
	}
	defer handle.Cancel()

	go func() {
		<-r.Context().Done()
		handle.Cancel()
	}()

	flusher, ok := w.(http.Flusher)
	if !ok {
		pe := protocol.NewProviderError(500, "internal", "streaming not supported")
		p.writeError(w, reqID, pe, false)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()
	sess.StreamMeta("stream_started")

	includeUsage := opts != nil && opts.IncludeUsage
	created := time.Now().Unix()
	first := true
	var usage *protocol.Usage
	finish := ""
	inTok, outTok := 0, 0
	streamErr := false

	writeChunk := func(obj any) {
		b, _ := json.Marshal(obj)
		sess.SSE(json.RawMessage(b))
		_, _ = w.Write([]byte("data: "))
		_, _ = w.Write(b)
		_, _ = w.Write([]byte("\n\n"))
		flusher.Flush()
	}

	for ev := range ch {
		switch ev.Type {
		case protocol.EventStreamStart:
			writeChunk(chunk(reqID, resolved.ClientModel, created, map[string]any{"role": "assistant", "content": ""}, nil))
		case protocol.EventTextDelta:
			if ev.Text != nil {
				if first {
					sess.MarkTTFT(start)
					sess.StreamMeta("first_chunk")
					p.Metrics.TTFT.WithLabelValues(resolved.Provider, resolved.ClientModel).Observe(time.Since(start).Seconds())
					first = false
				}
				writeChunk(chunk(reqID, resolved.ClientModel, created, map[string]any{"content": *ev.Text}, nil))
			}
		case protocol.EventToolCallStart, protocol.EventToolCallDelta:
			if first {
				sess.MarkTTFT(start)
				sess.StreamMeta("first_chunk")
				first = false
			}
			sess.StreamMeta("tool_call")
			if ev.ToolCallDelta != nil {
				writeChunk(chunk(reqID, resolved.ClientModel, created, map[string]any{
					"tool_calls": []any{toolCallDeltaJSON(ev.ToolCallDelta)},
				}, nil))
			}
		case protocol.EventToolCallEnd:
			sess.StreamMeta("tool_call")
		case protocol.EventUsage:
			usage = ev.Usage
		case protocol.EventError:
			streamErr = true
			if ev.Error != nil {
				writeChunk(map[string]any{"error": openaiError(ev.Error)})
				sess.Finish(statusOr(ev.Error, 502), "error", "stream_error", inTok, outTok, ev.Error.Type, ev.Error.Code, ev.Error.Message)
				p.Metrics.ProviderErrors.WithLabelValues(resolved.Provider, ev.Error.Code).Inc()
			}
		case protocol.EventStreamEnd:
			if ev.FinishReason != "" {
				finish = ev.FinishReason
			}
			if ev.Usage != nil {
				usage = ev.Usage
			}
		}
		if ev.FinishReason != "" {
			finish = ev.FinishReason
		}
	}

	if r.Context().Err() != nil {
		sess.Finish(499, "canceled", "client_disconnect", inTok, outTok, "canceled", "canceled", "client disconnected")
		log.Info("client disconnected")
		return
	}

	if !streamErr {
		if finish == "" {
			finish = "stop"
		}
		writeChunk(finalChunk(reqID, resolved.ClientModel, created, finish, nil))
		// OpenAI stream_options.include_usage: a trailing chunk with empty
		// choices and a usage object, immediately before data: [DONE].
		if includeUsage && usage != nil {
			writeChunk(usageChunk(reqID, resolved.ClientModel, created, openaiUsage(usage)))
			inTok, outTok = usage.InputTokens, usage.OutputTokens
		}
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
		flusher.Flush()
		if usage != nil {
			inTok, outTok = usage.InputTokens, usage.OutputTokens
			p.Metrics.InputTokens.WithLabelValues(resolved.Provider, resolved.ClientModel).Add(float64(inTok))
			p.Metrics.OutputTokens.WithLabelValues(resolved.Provider, resolved.ClientModel).Add(float64(outTok))
		}
		sess.StreamMeta("stream_finished")
		sess.Finish(200, "ok", finish, inTok, outTok, "", "", "")
		p.Metrics.RequestsTotal.WithLabelValues(resolved.Provider, resolved.ClientModel, "ok").Inc()
		p.Metrics.RequestDuration.WithLabelValues(resolved.Provider, resolved.ClientModel).Observe(time.Since(start).Seconds())
		log.Info("request completed")
	} else {
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
		flusher.Flush()
		p.Metrics.RequestsTotal.WithLabelValues(resolved.Provider, resolved.ClientModel, "error").Inc()
	}
}

func chunk(id, model string, created int64, delta any, finish *string) map[string]any {
	choice := map[string]any{"index": 0, "delta": delta, "finish_reason": finish}
	return map[string]any{
		"id":      id,
		"object":  "chat.completion.chunk",
		"created": created,
		"model":   model,
		"choices": []any{choice},
	}
}

func finalChunk(id, model string, created int64, finish string, usage any) map[string]any {
	out := chunk(id, model, created, map[string]any{}, &finish)
	if usage != nil {
		out["usage"] = usage
	}
	return out
}

func usageChunk(id, model string, created int64, usage any) map[string]any {
	return map[string]any{
		"id":      id,
		"object":  "chat.completion.chunk",
		"created": created,
		"model":   model,
		"choices": []any{},
		"usage":   usage,
	}
}

func toolCallDeltaJSON(d *protocol.ToolCallDelta) map[string]any {
	fn := map[string]any{}
	if d.Name != "" {
		fn["name"] = d.Name
	}
	if d.Arguments != "" {
		fn["arguments"] = d.Arguments
	}
	item := map[string]any{
		"index":    d.Index,
		"function": fn,
	}
	if d.ID != "" {
		item["id"] = d.ID
	}
	if d.Type != "" {
		item["type"] = d.Type
	} else {
		item["type"] = "function"
	}
	return item
}

type chatIn struct {
	Model           string             `json:"model"`
	Messages        []protocol.Message `json:"messages"`
	Tools           []protocol.Tool    `json:"tools"`
	ToolChoice      json.RawMessage    `json:"tool_choice"`
	Temperature     *float64           `json:"temperature"`
	MaxTokens       *int               `json:"max_tokens"`
	Stream          bool               `json:"stream"`
	StreamOptions   *streamOptions     `json:"stream_options"`
	ReasoningEffort string             `json:"reasoning_effort"`
	Reasoning       json.RawMessage    `json:"reasoning"`
}

func parseChatRequest(raw []byte) (*chatIn, map[string]json.RawMessage, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	var in chatIn
	if err := dec.Decode(&in); err != nil {
		return nil, nil, err
	}
	var all map[string]json.RawMessage
	if err := json.Unmarshal(raw, &all); err != nil {
		return nil, nil, err
	}
	known := []string{
		"model", "messages", "tools", "tool_choice", "temperature", "max_tokens",
		"stream", "stream_options", "reasoning_effort", "reasoning",
	}
	for _, k := range known {
		delete(all, k)
	}
	if len(all) == 0 {
		all = nil
	}
	return &in, all, nil
}

func toOpenAIResponse(id, clientModel string, resp *protocol.CompletionResponse) map[string]any {
	msg := map[string]any{
		"role":    resp.Message.Role,
		"content": resp.Message.Content,
	}
	if len(resp.Message.ToolCalls) > 0 {
		msg["tool_calls"] = resp.Message.ToolCalls
		msg["content"] = resp.Message.Content
	}
	out := map[string]any{
		"id":      id,
		"object":  "chat.completion",
		"created": time.Now().Unix(),
		"model":   clientModel,
		"choices": []any{
			map[string]any{
				"index":         0,
				"message":       msg,
				"finish_reason": resp.FinishReason,
			},
		},
	}
	if resp.Usage != nil {
		out["usage"] = openaiUsage(resp.Usage)
	}
	return out
}

func openaiUsage(u *protocol.Usage) map[string]any {
	return map[string]any{
		"prompt_tokens":     u.InputTokens,
		"completion_tokens": u.OutputTokens,
		"total_tokens":      u.InputTokens + u.OutputTokens,
	}
}

func (p *Public) writeError(w http.ResponseWriter, reqID string, pe *protocol.ProviderError, headersSent bool) {
	status := statusOr(pe, 502)
	body := map[string]any{"error": openaiError(pe)}
	if headersSent {
		b, _ := json.Marshal(body)
		_, _ = w.Write([]byte("data: "))
		_, _ = w.Write(b)
		_, _ = w.Write([]byte("\n\n"))
		return
	}
	w.Header().Set("X-Request-ID", reqID)
	writeJSON(w, status, body)
}

func openaiError(pe *protocol.ProviderError) map[string]any {
	if pe == nil {
		pe = protocol.NewProviderError(502, "provider_error", "unknown error")
	}
	return map[string]any{
		"message": pe.Message,
		"type":    pe.Type,
		"param":   pe.Param,
		"code":    pe.Code,
	}
}

func statusOr(pe *protocol.ProviderError, fallback int) int {
	if pe != nil && pe.HTTPStatus != 0 {
		return pe.HTTPStatus
	}
	return fallback
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
