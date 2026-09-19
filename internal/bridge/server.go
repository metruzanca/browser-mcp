package bridge

import (
	"context"
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

var (
	// ErrNotConnected is returned when no extension is currently connected.
	ErrNotConnected = errors.New("no browser extension connected")
	// ErrDisconnected is returned when the connection drops while waiting.
	ErrDisconnected = errors.New("browser extension disconnected")
)

const (
	defaultWriteTimeout = 10 * time.Second
	defaultReadTimeout  = 35 * time.Second
)

// Options configures connection lifecycle callbacks.
type Options struct {
	OnConnect    func()
	OnDisconnect func(err error)
}

// Server hosts the loopback HTTP/WebSocket bridge that the Chrome extension
// dials. Only one extension connection is served at a time; a new connection
// replaces the previous one.
type Server struct {
	port int
	addr string

	http *http.Server
	ln   net.Listener

	mu      sync.Mutex
	conn    *websocket.Conn
	nextID  uint64
	pending map[uint64]chan *Reply

	writeMu      sync.Mutex
	onConnect    func()
	onDisconnect func(error)
}

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

// New creates a bridge server. Pass port 0 to pick a free port (call Port()
// after Start).
func New(port int, opts Options) *Server {
	return &Server{
		port:         port,
		pending:      make(map[uint64]chan *Reply),
		onConnect:    opts.OnConnect,
		onDisconnect: opts.OnDisconnect,
	}
}

// Start binds the loopback listener and begins serving. It never blocks.
func (s *Server) Start() error {
	addr := fmt.Sprintf("127.0.0.1:%d", s.port)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("listen %s: %w", addr, err)
	}
	s.ln = ln
	s.addr = ln.Addr().String()

	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleStatus)
	mux.HandleFunc("/ws", s.handleWS)
	s.http = &http.Server{Handler: mux}
	go func() {
		if err := s.http.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			fmt.Fprintf(os.Stderr, "browser-mcp bridge: %v\n", err)
		}
	}()
	return nil
}

// Addr returns the bound "host:port" (after Start).
func (s *Server) Addr() string { return s.addr }

// Port returns the bound port (after Start).
func (s *Server) Port() int {
	_, port, _ := net.SplitHostPort(s.addr)
	var p int
	fmt.Sscanf(port, "%d", &p)
	return p
}

// Connected reports whether an extension is currently connected.
func (s *Server) Connected() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.conn != nil
}

// Request sends an action to the extension and waits for the correlated
// reply, honoring ctx (timeouts included).
func (s *Server) Request(ctx context.Context, action string, params map[string]any) (*Reply, error) {
	s.mu.Lock()
	conn := s.conn
	if conn == nil {
		s.mu.Unlock()
		return nil, ErrNotConnected
	}
	s.nextID++
	id := s.nextID
	ch := make(chan *Reply, 1)
	s.pending[id] = ch
	s.mu.Unlock()

	req := Request{ID: id, Action: action, Params: params}
	if err := s.write(conn, req); err != nil {
		s.drop(id)
		return nil, err
	}

	select {
	case <-ctx.Done():
		s.drop(id)
		// The extension may be wedged (suspended service worker, hung
		// executeScript). Reset the connection so the next request starts
		// on a fresh socket; the extension auto-reconnects in a few seconds.
		s.mu.Lock()
		if s.conn == conn {
			s.conn = nil
		}
		s.mu.Unlock()
		conn.Close()
		return nil, ctx.Err()
	case reply := <-ch:
		if reply == nil {
			return nil, ErrDisconnected
		}
		return reply, nil
	}
}

// Close shuts the bridge down.
func (s *Server) Close() {
	if s.http != nil {
		s.http.Close()
	}
	s.mu.Lock()
	if s.conn != nil {
		s.conn.Close()
		s.conn = nil
	}
	s.mu.Unlock()
}

func (s *Server) write(conn *websocket.Conn, v any) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	conn.SetWriteDeadline(time.Now().Add(defaultWriteTimeout))
	return conn.WriteJSON(v)
}

func (s *Server) drop(id uint64) {
	s.mu.Lock()
	delete(s.pending, id)
	s.mu.Unlock()
}

func (s *Server) deliver(reply Reply) {
	s.mu.Lock()
	ch, ok := s.pending[reply.ID]
	if ok {
		delete(s.pending, reply.ID)
	}
	s.mu.Unlock()
	if ok {
		select {
		case ch <- &reply:
		default:
		}
	}
}

func (s *Server) failPendingLocked(err error) {
	for id, ch := range s.pending {
		delete(s.pending, id)
		ch <- &Reply{ID: id, OK: false, Error: err.Error()}
	}
}

func (s *Server) handleWS(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	s.mu.Lock()
	old := s.conn
	s.conn = conn
	s.mu.Unlock()
	if old != nil {
		old.Close()
	}
	if s.onConnect != nil {
		s.onConnect()
	}
	s.readLoop(conn)
}

func (s *Server) readLoop(conn *websocket.Conn) {
	defer func() {
		s.mu.Lock()
		if s.conn == conn {
			s.conn = nil
		}
		s.failPendingLocked(ErrDisconnected)
		s.mu.Unlock()
		conn.Close()
		if s.onDisconnect != nil {
			s.onDisconnect(ErrDisconnected)
		}
	}()

	conn.SetReadDeadline(time.Now().Add(defaultReadTimeout))
	for {
		_, raw, err := conn.ReadMessage()
		if err != nil {
			return
		}
		conn.SetReadDeadline(time.Now().Add(defaultReadTimeout))

		var env struct {
			Type string `json:"type"`
			ID   uint64 `json:"id"`
		}
		if json.Unmarshal(raw, &env) != nil {
			continue
		}
		if env.Type == "ping" {
			s.write(conn, map[string]string{"type": "pong"})
			continue
		}
		if env.Type == "pong" {
			continue
		}
		if env.ID == 0 {
			continue
		}
		var reply Reply
		if err := json.Unmarshal(raw, &reply); err != nil {
			continue
		}
		s.deliver(reply)
	}
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{"service":"browser-mcp","websocket":"ws://%s/ws","extensionConnected":%v}`+"\n", s.addr, s.Connected())
}
