package mcp_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/metruzanca/browser-mcp/internal/bridge"
	bmcp "github.com/metruzanca/browser-mcp/internal/mcp"
)

// TestEndToEnd boots the MCP server in-process, connects a fake browser
// extension over the WebSocket bridge, and drives a few tools end to end.
func TestEndToEnd(t *testing.T) {
	br := bridge.New(0, bridge.Options{})
	if err := br.Start(); err != nil {
		t.Fatal(err)
	}
	defer br.Close()

	s := server.NewMCPServer("browser-mcp", "1.0.0", server.WithToolCapabilities(true))
	bmcp.New(br).Register(s)
	c, err := client.NewInProcessClient(s)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := c.Initialize(ctx, mcp.InitializeRequest{}); err != nil {
		t.Fatal(err)
	}

	// Fake extension: connect to the bridge, answer each action.
	ws, _, err := websocket.DefaultDialer.Dial("ws://"+br.Addr()+"/ws", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close()
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		for {
			var req struct {
				ID     uint64         `json:"id"`
				Action string         `json:"action"`
				Params map[string]any `json:"params"`
			}
			if err := ws.ReadJSON(&req); err != nil {
				return
			}
			switch req.Action {
			case "getTabInfo":
				ws.WriteJSON(map[string]any{"id": req.ID, "ok": true, "data": map[string]any{
					"url": "https://jobs.example.com/apply", "title": "Apply", "tabId": 7,
				}})
			case "executeScript":
				code, _ := req.Params["code"].(string)
				var args map[string]any
				if a, ok := req.Params["args"].(map[string]any); ok {
					args = a
				}
				result := map[string]any{
					"gotCode":   strings.Contains(code, "bmcp"),
					"args":      args,
					"worldUsed": "main",
				}
				ws.WriteJSON(map[string]any{"id": req.ID, "ok": true, "data": result})
			}
		}
	}()

	tools, err := c.ListTools(ctx, mcp.ListToolsRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if len(tools.Tools) < 10 {
		t.Fatalf("expected >= 10 tools, got %d", len(tools.Tools))
	}

	// browser_get_tab
	res, err := c.CallTool(ctx, mcp.CallToolRequest{
		Params: mcp.CallToolParams{Name: "browser_get_tab", Arguments: map[string]any{}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Content) == 0 {
		t.Fatal("get_tab returned no content")
	}
	text := res.Content[0].(mcp.TextContent).Text
	if !strings.Contains(text, "https://jobs.example.com/apply") {
		t.Fatalf("get_tab output missing URL: %s", text)
	}

	// browser_execute_js forwards code + args to the page (the fake
	// extension echoes them back, simulating the page-side execution).
	res, err = c.CallTool(ctx, mcp.CallToolRequest{
		Params: mcp.CallToolParams{Name: "browser_execute_js", Arguments: map[string]any{
			"code": "return { fromArgs: args.firstName, hasBmcp: !!bmcp };",
			"args": map[string]any{"firstName": "Sam"},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	text = res.Content[0].(mcp.TextContent).Text
	if !strings.Contains(text, `"firstName": "Sam"`) || !strings.Contains(text, "gotCode") {
		t.Fatalf("execute_js output wrong: %s", text)
	}

	// browser_type_text (wrapper) passes args through to the page code
	res, err = c.CallTool(ctx, mcp.CallToolRequest{
		Params: mcp.CallToolParams{Name: "browser_type_text", Arguments: map[string]any{
			"selector": "input[name=firstName]", "text": "Sam Zanca",
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	text = res.Content[0].(mcp.TextContent).Text
	var data map[string]any
	if err := json.Unmarshal([]byte(text), &data); err != nil {
		t.Fatalf("type_text output not JSON: %s (%v)", text, err)
	}
	args, _ := data["args"].(map[string]any)
	if args["selector"] != "input[name=firstName]" || args["text"] != "Sam Zanca" {
		t.Fatalf("type_text args not forwarded: %v", args)
	}
}

// TestNotConnected verifies the friendly error when no extension is attached.
func TestNotConnected(t *testing.T) {
	br := bridge.New(0, bridge.Options{})
	if err := br.Start(); err != nil {
		t.Fatal(err)
	}
	defer br.Close()

	s := server.NewMCPServer("browser-mcp", "1.0.0", server.WithToolCapabilities(true))
	bmcp.New(br).Register(s)
	c, err := client.NewInProcessClient(s)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := c.Initialize(ctx, mcp.InitializeRequest{}); err != nil {
		t.Fatal(err)
	}

	res, err := c.CallTool(ctx, mcp.CallToolRequest{
		Params: mcp.CallToolParams{Name: "browser_get_tab", Arguments: map[string]any{}},
	})
	if err != nil {
		t.Fatal(err)
	}
	text := res.Content[0].(mcp.TextContent).Text
	if !strings.Contains(text, "No browser extension is connected") {
		t.Fatalf("expected friendly not-connected error, got: %s", text)
	}
}
