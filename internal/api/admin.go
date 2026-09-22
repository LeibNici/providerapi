package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strings"

	"github.com/LeibNici/providerapi/internal/api/console"
	"github.com/LeibNici/providerapi/internal/config"
	"github.com/LeibNici/providerapi/internal/model"
	"github.com/LeibNici/providerapi/internal/observability"
	"github.com/LeibNici/providerapi/internal/plugin"
	"github.com/LeibNici/providerapi/internal/trace"
	"github.com/go-chi/chi/v5"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

type Admin struct {
	Cfg     *config.Config
	Plugins *plugin.Manager
	Trace   *trace.Service
	Metrics *observability.Metrics
}

func (a *Admin) Routes() http.Handler {
	r := chi.NewRouter()
	r.Use(a.auth)
	r.Get("/health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	r.Get("/admin/requests", a.listRequests)
	r.Get("/admin/requests/{id}", a.getRequest)
	r.Get("/admin/plugins", a.listPlugins)
	r.Get("/admin/models", a.listModels)
	r.Handle("/metrics", promhttp.HandlerFor(a.Metrics.Registry, promhttp.HandlerOpts{}))
	r.Mount("/", console.Handler())
	return r
}

func (a *Admin) auth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if a.Cfg.Admin.Token == "" {
			next.ServeHTTP(w, r)
			return
		}
		got := r.Header.Get("X-Admin-Token")
		if got == "" {
			h := r.Header.Get("Authorization")
			got = strings.TrimPrefix(h, "Bearer ")
		}
		if got != a.Cfg.Admin.Token {
			writeJSON(w, http.StatusUnauthorized, map[string]any{
				"error": map[string]any{"message": "unauthorized", "type": "auth_error", "code": "unauthorized"},
			})
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (a *Admin) listRequests(w http.ResponseWriter, r *http.Request) {
	rows, err := a.Trace.Store().ListRequests(r.Context(), 100)
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]any{"data": rows})
}

func (a *Admin) getRequest(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	row, err := a.Trace.Store().GetRequest(r.Context(), id)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			writeJSON(w, 404, map[string]string{"error": "not found"})
			return
		}
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	events, _ := a.Trace.Store().ListEvents(r.Context(), id)
	full, _ := a.Trace.LoadFull(id)
	timeline := make([]any, 0, len(events))
	for _, e := range events {
		timeline = append(timeline, map[string]any{
			"seq":     e.Seq,
			"ts":      e.TS,
			"type":    e.Type,
			"payload": json.RawMessage(e.Payload),
		})
	}
	out := map[string]any{
		"request_id":  row.ID,
		"http_status": row.HTTPStatus,
		"started_at":  row.StartedAt,
		"client": map[string]any{
			"model":             row.ClientModel,
			"client_request_id": row.ClientRequestID,
		},
		"normalized": map[string]any{
			"reasoning": row.Reasoning,
		},
		"routing": map[string]any{
			"provider":       row.Provider,
			"upstream_model": row.UpstreamModel,
			"plugin":         row.PluginID,
			"plugin_version": row.PluginVersion,
		},
		"upstream": map[string]any{
			"model": row.UpstreamModel,
		},
		"timeline": timeline,
		"usage": map[string]any{
			"input_tokens":  row.InputTokens,
			"output_tokens": row.OutputTokens,
			"ttft_ms":       row.TTFTMs,
			"duration_ms":   row.DurationMs,
		},
		"error": nil,
	}
	if row.ErrorMessage != "" {
		out["error"] = map[string]any{"type": row.ErrorType, "code": row.ErrorCode, "message": row.ErrorMessage}
	}
	if len(full) > 0 {
		var payload any
		_ = json.Unmarshal(full, &payload)
		out["full"] = payload
	}
	writeJSON(w, 200, out)
}

func (a *Admin) listPlugins(w http.ResponseWriter, r *http.Request) {
	var data []map[string]any
	for _, in := range a.Plugins.List() {
		a.Metrics.PluginHealth.WithLabelValues(in.ID).Set(boolGauge(in.Healthy))
		row := map[string]any{
			"instance_id":      in.ID,
			"plugin":           in.Plugin,
			"version":          in.Manifest.Version,
			"protocol_version": in.Manifest.ProtocolVersion,
			"healthy":          in.Healthy,
			"pid":              in.PID,
			"capabilities":     in.Manifest.Capabilities,
		}
		if errMsg := in.LastError(); errMsg != "" {
			row["last_error"] = errMsg
		}
		data = append(data, row)
	}
	writeJSON(w, 200, map[string]any{"data": data})
}

func (a *Admin) listModels(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{"data": model.ListAdminModels(a.Cfg)})
}

func boolGauge(v bool) float64 {
	if v {
		return 1
	}
	return 0
}
