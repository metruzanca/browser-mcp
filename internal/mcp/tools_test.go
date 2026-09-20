package mcp_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/metruzanca/browser-mcp/internal/bridge"
	"github.com/metruzanca/browser-mcp/internal/hub"
	bmcp "github.com/metruzanca/browser-mcp/internal/mcp"
)

// attachExtension dials the hub as the browser extension and answers each
// action with a canned response so the whole tool path is exercised.
func attachExtension(t *testing.T, h *hub.Hub) {
	t.Helper()
	ws, _, err := websocket.DefaultDialer.Dial("ws://"+h.Addr()+"/ws", nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ws.Close() })
	go func() {
		for {
			var req struct {
				ID      uint64         `json:"id"`
				Session string         `json:"session"`
				Action  string         `json:"action"`
				Params  map[string]any `json:"params"`
			}
			if err := ws.ReadJSON(&req); err != nil {
				return
			}
			var data map[string]any
			switch req.Action {
			case "getTabInfo":
				data = map[string]any{
					"url": "https://jobs.example.com/apply", "title": "Apply", "tabId": 7,
				}
			case "listWindows":
				data = map[string]any{"windows": []any{
					map[string]any{"windowId": 1, "focused": true, "activeTab": map[string]any{"tabId": 7, "url": "https://a.example"}},
					map[string]any{"windowId": 2, "focused": false, "activeTab": map[string]any{"tabId": 8, "url": "https://b.example"}},
				}}
			case "listTabs":
				data = map[string]any{"tabs": []any{
					map[string]any{"tabId": 7, "windowId": 1, "url": "https://a.example"},
					map[string]any{"tabId": 8, "windowId": 2, "url": "https://b.example"},
				}}
			case "executeScript":
				code, _ := req.Params["code"].(string)
				data = map[string]any{
					"gotCode":      strings.Contains(code, "bmcp"),
					"args":         req.Params["args"],
					"worldUsed":    "main",
					"tabId":        req.Params["tabId"],
					"windowId":     req.Params["windowId"],
					"pinnedTab":    req.Params["pinnedTab"],
					"pinnedWindow": req.Params["pinnedWindow"],
				}
			default:
				data = map[string]any{"action": req.Action, "params": req.Params}
			}
			ws.WriteJSON(map[string]any{"id": req.ID, "session": req.Session, "ok": true, "data": data})
		}
	}()
}

func setup(t *testing.T) (*bridge.Client, *client.Client) {
	t.Helper()
	h := hub.New(0, hub.Options{})
	if err := h.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(h.Close)
	attachExtension(t, h)

	br := bridge.NewClient(h.Addr(), "test-agent")
	br.SetSpawn(func(port int) error { return nil })
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := br.Connect(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(br.Close)

	s := server.NewMCPServer("browser-mcp", "1.0.0", server.WithToolCapabilities(true))
	bmcp.New(br).Register(s)
	c, err := client.NewInProcessClient(s)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	if _, err := c.Initialize(ctx, mcp.InitializeRequest{}); err != nil {
		t.Fatal(err)
	}
	return br, c
}

func callText(t *testing.T, c *client.Client, tool string, args map[string]any) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	res, err := c.CallTool(ctx, mcp.CallToolRequest{Params: mcp.CallToolParams{Name: tool, Arguments: args}})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Content) == 0 {
		t.Fatalf("%s: no content", tool)
	}
	if tc, ok := res.Content[0].(mcp.TextContent); ok {
		return tc.Text
	}
	return ""
}

