// Package hub implements the browser-mcp hub: a long-lived loopback server
// that one Chrome extension and many MCP agent sessions connect to. The hub
// routes requests from any agent to the extension (tagged with the agent's
// session) and back, and enforces each session's pinned target (window/tab).
package hub

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// Pin is a session's pinned target. Kind is "tab" or "window".
type Pin struct {
	Kind string `json:"kind"`
	ID   int    `json:"id"`
}

type Options struct {
	OnConnect    func()
	OnDisconnect func(err error)
}

// wsConn pairs a websocket with its own write lock.
type wsConn struct {
	c  *websocket.Conn
	mu sync.Mutex
}

func (w *wsConn) writeJSON(v any) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.c.SetWriteDeadline(time.Now().Add(10 * time.Second))
	return w.c.WriteJSON(v)
}

func (w *wsConn) close() { w.c.Close() }

type session struct {
	id   string
	conn *wsConn
	name string
	pin  *Pin
}

type Hub struct {
	port int
	addr string

	http *http.Server
	ln   net.Listener

	mu       sync.Mutex
	ext      *wsConn
	sessions map[string]*session

	onConnect    func()
	onDisconnect func(err error)
	done         chan struct{}
}

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

// New creates a hub. Pass port 0 to pick a free port (call Port() after Start).
func New(port int, opts Options) *Hub {
	return &Hub{
		port:         port,
		sessions:     make(map[string]*session),
		onConnect:    opts.OnConnect,
		onDisconnect: opts.OnDisconnect,
		done:         make(chan struct{}),
	}
}

// Start binds the loopback listener and begins serving. It never blocks.
func (h *Hub) Start() error {
	addr := fmt.Sprintf("127.0.0.1:%d", h.port)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("listen %s: %w", addr, err)
	}
	h.ln = ln
	h.addr = ln.Addr().String()

	mux := http.NewServeMux()
	mux.HandleFunc("/", h.handleStatus)
	mux.HandleFunc("/ws", h.handleExtension)
	mux.HandleFunc("/agent", h.handleAgent)
	h.http = &http.Server{Handler: mux}
	go func() {
		if err := h.http.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			fmt.Fprintf(os.Stderr, "browser-mcp hub: %v\n", err)
		}
	}()
	go h.pingLoop()
	return nil
}

// Addr returns the bound host:port (after Start).
func (h *Hub) Addr() string { return h.addr }

// Port returns the bound port (after Start).
func (h *Hub) Port() int {
	_, port, _ := net.SplitHostPort(h.addr)
	var p int
	fmt.Sscanf(port, "%d", &p)
	return p
}

// Connected reports whether the extension is connected.
func (h *Hub) Connected() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.ext != nil
}

func (h *Hub) Close() {
	select {
	case <-h.done:
	default:
		close(h.done)
	}
	if h.http != nil {
		h.http.Close()
	}
	h.mu.Lock()
	if h.ext != nil {
		h.ext.close()
		h.ext = nil
	}
	for _, s := range h.sessions {
		s.conn.close()
	}
	h.sessions = make(map[string]*session)
	h.mu.Unlock()
}

// ---- extension side --------------------------------------------------------

func (h *Hub) handleExtension(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	wc := &wsConn{c: conn}

	h.mu.Lock()
	old := h.ext
	h.ext = wc
	h.mu.Unlock()
	if old != nil {
		old.close()
	}
	if h.onConnect != nil {
		h.onConnect()
	}
	h.extensionLoop(wc)
}

func (h *Hub) extensionLoop(wc *wsConn) {
	defer func() {
		h.mu.Lock()
		if h.ext == wc {
			h.ext = nil
		}
		h.mu.Unlock()
		wc.close()
		if h.onDisconnect != nil {
			h.onDisconnect(errors.New("extension disconnected"))
		}
	}()

	wc.c.SetReadDeadline(time.Now().Add(40 * time.Second))
	for {
		var env struct {
			Type    string          `json:"type"`
			ID      uint64          `json:"id"`
			Session string          `json:"session"`
			OK      bool            `json:"ok"`
			Data    json.RawMessage `json:"data,omitempty"`
			Error   string          `json:"error,omitempty"`
		}
		if err := wc.c.ReadJSON(&env); err != nil {
			return
		}
		wc.c.SetReadDeadline(time.Now().Add(40 * time.Second))
		if env.Type == "ping" {
			wc.writeJSON(map[string]string{"type": "pong"})
			continue
		}
		if env.Session == "" {
			continue
		}
		h.mu.Lock()
		s := h.sessions[env.Session]
		h.mu.Unlock()
		if s == nil {
			continue
		}
		s.conn.writeJSON(map[string]any{
			"id": env.ID, "ok": env.OK, "data": env.Data, "error": env.Error,
		})
	}
}

// ---- agent side ------------------------------------------------------------

func (h *Hub) handleAgent(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	wc := &wsConn{c: conn}

	// First message must be a hello registering the session.
	var hello struct {
		ID     uint64         `json:"id"`
		Ctrl   string         `json:"ctrl"`
		Params map[string]any `json:"params"`
	}
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	if err := conn.ReadJSON(&hello); err != nil || hello.Ctrl != "hello" {
		conn.Close()
		return
	}
	conn.SetReadDeadline(time.Time{})
	sessionID, _ := hello.Params["session"].(string)
	name, _ := hello.Params["name"].(string)
	if sessionID == "" {
		wc.writeJSON(map[string]any{"id": hello.ID, "ok": false, "error": "hello requires params.session"})
		conn.Close()
		return
	}

	s := &session{id: sessionID, conn: wc, name: name}
	h.mu.Lock()
	if old, ok := h.sessions[sessionID]; ok {
		old.conn.close()
	}
	h.sessions[sessionID] = s
	h.mu.Unlock()

	wc.writeJSON(map[string]any{"id": hello.ID, "ok": true, "data": map[string]any{"session": sessionID}})
	h.agentLoop(s)
}

