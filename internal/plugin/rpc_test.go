package plugin

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/chenming/providerapi/internal/config"
	"github.com/chenming/providerapi/internal/protocol"
)

func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("caller")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "../.."))
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

func startMockInstance(t *testing.T) *Instance {
	t.Helper()
	dir := t.TempDir()
	bin := buildMock(t, dir)
	in := &Instance{
		ID:    "mock",
		Plugin: "mock",
		cfg:   config.ProviderInstance{Plugin: "mock"},
		bin:   config.PluginBinary{Command: bin},
		dir:   dir,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := in.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(in.Stop)
	return in
}

func TestHTTPStatusRPCRoundTrip(t *testing.T) {
	in := startMockInstance(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

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
		_, err := in.Complete(ctx, &protocol.CompletionRequest{
			RequestID: "rpc-test",
			Model:     tc.model,
			Messages:  []protocol.Message{{Role: "user", Content: protocol.TextContent("hi")}},
			Reasoning: protocol.ReasoningConfig{Level: protocol.ReasoningDefault},
		})
		if err == nil {
			t.Fatalf("%s: expected error", tc.model)
		}
		pe, ok := err.(*protocol.ProviderError)
		if !ok {
			t.Fatalf("%s: expected ProviderError, got %T", tc.model, err)
		}
		if pe.HTTPStatus != tc.wantHTTP {
			t.Fatalf("%s: HTTPStatus=%d want %d", tc.model, pe.HTTPStatus, tc.wantHTTP)
		}
	}
}
