package integration

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"
	"time"

	"github.com/chenming/providerapi/internal/app"
	"github.com/chenming/providerapi/internal/config"
	"log/slog"
)

func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("caller")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "../.."))
}

func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	return port
}

func buildMock(t *testing.T, dir string) string {
	t.Helper()
	bin := filepath.Join(dir, "mock")
	cmd := exec.Command("go", "build", "-o", bin, "./plugins/mock")
	cmd.Dir = repoRoot(t)
	cmd.Env = os.Environ()
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("build mock: %v\n%s", err, out)
	}
	return bin
}

func startMockAPI(t *testing.T) (public string, admin string) {
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
  level: full
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
  sonnet-high:
    provider: mock
    upstream_model: mock-echo
    reasoning: high
`, pubPort, admPort, filepath.Join(dir, "db.sqlite"), filepath.Join(dir, "traces"), bin)
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
	public = "http://127.0.0.1:" + strconv.Itoa(pubPort)
	admin = "http://127.0.0.1:" + strconv.Itoa(admPort)
	waitHTTP(t, public+"/health")
	return public, admin
}

func waitHTTP(t *testing.T, url string) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get(url)
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == 200 {
				return
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("server not ready: %s", url)
}

func postJSON(t *testing.T, url string, body []byte) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}
