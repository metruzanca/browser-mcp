// Command browser-mcp is a local MCP server that gives agents the ability to
// drive the browser tab the user is on. It speaks MCP over stdio (for
// Claude Code / opencode) and opens a loopback WebSocket bridge that the
// companion Chrome extension dials.
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/mark3labs/mcp-go/server"
	"github.com/metruzanca/browser-mcp/internal/bridge"
	"github.com/metruzanca/browser-mcp/internal/mcp"
)

func main() {
	port := flag.Int("port", 18765, "port for the browser-extension WebSocket bridge (0 = pick a free port)")
	flag.Parse()

	bs := bridge.New(*port, bridge.Options{
		OnConnect:    func() { fmt.Fprintln(os.Stderr, "browser-mcp: extension connected") },
		OnDisconnect: func(err error) { fmt.Fprintln(os.Stderr, "browser-mcp: extension disconnected:", err) },
	})
	if err := bs.Start(); err != nil {
		fmt.Fprintln(os.Stderr, "browser-mcp:", err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "browser-mcp: extension bridge listening on ws://127.0.0.1:%d/ws\n", bs.Port())
	fmt.Fprintln(os.Stderr, "browser-mcp: open the Chrome extension popup and click Connect")

	s := server.NewMCPServer("browser-mcp", "1.0.0", server.WithToolCapabilities(true))
	mcp.New(bs).Register(s)

	if err := server.ServeStdio(s); err != nil {
		fmt.Fprintln(os.Stderr, "browser-mcp:", err)
		os.Exit(1)
	}
}
