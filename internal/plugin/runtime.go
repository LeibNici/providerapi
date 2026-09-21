package plugin

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/chenming/providerapi/internal/config"
	"github.com/chenming/providerapi/internal/jsonrpc"
	"github.com/chenming/providerapi/internal/protocol"
	sdkplugin "github.com/chenming/providerapi/sdk/plugin"
)

type StreamHandle struct {
	ID     string
	once   sync.Once
	cancel func()
}

func (h *StreamHandle) Cancel() {
	if h == nil {
		return
	}
	h.once.Do(func() {
		if h.cancel != nil {
			h.cancel()
		}
	})
}

type Instance struct {
	ID       string
	Plugin   string
	Manifest protocol.Manifest
	Healthy  bool
	PID      int

	cfg  config.ProviderInstance
	bin  config.PluginBinary
	cred config.Credential
	dir  string

	mu   sync.Mutex
	cmd  *exec.Cmd
	conn *jsonrpc.Conn
	sock string

	streams    map[string]chan protocol.StreamEvent
	pending    map[string][]protocol.StreamEvent
	onTrace    map[string]func(json.RawMessage)
	stopCh     chan struct{}
	stopped    bool
	healthOnce sync.Once
}

func (in *Instance) SetTraceHandler(requestID string, h func(json.RawMessage)) {
	in.mu.Lock()
	defer in.mu.Unlock()
	if in.onTrace == nil {
		in.onTrace = map[string]func(json.RawMessage){}
	}
	if h == nil {
		delete(in.onTrace, requestID)
		return
	}
	in.onTrace[requestID] = h
}

func (in *Instance) Start(ctx context.Context) error {
	in.mu.Lock()
	in.streams = map[string]chan protocol.StreamEvent{}
	in.pending = map[string][]protocol.StreamEvent{}
	if in.onTrace == nil {
		in.onTrace = map[string]func(json.RawMessage){}
	}
	if in.stopCh == nil {
		in.stopCh = make(chan struct{})
	}
	in.mu.Unlock()

	if err := os.MkdirAll(in.dir, 0o700); err != nil {
		return err
	}
	in.sock = filepath.Join(in.dir, in.ID+".sock")
	_ = os.Remove(in.sock)

	cmd := exec.Command(in.bin.Command, append(in.bin.Args, "--socket", in.sock)...)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	cmd.Env = pluginEnv(in.cred)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start plugin %s: %w", in.ID, err)
	}
	in.cmd = cmd
	in.PID = cmd.Process.Pid

	if err := sdkplugin.WaitSocket(in.sock, 10*time.Second); err != nil {
		_ = in.kill()
		return err
	}
	nc, err := net.Dial("unix", in.sock)
	if err != nil {
		_ = in.kill()
		return err
	}
	conn := jsonrpc.NewConn(nc)
	conn.SetNotifyHandler(in.onNotify)
	go func() { _ = conn.Serve() }()
	in.conn = conn

	hs := protocol.HandshakeRequest{
		ProtocolVersion: sdkplugin.ProtocolVersion,
		InstanceID:      in.ID,
		Config:          mustJSON(in.cfg.Config),
	}
	var result protocol.HandshakeResult
	hctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := conn.Call(hctx, "plugin.handshake", hs, &result); err != nil {
		_ = in.kill()
		return fmt.Errorf("handshake %s: %w", in.ID, err)
	}
	if result.Manifest.ProtocolVersion != sdkplugin.ProtocolVersion && result.Manifest.ProtocolVersion != "" {
		_ = in.kill()
		return fmt.Errorf("plugin %s protocol %s unsupported", in.ID, result.Manifest.ProtocolVersion)
	}
	in.Manifest = result.Manifest
	in.Healthy = true
	go in.watch(cmd)
	in.healthOnce.Do(func() { go in.healthLoop() })
	return nil
}

func (in *Instance) watch(cmd *exec.Cmd) {
	_ = cmd.Wait()
	in.mu.Lock()
	stopped := in.stopped
	in.Healthy = false
	in.mu.Unlock()
	if stopped {
		return
	}
	in.failActiveStreams(protocol.PluginCrash(""))
	time.Sleep(time.Second)
	in.mu.Lock()
	if in.stopped {
		in.mu.Unlock()
		return
	}
	in.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	_ = in.Start(ctx)
}

