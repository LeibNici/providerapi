package protocol

import (
	"encoding/json"
	"testing"
)

func TestContentStringAndParts(t *testing.T) {
	var c Content
	if err := json.Unmarshal([]byte(`"hello"`), &c); err != nil {
		t.Fatal(err)
	}
	if c.String() != "hello" {
		t.Fatalf("got %q", c.String())
	}
	if err := json.Unmarshal([]byte(`[{"type":"text","text":"a"},{"type":"text","text":"b"}]`), &c); err != nil {
		t.Fatal(err)
	}
	if c.String() != "ab" {
		t.Fatalf("parts got %q", c.String())
	}
}

func TestParseReasoningLevel(t *testing.T) {
	l, ok := ParseReasoningLevel("high")
	if !ok || l != ReasoningHigh {
		t.Fatalf("%v %v", l, ok)
	}
	l, ok = ParseReasoningLevel("xhigh")
	if !ok || l != ReasoningMax {
		t.Fatalf("xhigh: %v %v", l, ok)
	}
	if _, ok := ParseReasoningLevel("nope"); ok {
		t.Fatal("expected reject")
	}
}

func TestMapProviderStatusUpstream500IsGateway502(t *testing.T) {
	pe := MapProviderStatus(500, "internal error", "internal", "")
	if pe.HTTPStatus != 502 {
		t.Fatalf("upstream 500 must map to gateway 502, got %d", pe.HTTPStatus)
	}
	if pe.ProviderStatus != 500 {
		t.Fatalf("ProviderStatus=%d want 500", pe.ProviderStatus)
	}
}
