package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/chenming/providerapi/internal/fsutil"
	_ "modernc.org/sqlite"
)

type Store struct {
	DB *sql.DB
}

func Open(path string) (*Store, error) {
	if err := fsutil.EnsurePrivateDir(fsutil.DirOf(path)); err != nil {
		return nil, err
	}
	dsn := path + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	s := &Store{DB: db}
	if err := s.migrate(); err != nil {
		_ = db.Close()
		return nil, err
	}
	_ = fsutil.EnsurePrivateFile(path)
	return s, nil
}

func (s *Store) Close() error { return s.DB.Close() }

func (s *Store) migrate() error {
	_, err := s.DB.Exec(`
CREATE TABLE IF NOT EXISTS requests (
  id TEXT PRIMARY KEY,
  client_request_id TEXT,
  started_at INTEGER NOT NULL,
  finished_at INTEGER,
  client_model TEXT,
  provider TEXT,
  upstream_model TEXT,
  reasoning TEXT,
  status TEXT,
  finish_reason TEXT,
  http_status INTEGER,
  input_tokens INTEGER,
  output_tokens INTEGER,
  ttft_ms INTEGER,
  duration_ms INTEGER,
  error_type TEXT,
  error_message TEXT,
  plugin_id TEXT,
  plugin_version TEXT
);
CREATE TABLE IF NOT EXISTS request_events (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  request_id TEXT NOT NULL,
  seq INTEGER NOT NULL,
  ts INTEGER NOT NULL,
  type TEXT NOT NULL,
  payload TEXT
);
CREATE TABLE IF NOT EXISTS plugins (
  instance_id TEXT PRIMARY KEY,
  plugin TEXT,
  version TEXT,
  protocol_version TEXT,
  healthy INTEGER,
  last_seen INTEGER,
  pid INTEGER
);
CREATE INDEX IF NOT EXISTS idx_requests_started ON requests(started_at DESC);
CREATE INDEX IF NOT EXISTS idx_events_req ON request_events(request_id, seq);
`)
	return err
}

type RequestRow struct {
	ID              string `json:"id"`
	ClientRequestID string `json:"client_request_id"`
	StartedAt       int64  `json:"started_at"`
	FinishedAt      int64  `json:"finished_at"`
	ClientModel     string `json:"client_model"`
	Provider        string `json:"provider"`
	UpstreamModel   string `json:"upstream_model"`
	Reasoning       string `json:"reasoning"`
	Status          string `json:"status"`
	FinishReason    string `json:"finish_reason"`
	HTTPStatus      int    `json:"http_status"`
	InputTokens     int    `json:"input_tokens"`
	OutputTokens    int    `json:"output_tokens"`
	TTFTMs          int    `json:"ttft_ms"`
	DurationMs      int    `json:"duration_ms"`
	ErrorType       string `json:"error_type"`
	ErrorMessage    string `json:"error_message"`
	PluginID        string `json:"plugin_id"`
	PluginVersion   string `json:"plugin_version"`
}

func (s *Store) UpsertRequest(ctx context.Context, r RequestRow) error {
	_, err := s.DB.ExecContext(ctx, `
INSERT INTO requests (
  id, client_request_id, started_at, finished_at, client_model, provider, upstream_model,
  reasoning, status, finish_reason, http_status, input_tokens, output_tokens, ttft_ms,
  duration_ms, error_type, error_message, plugin_id, plugin_version
) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
ON CONFLICT(id) DO UPDATE SET
  client_request_id=excluded.client_request_id,
  finished_at=excluded.finished_at,
  client_model=excluded.client_model,
  provider=excluded.provider,
  upstream_model=excluded.upstream_model,
  reasoning=excluded.reasoning,
  status=excluded.status,
  finish_reason=excluded.finish_reason,
  http_status=excluded.http_status,
  input_tokens=excluded.input_tokens,
  output_tokens=excluded.output_tokens,
  ttft_ms=excluded.ttft_ms,
  duration_ms=excluded.duration_ms,
  error_type=excluded.error_type,
  error_message=excluded.error_message,
  plugin_id=excluded.plugin_id,
  plugin_version=excluded.plugin_version
`, r.ID, r.ClientRequestID, r.StartedAt, nullInt(r.FinishedAt), r.ClientModel, r.Provider, r.UpstreamModel,
		r.Reasoning, r.Status, r.FinishReason, r.HTTPStatus, r.InputTokens, r.OutputTokens, r.TTFTMs,
		r.DurationMs, r.ErrorType, r.ErrorMessage, r.PluginID, r.PluginVersion)
	return err
}

func (s *Store) InsertEvent(ctx context.Context, requestID string, seq int, typ string, payload any) error {
	var raw string
	if payload != nil {
		b, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		raw = string(b)
	}
	_, err := s.DB.ExecContext(ctx, `INSERT INTO request_events (request_id, seq, ts, type, payload) VALUES (?,?,?,?,?)`,
		requestID, seq, time.Now().UnixMilli(), typ, raw)
	return err
}

