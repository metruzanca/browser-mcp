package bridge

import (
	"context"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/metruzanca/browser-mcp/internal/hub"
)

// startHubWithExtension starts an in-process hub and attaches a fake
// extension that echoes requests back with their session.
func startHubWithExtension(t *testing.T) *hub.Hub {
	t.Helper()
	h := hub.New(0, hub.Options{})
	if err := h.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(h.Close)
	ext, _, err := websocket.DefaultDialer.Dial("ws://"+h.Addr()+"/ws", nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ext.Close() })
	go func() {
		for {
			var req struct {
				ID      uint64         `json:"id"`
				Session string         `json:"session"`
				Action  string         `json:"action"`
				Params  map[string]any `json:"params"`
			}
			if err := ext.ReadJSON(&req); err != nil {
				return
			}
			ext.WriteJSON(map[string]any{
				"id": req.ID, "session": req.Session, "ok": true,
				"data": map[string]any{"action": req.Action, "params": req.Params},
			})
		}
	}()
	return h
}

func newClient(t *testing.T, h *hub.Hub) *Client {
	t.Helper()
	c := NewClient(h.Addr(), "test-agent")
	c.SetSpawn(func(port int) error { return nil }) // no real daemon in tests
	t.Cleanup(c.Close)
	return c
}

func TestClientRequestRoundTrip(t *testing.T) {
	h := startHubWithExtension(t)
	c := newClient(t, h)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	rep, err := c.Request(ctx, "getTabInfo", map[string]any{"x": 1})
	if err != nil {
		t.Fatal(err)
	}
	if !rep.OK {
		t.Fatalf("not ok: %+v", rep)
	}
	data := rep.Data
	if data["action"] != "getTabInfo" {
		t.Fatalf("wrong action echoed: %v", data)
	}
	if data["params"].(map[string]any)["x"] != float64(1) {
		t.Fatalf("params not forwarded: %v", data)
	}
}

func TestClientSetTargetAndGetTarget(t *testing.T) {
	h := startHubWithExtension(t)
	c := newClient(t, h)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	rep, err := c.SetTarget(ctx, map[string]any{"tabId": 99})
	if err != nil || !rep.OK {
		t.Fatalf("setTarget failed: %v %+v", err, rep)
	}
	rep, err = c.GetTarget(ctx)
	if err != nil || !rep.OK {
		t.Fatalf("getTarget failed: %v %+v", err, rep)
	}
	tgt := rep.Data["target"].(map[string]any)
	if tgt["kind"] != "tab" || tgt["id"] != float64(99) {
		t.Fatalf("target wrong: %v", tgt)
	}

	// A pinned request must reach the extension with the pinned tab forced.
	rep, err = c.Request(ctx, "getTabInfo", map[string]any{"tabId": 1})
	if err != nil {
		t.Fatal(err)
	}
	params := rep.Data["params"].(map[string]any)
	if params["tabId"] != float64(99) || params["pinnedTab"] != float64(99) {
		t.Fatalf("pin not forced: %v", params)
	}
}

func TestClientNoExtension(t *testing.T) {
	h := hub.New(0, hub.Options{})
	if err := h.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(h.Close)
	c := newClient(t, h)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	rep, err := c.Request(ctx, "getTabInfo", nil)
	if err != nil {
		t.Fatal(err)
	}
	if rep.OK || rep.Error != "no browser extension connected" {
		t.Fatalf("expected no-extension error, got %+v", rep)
	}
}

func TestClientTwoAgents(t *testing.T) {
	h := startHubWithExtension(t)
	a := newClient(t, h)
	b := newClient(t, h)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Pin each to different tabs; verify the hub keeps them separate.
	if _, err := a.SetTarget(ctx, map[string]any{"tabId": 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := b.SetTarget(ctx, map[string]any{"tabId": 2}); err != nil {
		t.Fatal(err)
	}

	ra, err := a.Request(ctx, "getTabInfo", nil)
	if err != nil {
		t.Fatal(err)
	}
	rb, err := b.Request(ctx, "getTabInfo", nil)
	if err != nil {
		t.Fatal(err)
	}
	pa := ra.Data["params"].(map[string]any)
	pb := rb.Data["params"].(map[string]any)
	if pa["tabId"] != float64(1) || pb["tabId"] != float64(2) {
		t.Fatalf("session pins crossed: a=%v b=%v", pa, pb)
	}
}
