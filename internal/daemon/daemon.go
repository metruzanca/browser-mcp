// Package daemon manages the detached browser-mcp hub daemon process: state
// directory, pid-file claiming (atomic), and spawning.
package daemon

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// StateDir returns the per-user state directory for browser-mcp.
func StateDir() string {
	if d := os.Getenv("XDG_STATE_HOME"); d != "" {
		return filepath.Join(d, "browser-mcp")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(os.TempDir(), "browser-mcp")
	}
	return filepath.Join(home, ".local", "state", "browser-mcp")
}

func pidPath(port int) string {
	return filepath.Join(StateDir(), fmt.Sprintf("daemon-%d.pid", port))
}

// LogPath returns the daemon log file for a port.
func LogPath(port int) string {
	return filepath.Join(StateDir(), fmt.Sprintf("daemon-%d.log", port))
}

// MkState creates the state directory if needed.
func MkState() error {
	return os.MkdirAll(StateDir(), 0o755)
}

// IsRunning reports whether a daemon for port owns its pid file and is alive.
func IsRunning(port int) bool {
	pid, err := readPID(port)
	if err != nil {
		return false
	}
	return processAlive(pid)
}

// Spawn starts a detached daemon for the given port (no-op if one is already
// running). stdout/stderr go to the daemon log; the process is detached from
// the caller's session.
func Spawn(port int) error {
	if IsRunning(port) {
		return nil
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(StateDir(), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(LogPath(port), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	cmd := exec.Command(exe, "daemon", "-port", strconv.Itoa(port))
	cmd.Stdout = f
	cmd.Stderr = f
	cmd.Stdin = nil
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	// Do not wait: the daemon outlives us.
	return nil
}

// ClaimPID atomically claims the daemon pid file for port. It returns owned =
// true only if this process created the file. If another live daemon owns it,
// owned = false and err = nil.
func ClaimPID(port int) (owned bool, err error) {
	if err := os.MkdirAll(StateDir(), 0o755); err != nil {
		return false, err
	}
	path := pidPath(port)
	for attempt := 0; attempt < 2; attempt++ {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err == nil {
			fmt.Fprintf(f, "%d\n", os.Getpid())
			f.Close()
			return true, nil
		}
		if !os.IsExist(err) {
			return false, err
		}
		// Someone owns the file: if they're alive, leave it; if stale, remove.
		pid, perr := readPID(port)
		if perr == nil && processAlive(pid) {
			return false, nil
		}
		if err := os.Remove(path); err != nil {
			return false, err
		}
	}
	return false, errors.New("could not claim daemon pid file")
}

// ReleasePID removes the daemon pid file for port if this process owns it.
func ReleasePID(port int) {
	pid, err := readPID(port)
	if err != nil || pid != os.Getpid() {
		return
	}
	os.Remove(pidPath(port))
}

func readPID(port int) (int, error) {
	b, err := os.ReadFile(pidPath(port))
	if err != nil {
		return 0, err
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil {
		return 0, err
	}
	return pid, nil
}

func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}