// TestEndToEnd boots a hub + fake extension + MCP server and drives tools.
func TestEndToEnd(t *testing.T) {
	_, c := setup(t)

	tools, err := c.ListTools(context.Background(), mcp.ListToolsRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if len(tools.Tools) < 20 {
		t.Fatalf("expected >= 20 tools, got %d", len(tools.Tools))
	}

	// browser_get_tab
	text := callText(t, c, "browser_get_tab", map[string]any{})
	if !strings.Contains(text, "https://jobs.example.com/apply") {
		t.Fatalf("get_tab missing URL: %s", text)
	}

	// browser_execute_js forwards code + args
	text = callText(t, c, "browser_execute_js", map[string]any{
		"code": "return { fromArgs: args.firstName, hasBmcp: !!bmcp };",
		"args": map[string]any{"firstName": "Sam"},
	})
	if !strings.Contains(text, `"firstName": "Sam"`) || !strings.Contains(text, "gotCode") {
		t.Fatalf("execute_js output wrong: %s", text)
	}

	// browser_type_text passes args through
	text = callText(t, c, "browser_type_text", map[string]any{
		"selector": "input[name=firstName]", "text": "Sam Zanca",
	})
	var data map[string]any
	if err := json.Unmarshal([]byte(text), &data); err != nil {
		t.Fatalf("type_text output not JSON: %s (%v)", text, err)
	}
	args, _ := data["args"].(map[string]any)
	if args["selector"] != "input[name=firstName]" || args["text"] != "Sam Zanca" {
		t.Fatalf("type_text args not forwarded: %v", args)
	}

	// browser_list_windows
	text = callText(t, c, "browser_list_windows", map[string]any{})
	if !strings.Contains(text, `"windowId": 1`) || !strings.Contains(text, `"windowId": 2`) {
		t.Fatalf("list_windows output wrong: %s", text)
	}

	// browser_list_tabs
	text = callText(t, c, "browser_list_tabs", map[string]any{"windowId": 1})
	if !strings.Contains(text, `"windowId": 1`) {
		t.Fatalf("list_tabs output wrong: %s", text)
	}

	// browser_get_target (unbound)
	text = callText(t, c, "browser_get_target", map[string]any{})
	if !strings.Contains(text, `"kind": "active"`) {
		t.Fatalf("get_target default wrong: %s", text)
	}

	// browser_set_target to a tab, then verify the pin reaches the page
	text = callText(t, c, "browser_set_target", map[string]any{"tabId": 42})
	if !strings.Contains(text, `"kind": "tab"`) {
		t.Fatalf("set_target output wrong: %s", text)
	}
	text = callText(t, c, "browser_get_target", map[string]any{})
	if !strings.Contains(text, `"id": 42`) {
		t.Fatalf("get_target after pin wrong: %s", text)
	}
	// A pinned execute_js must carry pinnedTab to the page
	text = callText(t, c, "browser_execute_js", map[string]any{"code": "return 1;"})
	if !strings.Contains(text, `"pinnedTab": 42`) {
		t.Fatalf("pin not applied to execute_js: %s", text)
	}
}

// TestSetTargetWindow pins to a window and checks windowId is forwarded.
func TestSetTargetWindow(t *testing.T) {
	_, c := setup(t)
	callText(t, c, "browser_set_target", map[string]any{"windowId": 2})
	text := callText(t, c, "browser_execute_js", map[string]any{"code": "return 1;"})
	if !strings.Contains(text, `"windowId": 2`) || !strings.Contains(text, `"pinnedWindow": 2`) {
		t.Fatalf("window pin not applied: %s", text)
	}
}

// TestNotConnected verifies the friendly error when no extension is attached.
func TestNotConnected(t *testing.T) {
	h := hub.New(0, hub.Options{})
	if err := h.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(h.Close)

	br := bridge.NewClient(h.Addr(), "test-agent")
	br.SetSpawn(func(port int) error { return nil })
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := br.Connect(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(br.Close)

	s := server.NewMCPServer("browser-mcp", "1.0.0", server.WithToolCapabilities(true))
	bmcp.New(br).Register(s)
	c, err := client.NewInProcessClient(s)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	if _, err := c.Initialize(ctx, mcp.InitializeRequest{}); err != nil {
		t.Fatal(err)
	}

	text := callText(t, c, "browser_get_tab", map[string]any{})
	if !strings.Contains(text, "No browser extension is connected") {
		t.Fatalf("expected friendly not-connected error, got: %s", text)
	}
}

// extResponse is what a custom fake extension returns for a request.
type extResponse struct {
	data map[string]any
	raw  string // optional raw JSON to send as `data` (for non-object results)
	err  string
}

type extRequest struct {
	ID      uint64
	Session string
	Action  string
	Params  map[string]any
}

func setupCustom(t *testing.T, respond func(extRequest) extResponse) (*bridge.Client, *client.Client) {
	t.Helper()
	h := hub.New(0, hub.Options{})
	if err := h.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(h.Close)
	ws, _, err := websocket.DefaultDialer.Dial("ws://"+h.Addr()+"/ws", nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ws.Close() })
	go func() {
		for {
			var req extRequest
			if err := ws.ReadJSON(&req); err != nil {
				return
			}
			r := respond(req)
			if r.err != "" {
				ws.WriteJSON(map[string]any{"id": req.ID, "session": req.Session, "ok": false, "error": r.err})
				continue
			}
			if r.raw != "" {
				ws.WriteJSON(map[string]any{"id": req.ID, "session": req.Session, "ok": true, "data": json.RawMessage(r.raw)})
				continue
			}
			ws.WriteJSON(map[string]any{"id": req.ID, "session": req.Session, "ok": true, "data": r.data})
		}
	}()

	br := bridge.NewClient(h.Addr(), "test-agent")
	br.SetSpawn(func(port int) error { return nil })
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := br.Connect(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(br.Close)
	s := server.NewMCPServer("browser-mcp", "1.0.0", server.WithToolCapabilities(true))
	bmcp.New(br).Register(s)
	c, err := client.NewInProcessClient(s)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	if _, err := c.Initialize(ctx, mcp.InitializeRequest{}); err != nil {
		t.Fatal(err)
	}
	return br, c
}

func TestUploadFile(t *testing.T) {
	_, c := setupCustom(t, func(req extRequest) extResponse {
		if req.Action == "executeScript" {
			return extResponse{data: map[string]any{"args": req.Params["args"]}}
		}
		return extResponse{data: map[string]any{}}
	})

	dir := t.TempDir()
	f := filepath.Join(dir, "resume.pdf")
	content := []byte("%PDF-1.4 hello upload")
	if err := os.WriteFile(f, content, 0o644); err != nil {
		t.Fatal(err)
	}

	text := callText(t, c, "browser_upload_file", map[string]any{"path": f})
	var out map[string]any
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatalf("upload output not JSON: %s (%v)", text, err)
	}
	args, _ := out["args"].(map[string]any)
	if args["fileName"] != "resume.pdf" {
		t.Fatalf("fileName wrong: %v", args)
	}
	b64, _ := args["b64"].(string)
	decoded, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		t.Fatalf("b64 not decodable: %v", err)
	}
	if string(decoded) != string(content) {
		t.Fatalf("b64 content mismatch: got %q", decoded)
	}
}

