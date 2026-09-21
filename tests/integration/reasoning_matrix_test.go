package integration

import (
	"encoding/json"
	"io"
	"strings"
	"testing"
)

func TestReasoningSubstituteBodyMatrix(t *testing.T) {
	up := fakeOpenAICompatUpstream()
	defer up.Close()
	h := startCompatAPI(t, up.URL+"/v1", "FAKE_UPSTREAM_KEY", nil)

	cases := []struct {
		name       string
		body       string
		wantHTTP   int
		wantEffort any // nil means reasoning field must be absent
		wantCode   string
		noUpstream bool
	}{
		{
			name:     "1 sonnet-high alias → high",
			body:     `{"model":"sonnet-high","messages":[{"role":"user","content":"hi"}]}`,
			wantHTTP: 200,
			wantEffort: "high",
		},
		{
			name:     "2 sonnet-max alias → max",
			body:     `{"model":"sonnet-max","messages":[{"role":"user","content":"hi"}]}`,
			wantHTTP: 200,
			wantEffort: "max",
		},
		{
			name:     "3 sonnet-high + reasoning_effort=low overrides alias",
			body:     `{"model":"sonnet-high","reasoning_effort":"low","messages":[{"role":"user","content":"hi"}]}`,
			wantHTTP: 200,
			wantEffort: "low",
		},
		{
			name:     "4 sonnet-high + reasoning.effort=medium",
			body:     `{"model":"sonnet-high","reasoning":{"effort":"medium"},"messages":[{"role":"user","content":"hi"}]}`,
			wantHTTP: 200,
			wantEffort: "medium",
		},
		{
			name:       "5 reasoning_effort=ultra → 400",
			body:       `{"model":"sonnet-high","reasoning_effort":"ultra","messages":[{"role":"user","content":"hi"}]}`,
			wantHTTP:   400,
			wantCode:   "invalid_request",
			noUpstream: true,
		},
		{
			name:       "6 max with provider missing max mapping → reasoning_unsupported",
			body:       `{"model":"sonnet-max-unmapped","messages":[{"role":"user","content":"hi"}]}`,
			wantHTTP:   400,
			wantCode:   "reasoning_unsupported",
			noUpstream: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp := postJSON(t, h.public+"/v1/chat/completions", []byte(tc.body))
			raw, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if resp.StatusCode != tc.wantHTTP {
				t.Fatalf("status %d want %d body=%s", resp.StatusCode, tc.wantHTTP, raw)
			}
			if tc.wantCode != "" {
				var out map[string]any
				if err := json.Unmarshal(raw, &out); err != nil {
					t.Fatal(err)
				}
				errv, _ := out["error"].(map[string]any)
				code, _ := errv["code"].(string)
				if code != tc.wantCode {
					t.Fatalf("error.code=%q want %q body=%s", code, tc.wantCode, raw)
				}
				if tc.wantCode == "reasoning_unsupported" && !strings.Contains(string(raw), "not mapped") && !strings.Contains(string(raw), "reasoning_unsupported") {
					t.Fatalf("expected reasoning_unsupported message: %s", raw)
				}
			}
			if tc.noUpstream {
				return
			}
			got := up.LastBody()
			if got == nil {
				t.Fatal("fake upstream captured no body")
			}
			if got["model"] != "anthropic/claude-sonnet-4.5" {
				t.Fatalf("upstream model=%v want anthropic/claude-sonnet-4.5 body=%v", got["model"], got)
			}
			assertEffort(t, got, tc.wantEffort)
		})
	}
}

func TestReasoningEffortObjectWinsOverReasoningEffortField(t *testing.T) {
	// Both fields present: reasoning.effort wins over reasoning_effort.
	up := fakeOpenAICompatUpstream()
	defer up.Close()
	h := startCompatAPI(t, up.URL+"/v1", "FAKE_UPSTREAM_KEY", nil)

	body := `{"model":"sonnet-high","reasoning_effort":"low","reasoning":{"effort":"high"},"messages":[{"role":"user","content":"hi"}]}`
	resp := postJSON(t, h.public+"/v1/chat/completions", []byte(body))
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status %d %s", resp.StatusCode, raw)
	}
	got := up.LastBody()
	assertEffort(t, got, "high")
}

func TestReasoningCIDefaultHighMaxBodies(t *testing.T) {
	up := fakeOpenAICompatUpstream()
	defer up.Close()
	h := startCompatAPI(t, up.URL+"/v1", "FAKE_UPSTREAM_KEY", nil)

	t.Run("sonnet default has no reasoning field", func(t *testing.T) {
		resp := postJSON(t, h.public+"/v1/chat/completions", []byte(`{"model":"sonnet","messages":[{"role":"user","content":"hi"}]}`))
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Fatalf("status %d %s", resp.StatusCode, b)
		}
		got := up.LastBody()
		if _, ok := got["reasoning"]; ok {
			t.Fatalf("sonnet default must not inject reasoning, got %v", got["reasoning"])
		}
		if got["model"] != "anthropic/claude-sonnet-4.5" {
			t.Fatalf("model=%v", got["model"])
		}
	})
	t.Run("sonnet-high effort high", func(t *testing.T) {
		resp := postJSON(t, h.public+"/v1/chat/completions", []byte(`{"model":"sonnet-high","messages":[{"role":"user","content":"hi"}]}`))
		resp.Body.Close()
		assertEffort(t, up.LastBody(), "high")
	})
	t.Run("sonnet-max effort max", func(t *testing.T) {
		resp := postJSON(t, h.public+"/v1/chat/completions", []byte(`{"model":"sonnet-max","messages":[{"role":"user","content":"hi"}]}`))
		resp.Body.Close()
		assertEffort(t, up.LastBody(), "max")
	})
}

func assertEffort(t *testing.T, body map[string]any, want any) {
	t.Helper()
	if body == nil {
		t.Fatal("nil upstream body")
	}
	if want == nil {
		if _, ok := body["reasoning"]; ok {
			t.Fatalf("unexpected reasoning field: %v", body["reasoning"])
		}
		return
	}
	reasoning, _ := body["reasoning"].(map[string]any)
	if reasoning["effort"] != want {
		t.Fatalf("captured upstream body reasoning.effort=%v want %v full=%v", reasoning["effort"], want, body)
	}
}
