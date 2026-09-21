package integration

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/chenming/providerapi/internal/app"
	"github.com/chenming/providerapi/internal/config"
	"log/slog"
)

var (
	authCaptureMu sync.Mutex
	authCaptured  string
)

// fakeOpenAICompatUpstream is an httptest OpenAI-compatible upstream for integration tests.
func fakeOpenAICompatUpstream() *httptest.Server {
	var mu sync.Mutex
	lastAuth := ""
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/chat/completions") {
			http.NotFound(w, r)
			return
		}
		var req map[string]any
		_ = json.NewDecoder(r.Body).Decode(&req)
		model, _ := req["model"].(string)
		mode := r.Header.Get("X-Fake-Mode")
		if mode == "" {
			mode = model
		}
		mu.Lock()
		lastAuth = r.Header.Get("Authorization")
		mu.Unlock()

		switch mode {
		case "fake-check-auth":
			authCaptureMu.Lock()
			authCaptured = lastAuth
			authCaptureMu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id": "cmpl-auth", "model": model,
				"choices": []any{map[string]any{
					"message": map[string]any{"role": "assistant", "content": "AUTH_OK"},
					"finish_reason": "stop",
				}},
			})
			return
		case "fake-response-headers":
			w.Header().Set("X-Generation-Id", "gen-fake-test-123")
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id": "cmpl-fake", "model": model,
				"choices": []any{map[string]any{
					"message": map[string]any{"role": "assistant", "content": "OK"},
					"finish_reason": "stop",
				}},
			})
			return
		case "fake-success":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id": "cmpl-fake", "model": model,
				"choices": []any{map[string]any{
					"message": map[string]any{"role": "assistant", "content": "FAKE_OK"},
					"finish_reason": "stop",
				}},
			})
			return
		case "fake-404":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(404)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"message": "not found", "type": "not_found", "code": 404}})
			return
		case "fake-401":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(401)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"message": "unauthorized", "type": "unauthorized", "code": 401}})
			return
		case "fake-429":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(429)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"message": "rate limited", "type": "rate_limit", "code": 429}})
			return
		case "fake-500":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(500)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"message": "internal error", "type": "internal", "code": 500}})
			return
		case "fake-stream":
			w.Header().Set("Content-Type", "text/event-stream")
			flusher, ok := w.(http.Flusher)
			if !ok {
				http.Error(w, "no flush", 500)
				return
			}
			for _, line := range []string{
				`data: {"choices":[{"delta":{"content":"hello"}}]}`,
				`data: {"choices":[{"delta":{"content":" world"},"finish_reason":"stop"}]}`,
				"data: [DONE]",
			} {
				_, _ = w.Write([]byte(line + "\n\n"))
				flusher.Flush()
			}
			return
		case "fake-stream-multi-tool":
			w.Header().Set("Content-Type", "text/event-stream")
			flusher, _ := w.(http.Flusher)
			lines := []string{
				`data: {"choices":[{"delta":{"content":"using tools","tool_calls":[{"index":0,"id":"call_0","type":"function","function":{"name":"read_file","arguments":""}},{"index":1,"id":"call_1","type":"function","function":{"name":"list_dir","arguments":""}}]}}]}`,
				`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"path\":\"a\""}},{"index":1,"function":{"arguments":"{\"path\":\"b\""}}]}}]}`,
				`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"}"}},{"index":1,"function":{"arguments":"}"}}]},"finish_reason":"tool_calls"}]}`,
				"data: [DONE]",
			}
			for _, line := range lines {
				_, _ = w.Write([]byte(line + "\n\n"))
				if flusher != nil {
					flusher.Flush()
				}
			}
			return
		case "fake-stream-malformed":
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte("data: {not-json}\n\n"))
			return
		case "hanging-headers":
			select {
			case <-r.Context().Done():
			case <-time.After(5 * time.Minute):
			}
			return
		default:
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(400)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"message": "unknown fake mode: " + mode}})
		}
	}))
}

