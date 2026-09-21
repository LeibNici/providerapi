package main

import (
	"bytes"
	"io"
	"os"
	"strings"
	"testing"
)

func TestPrintTraceProviderHTTP(t *testing.T) {
	data := map[string]any{
		"client": map[string]any{"model": "fake-stream"},
		"routing": map[string]any{
			"provider":        "fake",
			"upstream_model":  "fake-stream",
			"plugin":          "openai-compat",
			"plugin_version":  "0.1.0",
		},
		"normalized": map[string]any{"reasoning": "default"},
		"usage":      map[string]any{"ttft_ms": 1, "duration_ms": 10.0, "input_tokens": 1, "output_tokens": 2},
		"timeline": []any{
			map[string]any{
				"type": "provider_request",
				"payload": map[string]any{
					"type":   "provider_request",
					"method": "POST",
					"url":    "https://example.com/v1/chat/completions",
					"headers": map[string]any{
						"Authorization": "[REDACTED]",
						"Content-Type":  "application/json",
					},
				},
			},
			map[string]any{
				"type": "provider_response",
				"payload": map[string]any{
					"type":   "provider_response",
					"method": "POST",
					"status": 200,
					"headers": map[string]any{
						"X-Generation-Id": "gen-fake-stream-456",
					},
				},
			},
		},
	}
	out := captureStdout(t, func() { printTrace("req_test", data) })
	for _, want := range []string{
		"Provider Request",
		"method: POST",
		"Provider Response",
		"status: 200",
		"X-Generation-Id: gen-fake-stream-456",
		"Authorization: [REDACTED]",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in:\n%s", want, out)
		}
	}
}

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stdout
	os.Stdout = w
	fn()
	_ = w.Close()
	os.Stdout = old
	var buf bytes.Buffer
	if _, err := io.Copy(&buf, r); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}
