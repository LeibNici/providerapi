package main

import (
	"context"
	"flag"
	"os"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/chenming/providerapi/internal/protocol"
	sdkplugin "github.com/chenming/providerapi/sdk/plugin"
)

type Mock struct {
	instance string
}

func main() {
	socket := flag.String("socket", "", "unix socket path")
	flag.Parse()
	if *socket == "" {
		panic("--socket is required")
	}
	m := &Mock{}
	if err := sdkplugin.ListenAndServe(*socket, m); err != nil {
		os.Stderr.WriteString(err.Error() + "\n")
		os.Exit(1)
	}
}

func (m *Mock) Handshake(ctx context.Context, req protocol.HandshakeRequest) (*protocol.HandshakeResult, error) {
	m.instance = req.InstanceID
	return &protocol.HandshakeResult{Manifest: protocol.Manifest{
		ProtocolVersion: sdkplugin.ProtocolVersion,
		ID:              "mock",
		Version:         "0.1.0",
		Capabilities:    protocol.Capabilities{Stream: true, Tools: true, Reasoning: true},
	}}, nil
}

func (m *Mock) Health(ctx context.Context) error { return nil }

func (m *Mock) Models(ctx context.Context) ([]protocol.ModelInfo, error) {
	return []protocol.ModelInfo{
		{ID: "mock-echo"},
		{ID: "mock-error"},
		{ID: "mock-timeout"},
		{ID: "mock-tool"},
		{ID: "mock-unauthorized"},
		{ID: "mock-rate-limited"},
		{ID: "mock-unavailable"},
		{ID: "mock-bad-request"},
	}, nil
}

func (m *Mock) Complete(ctx context.Context, req *protocol.CompletionRequest) (*protocol.CompletionResponse, error) {
	if err := simulate(ctx, req); err != nil {
		return nil, err
	}
	if tool, ok := maybeToolCall(req); ok {
		return &protocol.CompletionResponse{
			ID:    req.RequestID,
			Model: req.Model,
			Message: protocol.Message{
				Role:      "assistant",
				Content:   protocol.TextContent(""),
				ToolCalls: []protocol.ToolCall{tool},
			},
			FinishReason: "tool_calls",
			Usage:        &protocol.Usage{InputTokens: 12, OutputTokens: 6, TotalTokens: 18},
		}, nil
	}
	if lastToolResult(req) {
		return &protocol.CompletionResponse{
			ID:           req.RequestID,
			Model:        req.Model,
			Message:      protocol.Message{Role: "assistant", Content: protocol.TextContent("Tool result received. Final answer.")},
			FinishReason: "stop",
			Usage:        &protocol.Usage{InputTokens: 20, OutputTokens: 8, TotalTokens: 28},
		}, nil
	}
	text := "Hello from mock plugin."
	return &protocol.CompletionResponse{
		ID:           req.RequestID,
		Model:        req.Model,
		Message:      protocol.Message{Role: "assistant", Content: protocol.TextContent(text)},
		FinishReason: "stop",
		Usage:        &protocol.Usage{InputTokens: 8, OutputTokens: utf8.RuneCountInString(text), TotalTokens: 8 + utf8.RuneCountInString(text)},
	}, nil
}

func (m *Mock) Stream(ctx context.Context, req *protocol.CompletionRequest) (<-chan protocol.StreamEvent, error) {
	ch := make(chan protocol.StreamEvent, 16)
	go func() {
		defer close(ch)
		if err := simulate(ctx, req); err != nil {
			if pe, ok := err.(*protocol.ProviderError); ok {
				ch <- protocol.StreamEvent{Type: protocol.EventError, Error: pe}
			} else {
				ch <- protocol.StreamEvent{Type: protocol.EventError, Error: protocol.AsProviderError(err)}
			}
			return
		}
		if tool, ok := maybeToolCall(req); ok {
			ch <- protocol.StreamEvent{Type: protocol.EventStreamStart}
			ch <- protocol.StreamEvent{Type: protocol.EventToolCallStart, ToolCallDelta: &protocol.ToolCallDelta{
				Index: 0, ID: tool.ID, Type: "function", Name: tool.Function.Name,
			}}
			ch <- protocol.StreamEvent{Type: protocol.EventToolCallDelta, ToolCallDelta: &protocol.ToolCallDelta{
				Index: 0, ID: tool.ID, Arguments: tool.Function.Arguments,
			}}
			ch <- protocol.StreamEvent{Type: protocol.EventToolCallEnd, ToolCallDelta: &protocol.ToolCallDelta{Index: 0, ID: tool.ID}}
			ch <- protocol.StreamEvent{Type: protocol.EventUsage, Usage: &protocol.Usage{InputTokens: 12, OutputTokens: 6}}
			ch <- protocol.StreamEvent{Type: protocol.EventStreamEnd, FinishReason: "tool_calls"}
			return
		}
		if lastToolResult(req) {
			streamText(ctx, ch, "Tool result received. Final answer.")
			return
		}
		streamText(ctx, ch, "Hello from mock plugin.")
	}()
	return ch, nil
}

