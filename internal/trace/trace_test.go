package trace

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/LeibNici/providerapi/internal/protocol"
	"github.com/LeibNici/providerapi/internal/store"
)

const (
	testPrompt    = "my private prompt"
	testToolPath  = "/private/project/secret-file.ts"
	testToolName  = "read_file"
)

func TestMetadataPrivacy(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "db.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	svc, err := New(st, filepath.Join(dir, "traces"), "metadata", time.Hour, time.Hour)
	if err != nil {
		t.Fatal(err)
	}

	sess := svc.Start("req-meta-1", "client-1")
	raw := json.RawMessage(`{
		"model": "mock",
		"stream": false,
		"messages": [{"role":"user","content":"` + testPrompt + `"}],
		"tools": [{"type":"function","function":{"name":"` + testToolName + `","parameters":{"type":"object"}}}],
		"tool_choice": {"type":"function","function":{"name":"` + testToolName + `"}}
	}`)
	sess.ClientRaw(raw)

	canon := &protocol.CompletionRequest{
		RequestID: "req-meta-1",
		Model:     "mock-echo",
		Messages: []protocol.Message{
			{Role: "user", Content: protocol.TextContent(testPrompt)},
			{
				Role: "assistant",
				ToolCalls: []protocol.ToolCall{{
					ID:   "call_1",
					Type: "function",
					Function: protocol.FunctionCall{
						Name:      testToolName,
						Arguments: `{"path":"` + testToolPath + `"}`,
					},
				}},
			},
		},
		Tools: []protocol.Tool{{
			Type: "function",
			Function: &protocol.FunctionTool{Name: testToolName},
		}},
		Reasoning: protocol.ReasoningConfig{Level: protocol.ReasoningDefault},
	}
	sess.Normalized(canon)
	sess.PluginRequest(canon)

	auditReq, _ := json.Marshal(map[string]any{
		"type":    "provider_request",
		"method":  "POST",
		"url":     "https://api.example.com/v1/chat?api_key=secret-key",
		"headers": map[string]string{"Authorization": "Bearer sk-secret", "Content-Type": "application/json"},
		"body":    map[string]any{"messages": []any{map[string]any{"content": testPrompt}}},
	})
	sess.ProviderAudit(auditReq)

	auditResp, _ := json.Marshal(map[string]any{
		"type":    "provider_response",
		"method":  "POST",
		"url":     "https://api.example.com/v1/chat?token=abc",
		"status":  200,
		"headers": map[string]string{"Set-Cookie": "session=secret"},
		"body":    map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": testPrompt}}}},
	})
	sess.ProviderAudit(auditResp)
	sess.Finish(200, "ok", "stop", 1, 2, "", "", "")

	assertNoSensitive(t, st, "req-meta-1", dir)
}

func TestFullModeKeepsPromptInGzip(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "db.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	svc, err := New(st, filepath.Join(dir, "traces"), "full", time.Hour, time.Hour)
	if err != nil {
		t.Fatal(err)
	}

	sess := svc.Start("req-full-1", "client-1")
	raw := json.RawMessage(`{"model":"mock","messages":[{"role":"user","content":"` + testPrompt + `"}]}`)
	sess.ClientRaw(raw)
	sess.Finish(200, "ok", "stop", 0, 0, "", "", "")

	gzPath := filepath.Join(dir, "traces", "req-full-1.json.gz")
	data, err := readGzipFile(gzPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(data, testPrompt) {
		t.Fatalf("full gzip should contain test prompt, got: %s", data)
	}
}

func assertNoSensitive(t *testing.T, st *store.Store, reqID, tracesDir string) {
	events, err := st.ListEvents(context.Background(), reqID)
	if err != nil {
		t.Fatal(err)
	}
	var sqliteDump strings.Builder
	for _, e := range events {
		sqliteDump.WriteString(string(e.Payload))
	}
	row, err := st.GetRequest(context.Background(), reqID)
	if err != nil {
		t.Fatal(err)
	}
	sqliteDump.WriteString(row.ErrorMessage)
	sqliteDump.WriteString(row.ErrorCode)

	for _, needle := range []string{testPrompt, testToolPath} {
		if strings.Contains(sqliteDump.String(), needle) {
			t.Fatalf("sqlite metadata leaked %q in events/request row", needle)
		}
	}
	if strings.Contains(sqliteDump.String(), "api_key=secret-key") || strings.Contains(sqliteDump.String(), "?token=") {
		t.Fatalf("sqlite metadata leaked URL query values: %s", sqliteDump.String())
	}
	if strings.Contains(sqliteDump.String(), "Bearer sk-secret") {
		t.Fatalf("sqlite metadata leaked authorization header")
	}

	gzPath := filepath.Join(tracesDir, "traces", reqID+".json.gz")
	if _, err := os.Stat(gzPath); err == nil {
		data, err := readGzipFile(gzPath)
		if err != nil {
			t.Fatal(err)
		}
		for _, needle := range []string{testPrompt, testToolPath} {
			if strings.Contains(data, needle) {
				t.Fatalf("metadata mode gzip leaked %q", needle)
			}
		}
	}
}

func readGzipFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return "", err
	}
	defer gz.Close()
	b, err := io.ReadAll(gz)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func TestStripURLQuery(t *testing.T) {
	got := stripURLQuery("https://api.example.com/v1/chat?api_key=secret")
	want := "https://api.example.com/v1/chat"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}
