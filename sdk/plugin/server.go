package plugin

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/chenming/providerapi/internal/idgen"
	"github.com/chenming/providerapi/internal/jsonrpc"
	"github.com/chenming/providerapi/internal/protocol"
	"github.com/chenming/providerapi/internal/redact"
)

const ProtocolVersion = "1"

type Handler interface {
	Handshake(ctx context.Context, req protocol.HandshakeRequest) (*protocol.HandshakeResult, error)
	Health(ctx context.Context) error
	Models(ctx context.Context) ([]protocol.ModelInfo, error)
	Complete(ctx context.Context, req *protocol.CompletionRequest) (*protocol.CompletionResponse, error)
	Stream(ctx context.Context, req *protocol.CompletionRequest) (<-chan protocol.StreamEvent, error)
}

type Server struct {
	handler Handler
	audit   *AuditEmitter

	mu      sync.Mutex
	conn    *jsonrpc.Conn
	streams map[string]context.CancelFunc
}

func NewServer(h Handler) *Server {
	s := &Server{
		handler: h,
		streams: map[string]context.CancelFunc{},
	}
	s.audit = &AuditEmitter{s: s}
	return s
}

func (s *Server) Audit() *AuditEmitter { return s.audit }

func ListenAndServe(socketPath string, h Handler) error {
	return ListenAndServeWithHook(socketPath, h, nil)
}

func ListenAndServeWithHook(socketPath string, h Handler, hook func(*Server)) error {
	if err := os.MkdirAll(filepath.Dir(socketPath), 0o700); err != nil {
		return err
	}
	_ = os.Remove(socketPath)
	ln, err := net.Listen("unix", socketPath)
	if err != nil {
		return err
	}
	defer ln.Close()
	_ = os.Chmod(socketPath, 0o600)

	srv := NewServer(h)
	if hook != nil {
		hook(srv)
	}
	for {
		conn, err := ln.Accept()
		if err != nil {
			return err
		}
		if err := srv.ServeConn(conn); err != nil {
			return err
		}
	}
}

func (s *Server) ServeConn(nc net.Conn) error {
	c := jsonrpc.NewConn(nc)
	s.mu.Lock()
	s.conn = c
	s.mu.Unlock()
	c.SetHandler(s.handle)
	return c.Serve()
}

func (s *Server) handle(ctx context.Context, req jsonrpc.Request) (any, error) {
	switch req.Method {
	case "plugin.handshake":
		var in protocol.HandshakeRequest
		if err := json.Unmarshal(req.Params, &in); err != nil {
			return nil, err
		}
		return s.handler.Handshake(ctx, in)
	case "plugin.health":
		if err := s.handler.Health(ctx); err != nil {
			return nil, err
		}
		return map[string]string{"status": "ok"}, nil
	case "plugin.models":
		models, err := s.handler.Models(ctx)
		if err != nil {
			return nil, err
		}
		return map[string]any{"models": models}, nil
	case "completion.execute":
		var in struct {
			Request *protocol.CompletionRequest `json:"request"`
		}
		if err := json.Unmarshal(req.Params, &in); err != nil {
			return nil, err
		}
		res, err := s.handler.Complete(ctx, in.Request)
		return res, rpcErr(err)
	case "completion.stream":
		var in struct {
			Request *protocol.CompletionRequest `json:"request"`
		}
		if err := json.Unmarshal(req.Params, &in); err != nil {
			return nil, err
		}
		streamID := idgen.StreamID()
		sctx, cancel := context.WithCancel(context.Background())
		s.mu.Lock()
		s.streams[streamID] = cancel
		s.mu.Unlock()
		ch, err := s.handler.Stream(sctx, in.Request)
		if err != nil {
			cancel()
			s.mu.Lock()
			delete(s.streams, streamID)
			s.mu.Unlock()
			return nil, rpcErr(err)
		}
		go s.forwardStream(streamID, ch, cancel)
		return map[string]string{"stream_id": streamID}, nil
	case "completion.cancel":
		var in struct {
			StreamID string `json:"stream_id"`
		}
		if err := json.Unmarshal(req.Params, &in); err != nil {
			return nil, err
		}
		s.mu.Lock()
		cancel := s.streams[in.StreamID]
		s.mu.Unlock()
		if cancel != nil {
			cancel()
		}
		return map[string]bool{"ok": true}, nil
	default:
		return nil, &jsonrpc.Error{Code: -32601, Message: fmt.Sprintf("method not found: %s", req.Method)}
	}
}

