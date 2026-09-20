// Package bridge implements the agent client that connects an MCP server to
// the browser-mcp hub. Each opencode session runs one Client; the hub owns the
// single connection to the Chrome extension and routes requests here.
package bridge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/metruzanca/browser-mcp/internal/daemon"
)

var (
	// ErrNotConnected is returned when no browser extension is reachable.
	ErrNotConnected = errors.New("no browser extension connected")
	// ErrNoHub is returned when the hub daemon cannot be reached or started.
	ErrNoHub = errors.New("browser-mcp hub is not running")
	// ErrNoExtension is the hub's reply when it is up but no extension is
	// attached; the client surfaces it as a tool result.
	ErrNoExtension = errors.New("no browser extension connected")
)

const (
	dialTimeout   = 3 * time.Second
	daemonRetry   = 4 * time.Second
	daemonBackoff = 150 * time.Millisecond
	writeTimeout  = 10 * time.Second
)

// Reply is a correlated response from the hub (a proxied extension reply or a
// control reply). Data is raw JSON so string/number/array results from the page
// survive intact instead of being forced into an object.
type Reply struct {
	ID    uint64          `json:"id"`
	OK    bool            `json:"ok"`
	Data  json.RawMessage `json:"data,omitempty"`
	Error string          `json:"error,omitempty"`
}

// Client dials the hub as an agent session and proxies requests to the
// extension through it.
type Client struct {
	hubAddr    string
	name       string
	session    string
	daemonPort int
	spawn      func(port int) error

	mu      sync.Mutex
	conn    *wsConn
	nextID  uint64
	pending map[uint64]chan *Reply
}

type wsConn struct {
	c  *websocket.Conn
	mu sync.Mutex
}

func (w *wsConn) writeJSON(v any) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.c.SetWriteDeadline(time.Now().Add(writeTimeout))
	return w.c.WriteJSON(v)
}

// NewClient returns a client for the given hub. name identifies the agent in
// the hub (e.g. "opencode:project"). spawn, if non-nil, is used to start the
// hub daemon when none is reachable; SetDaemonPort must be called first.
func NewClient(hubAddr, name string) *Client {
	return &Client{
		hubAddr: hubAddr,
		name:    name,
		session: uuid.NewString(),
		pending: make(map[uint64]chan *Reply),
		spawn:   daemon.Spawn,
	}
}

// SetDaemonPort configures the port used when spawning the hub daemon.
func (c *Client) SetDaemonPort(port int) { c.daemonPort = port }

// SetSpawn overrides the daemon spawner (used in tests).
func (c *Client) SetSpawn(fn func(port int) error) { c.spawn = fn }

// HubAddr returns the hub address.
func (c *Client) HubAddr() string { return c.hubAddr }

// Session returns this client's stable session id.
func (c *Client) Session() string { return c.session }

// Connected reports whether a hub connection is currently established.
func (c *Client) Connected() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.conn != nil
}

// Connect establishes (or re-establishes) the hub connection. It is
// idempotent.
func (c *Client) Connect(ctx context.Context) error {
	c.mu.Lock()
	if c.conn != nil {
		c.mu.Unlock()
		return nil
	}
	c.mu.Unlock()

	ctx, cancel := context.WithTimeout(ctx, dialTimeout)
	defer cancel()
	d := websocket.Dialer{}
	ws, _, err := d.DialContext(ctx, "ws://"+c.hubAddr+"/agent", nil)
	if err != nil {
		return ErrNoHub
	}
	hello := map[string]any{
		"ctrl":   "hello",
		"id":     0,
		"params": map[string]any{"session": c.session, "name": c.name},
	}
	ws.SetWriteDeadline(time.Now().Add(writeTimeout))
	if err := ws.WriteJSON(hello); err != nil {
		ws.Close()
		return err
	}
	ws.SetReadDeadline(time.Now().Add(dialTimeout))
	var ack Reply
	if err := ws.ReadJSON(&ack); err != nil {
		ws.Close()
		return err
	}
	if !ack.OK {
		ws.Close()
		return fmt.Errorf("hub rejected session: %s", ack.Error)
	}
	ws.SetReadDeadline(time.Time{})

	wc := &wsConn{c: ws}
	c.mu.Lock()
	if c.conn != nil {
		c.mu.Unlock()
		ws.Close()
		return nil
	}
	c.conn = wc
	c.mu.Unlock()
	go c.readLoop(wc)
	return nil
}

