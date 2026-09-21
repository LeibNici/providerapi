package redact

import (
	"encoding/json"
	"strings"
)

var keyHints = []string{
	"authorization",
	"api-key",
	"api_key",
	"apikey",
	"cookie",
	"token",
	"secret",
	"credential",
	"password",
	"x-api-key",
}

const Redacted = "[REDACTED]"

func HeaderName(name string) bool {
	n := strings.ToLower(name)
	n = strings.ReplaceAll(n, "_", "-")
	for _, h := range keyHints {
		if n == h || strings.Contains(n, h) {
			return true
		}
	}
	return false
}

func Headers(h map[string]string) map[string]string {
	if h == nil {
		return nil
	}
	out := make(map[string]string, len(h))
	for k, v := range h {
		if HeaderName(k) {
			out[k] = Redacted
			continue
		}
		out[k] = Value(v)
	}
	return out
}

func Value(s string) string {
	low := strings.ToLower(s)
	if strings.Contains(low, "bearer ") || strings.Contains(low, "sk-") || strings.Contains(low, "api_key") {
		return Redacted
	}
	return s
}

func JSON(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return raw
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return raw
	}
	redactValue(v)
	b, err := json.Marshal(v)
	if err != nil {
		return raw
	}
	return b
}

func Any(v any) any {
	b, err := json.Marshal(v)
	if err != nil {
		return v
	}
	var x any
	if err := json.Unmarshal(b, &x); err != nil {
		return v
	}
	redactValue(x)
	return x
}

func redactValue(v any) {
	switch t := v.(type) {
	case map[string]any:
		for k, child := range t {
			if HeaderName(k) {
				t[k] = Redacted
				continue
			}
			if s, ok := child.(string); ok {
				t[k] = Value(s)
				continue
			}
			redactValue(child)
		}
	case []any:
		for _, child := range t {
			redactValue(child)
		}
	}
}
