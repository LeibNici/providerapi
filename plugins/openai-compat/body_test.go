package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/LeibNici/providerapi/internal/protocol"
)

func TestBuildBodyReasoningMerge(t *testing.T) {
	c := &Compat{cfg: InstanceConfig{
		ReasoningMap: map[string]json.RawMessage{
			"high": json.RawMessage(`{"reasoning":{"effort":"high"},"model":"hacked"}`),
		},
	}}
	req := &protocol.CompletionRequest{
		Model:     "anthropic/claude-sonnet-4.5",
		Messages:  []protocol.Message{{Role: "user", Content: protocol.TextContent("hi")}},
		Reasoning: protocol.ReasoningConfig{Level: protocol.ReasoningHigh},
	}
	raw, err := c.buildBody(req, false)
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatal(err)
	}
	if body["model"] != "anthropic/claude-sonnet-4.5" {
		t.Fatalf("model overwritten: %v", body["model"])
	}
	reasoning, _ := body["reasoning"].(map[string]any)
	if reasoning["effort"] != "high" {
		t.Fatalf("reasoning=%v", body["reasoning"])
	}
}

func TestBuildBodyToolStrictPreserved(t *testing.T) {
	c := &Compat{cfg: InstanceConfig{}}
	raw := json.RawMessage(`{"type":"function","function":{"name":"read_file","parameters":{"type":"object"},"strict":true}}`)
	var tool protocol.Tool
	if err := json.Unmarshal(raw, &tool); err != nil {
		t.Fatal(err)
	}
	req := &protocol.CompletionRequest{
		Model:    "m",
		Messages: []protocol.Message{{Role: "user", Content: protocol.TextContent("hi")}},
		Tools:    []protocol.Tool{tool},
	}
	bodyRaw, err := c.buildBody(req, false)
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	if err := json.Unmarshal(bodyRaw, &body); err != nil {
		t.Fatal(err)
	}
	tools, _ := body["tools"].([]any)
	fn, _ := tools[0].(map[string]any)["function"].(map[string]any)
	if fn["strict"] != true {
		t.Fatalf("strict not in upstream body: %v", fn)
	}
}

func TestBuildBodyMissingReasoningMapping(t *testing.T) {
	c := &Compat{cfg: InstanceConfig{ReasoningMap: map[string]json.RawMessage{}}}
	req := &protocol.CompletionRequest{
		Model:     "m",
		Messages:  []protocol.Message{{Role: "user", Content: protocol.TextContent("hi")}},
		Reasoning: protocol.ReasoningConfig{Level: protocol.ReasoningMax},
	}
	_, err := c.buildBody(req, false)
	if err == nil || !strings.Contains(err.Error(), "not mapped") {
		t.Fatalf("expected missing mapping error, got %v", err)
	}
}