func (in *Instance) healthLoop() {
	t := time.NewTicker(15 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-in.stopCh:
			return
		case <-t.C:
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			err := in.Health(ctx)
			cancel()
			in.mu.Lock()
			in.Healthy = err == nil
			in.mu.Unlock()
		}
	}
}

func (in *Instance) failActiveStreams(err *protocol.ProviderError) {
	if err == nil {
		err = protocol.PluginCrash("")
	}
	in.mu.Lock()
	snapshot := make(map[string]chan protocol.StreamEvent, len(in.streams))
	for id, ch := range in.streams {
		snapshot[id] = ch
		delete(in.streams, id)
	}
	in.pending = map[string][]protocol.StreamEvent{}
	in.mu.Unlock()

	ev := protocol.StreamEvent{Type: protocol.EventError, Error: err}
	for _, ch := range snapshot {
		func() {
			defer func() { _ = recover() }()
			select {
			case ch <- ev:
			default:
			}
		}()
		func() {
			defer func() { _ = recover() }()
			close(ch)
		}()
	}
}

func (in *Instance) closeStreamLocked(id string) {
	if ch := in.streams[id]; ch != nil {
		delete(in.streams, id)
		close(ch)
	}
	delete(in.pending, id)
}

func (in *Instance) closeStream(id string) {
	in.mu.Lock()
	defer in.mu.Unlock()
	in.closeStreamLocked(id)
}

func (in *Instance) onNotify(method string, params json.RawMessage) {
	switch method {
	case "plugin.stream.event":
		var n struct {
			StreamID string               `json:"stream_id"`
			Seq      int                  `json:"seq"`
			Event    protocol.StreamEvent `json:"event"`
		}
		if err := json.Unmarshal(params, &n); err != nil {
			return
		}
		in.mu.Lock()
		ch := in.streams[n.StreamID]
		if ch == nil {
			in.pending[n.StreamID] = append(in.pending[n.StreamID], n.Event)
			in.mu.Unlock()
			return
		}
		in.mu.Unlock()
		func() {
			defer func() { _ = recover() }()
			ch <- n.Event
		}()
		if n.Event.Type == protocol.EventStreamEnd || n.Event.Type == protocol.EventError {
			in.closeStream(n.StreamID)
		}
	case "plugin.trace.event":
		var meta struct {
			RequestID string `json:"request_id"`
		}
		_ = json.Unmarshal(params, &meta)
		in.mu.Lock()
		h := in.onTrace[meta.RequestID]
		in.mu.Unlock()
		if h != nil {
			h(params)
		}
	}
}

func (in *Instance) Health(ctx context.Context) error {
	in.mu.Lock()
	c := in.conn
	in.mu.Unlock()
	if c == nil {
		return fmt.Errorf("not connected")
	}
	var out map[string]string
	return c.Call(ctx, "plugin.health", map[string]any{}, &out)
}

func (in *Instance) Models(ctx context.Context) ([]protocol.ModelInfo, error) {
	var out struct {
		Models []protocol.ModelInfo `json:"models"`
	}
	if err := in.conn.Call(ctx, "plugin.models", map[string]any{}, &out); err != nil {
		return nil, err
	}
	return out.Models, nil
}

func (in *Instance) Complete(ctx context.Context, req *protocol.CompletionRequest) (*protocol.CompletionResponse, error) {
	var out protocol.CompletionResponse
	if err := in.conn.Call(ctx, "completion.execute", map[string]any{"request": req}, &out); err != nil {
		return nil, decodeRPCError(err)
	}
	return &out, nil
}

func (in *Instance) Stream(ctx context.Context, req *protocol.CompletionRequest) (<-chan protocol.StreamEvent, *StreamHandle, error) {
	var out struct {
		StreamID string `json:"stream_id"`
	}
	if err := in.conn.Call(ctx, "completion.stream", map[string]any{"request": req}, &out); err != nil {
		return nil, nil, decodeRPCError(err)
	}
	ch := make(chan protocol.StreamEvent, 64)
	in.mu.Lock()
	ended := false
	for _, ev := range in.pending[out.StreamID] {
		ch <- ev
		if ev.Type == protocol.EventStreamEnd || ev.Type == protocol.EventError {
			ended = true
		}
	}
	delete(in.pending, out.StreamID)
	if ended {
		close(ch)
	} else {
		in.streams[out.StreamID] = ch
	}
	in.mu.Unlock()

	h := &StreamHandle{ID: out.StreamID, cancel: func() {
		cctx, ccancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer ccancel()
		_ = in.conn.Call(cctx, "completion.cancel", map[string]any{"stream_id": out.StreamID}, nil)
		in.closeStream(out.StreamID)
	}}
	go func() {
		<-ctx.Done()
		h.Cancel()
	}()
	return ch, h, nil
}