func streamText(ctx context.Context, ch chan protocol.StreamEvent, text string) {
	ch <- protocol.StreamEvent{Type: protocol.EventStreamStart}
	for _, tok := range tokenize(text) {
		if ctx.Err() != nil {
			return
		}
		t := tok
		ch <- protocol.StreamEvent{Type: protocol.EventTextDelta, Text: &t}
		time.Sleep(15 * time.Millisecond)
	}
	ch <- protocol.StreamEvent{Type: protocol.EventUsage, Usage: &protocol.Usage{InputTokens: 8, OutputTokens: utf8.RuneCountInString(text)}}
	ch <- protocol.StreamEvent{Type: protocol.EventStreamEnd, FinishReason: "stop"}
}

func tokenize(s string) []string {
	parts := strings.Fields(s)
	out := make([]string, 0, len(parts))
	for i, p := range parts {
		if i < len(parts)-1 {
			out = append(out, p+" ")
		} else {
			out = append(out, p)
		}
	}
	if len(out) == 0 {
		return []string{s}
	}
	return out
}

func simulate(ctx context.Context, req *protocol.CompletionRequest) error {
	switch req.Model {
	case "mock-error":
		return protocol.MapProviderStatus(500, "simulated provider 500", "internal", "")
	case "mock-unauthorized":
		return protocol.MapProviderStatus(401, "unauthorized", "unauthorized", "")
	case "mock-rate-limited":
		return protocol.MapProviderStatus(429, "rate limited", "rate_limit", "")
	case "mock-unavailable":
		return protocol.MapProviderStatus(503, "unavailable", "unavailable", "")
	case "mock-bad-request":
		return protocol.MapProviderStatus(400, "bad request", "bad_request", "")
	case "mock-timeout":
		select {
		case <-ctx.Done():
			return protocol.Timeout("mock timeout")
		case <-time.After(30 * time.Second):
			return protocol.Timeout("mock timeout")
		}
	}
	return nil
}

func maybeToolCall(req *protocol.CompletionRequest) (protocol.ToolCall, bool) {
	if lastToolResult(req) {
		return protocol.ToolCall{}, false
	}
	if len(req.Tools) == 0 {
		return protocol.ToolCall{}, false
	}
	if req.Model != "mock-tool" && !lastUserContains(req, "use tool") {
		return protocol.ToolCall{}, false
	}
	name := "read_file"
	if req.Tools[0].Function != nil && req.Tools[0].Function.Name != "" {
		name = req.Tools[0].Function.Name
	}
	return protocol.ToolCall{
		ID:   "call_mock_1",
		Type: "function",
		Function: protocol.FunctionCall{
			Name:      name,
			Arguments: `{"path":"README.md"}`,
		},
	}, true
}

func lastUserContains(req *protocol.CompletionRequest, needle string) bool {
	for i := len(req.Messages) - 1; i >= 0; i-- {
		if req.Messages[i].Role == "user" {
			return strings.Contains(strings.ToLower(req.Messages[i].Content.String()), needle)
		}
	}
	return false
}

func lastToolResult(req *protocol.CompletionRequest) bool {
	if len(req.Messages) == 0 {
		return false
	}
	return req.Messages[len(req.Messages)-1].Role == "tool"
}