func buildOpenAICompat(t *testing.T, dir string) string {
	t.Helper()
	bin := filepath.Join(dir, "openai-compat")
	cmd := exec.Command("go", "build", "-o", bin, "./plugins/openai-compat")
	cmd.Dir = repoRoot(t)
	cmd.Env = os.Environ()
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("build openai-compat: %v\n%s", err, out)
	}
	return bin
}

type compatHarness struct {
	public string
	admin  string
	app    *app.App
}

func startCompatAPI(t *testing.T, upstreamURL string, credEnv string, hostEnv map[string]string) *compatHarness {
	t.Helper()
	return startCompatAPIWithCredEnv(t, upstreamURL, credEnv, hostEnv)
}

func startCompatAPIWithCredEnv(t *testing.T, upstreamURL string, credEnvName string, hostEnv map[string]string) *compatHarness {
	t.Helper()
	for k, v := range hostEnv {
		t.Setenv(k, v)
	}
	if credEnvName == "" {
		credEnvName = "FAKE_UPSTREAM_KEY"
	}
	if os.Getenv(credEnvName) == "" {
		t.Setenv(credEnvName, "fake-secret-key")
	}

	dir := t.TempDir()
	bin := buildOpenAICompat(t, dir)
	pubPort := freePort(t)
	admPort := freePort(t)
	cfgPath := filepath.Join(dir, "config.yaml")
	yaml := fmt.Sprintf(`
server:
  host: 127.0.0.1
  port: %d
admin:
  host: 127.0.0.1
  port: %d
storage:
  sqlite: %s
  traces: %s
debug:
  level: metadata
  retention: 168h
  full_retention: 24h
plugins:
  openai-compat:
    command: %s
providers:
  fake:
    plugin: openai-compat
    credential: fake-cred
    config:
      base_url: %s
credentials:
  fake-cred:
    provider: fake
    env: %s
models:
  fake-success:
    provider: fake
    upstream_model: fake-success
  fake-404:
    provider: fake
    upstream_model: fake-404
  fake-401:
    provider: fake
    upstream_model: fake-401
  fake-429:
    provider: fake
    upstream_model: fake-429
  fake-500:
    provider: fake
    upstream_model: fake-500
  fake-stream:
    provider: fake
    upstream_model: fake-stream
  fake-stream-multi-tool:
    provider: fake
    upstream_model: fake-stream-multi-tool
  fake-stream-malformed:
    provider: fake
    upstream_model: fake-stream-malformed
  fake-check-auth:
    provider: fake
    upstream_model: fake-check-auth
  fake-response-headers:
    provider: fake
    upstream_model: fake-response-headers
  hanging-headers:
    provider: fake
    upstream_model: hanging-headers
`, pubPort, admPort, filepath.Join(dir, "db.sqlite"), filepath.Join(dir, "traces"), bin, upstreamURL, credEnvName)

	if err := os.WriteFile(cfgPath, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	a, err := app.New(cfg, log)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() { errCh <- a.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-errCh:
		case <-time.After(5 * time.Second):
		}
	})
	public := fmt.Sprintf("http://127.0.0.1:%d", pubPort)
	admin := fmt.Sprintf("http://127.0.0.1:%d", admPort)
	waitHTTP(t, public+"/health")
	return &compatHarness{public: public, admin: admin, app: a}
}

func TestFakeUpstreamNonStreamSuccess(t *testing.T) {
	up := fakeOpenAICompatUpstream()
	defer up.Close()
	h := startCompatAPI(t, up.URL+"/v1", "FAKE_UPSTREAM_KEY", nil)

	resp := postJSON(t, h.public+"/v1/chat/completions", []byte(`{"model":"fake-success","messages":[{"role":"user","content":"hi"}]}`))
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("status %d %s", resp.StatusCode, b)
	}
	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	choices, _ := out["choices"].([]any)
	ch, _ := choices[0].(map[string]any)
	msg, _ := ch["message"].(map[string]any)
	if msg["content"] != "FAKE_OK" {
		t.Fatalf("content=%v", msg["content"])
	}
}

