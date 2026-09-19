package bridge

import (
	"context"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestRequestReply(t *testing.T) {
	s := New(0, Options{})
	if err := s.Start(); err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, _, err := websocket.DefaultDialer.Dial("ws://"+s.Addr()+"/ws", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	go func() {
		var req Request
		if err := conn.ReadJSON(&req); err != nil {
			return
		}
		conn.WriteJSON(Reply{ID: req.ID, OK: true, Data: map[string]any{"echo": req.Params["x"]}})
	}()

	reply, err := s.Request(ctx, "test", map[string]any{"x": "hello"})
	if err != nil {
		t.Fatal(err)
	}
	if !reply.OK {
		t.Fatalf("reply not ok: %+v", reply)
	}
	if got, want := reply.Data["echo"], "hello"; got != want {
		t.Fatalf("echo = %v, want %v", got, want)
	}
}

func TestErrorReply(t *testing.T) {
	s := New(0, Options{})
	if err := s.Start(); err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, _, err := websocket.DefaultDialer.Dial("ws://"+s.Addr()+"/ws", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	go func() {
		var req Request
		if err := conn.ReadJSON(&req); err != nil {
			return
		}
		conn.WriteJSON(Reply{ID: req.ID, OK: false, Error: "boom"})
	}()

	reply, err := s.Request(ctx, "test", nil)
	if err != nil {
		t.Fatal(err)
	}
	if reply.OK {
		t.Fatalf("expected error reply, got %+v", reply)
	}
	if reply.Error != "boom" {
		t.Fatalf("error = %q, want boom", reply.Error)
	}
}

func TestNotConnected(t *testing.T) {
	s := New(0, Options{})
	if err := s.Start(); err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	_, err := s.Request(context.Background(), "x", nil)
	if err != ErrNotConnected {
		t.Fatalf("want ErrNotConnected, got %v", err)
	}
}

func TestKeepalivePong(t *testing.T) {
	s := New(0, Options{})
	if err := s.Start(); err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	conn, _, err := websocket.DefaultDialer.Dial("ws://"+s.Addr()+"/ws", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	conn.WriteJSON(map[string]string{"type": "ping"})
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	var got map[string]string
	if err := conn.ReadJSON(&got); err != nil {
		t.Fatal(err)
	}
	if got["type"] != "pong" {
		t.Fatalf("got %v, want pong", got)
	}
}
