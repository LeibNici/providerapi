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
		}
	}
	if !gotText || !gotDone {
		t.Fatalf("stream incomplete text=%v done=%v", gotText, gotDone)
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
	if resp.StatusCode != 500 {
		t.Fatalf("expected gateway 500 for upstream 500, got %d", resp.StatusCode)
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
