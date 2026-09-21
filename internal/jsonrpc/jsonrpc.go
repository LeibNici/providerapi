package jsonrpc

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"sync"
	"sync/atomic"
)

type Request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *Error          `json:"error,omitempty"`
}

type Error struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	return e.Message
}

type Handler func(ctx context.Context, req Request) (any, error)

type Conn struct {
	nc       net.Conn
	mu       sync.Mutex
	pending  map[string]chan Request
	nextID   atomic.Int64
	handler  Handler
	notify   func(method string, params json.RawMessage)
	closed   atomic.Bool
	closeErr error
	closeCh  chan struct{}
}

func NewConn(nc net.Conn) *Conn {
	return &Conn{
		nc:      nc,
		pending: map[string]chan Request{},
		closeCh: make(chan struct{}),
	}
}

func (c *Conn) SetHandler(h Handler) { c.handler = h }

func (c *Conn) SetNotifyHandler(h func(method string, params json.RawMessage)) {
	c.notify = h
}

func (c *Conn) Serve() error {
	dec := json.NewDecoder(c.nc)
	for {
		var msg Request
		if err := dec.Decode(&msg); err != nil {
			c.closeWith(err)
			if err == io.EOF {
				return nil
			}
			return err
		}
		hasID := len(msg.ID) > 0 && string(msg.ID) != "null"
		if msg.Method != "" && !hasID {
			c.dispatch(msg)
			continue
		}
		go c.dispatch(msg)
	}
}

func (c *Conn) dispatch(msg Request) {
	hasID := len(msg.ID) > 0 && string(msg.ID) != "null"
	switch {
	case msg.Method != "" && hasID:
		if c.handler == nil {
			_ = c.WriteError(msg.ID, &Error{Code: -32601, Message: "method not found"})
			return
		}
		result, err := c.handler(context.Background(), msg)
		if err != nil {
			if re, ok := err.(*Error); ok {
				_ = c.WriteError(msg.ID, re)
				return
			}
			_ = c.WriteError(msg.ID, &Error{Code: -32000, Message: err.Error()})
			return
		}
		_ = c.WriteResult(msg.ID, result)
	case msg.Method != "" && !hasID:
		if c.notify != nil {
			c.notify(msg.Method, msg.Params)
		}
	case hasID:
		key := string(msg.ID)
		c.mu.Lock()
		ch := c.pending[key]
		if ch != nil {
			delete(c.pending, key)
		}
		c.mu.Unlock()
		if ch != nil {
			ch <- msg
		}
	}
}

func (c *Conn) Call(ctx context.Context, method string, params any, result any) error {
	id := c.nextID.Add(1)
	idRaw, _ := json.Marshal(id)
	ch := make(chan Request, 1)
	c.mu.Lock()
	c.pending[string(idRaw)] = ch
	c.mu.Unlock()

	if err := c.write(Request{
		JSONRPC: "2.0",
		ID:      idRaw,
		Method:  method,
		Params:  mustRaw(params),
	}); err != nil {
		c.mu.Lock()
		delete(c.pending, string(idRaw))
		c.mu.Unlock()
		return err
	}

	select {
	case <-ctx.Done():
		c.mu.Lock()
		delete(c.pending, string(idRaw))
		c.mu.Unlock()
		return ctx.Err()
	case <-c.closeCh:
		return fmt.Errorf("jsonrpc: connection closed: %w", c.closeErr)
	case msg := <-ch:
		if msg.Error != nil {
			return msg.Error
		}
		if result == nil || len(msg.Result) == 0 || string(msg.Result) == "null" {
			return nil
		}
		return json.Unmarshal(msg.Result, result)
	}
}

func (c *Conn) Notify(method string, params any) error {
	return c.write(Request{
		JSONRPC: "2.0",
		Method:  method,
		Params:  mustRaw(params),
	})
}

func (c *Conn) WriteResult(id json.RawMessage, result any) error {
	return c.write(Request{
		JSONRPC: "2.0",
		ID:      id,
		Result:  mustRaw(result),
	})
}

func (c *Conn) WriteError(id json.RawMessage, err *Error) error {
	return c.write(Request{
		JSONRPC: "2.0",
		ID:      id,
		Error:   err,
	})
}

func (c *Conn) write(msg Request) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed.Load() {
		return io.ErrClosedPipe
	}
	enc := json.NewEncoder(c.nc)
	return enc.Encode(msg)
}

func (c *Conn) Close() error {
	c.closeWith(io.EOF)
	return c.nc.Close()
}

func (c *Conn) Done() <-chan struct{} { return c.closeCh }

func (c *Conn) closeWith(err error) {
	if c.closed.CompareAndSwap(false, true) {
		c.closeErr = err
		close(c.closeCh)
	}
}

func mustRaw(v any) json.RawMessage {
	if v == nil {
		return nil
	}
	if raw, ok := v.(json.RawMessage); ok {
		return raw
	}
	b, err := json.Marshal(v)
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return b
}