func (s *Store) ListRequests(ctx context.Context, limit int) ([]RequestRow, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT id, client_request_id, started_at, IFNULL(finished_at,0),
		IFNULL(client_model,''), IFNULL(provider,''), IFNULL(upstream_model,''), IFNULL(reasoning,''),
		IFNULL(status,''), IFNULL(finish_reason,''), IFNULL(http_status,0), IFNULL(input_tokens,0),
		IFNULL(output_tokens,0), IFNULL(ttft_ms,0), IFNULL(duration_ms,0), IFNULL(error_type,''),
		IFNULL(error_message,''), IFNULL(plugin_id,''), IFNULL(plugin_version,'')
		FROM requests ORDER BY started_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RequestRow
	for rows.Next() {
		var r RequestRow
		if err := rows.Scan(&r.ID, &r.ClientRequestID, &r.StartedAt, &r.FinishedAt, &r.ClientModel, &r.Provider,
			&r.UpstreamModel, &r.Reasoning, &r.Status, &r.FinishReason, &r.HTTPStatus, &r.InputTokens,
			&r.OutputTokens, &r.TTFTMs, &r.DurationMs, &r.ErrorType, &r.ErrorMessage, &r.PluginID, &r.PluginVersion); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) GetRequest(ctx context.Context, id string) (*RequestRow, error) {
	r := &RequestRow{}
	err := s.DB.QueryRowContext(ctx, `SELECT id, client_request_id, started_at, IFNULL(finished_at,0),
		IFNULL(client_model,''), IFNULL(provider,''), IFNULL(upstream_model,''), IFNULL(reasoning,''),
		IFNULL(status,''), IFNULL(finish_reason,''), IFNULL(http_status,0), IFNULL(input_tokens,0),
		IFNULL(output_tokens,0), IFNULL(ttft_ms,0), IFNULL(duration_ms,0), IFNULL(error_type,''),
		IFNULL(error_message,''), IFNULL(plugin_id,''), IFNULL(plugin_version,'')
		FROM requests WHERE id=?`, id).Scan(&r.ID, &r.ClientRequestID, &r.StartedAt, &r.FinishedAt, &r.ClientModel, &r.Provider,
		&r.UpstreamModel, &r.Reasoning, &r.Status, &r.FinishReason, &r.HTTPStatus, &r.InputTokens,
		&r.OutputTokens, &r.TTFTMs, &r.DurationMs, &r.ErrorType, &r.ErrorMessage, &r.PluginID, &r.PluginVersion)
	if err == sql.ErrNoRows {
		return nil, os.ErrNotExist
	}
	return r, err
}

type EventRow struct {
	Seq     int
	TS      int64
	Type    string
	Payload json.RawMessage
}

func (s *Store) ListEvents(ctx context.Context, requestID string) ([]EventRow, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT seq, ts, type, IFNULL(payload,'') FROM request_events WHERE request_id=? ORDER BY seq`, requestID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []EventRow
	for rows.Next() {
		var e EventRow
		var payload string
		if err := rows.Scan(&e.Seq, &e.TS, &e.Type, &payload); err != nil {
			return nil, err
		}
		if payload == "" {
			e.Payload = json.RawMessage("null")
		} else {
			e.Payload = json.RawMessage(payload)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (s *Store) UpsertPlugin(ctx context.Context, instanceID, plugin, version, proto string, healthy bool, pid int) error {
	_, err := s.DB.ExecContext(ctx, `INSERT INTO plugins (instance_id, plugin, version, protocol_version, healthy, last_seen, pid)
VALUES (?,?,?,?,?,?,?)
ON CONFLICT(instance_id) DO UPDATE SET plugin=excluded.plugin, version=excluded.version,
protocol_version=excluded.protocol_version, healthy=excluded.healthy, last_seen=excluded.last_seen, pid=excluded.pid`,
		instanceID, plugin, version, proto, boolInt(healthy), time.Now().UnixMilli(), pid)
	return err
}

func (s *Store) ListPlugins(ctx context.Context) ([]map[string]any, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT instance_id, plugin, version, protocol_version, healthy, last_seen, pid FROM plugins`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []map[string]any
	for rows.Next() {
		var id, p, v, proto string
		var healthy, last, pid int
		if err := rows.Scan(&id, &p, &v, &proto, &healthy, &last, &pid); err != nil {
			return nil, err
		}
		out = append(out, map[string]any{
			"instance_id":      id,
			"plugin":           p,
			"version":          v,
			"protocol_version": proto,
			"healthy":          healthy == 1,
			"last_seen":        last,
			"pid":              pid,
		})
	}
	return out, rows.Err()
}

func (s *Store) DeleteExpired(ctx context.Context, cutoffMs int64) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM request_events WHERE request_id IN (SELECT id FROM requests WHERE started_at < ?)`, cutoffMs)
	if err != nil {
		return err
	}
	_, err = s.DB.ExecContext(ctx, `DELETE FROM requests WHERE started_at < ?`, cutoffMs)
	return err
}

func nullInt(v int64) any {
	if v == 0 {
		return nil
	}
	return v
}

func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

func (s *Store) Ping() error {
	if s == nil || s.DB == nil {
		return fmt.Errorf("store closed")
	}
	return s.DB.Ping()
}
