package protocol

import "encoding/json"

type ReasoningLevel string

const (
	ReasoningDefault ReasoningLevel = "default"
	ReasoningOff     ReasoningLevel = "off"
	ReasoningLow     ReasoningLevel = "low"
	ReasoningMedium  ReasoningLevel = "medium"
	ReasoningHigh    ReasoningLevel = "high"
	ReasoningMax     ReasoningLevel = "max"
)

type ReasoningConfig struct {
	Level ReasoningLevel `json:"level"`
}

type CompletionRequest struct {
	RequestID   string                     `json:"request_id"`
	Model       string                     `json:"model"`
	Messages    []Message                  `json:"messages"`
	Tools       []Tool                     `json:"tools,omitempty"`
	ToolChoice  json.RawMessage            `json:"tool_choice,omitempty"`
	Stream      bool                       `json:"stream"`
	Temperature *float64                   `json:"temperature,omitempty"`
	MaxTokens   *int                       `json:"max_tokens,omitempty"`
	Reasoning   ReasoningConfig            `json:"reasoning"`
	Extra       map[string]json.RawMessage `json:"extra,omitempty"`
}

type Message struct {
	Role       string          `json:"role"`
	Content    Content         `json:"content"`
	Name       string          `json:"name,omitempty"`
	ToolCallID string          `json:"tool_call_id,omitempty"`
	ToolCalls  []ToolCall      `json:"tool_calls,omitempty"`
	Extra      json.RawMessage `json:"extra,omitempty"`
}

type ToolCall struct {
	ID       string       `json:"id,omitempty"`
	Type     string       `json:"type,omitempty"`
	Index    *int         `json:"index,omitempty"`
	Function FunctionCall `json:"function"`
}

type FunctionCall struct {
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`
}

type Tool struct {
	Type     string          `json:"type"`
	Function *FunctionTool   `json:"function,omitempty"`
	Custom   *CustomTool     `json:"custom,omitempty"`
	Raw      json.RawMessage `json:"raw,omitempty"`
}

type FunctionTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

type CustomTool struct {
	Name        string          `json:"name,omitempty"`
	Description string          `json:"description,omitempty"`
	Input       json.RawMessage `json:"input,omitempty"`
}

type ModelInfo struct {
	ID      string `json:"id"`
	Object  string `json:"object,omitempty"`
	OwnedBy string `json:"owned_by,omitempty"`
}

type CompletionResponse struct {
	ID           string  `json:"id"`
	Model        string  `json:"model"`
	Message      Message `json:"message"`
	FinishReason string  `json:"finish_reason,omitempty"`
	Usage        *Usage  `json:"usage,omitempty"`
}

type Usage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
	TotalTokens  int `json:"total_tokens,omitempty"`
}

type EventType string

const (
	EventStreamStart   EventType = "stream_start"
	EventTextDelta     EventType = "text_delta"
	EventToolCallStart EventType = "tool_call_start"
	EventToolCallDelta EventType = "tool_call_delta"
	EventToolCallEnd   EventType = "tool_call_end"
	EventUsage         EventType = "usage"
	EventStreamEnd     EventType = "stream_end"
	EventError         EventType = "error"
)

type StreamEvent struct {
	Type          EventType      `json:"type"`
	Text          *string        `json:"text,omitempty"`
	ToolCallDelta *ToolCallDelta `json:"tool_call,omitempty"`
	Usage         *Usage         `json:"usage,omitempty"`
	FinishReason  string         `json:"finish_reason,omitempty"`
	Error         *ProviderError `json:"error,omitempty"`
}

type ToolCallDelta struct {
	Index        int    `json:"index"`
	ID           string `json:"id,omitempty"`
	Type         string `json:"type,omitempty"`
	Name         string `json:"name,omitempty"`
	Arguments    string `json:"arguments,omitempty"`
	FinishReason string `json:"finish_reason,omitempty"`
}

type HandshakeRequest struct {
	ProtocolVersion string          `json:"protocol_version"`
	InstanceID      string          `json:"instance_id"`
	Config          json.RawMessage `json:"config"`
}

type HandshakeResult struct {
	Manifest Manifest `json:"manifest"`
}

type Manifest struct {
	ProtocolVersion string       `json:"protocol_version"`
	ID              string       `json:"id"`
	Version         string       `json:"version"`
	Capabilities    Capabilities `json:"capabilities"`
}

type Capabilities struct {
	Stream    bool `json:"stream"`
	Tools     bool `json:"tools"`
	Reasoning bool `json:"reasoning"`
}

func Ptr[T any](v T) *T {
	return &v
}
