package integration

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/LeibNici/providerapi/internal/app"
	"github.com/LeibNici/providerapi/internal/config"
	"log/slog"
)

func startMockAPIWithLevel(t *testing.T, level string) (public string, admin string, cfg *config.Config, a *app.App) {
	t.Helper()
	dir := t.TempDir()
	bin := buildMock(t, dir)
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
  level: %s
  retention: 168h
  full_retention: 24h
plugins:
  mock:
    command: %s
providers:
  mock:
    plugin: mock
    config: {}
models:
  mock:
    provider: mock
    upstream_model: mock-echo
  mock-error:
    provider: mock
    upstream_model: mock-error
  mock-timeout:
    provider: mock
    upstream_model: mock-timeout
  mock-tool:
    provider: mock
    upstream_model: mock-tool
  mock-unauthorized:
    provider: mock
    upstream_model: mock-unauthorized
  mock-rate-limited:
    provider: mock
    upstream_model: mock-rate-limited
  mock-unavailable:
    provider: mock
    upstream_model: mock-unavailable
  mock-bad-request:
    provider: mock
    upstream_model: mock-bad-request
  sonnet-high:
    provider: mock
    upstream_model: mock-echo
    reasoning: high
`, pubPort, admPort, filepath.Join(dir, "db.sqlite"), filepath.Join(dir, "traces"), level, bin)
	if err := os.WriteFile(cfgPath, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	appInst, err := app.New(cfg, log)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() { errCh <- appInst.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-errCh:
		case <-time.After(5 * time.Second):
		}
	})
	public = fmt.Sprintf("http://127.0.0.1:%d", pubPort)
	admin = fmt.Sprintf("http://127.0.0.1:%d", admPort)
	waitHTTP(t, public+"/health")
	return public, admin, cfg, appInst
}

func TestHTTPStatusMapping(t *testing.T) {
	public, _ := startMockAPI(t)
	cases := []struct {
		model    string
		wantHTTP int
	}{
		{"mock-bad-request", 400},
		{"mock-unauthorized", 401},
		{"mock-rate-limited", 429},
		{"mock-unavailable", 503},
		{"mock-error", 502},
	}
	for _, tc := range cases {
		body := fmt.Sprintf(`{"model":"%s","messages":[{"role":"user","content":"hi"}]}`, tc.model)
		resp := postJSON(t, public+"/v1/chat/completions", []byte(body))
		defer resp.Body.Close()
		if resp.StatusCode != tc.wantHTTP {
			b, _ := io.ReadAll(resp.Body)
			t.Fatalf("%s: status %d want %d body=%s", tc.model, resp.StatusCode, tc.wantHTTP, b)
		}
		var out map[string]any
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			t.Fatal(err)
		}
		errv, _ := out["error"].(map[string]any)
		if errv["http_status"] != nil {
			t.Fatalf("%s: openai error body must not include http_status: %v", tc.model, out)
		}
	}
}

func TestMetadataPrivacyIntegration(t *testing.T) {
	public, admin, _, _ := startMockAPIWithLevel(t, "metadata")
	const prompt = "my private prompt"
	const toolPath = "/private/project/secret-file.ts"
	body := fmt.Sprintf(`{
		"model":"mock-tool",
		"messages":[{"role":"user","content":"%s"}],
		"tools":[{"type":"function","function":{"name":"read_file","parameters":{"type":"object"}}}],
		"tool_choice":{"type":"function","function":{"name":"read_file"}}
	}`, prompt)
	resp := postJSON(t, public+"/v1/chat/completions", []byte(body))
	id := resp.Header.Get("X-Request-ID")
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status %d %s", resp.StatusCode, b)
	}

	tr, err := http.Get(admin + "/admin/requests/" + id)
	if err != nil {
		t.Fatal(err)
	}
	defer tr.Body.Close()
	traceBody, _ := io.ReadAll(tr.Body)
	if strings.Contains(string(traceBody), prompt) || strings.Contains(string(traceBody), toolPath) {
		t.Fatalf("admin trace leaked sensitive data: %s", traceBody)
	}
}

func TestPluginCrashDuringStream(t *testing.T) {
	public, admin, _, appInst := startMockAPIWithLevel(t, "metadata")
	inst, err := appInst.Plugins.Get("mock")
	if err != nil {
		t.Fatal(err)
	}
	pid := inst.PID
	if pid <= 0 {
		t.Fatal("expected plugin PID")
	}

	body := `{"model":"mock-timeout","stream":true,"messages":[{"role":"user","content":"wait"}]}`
	req, err := http.NewRequest(http.MethodPost, public+"/v1/chat/completions", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	reqID := resp.Header.Get("X-Request-ID")
	if reqID == "" {
		t.Fatal("missing request id")
	}

	time.Sleep(200 * time.Millisecond)
	if err := syscall.Kill(pid, syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(3 * time.Second)
	var gotError bool
	sc := bufio.NewScanner(resp.Body)
	for time.Now().Before(deadline) {
		if !sc.Scan() {
			break
		}
		line := sc.Text()
		if strings.Contains(line, `"error"`) {
			gotError = true
		}
		if strings.Contains(line, "[DONE]") {
			break
		}
	}
	if !gotError {
		t.Fatal("expected SSE error event within 3s")
	}

	waitHTTP(t, public+"/health")

	tr, err := http.Get(admin + "/admin/requests/" + reqID)
	if err != nil {
		t.Fatal(err)
	}
	defer tr.Body.Close()
	var trace map[string]any
	if err := json.NewDecoder(tr.Body).Decode(&trace); err != nil {
		t.Fatal(err)
	}
	rows, err := appInst.Trace.Store().ListRequests(context.Background(), 10)
	if err != nil {
		t.Fatal(err)
	}
	var row map[string]any
	for _, r := range rows {
		if r.ID == reqID {
			row = map[string]any{
				"status":        r.Status,
				"error_type":    r.ErrorType,
				"error_code":    r.ErrorCode,
				"error_message": r.ErrorMessage,
			}
			break
		}
	}
	if row == nil {
		t.Fatalf("request row not found for %s", reqID)
	}
	if row["status"] != "error" {
		t.Fatalf("expected status error, got %v", row)
	}
	if row["error_type"] != "provider_error" {
		t.Fatalf("expected error_type provider_error, got %v", row)
	}
	if row["error_code"] != "plugin_crash" {
		t.Fatalf("expected error_code plugin_crash, got %v", row)
	}

	// plugin should restart and accept requests again
	deadline = time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		resp2 := postJSON(t, public+"/v1/chat/completions", []byte(`{"model":"mock","messages":[{"role":"user","content":"hi"}]}`))
		if resp2.StatusCode == 200 {
			resp2.Body.Close()
			return
		}
		b, _ := io.ReadAll(resp2.Body)
		resp2.Body.Close()
		if !strings.Contains(string(b), "closed pipe") && !strings.Contains(string(b), "plugin_unavailable") {
			t.Fatalf("plugin restart failed: %d %s", resp2.StatusCode, b)
		}
		time.Sleep(250 * time.Millisecond)
	}
	t.Fatal("plugin did not recover within 10s")
}
