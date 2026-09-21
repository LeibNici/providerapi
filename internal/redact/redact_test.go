package redact

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRedactHeadersAndJSON(t *testing.T) {
	h := Headers(map[string]string{
		"Authorization": "Bearer sk-secret",
		"Content-Type":  "application/json",
	})
	if h["Authorization"] != Redacted {
		t.Fatalf("%v", h)
	}
	if h["Content-Type"] != "application/json" {
		t.Fatalf("%v", h)
	}
	raw := JSON(json.RawMessage(`{"api_key":"sk-or-v1-secret","model":"x"}`))
	if strings.Contains(string(raw), "sk-or-") {
		t.Fatalf("key leaked: %s", raw)
	}
}