func (h *Hub) removeSession(id string, c *wsConn) {
	h.mu.Lock()
	if s, ok := h.sessions[id]; ok && s.conn == c {
		delete(h.sessions, id)
	}
	h.mu.Unlock()
}

type agentMessage struct {
	ID     uint64         `json:"id"`
	Ctrl   string         `json:"ctrl,omitempty"`
	Action string         `json:"action,omitempty"`
	Params map[string]any `json:"params,omitempty"`
}

func (h *Hub) agentLoop(s *session) {
	defer h.removeSession(s.id, s.conn)
	for {
		var msg agentMessage
		if err := s.conn.c.ReadJSON(&msg); err != nil {
			return
		}
		if msg.Ctrl != "" {
			h.handleControl(s, msg)
			continue
		}
		h.forward(s, msg)
	}
}

func (h *Hub) handleControl(s *session, msg agentMessage) {
	switch msg.Ctrl {
	case "setTarget":
		pin, err := parsePin(msg.Params)
		if err != nil {
			s.conn.writeJSON(map[string]any{"id": msg.ID, "ok": false, "error": err.Error()})
			return
		}
		h.mu.Lock()
		s.pin = pin
		h.mu.Unlock()
		s.conn.writeJSON(map[string]any{"id": msg.ID, "ok": true, "data": map[string]any{"target": pinValue(pin)}})
	case "getTarget":
		h.mu.Lock()
		pin := s.pin
		h.mu.Unlock()
		s.conn.writeJSON(map[string]any{"id": msg.ID, "ok": true, "data": map[string]any{"target": pinValue(pin)}})
	default:
		s.conn.writeJSON(map[string]any{"id": msg.ID, "ok": false, "error": "unknown ctrl: " + msg.Ctrl})
	}
}

func parsePin(params map[string]any) (*Pin, error) {
	if params == nil {
		return nil, nil
	}
	if v, ok := params["clear"].(bool); ok && v {
		return nil, nil
	}
	if v, ok := num(params["tabId"]); ok && v > 0 {
		return &Pin{Kind: "tab", ID: int(v)}, nil
	}
	if v, ok := num(params["windowId"]); ok && v > 0 {
		return &Pin{Kind: "window", ID: int(v)}, nil
	}
	return nil, errors.New("setTarget requires tabId, windowId, or clear")
}

func num(v any) (int, bool) {
	switch n := v.(type) {
	case float64:
		return int(n), true
	case int:
		return n, true
	case int64:
		return int(n), true
	case json.Number:
		if i, err := n.Int64(); err == nil {
			return int(i), true
		}
	}
	return 0, false
}

func pinValue(pin *Pin) any {
	if pin == nil {
		return map[string]any{"kind": "active"}
	}
	return map[string]any{"kind": pin.Kind, "id": pin.ID}
}

// forward applies the session's pin to the request params and sends it to the
// extension.
func (h *Hub) forward(s *session, msg agentMessage) {
	h.mu.Lock()
	ext := h.ext
	pin := s.pin
	h.mu.Unlock()
	if ext == nil {
		s.conn.writeJSON(map[string]any{"id": msg.ID, "ok": false, "error": "no browser extension connected"})
		return
	}

	params := cloneParams(msg.Params)
	if pin != nil {
		if pin.Kind == "tab" {
			params["tabId"] = pin.ID
			delete(params, "windowId")
			params["pinnedTab"] = pin.ID
		} else {
			params["windowId"] = pin.ID
			params["pinnedWindow"] = pin.ID
		}
	}
	req := map[string]any{
		"id":      msg.ID,
		"session": s.id,
		"action":  msg.Action,
		"params":  params,
	}
	if err := ext.writeJSON(req); err != nil {
		s.conn.writeJSON(map[string]any{"id": msg.ID, "ok": false, "error": "extension disconnected"})
		h.mu.Lock()
		if h.ext == ext {
			h.ext = nil
		}
		h.mu.Unlock()
		ext.close()
	}
}

func cloneParams(p map[string]any) map[string]any {
	if p == nil {
		return map[string]any{}
	}
	out := make(map[string]any, len(p))
	for k, v := range p {
		out[k] = v
	}
	return out
}

// pingLoop keeps connections healthy and detects half-open sockets.
func (h *Hub) pingLoop() {
	t := time.NewTicker(20 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-h.done:
			return
		case <-t.C:
			h.mu.Lock()
			ext := h.ext
			sessions := make([]*session, 0, len(h.sessions))
			for _, s := range h.sessions {
				sessions = append(sessions, s)
			}
			h.mu.Unlock()
			if ext != nil {
				if err := ext.writeJSON(map[string]string{"type": "ping"}); err != nil {
					ext.close()
				}
			}
			for _, s := range sessions {
				if err := s.conn.writeJSON(map[string]string{"type": "ping"}); err != nil {
					s.conn.close()
				}
			}
		}
	}
}

func (h *Hub) handleStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	h.mu.Lock()
	sessions := len(h.sessions)
	h.mu.Unlock()
	fmt.Fprintf(w, `{"service":"browser-mcp-hub","websocket":"ws://%s/ws","extensionConnected":%v,"agents":%d}`+"\n", h.addr, h.Connected(), sessions)
}
