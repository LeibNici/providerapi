package trace

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/chenming/providerapi/internal/fsutil"
	"github.com/chenming/providerapi/internal/redact"
	"github.com/chenming/providerapi/internal/store"
)

type Level string

const (
	LevelOff      Level = "off"
	LevelMetadata Level = "metadata"
	LevelFull     Level = "full"
)

type Payload struct {
	ClientRaw        any `json:"client_raw,omitempty"`
	Normalized       any `json:"normalized,omitempty"`
	Routing          any `json:"routing,omitempty"`
	PluginRequest    any `json:"plugin_request,omitempty"`
	ProviderRequest  any `json:"provider_request,omitempty"`
	ProviderResponse any `json:"provider_response,omitempty"`
	ClientResponse   any `json:"client_response,omitempty"`
	SSE              any `json:"sse,omitempty"`
}

type Session struct {
	svc     *Service
	mu      sync.Mutex
	row     store.RequestRow
	seq     int
	payload Payload
	sse     []any
	level   Level
}

type Service struct {
	store   *store.Store
	dir     string
	level   Level
	retain  time.Duration
	fullRet time.Duration
}

func New(st *store.Store, tracesDir string, level string, retain, fullRetain time.Duration) (*Service, error) {
	if err := fsutil.EnsurePrivateDir(tracesDir); err != nil {
		return nil, err
	}
	lv := Level(level)
	if lv == "" {
		lv = LevelMetadata
	}
	return &Service{store: st, dir: tracesDir, level: lv, retain: retain, fullRet: fullRetain}, nil
}

func (s *Service) Start(id, clientRequestID string) *Session {
	sess := &Session{
		svc:   s,
		level: s.level,
		row: store.RequestRow{
			ID:              id,
			ClientRequestID: clientRequestID,
			StartedAt:       time.Now().UnixMilli(),
			Status:          "started",
		},
	}
	if s.level != LevelOff {
		_ = s.store.UpsertRequest(context.Background(), sess.row)
	}
	return sess
}

func (sess *Session) event(typ string, payload any) {
	if sess.svc.level == LevelOff {
		return
	}
	sess.mu.Lock()
	sess.seq++
	seq := sess.seq
	sess.mu.Unlock()
	_ = sess.svc.store.InsertEvent(context.Background(), sess.row.ID, seq, typ, redact.Any(payload))
}

func (sess *Session) SetRouting(clientModel, provider, upstream, reasoning, pluginID, pluginVer string) {
	sess.mu.Lock()
	sess.row.ClientModel = clientModel
	sess.row.Provider = provider
	sess.row.UpstreamModel = upstream
	sess.row.Reasoning = reasoning
	sess.row.PluginID = pluginID
	sess.row.PluginVersion = pluginVer
	sess.payload.Routing = map[string]any{
		"client_model":   clientModel,
		"provider":       provider,
		"upstream_model": upstream,
		"reasoning":      reasoning,
		"plugin":         pluginID,
		"plugin_version": pluginVer,
	}
	sess.mu.Unlock()
	sess.event("routing", sess.payload.Routing)
}

func (sess *Session) ClientRaw(v any) {
	sess.mu.Lock()
	level := sess.level
	if level == LevelFull {
		sess.payload.ClientRaw = redact.Any(v)
	} else {
		sess.payload.ClientRaw = summarizeClientRaw(v)
	}
	sess.mu.Unlock()
	if level == LevelFull {
		sess.event("client_raw", redact.Any(v))
	} else {
		sess.event("client_raw", summarizeClientRaw(v))
	}
}

func (sess *Session) Normalized(v any) {
	sess.mu.Lock()
	level := sess.level
	if level == LevelFull {
		sess.payload.Normalized = redact.Any(v)
	} else {
		sess.payload.Normalized = summarizeCompletionRequest(v)
	}
	sess.mu.Unlock()
	if level == LevelFull {
		sess.event("normalized", redact.Any(v))
	} else {
		sess.event("normalized", summarizeCompletionRequest(v))
	}
}

func (sess *Session) PluginRequest(v any) {
	sess.mu.Lock()
	level := sess.level
	if level == LevelFull {
		sess.payload.PluginRequest = redact.Any(v)
	} else {
		sess.payload.PluginRequest = summarizeCompletionRequest(v)
	}
	sess.mu.Unlock()
	if level == LevelFull {
		sess.event("plugin_request", redact.Any(v))
	} else {
		sess.event("plugin_request", summarizeCompletionRequest(v))
	}
}

