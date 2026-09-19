package hub

import (
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func dial(t *testing.T, url string) *websocket.Conn {
	t.Helper()
	conn, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatal(err)
	}
	return conn
}

func readJSON(t *testing.T, c *websocket.Conn) map[string]any {
	t.Helper()
	var v map[string]any
	c.SetReadDeadline(time.Now().Add(3 * time.Second))
	if err := c.ReadJSON(&v); err != nil {
		t.Fatal(err)
	}
	return v
}

func connectAgent(t *testing.T, hubURL, session, name string) *websocket.Conn {
	t.Helper()
	c := dial(t, hubURL)
	if err := c.WriteJSON(map[string]any{
		"ctrl": "hello", "id": 0,
		"params": map[string]any{"session": session, "name": name},
	}); err != nil {
		t.Fatal(err)
	}
	ack := readJSON(t, c)
	if ack["ok"] != true {
		t.Fatalf("hello rejected: %v", ack)
	}
	return c
}

func startHub(t *testing.T) *Hub {
	t.Helper()
	h := New(0, Options{})
	if err := h.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(h.Close)
	return h
}

func TestRequestRouting(t *testing.T) {
	h := startHub(t)
	hubURL := "ws://" + h.Addr()

	ext := dial(t, hubURL+"/ws")
	defer ext.Close()

	a1 := connectAgent(t, hubURL+"/agent", "sess-1", "agent one")
	defer a1.Close()
	a2 := connectAgent(t, hubURL+"/agent", "sess-2", "agent two")
	defer a2.Close()

	// Both agents issue a request with the same id.
	go func() {
		ext.SetReadDeadline(time.Now().Add(3 * time.Second))
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
				"data": map[string]any{"who": req.Session, "action": req.Action},
			})
		}
	}()

	a1.WriteJSON(map[string]any{"id": 1, "action": "getTabInfo"})
	a2.WriteJSON(map[string]any{"id": 1, "action": "getTabInfo"})

	r1 := readJSON(t, a1)
	r2 := readJSON(t, a2)
	if r1["ok"] != true || r1["data"].(map[string]any)["who"] != "sess-1" {
		t.Fatalf("a1 routing wrong: %v", r1)
	}
	if r2["ok"] != true || r2["data"].(map[string]any)["who"] != "sess-2" {
		t.Fatalf("a2 routing wrong: %v", r2)
	}
}

func TestPinInjection(t *testing.T) {
	h := startHub(t)
	hubURL := "ws://" + h.Addr()

	ext := dial(t, hubURL+"/ws")
	defer ext.Close()
	a := connectAgent(t, hubURL+"/agent", "sess-pin", "pinner")
	defer a.Close()

	// Pin to tab 42.
	a.WriteJSON(map[string]any{"id": 1, "ctrl": "setTarget", "params": map[string]any{"tabId": 42}})
	ack := readJSON(t, a)
	if ack["ok"] != true {
		t.Fatalf("setTarget failed: %v", ack)
	}

	got := make(chan map[string]any, 1)
	go func() {
		ext.SetReadDeadline(time.Now().Add(3 * time.Second))
		var req struct {
			ID      uint64         `json:"id"`
			Session string         `json:"session"`
			Action  string         `json:"action"`
			Params  map[string]any `json:"params"`
		}
		if err := ext.ReadJSON(&req); err != nil {
			return
		}
		got <- req.Params
		ext.WriteJSON(map[string]any{"id": req.ID, "session": req.Session, "ok": true, "data": map[string]any{}})
	}()

	// Caller asks for a different tab: hub must force the pinned tab.
	a.WriteJSON(map[string]any{"id": 2, "action": "getTabInfo", "params": map[string]any{"tabId": 7}})
	readJSON(t, a)
	params := <-got
	if params["tabId"] != float64(42) {
		t.Fatalf("pinned tabId not forced: %v", params)
	}
	if _, ok := params["pinnedTab"]; !ok {
		t.Fatalf("pinnedTab marker missing: %v", params)
	}

	// getTarget reflects the pin.
	a.WriteJSON(map[string]any{"id": 3, "ctrl": "getTarget"})
	gt := readJSON(t, a)
	tgt := gt["data"].(map[string]any)["target"].(map[string]any)
	if tgt["kind"] != "tab" || tgt["id"] != float64(42) {
		t.Fatalf("getTarget wrong: %v", tgt)
	}

	// clear the pin
	a.WriteJSON(map[string]any{"id": 4, "ctrl": "setTarget", "params": map[string]any{"clear": true}})
	readJSON(t, a)
	a.WriteJSON(map[string]any{"id": 5, "ctrl": "getTarget"})
	gt = readJSON(t, a)
	if gt["data"].(map[string]any)["target"].(map[string]any)["kind"] != "active" {
		t.Fatalf("clear failed: %v", gt)
	}
}

func TestNoExtension(t *testing.T) {
	h := startHub(t)
	hubURL := "ws://" + h.Addr()

	a := connectAgent(t, hubURL+"/agent", "sess-x", "lonely")
	defer a.Close()
	a.WriteJSON(map[string]any{"id": 1, "action": "getTabInfo"})
	rep := readJSON(t, a)
	if rep["ok"] == true || rep["error"] != "no browser extension connected" {
		t.Fatalf("expected no-extension error, got %v", rep)
	}
}

func TestSessionReplacement(t *testing.T) {
	h := startHub(t)
	hubURL := "ws://" + h.Addr()
	ext := dial(t, hubURL+"/ws")
	defer ext.Close()

	old := connectAgent(t, hubURL+"/agent", "sess-r", "first")
	new := connectAgent(t, hubURL+"/agent", "sess-r", "second")
	defer new.Close()

	// The old connection should be gone; only the new one gets replies.
	go func() {
		ext.SetReadDeadline(time.Now().Add(3 * time.Second))
		var req struct {
			ID      uint64 `json:"id"`
			Session string `json:"session"`
		}
		if err := ext.ReadJSON(&req); err != nil {
			return
		}
		ext.WriteJSON(map[string]any{"id": req.ID, "session": req.Session, "ok": true, "data": map[string]any{"s": req.Session}})
	}()

	new.WriteJSON(map[string]any{"id": 1, "action": "getTabInfo"})
	rep := readJSON(t, new)
	if rep["ok"] != true {
		t.Fatalf("new session reply failed: %v", rep)
	}
	_ = old
}

func TestControlRejectedByUnknown(t *testing.T) {
	h := startHub(t)
	hubURL := "ws://" + h.Addr()
	a := connectAgent(t, hubURL+"/agent", "sess-c", "ctrl")
	defer a.Close()
	a.WriteJSON(map[string]any{"id": 1, "ctrl": "nope"})
	rep := readJSON(t, a)
	if rep["ok"] == true {
		t.Fatalf("unknown ctrl should fail: %v", rep)
	}
}

func TestPinRequiresParam(t *testing.T) {
	h := startHub(t)
	hubURL := "ws://" + h.Addr()
	a := connectAgent(t, hubURL+"/agent", "sess-p", "pinner")
	defer a.Close()
	a.WriteJSON(map[string]any{"id": 1, "ctrl": "setTarget", "params": map[string]any{}})
	rep := readJSON(t, a)
	if rep["ok"] == true {
		t.Fatalf("setTarget without param should fail: %v", rep)
	}
}
