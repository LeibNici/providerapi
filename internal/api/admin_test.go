package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/LeibNici/providerapi/internal/config"
	"github.com/go-chi/chi/v5"
	"github.com/LeibNici/providerapi/internal/model"
	"github.com/LeibNici/providerapi/internal/observability"
	"github.com/LeibNici/providerapi/internal/plugin"
	"github.com/LeibNici/providerapi/internal/protocol"
	"github.com/LeibNici/providerapi/internal/store"
	"github.com/LeibNici/providerapi/internal/trace"
)

func TestAdminListModelsDTO(t *testing.T) {
	cfg := &config.Config{
		Models: map[string]config.ModelAlias{
			"a": {Provider: "prov", UpstreamModel: "up", ContextLength: 64000},
			"b": {Provider: "prov", UpstreamModel: "up2"},
		},
	}
	adm := &Admin{Cfg: cfg, Plugins: plugin.NewManager(cfg), Metrics: observability.New()}
	req := httptest.NewRequest(http.MethodGet, "/admin/models", nil)
	rec := httptest.NewRecorder()
	adm.listModels(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status %d", rec.Code)
	}
	var body struct {
		Data []model.AdminModelInfo `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	byID := map[string]model.AdminModelInfo{}
	for _, m := range body.Data {
		byID[m.ID] = m
	}
	if byID["a"].ContextLengthSource != "configured" {
		t.Fatalf("a: %+v", byID["a"])
	}
	if byID["b"].ContextLengthSource != "fallback" {
		t.Fatalf("b: %+v", byID["b"])
	}
}

func TestAdminGetRequestFields(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "db.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	tr, err := trace.New(st, filepath.Join(dir, "traces"), "metadata", time.Hour, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	sess := tr.Start("req-admin-1", "client-x")
	sess.SetRouting("alias", "prov", "up-model", "low", "plug-1", "1.0")
	sess.Finish(429, "error", "", 3, 4, "provider_error", "rate_limit", "too many")

	cfg := &config.Config{}
	adm := &Admin{Cfg: cfg, Plugins: plugin.NewManager(cfg), Trace: tr, Metrics: observability.New()}
	req := httptest.NewRequest(http.MethodGet, "/admin/requests/req-admin-1", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", "req-admin-1")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	rec := httptest.NewRecorder()
	adm.getRequest(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out["http_status"] != float64(429) {
		t.Fatalf("http_status: %v", out["http_status"])
	}
	started, _ := out["started_at"].(float64)
	if started <= 0 {
		t.Fatalf("started_at: %v", out["started_at"])
	}
	if out["full"] != nil {
		t.Fatalf("metadata mode should not include full trace")
	}
}

func TestAdminRequestDetailMetadataNoPrompt(t *testing.T) {
	const prompt = "secret-admin-prompt-value"
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "db.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	tr, err := trace.New(st, filepath.Join(dir, "traces"), "metadata", time.Hour, time.Hour)
	if err != nil {
		t.Fatal(err)
	}

	sess := tr.Start("req-meta-admin", "c1")
	raw := json.RawMessage(`{"model":"m","messages":[{"role":"user","content":"` + prompt + `"}]}`)
	sess.ClientRaw(raw)
	canon := &protocol.CompletionRequest{
		RequestID: "req-meta-admin",
		Model:     "m",
		Messages:  []protocol.Message{{Role: "user", Content: protocol.TextContent(prompt)}},
	}
	sess.Normalized(canon)
	sess.Finish(200, "ok", "stop", 1, 1, "", "", "")

	cfg := &config.Config{}
	adm := &Admin{Cfg: cfg, Plugins: plugin.NewManager(cfg), Trace: tr, Metrics: observability.New()}
	req := httptest.NewRequest(http.MethodGet, "/admin/requests/req-meta-admin", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", "req-meta-admin")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	rec := httptest.NewRecorder()
	adm.getRequest(rec, req)
	body := rec.Body.String()
	if strings.Contains(body, prompt) {
		t.Fatalf("admin detail leaked prompt: %s", body)
	}
	events, err := st.ListEvents(context.Background(), "req-meta-admin")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range events {
		if strings.Contains(string(e.Payload), prompt) {
			t.Fatalf("event leaked prompt: %s", e.Payload)
		}
	}
}