func (in *Instance) Stop() {
	in.mu.Lock()
	if in.stopped {
		in.mu.Unlock()
		return
	}
	in.stopped = true
	close(in.stopCh)
	in.mu.Unlock()
	in.failActiveStreams(protocol.NewProviderError(499, "plugin_stopped", "plugin stopped"))
	if in.conn != nil {
		_ = in.conn.Close()
	}
	_ = in.kill()
	_ = os.Remove(in.sock)
}

func (in *Instance) kill() error {
	if in.cmd == nil || in.cmd.Process == nil {
		return nil
	}
	pgid, err := syscall.Getpgid(in.cmd.Process.Pid)
	if err == nil {
		_ = syscall.Kill(-pgid, syscall.SIGTERM)
	} else {
		_ = in.cmd.Process.Signal(syscall.SIGTERM)
	}
	done := make(chan struct{})
	go func() {
		_, _ = in.cmd.Process.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		if pgid, err := syscall.Getpgid(in.cmd.Process.Pid); err == nil {
			_ = syscall.Kill(-pgid, syscall.SIGKILL)
		} else {
			_ = in.cmd.Process.Kill()
		}
	}
	return nil
}

type Manager struct {
	cfg       *config.Config
	mu        sync.RWMutex
	instances map[string]*Instance
}

func NewManager(cfg *config.Config) *Manager {
	return &Manager{cfg: cfg, instances: map[string]*Instance{}}
}

func (m *Manager) StartAll(ctx context.Context) error {
	dir := filepath.Join(m.cfg.DataDir(), "plugins")
	for id, inst := range m.cfg.Providers {
		bin := m.cfg.Plugins[inst.Plugin]
		var cred config.Credential
		if inst.Credential != "" {
			cred = m.cfg.Credentials[inst.Credential]
		}
		in := &Instance{
			ID:     id,
			Plugin: inst.Plugin,
			cfg:    inst,
			bin:    bin,
			cred:   cred,
			dir:    dir,
		}
		if err := in.Start(ctx); err != nil {
			m.StopAll()
			return err
		}
		m.mu.Lock()
		m.instances[id] = in
		m.mu.Unlock()
	}
	return nil
}

func (m *Manager) StopAll() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, in := range m.instances {
		in.Stop()
	}
}

func (m *Manager) Get(id string) (*Instance, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	in := m.instances[id]
	if in == nil {
		return nil, fmt.Errorf("provider instance %q is not running", id)
	}
	return in, nil
}

func (m *Manager) List() []*Instance {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]*Instance, 0, len(m.instances))
	for _, in := range m.instances {
		out = append(out, in)
	}
	return out
}

func decodeRPCError(err error) error {
	if err == nil {
		return nil
	}
	if je, ok := err.(*jsonrpc.Error); ok && len(je.Data) > 0 {
		var pe protocol.ProviderError
		if json.Unmarshal(je.Data, &pe) == nil && pe.Message != "" {
			if pe.HTTPStatus == 0 {
				pe.HTTPStatus = 502
			}
			return &pe
		}
	}
	return protocol.AsProviderError(err)
}

func pluginEnv(cred config.Credential) []string {
	env := make([]string, 0, len(os.Environ()))
	for _, e := range os.Environ() {
		if strings.HasPrefix(e, "PROVIDERAPI_CREDENTIAL=") {
			continue
		}
		env = append(env, e)
	}
	if cred.Env != "" {
		if v := os.Getenv(cred.Env); v != "" {
			env = append(env, "PROVIDERAPI_CREDENTIAL="+v)
		}
	}
	return env
}

func mustJSON(v any) json.RawMessage {
	if v == nil {
		return json.RawMessage(`{}`)
	}
	b, err := json.Marshal(v)
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return b
}
