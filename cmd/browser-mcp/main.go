// Command browser-mcp is a local MCP server that gives agents the ability to
// drive the browser tab the user is on. It speaks MCP over stdio (for
// Claude Code / opencode) and connects to the browser-mcp hub, which owns the
// WebSocket to the Chrome extension.
//
// Running with no subcommand starts an agent session (stdio MCP). Running
// `browser-mcp daemon` starts the long-lived hub that the extension connects
// to. Agents auto-spawn the daemon on first use.
package main

import (
	"context"
	"flag"
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/mark3labs/mcp-go/server"
	"github.com/metruzanca/browser-mcp/internal/bridge"
	"github.com/metruzanca/browser-mcp/internal/daemon"
	"github.com/metruzanca/browser-mcp/internal/hub"
	"github.com/metruzanca/browser-mcp/internal/mcp"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "daemon" {
		runDaemon(os.Args[2:])
		return
	}
	runAgent(os.Args[1:])
}

func runAgent(args []string) {
	fs := flag.NewFlagSet("browser-mcp", flag.ExitOnError)
	hubAddr := fs.String("hub", "127.0.0.1:18765", "hub address (host:port)")
	name := fs.String("name", defaultAgentName(), "agent display name shown by the hub")
	fs.Parse(args)

	cl := bridge.NewClient(*hubAddr, *name)
	// The hub daemon binds the same port the agent dials.
	cl.SetDaemonPort(hubPort(*hubAddr))

	s := server.NewMCPServer("browser-mcp", "1.0.0", server.WithToolCapabilities(true))
	mcp.New(cl).Register(s)

	// Warm the hub connection so the first tool call is fast (and log where
	// the hub lives). Failure is non-fatal; tools report it lazily. This also
	// spawns the daemon on first use.
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	if err := cl.Ensure(ctx); err != nil {
		fmt.Fprintln(os.Stderr, "browser-mcp: warning: hub unavailable:", err)
		fmt.Fprintln(os.Stderr, "browser-mcp: tools will report it until a hub is running")
	} else {
		fmt.Fprintf(os.Stderr, "browser-mcp: connected to hub at ws://%s (session %s)\n", *hubAddr, cl.Session()[:8])
	}

	if err := server.ServeStdio(s); err != nil {
		fmt.Fprintln(os.Stderr, "browser-mcp:", err)
		os.Exit(1)
	}
	cl.Close()
}

func runDaemon(args []string) {
	fs := flag.NewFlagSet("daemon", flag.ExitOnError)
	port := fs.Int("port", 18765, "hub port")
	fs.Parse(args)

	// Double-fork: re-exec a grandchild and exit, so the real hub gets
	// reparented to init and is no longer inside whatever process tree
	// started it (open code sessions may kill their whole tree on exit).
	if os.Getenv("BMCP_DAEMONIZED") != "1" {
		exe, err := os.Executable()
		if err != nil {
			fmt.Fprintln(os.Stderr, "browser-mcp daemon:", err)
			os.Exit(1)
		}
		if err := daemon.MkState(); err != nil {
			os.Exit(1)
		}
		logf, err := os.OpenFile(daemon.LogPath(*port), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			fmt.Fprintln(os.Stderr, "browser-mcp daemon:", err)
			os.Exit(1)
		}
		cmd := exec.Command(exe, append([]string{"daemon", "-port", strconv.Itoa(*port)}, args[2:]...)...)
		cmd.Env = append(os.Environ(), "BMCP_DAEMONIZED=1")
		cmd.Stdout = logf
		cmd.Stderr = logf
		cmd.Stdin = nil
		cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
		if err := cmd.Start(); err != nil {
			fmt.Fprintln(os.Stderr, "browser-mcp daemon:", err)
			os.Exit(1)
		}
		os.Exit(0)
	}

	if daemon.IsRunning(*port) {
		fmt.Fprintf(os.Stderr, "browser-mcp daemon: already running on port %d\n", *port)
		return
	}
	owned, err := daemon.ClaimPID(*port)
	if err != nil {
		fmt.Fprintln(os.Stderr, "browser-mcp daemon:", err)
		os.Exit(1)
	}
	if !owned {
		fmt.Fprintf(os.Stderr, "browser-mcp daemon: another instance owns port %d\n", *port)
		return
	}
	defer daemon.ReleasePID(*port)

	h := hub.New(*port, hub.Options{
		OnConnect:    func() { fmt.Fprintln(os.Stderr, "browser-mcp daemon: extension connected") },
		OnDisconnect: func(err error) { fmt.Fprintln(os.Stderr, "browser-mcp daemon: extension disconnected:", err) },
	})
	if err := h.Start(); err != nil {
		fmt.Fprintln(os.Stderr, "browser-mcp daemon:", err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "browser-mcp daemon: hub on ws://127.0.0.1:%d/ws (pid %d)\n", h.Port(), os.Getpid())

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	<-sig
	h.Close()
}

func hubPort(addr string) int {
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		return 18765
	}
	p, err := strconv.Atoi(port)
	if err != nil {
		return 18765
	}
	return p
}

func defaultAgentName() string {
	wd, err := os.Getwd()
	if err != nil {
		return "opencode"
	}
	base := filepath.Base(wd)
	if base == "/" || base == "." {
		return "opencode"
	}
	return "opencode:" + base
}