// Ensure returns a live hub connection, spawning the hub daemon if necessary.
func (c *Client) Ensure(ctx context.Context) error { return c.ensure(ctx) }

// ensure returns a live connection, starting the hub daemon if necessary.
func (c *Client) ensure(ctx context.Context) error {
	c.mu.Lock()
	ok := c.conn != nil
	c.mu.Unlock()
	if ok {
		return nil
	}
	if err := c.Connect(ctx); err == nil {
		return nil
	}
	if c.spawn != nil {
		if serr := c.spawn(c.daemonPort); serr != nil {
			return ErrNoHub
		}
	}
	deadline := time.Now().Add(daemonRetry)
	for time.Now().Before(deadline) {
		if err := c.Connect(ctx); err == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(daemonBackoff):
		}
	}
	return ErrNoHub
}

// Request sends an action to the extension (via the hub) and waits for the
// correlated reply, honoring ctx (timeouts included).
func (c *Client) Request(ctx context.Context, action string, params map[string]any) (*Reply, error) {
	return c.send(ctx, "action", action, params)
}

// Control sends a hub-side control message (e.g. setTarget/getTarget).
func (c *Client) Control(ctx context.Context, ctrl string, params map[string]any) (*Reply, error) {
	return c.send(ctx, "ctrl", ctrl, params)
}

// SetTarget pins this session to a tab or window. Pass nil to clear (active).
func (c *Client) SetTarget(ctx context.Context, pin map[string]any) (*Reply, error) {
	return c.Control(ctx, "setTarget", pin)
}

// GetTarget returns this session's current pin.
func (c *Client) GetTarget(ctx context.Context) (*Reply, error) {
	return c.Control(ctx, "getTarget", nil)
}

func (c *Client) send(ctx context.Context, kind, name string, params map[string]any) (*Reply, error) {
	if err := c.ensure(ctx); err != nil {
		return nil, err
	}
	c.mu.Lock()
	wc := c.conn
	if wc == nil {
		c.mu.Unlock()
		return nil, ErrNotConnected
	}
	c.nextID++
	id := c.nextID
	ch := make(chan *Reply, 1)
	c.pending[id] = ch
	c.mu.Unlock()

	msg := map[string]any{"id": id, "params": params}
	if kind == "ctrl" {
		msg["ctrl"] = name
	} else {
		msg["action"] = name
	}
	if err := wc.writeJSON(msg); err != nil {
		c.drop(id)
		return nil, err
	}

	select {
	case <-ctx.Done():
		c.drop(id)
		return nil, ctx.Err()
	case rep := <-ch:
		if rep == nil {
			return nil, ErrNotConnected
		}
		return rep, nil
	}
}

func (c *Client) drop(id uint64) {
	c.mu.Lock()
	delete(c.pending, id)
	c.mu.Unlock()
}

func (c *Client) deliver(id uint64, rep *Reply) {
	c.mu.Lock()
	ch, ok := c.pending[id]
	if ok {
		delete(c.pending, id)
	}
	c.mu.Unlock()
	if ok {
		select {
		case ch <- rep:
		default:
		}
	}
}

func (c *Client) readLoop(wc *wsConn) {
	defer func() {
		c.mu.Lock()
		if c.conn == wc {
			c.conn = nil
			for id, ch := range c.pending {
				delete(c.pending, id)
				ch <- nil
			}
		}
		c.mu.Unlock()
		wc.c.Close()
	}()

	for {
		var env struct {
			Type  string          `json:"type"`
			ID    uint64          `json:"id"`
			OK    bool            `json:"ok"`
			Data  json.RawMessage `json:"data,omitempty"`
			Error string          `json:"error,omitempty"`
		}
		if err := wc.c.ReadJSON(&env); err != nil {
			return
		}
		if env.Type == "ping" {
			wc.writeJSON(map[string]string{"type": "pong"})
			continue
		}
		if env.ID == 0 {
			continue
		}
		c.deliver(env.ID, &Reply{ID: env.ID, OK: env.OK, Data: env.Data, Error: env.Error})
	}
}

// Close tears down the hub connection.
func (c *Client) Close() {
	c.mu.Lock()
	wc := c.conn
	c.conn = nil
	c.mu.Unlock()
	if wc != nil {
		wc.c.Close()
	}
}
