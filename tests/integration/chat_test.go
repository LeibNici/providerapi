package integration

import (
	"bufio"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestHealth(t *testing.T) {
	public, _ := startMockAPI(t)
	resp, err := http.Get(public + "/health")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status %d", resp.StatusCode)
	}
	var body map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body["status"] != "ok" {
		t.Fatalf("body=%v", body)
	}
}

func TestChatNonStream(t *testing.T) {
	public, _ := startMockAPI(t)
	raw, _ := os.ReadFile(filepath.Join(repoRoot(t), "tests/fixtures/cursor/cursor_basic_chat.json"))
	resp := postJSON(t, public+"/v1/chat/completions", raw)
	defer resp.Body.Close()
	if resp.Header.Get("X-Request-ID") == "" {
		t.Fatal("missing X-Request-ID")
	}
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("status %d %s", resp.StatusCode, b)
	}
	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if out["model"] != "mock" {
		t.Fatalf("client-facing model leaked or wrong: %v", out["model"])
	}
	if out["object"] != "chat.completion" {
		t.Fatalf("object=%v", out["object"])
	}
}

func TestChatStream(t *testing.T) {
	public, _ := startMockAPI(t)
	raw, _ := os.ReadFile(filepath.Join(repoRoot(t), "tests/fixtures/cursor/cursor_stream.json"))
	resp := postJSON(t, public+"/v1/chat/completions", raw)
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("status %d %s", resp.StatusCode, b)
	}
	sc := bufio.NewScanner(resp.Body)
	gotDone := false
	gotText := false
	gotUsageChunk := false
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "data:") {
			data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			if data == "[DONE]" {
				gotDone = true
				break
			}
			if strings.Contains(data, `"content"`) {
				gotText = true
			}
			var obj map[string]any
			if err := json.Unmarshal([]byte(data), &obj); err == nil {
				if _, ok := obj["usage"]; ok {
					choices, _ := obj["choices"].([]any)
					if len(choices) == 0 {
						gotUsageChunk = true
					}
				}
			}
		}
	}
	if !gotText || !gotDone {
		t.Fatalf("stream incomplete text=%v done=%v", gotText, gotDone)
	}
	if !gotUsageChunk {
		t.Fatal("include_usage stream must emit a trailing usage chunk with empty choices")
	}
}

func TestChatStreamUsageWithoutIncludeFlag(t *testing.T) {
	public, _ := startMockAPI(t)
	resp := postJSON(t, public+"/v1/chat/completions", []byte(`{"model":"mock","messages":[{"role":"user","content":"hi"}],"stream":true}`))
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("status %d %s", resp.StatusCode, b)
	}
	sc := bufio.NewScanner(resp.Body)
	gotUsageChunk := false
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "[DONE]" {
			break
		}
		var obj map[string]any
		if err := json.Unmarshal([]byte(data), &obj); err != nil {
			continue
		}
		if _, ok := obj["usage"]; !ok {
			continue
		}
		choices, _ := obj["choices"].([]any)
		if len(choices) == 0 {
			gotUsageChunk = true
		}
	}
	if !gotUsageChunk {
		t.Fatal("stream must emit trailing usage even without stream_options.include_usage")
	}
}

func TestUnknownFields(t *testing.T) {
	public, _ := startMockAPI(t)
	raw, _ := os.ReadFile(filepath.Join(repoRoot(t), "tests/fixtures/cursor/cursor_unknown_fields.json"))
	resp := postJSON(t, public+"/v1/chat/completions", raw)
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("unknown fields should not 400: %d %s", resp.StatusCode, b)
	}
}

func TestProviderError(t *testing.T) {
	public, _ := startMockAPI(t)
	raw, _ := os.ReadFile(filepath.Join(repoRoot(t), "tests/fixtures/cursor/cursor_provider_error.json"))
	resp := postJSON(t, public+"/v1/chat/completions", raw)
	defer resp.Body.Close()
	if resp.StatusCode != 502 {
		t.Fatalf("expected gateway 502 for upstream 500, got %d", resp.StatusCode)
	}
	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	errv, _ := out["error"].(map[string]any)
	if errv["type"] == nil {
		t.Fatalf("missing error.type: %v", out)
	}
}

func TestToolCallAndResult(t *testing.T) {
	public, _ := startMockAPI(t)
	raw, _ := os.ReadFile(filepath.Join(repoRoot(t), "tests/fixtures/cursor/cursor_function_tool.json"))
	resp := postJSON(t, public+"/v1/chat/completions", raw)
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status %d %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), `"tool_calls"`) {
		t.Fatalf("expected tool_calls: %s", body)
	}

	raw, _ = os.ReadFile(filepath.Join(repoRoot(t), "tests/fixtures/cursor/cursor_tool_result.json"))
	resp = postJSON(t, public+"/v1/chat/completions", raw)
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("tool result status %d %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), "Final answer") {
		t.Fatalf("expected final answer: %s", body)
	}
}

func TestReasoningAlias(t *testing.T) {
	public, admin := startMockAPI(t)
	raw, _ := os.ReadFile(filepath.Join(repoRoot(t), "tests/fixtures/cursor/cursor_reasoning_alias.json"))
	resp := postJSON(t, public+"/v1/chat/completions", raw)
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status %d %s", resp.StatusCode, body)
	}
	var out map[string]any
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatal(err)
	}
	if out["model"] != "sonnet-high" {
		t.Fatalf("model should remain alias, got %v", out["model"])
	}
	id := resp.Header.Get("X-Request-ID")
	tr, err := http.Get(admin + "/admin/requests/" + id)
	if err != nil {
		t.Fatal(err)
	}
	defer tr.Body.Close()
	var trace map[string]any
	if err := json.NewDecoder(tr.Body).Decode(&trace); err != nil {
		t.Fatal(err)
	}
	norm, _ := trace["normalized"].(map[string]any)
	if norm["reasoning"] != "high" {
		t.Fatalf("expected reasoning high, got %v", trace)
	}
}

func TestModelsHideUpstream(t *testing.T) {
	public, _ := startMockAPI(t)
	resp, err := http.Get(public + "/v1/models")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if strings.Contains(string(b), "mock-echo") || strings.Contains(string(b), "anthropic/") {
		t.Fatalf("upstream model leaked: %s", b)
	}
	var listed struct {
		Data []struct {
			ID            string `json:"id"`
			ContextLength int    `json:"context_length"`
		} `json:"data"`
	}
	if err := json.Unmarshal(b, &listed); err != nil {
		t.Fatal(err)
	}
	if len(listed.Data) == 0 {
		t.Fatal("expected models")
	}
	for _, m := range listed.Data {
		if m.ContextLength <= 0 {
			t.Fatalf("model %s missing context_length: %+v", m.ID, m)
		}
	}
}

func TestClientDisconnect(t *testing.T) {
	public, _ := startMockAPI(t)
	raw, _ := os.ReadFile(filepath.Join(repoRoot(t), "tests/fixtures/cursor/cursor_client_disconnect.json"))
	req, err := http.NewRequest(http.MethodPost, public+"/v1/chat/completions", strings.NewReader(string(raw)))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 40 * time.Millisecond}
	_, _ = client.Do(req)
}

func TestMetricsAndAdmin(t *testing.T) {
	_, admin := startMockAPI(t)
	resp, err := http.Get(admin + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(b), "providerapi_requests_total") && resp.StatusCode != 200 {
		t.Fatalf("metrics: %d %s", resp.StatusCode, b)
	}
	resp2, err := http.Get(admin + "/admin/plugins")
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != 200 {
		t.Fatalf("plugins %d", resp2.StatusCode)
	}
}