func (sess *Session) ProviderAudit(raw json.RawMessage) {
	redacted := redact.JSON(raw)
	var m map[string]any
	_ = json.Unmarshal(redacted, &m)
	typ, _ := m["type"].(string)
	sess.mu.Lock()
	level := sess.level
	if level == LevelFull {
		switch typ {
		case "provider_request":
			sess.payload.ProviderRequest = m
		case "provider_response":
			sess.payload.ProviderResponse = m
		}
	} else {
		summary := summarizeProviderAudit(m)
		switch typ {
		case "provider_request":
			sess.payload.ProviderRequest = summary
		case "provider_response":
			sess.payload.ProviderResponse = summary
		}
	}
	sess.mu.Unlock()
	if typ == "" {
		typ = "provider_audit"
	}
	if level == LevelFull {
		sess.event(typ, m)
		return
	}
	if typ == "provider_request" || typ == "provider_response" {
		sess.event(typ, summarizeProviderAudit(m))
		return
	}
	sess.event(typ, map[string]any{"type": typ})
}

func (sess *Session) StreamMeta(kind string) {
	if sess.level == LevelFull {
		return
	}
	sess.event(kind, map[string]string{"event": kind})
}

func (sess *Session) SSE(chunk any) {
	if sess.level != LevelFull {
		return
	}
	sess.mu.Lock()
	sess.sse = append(sess.sse, chunk)
	sess.payload.SSE = sess.sse
	sess.mu.Unlock()
}

func (sess *Session) ClientResponse(v any) {
	sess.mu.Lock()
	sess.payload.ClientResponse = redact.Any(v)
	sess.mu.Unlock()
	if sess.level == LevelFull {
		sess.event("client_response", v)
	}
}

func (sess *Session) MarkTTFT(start time.Time) {
	sess.mu.Lock()
	if sess.row.TTFTMs == 0 {
		sess.row.TTFTMs = int(time.Since(start).Milliseconds())
	}
	sess.mu.Unlock()
}

func (sess *Session) Finish(httpStatus int, status, finish string, inTok, outTok int, errType, errCode, errMsg string) {
	sess.mu.Lock()
	sess.row.FinishedAt = time.Now().UnixMilli()
	sess.row.DurationMs = int(sess.row.FinishedAt - sess.row.StartedAt)
	sess.row.HTTPStatus = httpStatus
	sess.row.Status = status
	sess.row.FinishReason = finish
	sess.row.InputTokens = inTok
	sess.row.OutputTokens = outTok
	sess.row.ErrorType = errType
	sess.row.ErrorCode = errCode
	sess.row.ErrorMessage = errMsg
	row := sess.row
	payload := sess.payload
	level := sess.level
	sess.mu.Unlock()
	if level == LevelOff {
		return
	}
	_ = sess.svc.store.UpsertRequest(context.Background(), row)
	if level == LevelFull {
		_ = sess.svc.writeFull(row.ID, payload)
	}
}

func (sess *Session) Row() store.RequestRow {
	sess.mu.Lock()
	defer sess.mu.Unlock()
	return sess.row
}

func (s *Service) writeFull(id string, payload Payload) error {
	path := filepath.Join(s.dir, id+".json.gz")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	gz := gzip.NewWriter(f)
	enc := json.NewEncoder(gz)
	if err := enc.Encode(redact.Any(payload)); err != nil {
		_ = gz.Close()
		return err
	}
	if err := gz.Close(); err != nil {
		return err
	}
	return fsutil.EnsurePrivateFile(path)
}

func (s *Service) LoadFull(id string) (json.RawMessage, error) {
	path := filepath.Join(s.dir, id+".json.gz")
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return nil, err
	}
	defer gz.Close()
	var raw json.RawMessage
	if err := json.NewDecoder(gz).Decode(&raw); err != nil {
		return nil, err
	}
	return raw, nil
}

func (s *Service) RetentionLoop(ctx context.Context) {
	t := time.NewTicker(time.Hour)
	defer t.Stop()
	for {
		s.sweep()
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (s *Service) sweep() {
	metaCutoff := time.Now().Add(-s.retain).UnixMilli()
	fullCutoff := time.Now().Add(-s.fullRet).UnixMilli()
	_ = s.store.DeleteExpired(context.Background(), metaCutoff)
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		info, err := e.Info()
		if err != nil {
			continue
		}
		if info.ModTime().UnixMilli() < fullCutoff {
			_ = os.Remove(filepath.Join(s.dir, e.Name()))
		}
	}
}

func (s *Service) Store() *store.Store { return s.store }