func TestFakeUpstreamStreamHTTPStatus(t *testing.T) {
	up := fakeOpenAICompatUpstream()
	defer up.Close()
	h := startCompatAPI(t, up.URL+"/v1", "FAKE_UPSTREAM_KEY", nil)

	cases := []struct {
		model    string
		wantHTTP int
	}{
		{"fake-404", 404},
		{"fake-401", 401},
		{"fake-429", 429},
		{"fake-500", 500},
	}
	for _, tc := range cases {
		body := fmt.Sprintf(`{"model":"%s","stream":true,"messages":[{"role":"user","content":"hi"}]}`, tc.model)
		resp := postJSON(t, h.public+"/v1/chat/completions", []byte(body))
		defer resp.Body.Close()
		if resp.StatusCode != tc.wantHTTP {
			b, _ := io.ReadAll(resp.Body)
			t.Fatalf("%s: status %d want %d body=%s", tc.model, resp.StatusCode, tc.wantHTTP, b)
		}
		var errBody map[string]any
		if err := json.NewDecoder(resp.Body).Decode(&errBody); err != nil {
			t.Fatalf("%s: decode error body: %v", tc.model, err)
		}
		if _, ok := errBody["error"]; !ok {
			t.Fatalf("%s: missing error object: %v", tc.model, errBody)
		}
	}
}

func TestFakeUpstreamStreamSuccess(t *testing.T) {
	up := fakeOpenAICompatUpstream()
	defer up.Close()
	h := startCompatAPI(t, up.URL+"/v1", "FAKE_UPSTREAM_KEY", nil)

	resp := postJSON(t, h.public+"/v1/chat/completions", []byte(`{"model":"fake-stream","stream":true,"messages":[{"role":"user","content":"hi"}]}`))
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("status %d %s", resp.StatusCode, b)
	}
	text := readSSEText(t, resp.Body)
	if !strings.Contains(text, "hello") || !strings.Contains(text, "world") {
		t.Fatalf("missing stream text: %s", text)
	}
}

func TestFakeUpstreamStreamMultiTool(t *testing.T) {
	up := fakeOpenAICompatUpstream()
	defer up.Close()
	h := startCompatAPI(t, up.URL+"/v1", "FAKE_UPSTREAM_KEY", nil)

	raw, err := os.ReadFile(filepath.Join(repoRoot(t), "tests/fixtures/cursor/cursor_multi_tool.json"))
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatal(err)
	}
	body["model"] = "fake-stream-multi-tool"
	body["stream"] = true
	payload, _ := json.Marshal(body)

	resp := postJSON(t, h.public+"/v1/chat/completions", payload)
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("status %d %s", resp.StatusCode, b)
	}

	calls := parseSSEToolCalls(t, resp.Body)
	if len(calls) < 2 {
		t.Fatalf("expected >=2 tool call indices, got %d: %v", len(calls), calls)
	}
	if calls[0].name != "read_file" || calls[1].name != "list_dir" {
		t.Fatalf("tool names wrong: %v", calls)
	}
	if !strings.Contains(calls[0].args, "a") || !strings.Contains(calls[1].args, "b") {
		t.Fatalf("argument fragments missing: %v", calls)
	}
}

func TestFakeUpstreamStreamMalformed(t *testing.T) {
	up := fakeOpenAICompatUpstream()
	defer up.Close()
	h := startCompatAPI(t, up.URL+"/v1", "FAKE_UPSTREAM_KEY", nil)

	resp := postJSON(t, h.public+"/v1/chat/completions", []byte(`{"model":"fake-stream-malformed","stream":true,"messages":[{"role":"user","content":"hi"}]}`))
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200 SSE wrapper, got %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), `"error"`) {
		t.Fatalf("expected SSE error event, got %s", body)
	}
}