func TestNonObjectReturnSurfaces(t *testing.T) {
	_, c := setupCustom(t, func(req extRequest) extResponse {
		if req.Action == "executeScript" && strings.Contains(req.Params["code"].(string), "__RAW__") {
			return extResponse{raw: `"hello raw string"`}
		}
		return extResponse{data: map[string]any{}}
	})
	text := callText(t, c, "browser_execute_js", map[string]any{"code": "return '__RAW__';"})
	if !strings.Contains(text, "hello raw string") {
		t.Fatalf("string return collapsed: %s", text)
	}
}

func TestPayloadGuard(t *testing.T) {
	_, c := setupCustom(t, func(req extRequest) extResponse {
		return extResponse{data: map[string]any{}}
	})
	big := strings.Repeat("x", 200*1024)
	text := callText(t, c, "browser_execute_js", map[string]any{
		"code": "return 1;",
		"args": map[string]any{"blob": big},
	})
	if !strings.Contains(text, "payload too large") {
		t.Fatalf("expected size guard error, got: %.80s", text)
	}
}

func TestRetryReadOnce(t *testing.T) {
	var mu sync.Mutex
	calls := 0
	_, c := setupCustom(t, func(req extRequest) extResponse {
		if req.Action != "getTabInfo" {
			return extResponse{data: map[string]any{}}
		}
		mu.Lock()
		calls++
		n := calls
		mu.Unlock()
		if n == 1 {
			return extResponse{err: "browser: request timed out after 20000ms"}
		}
		return extResponse{data: map[string]any{"url": "https://ok.example", "title": "ok", "tabId": 1}}
	})

	text := callText(t, c, "browser_get_tab", map[string]any{})
	if !strings.Contains(text, "https://ok.example") {
		t.Fatalf("retry did not recover: %s", text)
	}
	mu.Lock()
	defer mu.Unlock()
	if calls != 2 {
		t.Fatalf("expected 2 calls after retry, got %d", calls)
	}
}

func TestWriteDoesNotRetry(t *testing.T) {
	var mu sync.Mutex
	calls := 0
	_, c := setupCustom(t, func(req extRequest) extResponse {
		mu.Lock()
		calls++
		mu.Unlock()
		return extResponse{err: "browser: request timed out after 20000ms"}
	})
	text := callText(t, c, "browser_type_text", map[string]any{"selector": "input", "text": "hi"})
	if !strings.Contains(text, "timed out") {
		t.Fatalf("write should fail fast: %s", text)
	}
	mu.Lock()
	defer mu.Unlock()
	if calls != 1 {
		t.Fatalf("write should not retry, got %d calls", calls)
	}
}