func (s *Server) forwardStream(streamID string, ch <-chan protocol.StreamEvent, cancel context.CancelFunc) {
	defer func() {
		cancel()
		s.mu.Lock()
		delete(s.streams, streamID)
		s.mu.Unlock()
	}()
	seq := 0
	ended := false
	for ev := range ch {
		if ev.Type == protocol.EventStreamEnd || ev.Type == protocol.EventError {
			ended = true
		}
		seq++
		_ = s.notify("plugin.stream.event", map[string]any{
			"stream_id": streamID,
			"seq":       seq,
			"event":     ev,
		})
	}
	if !ended {
		seq++
		_ = s.notify("plugin.stream.event", map[string]any{
			"stream_id": streamID,
			"seq":       seq,
			"event":     protocol.StreamEvent{Type: protocol.EventStreamEnd, FinishReason: "stop"},
		})
	}
}

func (s *Server) notify(method string, params any) error {
	s.mu.Lock()
	c := s.conn
	s.mu.Unlock()
	if c == nil {
		return fmt.Errorf("not connected")
	}
	return c.Notify(method, params)
}

type AuditEmitter struct {
	s *Server
}

type ProviderHTTPAudit struct {
	RequestID string            `json:"request_id,omitempty"`
	StreamID  string            `json:"stream_id,omitempty"`
	Type      string            `json:"type"`
	Method    string            `json:"method,omitempty"`
	URL       string            `json:"url,omitempty"`
	Headers   map[string]string `json:"headers,omitempty"`
	Body      json.RawMessage   `json:"body,omitempty"`
	Status    int               `json:"status,omitempty"`
}

func (a *AuditEmitter) emit(ev ProviderHTTPAudit) {
	if a == nil || a.s == nil {
		return
	}
	ev.Headers = redact.Headers(ev.Headers)
	ev.Body = redact.JSON(ev.Body)
	_ = a.s.notify("plugin.trace.event", ev)
}

func (a *AuditEmitter) ProviderRequest(reqID, method, url string, headers map[string]string, body json.RawMessage) {
	a.emit(ProviderHTTPAudit{
		RequestID: reqID,
		Type:      "provider_request",
		Method:    method,
		URL:       url,
		Headers:   headers,
		Body:      body,
	})
}

func (a *AuditEmitter) ProviderResponse(reqID, method, url string, status int, headers map[string]string, body json.RawMessage) {
	a.emit(ProviderHTTPAudit{
		RequestID: reqID,
		Type:      "provider_response",
		Method:    method,
		URL:       url,
		Headers:   headers,
		Body:      body,
		Status:    status,
	})
}

func (a *AuditEmitter) ProviderStreamEvent(reqID, kind string, extra map[string]any) {
	if a == nil || a.s == nil {
		return
	}
	if extra == nil {
		extra = map[string]any{}
	}
	extra["request_id"] = reqID
	extra["type"] = kind
	_ = a.s.notify("plugin.trace.event", extra)
}

func WaitSocket(path string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return nil
		}
		time.Sleep(20 * time.Millisecond)
	}
	return fmt.Errorf("plugin socket %s not ready", path)
}

func rpcErr(err error) error {
	if err == nil {
		return nil
	}
	if pe, ok := err.(*protocol.ProviderError); ok {
		b, _ := json.Marshal(pe)
		return &jsonrpc.Error{Code: -32000, Message: pe.Message, Data: b}
	}
	return err
}