func TestFakeUpstreamProviderAuditHeaders(t *testing.T) {
	up := fakeOpenAICompatUpstream()
	defer up.Close()
	h := startCompatAPI(t, up.URL+"/v1", "FAKE_UPSTREAM_KEY", nil)

	resp := postJSON(t, h.public+"/v1/chat/completions", []byte(`{"model":"fake-response-headers","messages":[{"role":"user","content":"hi"}]}`))
	reqID := resp.Header.Get("X-Request-ID")
	resp.Body.Close()

	tr, err := http.Get(h.admin + "/admin/requests/" + reqID)
	if err != nil {
		t.Fatal(err)
	}
	defer tr.Body.Close()
	traceBody, _ := io.ReadAll(tr.Body)
	if !strings.Contains(string(traceBody), "X-Generation-Id") {
		t.Fatalf("expected X-Generation-Id in trace: %s", traceBody)
	}
	if strings.Contains(string(traceBody), "gen-fake-test-123") {
		// generation id value itself is fine in metadata headers summary
	}
}

func TestFakeUpstreamCancelBeforeHeaders(t *testing.T) {
	up := fakeOpenAICompatUpstream()
	defer up.Close()
	h := startCompatAPI(t, up.URL+"/v1", "FAKE_UPSTREAM_KEY", nil)

	ctx, cancel := context.WithCancel(context.Background())
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, h.public+"/v1/chat/completions", strings.NewReader(`{"model":"hanging-headers","stream":true,"messages":[{"role":"user","content":"hi"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{}
	start := time.Now()
	resp, err := client.Do(req)
	if err != nil {
		// client may get canceled error before response
		cancel()
		if time.Since(start) > 10*time.Second {
			t.Fatalf("cancel took too long: %v", time.Since(start))
		}
		return
	}
	go func() {
		time.Sleep(200 * time.Millisecond)
		cancel()
	}()
	_, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	elapsed := time.Since(start)
	if elapsed > 45*time.Second {
		t.Fatalf("cancel before headers took %v, expected bounded by ResponseHeaderTimeout (~30s), not 10min", elapsed)
	}
}

func TestCredentialProviderAPIOverOpenAICompat(t *testing.T) {
	up := fakeOpenAICompatUpstream()
	defer up.Close()

	t.Setenv("FAKE_UPSTREAM_KEY", "A")
	t.Setenv("OPENAI_COMPAT_API_KEY", "B")

	dir := t.TempDir()
	bin := buildOpenAICompat(t, dir)
	pubPort := freePort(t)
	admPort := freePort(t)
	cfgPath := filepath.Join(dir, "config.yaml")
	yaml := fmt.Sprintf(`
server:
  host: 127.0.0.1
  port: %d
admin:
  host: 127.0.0.1
  port: %d
storage:
  sqlite: %s
  traces: %s
debug:
  level: metadata
plugins:
  openai-compat:
    command: %s
providers:
  fake:
    plugin: openai-compat
    credential: fake-cred
    config:
      base_url: %s
credentials:
  fake-cred:
    provider: fake
    env: %s
models:
  fake-check-auth:
    provider: fake
    upstream_model: fake-check-auth
`, pubPort, admPort, filepath.Join(dir, "db.sqlite"), filepath.Join(dir, "traces"), bin, up.URL+"/v1", "FAKE_UPSTREAM_KEY")
	if err := os.WriteFile(cfgPath, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	a, err := app.New(cfg, log)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	go func() { _ = a.Run(ctx) }()
	t.Cleanup(cancel)
	public := fmt.Sprintf("http://127.0.0.1:%d", pubPort)
	waitHTTP(t, public+"/health")

	resp := postJSON(t, public+"/v1/chat/completions", []byte(`{"model":"fake-check-auth","messages":[{"role":"user","content":"hi"}]}`))
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("status %d %s", resp.StatusCode, b)
	}
	authCaptureMu.Lock()
	got := authCaptured
	authCaptureMu.Unlock()
	if got != "Bearer A" {
		t.Fatalf("expected Bearer A, got %q", got)
	}
}

func TestCredentialReplacesInheritedHostValue(t *testing.T) {
	up := fakeOpenAICompatUpstream()
	defer up.Close()

	t.Setenv("PROVIDERAPI_CREDENTIAL", "WRONG")
	t.Setenv("OPENROUTER_API_KEY", "RIGHT")

	h := startCompatAPIWithCredEnv(t, up.URL+"/v1", "OPENROUTER_API_KEY", nil)

	resp := postJSON(t, h.public+"/v1/chat/completions", []byte(`{"model":"fake-check-auth","messages":[{"role":"user","content":"hi"}]}`))
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("status %d %s", resp.StatusCode, b)
	}
	authCaptureMu.Lock()
	got := authCaptured
	authCaptureMu.Unlock()
	if got != "Bearer RIGHT" {
		t.Fatalf("expected Bearer RIGHT from instance secret, got %q", got)
	}
}

func TestToolStrictPreservedThroughStack(t *testing.T) {
	var sawStrict bool
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/chat/completions") {
			http.NotFound(w, r)
			return
		}
		var req map[string]any
		_ = json.NewDecoder(r.Body).Decode(&req)
		tools, _ := req["tools"].([]any)
		if len(tools) == 0 {
			http.Error(w, "no tools", 400)
			return
		}
		tool, _ := tools[0].(map[string]any)
		fn, _ := tool["function"].(map[string]any)
		if fn["strict"] == true {
			sawStrict = true
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "cmpl-strict", "model": req["model"],
			"choices": []any{map[string]any{
				"message": map[string]any{"role": "assistant", "content": "OK"},
				"finish_reason": "stop",
			}},
		})
	}))
	defer up.Close()
	h := startCompatAPI(t, up.URL+"/v1", "FAKE_UPSTREAM_KEY", nil)

	body := `{"model":"fake-success","messages":[{"role":"user","content":"hi"}],"tools":[{"type":"function","function":{"name":"read_file","parameters":{"type":"object"},"strict":true}}]}`
	resp := postJSON(t, h.public+"/v1/chat/completions", []byte(body))
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("status %d %s", resp.StatusCode, b)
	}
	if !sawStrict {
		t.Fatal("upstream did not receive function.strict=true")
	}
}

func readSSEText(t *testing.T, r io.Reader) string {
	t.Helper()
	sc := bufio.NewScanner(r)
	var parts []string
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "[DONE]" {
			break
		}
		var chunk map[string]any
		if json.Unmarshal([]byte(data), &chunk) == nil {
			choices, _ := chunk["choices"].([]any)
			if len(choices) > 0 {
				ch, _ := choices[0].(map[string]any)
				delta, _ := ch["delta"].(map[string]any)
				if c, ok := delta["content"].(string); ok {
					parts = append(parts, c)
				}
			}
		}
	}
	return strings.Join(parts, "")
}

type toolAccum struct {
	name string
	args string
}

func parseSSEToolCalls(t *testing.T, r io.Reader) []toolAccum {
	t.Helper()
	sc := bufio.NewScanner(r)
	byIndex := make(map[int]*toolAccum)
	maxIdx := -1
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "[DONE]" {
			break
		}
		var chunk map[string]any
		if json.Unmarshal([]byte(data), &chunk) != nil {
			continue
		}
		choices, _ := chunk["choices"].([]any)
		if len(choices) == 0 {
			continue
		}
		ch, _ := choices[0].(map[string]any)
		delta, _ := ch["delta"].(map[string]any)
		tcs, _ := delta["tool_calls"].([]any)
		for _, tcRaw := range tcs {
			tc, _ := tcRaw.(map[string]any)
			idxF, _ := tc["index"].(float64)
			idx := int(idxF)
			if byIndex[idx] == nil {
				byIndex[idx] = &toolAccum{}
			}
			if id, ok := tc["id"].(string); ok && id != "" {
				_ = id
			}
			fn, _ := tc["function"].(map[string]any)
			if name, ok := fn["name"].(string); ok && name != "" {
				byIndex[idx].name = name
			}
			if args, ok := fn["arguments"].(string); ok {
				byIndex[idx].args += args
			}
			if idx > maxIdx {
				maxIdx = idx
			}
		}
	}
	out := make([]toolAccum, maxIdx+1)
	for i := 0; i <= maxIdx; i++ {
		if byIndex[i] != nil {
			out[i] = *byIndex[i]
		}
	}
	return out
}
