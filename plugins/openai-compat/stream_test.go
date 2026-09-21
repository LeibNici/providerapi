package main

import (
	"testing"

	"github.com/chenming/providerapi/internal/protocol"
)

func TestParseStreamChunkMultiToolAndContent(t *testing.T) {
	data := `{"choices":[{"delta":{"content":"hi","tool_calls":[{"index":0,"id":"call_a","type":"function","function":{"name":"read_file","arguments":""}},{"index":1,"id":"call_b","type":"function","function":{"name":"list_dir","arguments":""}}]}}]}`
	events, _, _, err := parseStreamChunk(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 3 {
		t.Fatalf("expected 3 events (content + 2 tools), got %d", len(events))
	}
	if events[0].Type != protocol.EventTextDelta {
		t.Fatalf("first event should be text delta, got %s", events[0].Type)
	}
	if events[1].Type != protocol.EventToolCallStart || events[1].ToolCallDelta.Name != "read_file" {
		t.Fatalf("tool 0: %+v", events[1])
	}
	if events[2].Type != protocol.EventToolCallStart || events[2].ToolCallDelta.Name != "list_dir" {
		t.Fatalf("tool 1: %+v", events[2])
	}
	if events[1].ToolCallDelta.Index != 0 || events[2].ToolCallDelta.Index != 1 {
		t.Fatalf("tool indices wrong: %d %d", events[1].ToolCallDelta.Index, events[2].ToolCallDelta.Index)
	}
}

func TestParseStreamChunkArgumentDeltasByIndex(t *testing.T) {
	data := `{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"path\""}},{"index":1,"function":{"arguments":"{\"dir\""}}]}}]}`
	events, _, _, err := parseStreamChunk(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 {
		t.Fatalf("expected 2 delta events, got %d", len(events))
	}
	if events[0].ToolCallDelta.Arguments != `{"path"` || events[0].ToolCallDelta.Index != 0 {
		t.Fatalf("delta 0: %+v", events[0])
	}
	if events[1].ToolCallDelta.Arguments != `{"dir"` || events[1].ToolCallDelta.Index != 1 {
		t.Fatalf("delta 1: %+v", events[1])
	}
}
