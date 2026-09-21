package protocol

import (
	"encoding/json"
	"testing"
)

func TestToolUnmarshalJSONPreservesRawFromCanonical(t *testing.T) {
	input := `{"type":"function","function":{"name":"read_file"},"raw":{"type":"function","function":{"name":"read_file","strict":true}}}`
	var tool Tool
	if err := json.Unmarshal([]byte(input), &tool); err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err := json.Unmarshal(tool.Raw, &raw); err != nil {
		t.Fatal(err)
	}
	fn, _ := raw["function"].(map[string]any)
	if fn["strict"] != true {
		t.Fatalf("expected strict in raw, got %v", raw)
	}
}

func TestToolUnmarshalJSONStoresEntireInputWhenNoRaw(t *testing.T) {
	input := `{"type":"function","function":{"name":"read_file","strict":true}}`
	var tool Tool
	if err := json.Unmarshal([]byte(input), &tool); err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err := json.Unmarshal(tool.Raw, &raw); err != nil {
		t.Fatal(err)
	}
	fn, _ := raw["function"].(map[string]any)
	if fn["strict"] != true {
		t.Fatalf("expected strict preserved in raw, got %v", raw)
	}
}

func TestToolUpstreamObjectNestedOverlay(t *testing.T) {
	raw := json.RawMessage(`{"type":"function","function":{"name":"old","strict":true,"parameters":{"type":"object","additionalProperties":false}}}`)
	tool := Tool{
		Type: "function",
		Raw:  raw,
		Function: &FunctionTool{
			Name:        "read_file",
			Description: "Read a file",
			Parameters:  json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}}}`),
		},
	}
	out := tool.UpstreamObject()
	fn, ok := out["function"].(map[string]any)
	if !ok {
		t.Fatalf("function missing: %v", out)
	}
	if fn["strict"] != true {
		t.Fatalf("strict lost: %v", fn)
	}
	if fn["name"] != "read_file" {
		t.Fatalf("name not overlaid: %v", fn)
	}
	params, _ := fn["parameters"].(map[string]any)
	if params["additionalProperties"] != false {
		t.Fatalf("unknown nested field lost: %v", params)
	}
	props, _ := params["properties"].(map[string]any)
	if props["path"] == nil {
		t.Fatalf("canonical parameters not merged: %v", params)
	}
}

func TestToolRoundTripRPC(t *testing.T) {
	orig := `{"type":"function","function":{"name":"read_file","strict":true}}`
	var tool Tool
	if err := json.Unmarshal([]byte(orig), &tool); err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(tool)
	if err != nil {
		t.Fatal(err)
	}
	var again Tool
	if err := json.Unmarshal(b, &again); err != nil {
		t.Fatal(err)
	}
	out := again.UpstreamObject()
	fn, _ := out["function"].(map[string]any)
	if fn["strict"] != true {
		t.Fatalf("RPC round-trip lost strict: %v", out)
	}
}
