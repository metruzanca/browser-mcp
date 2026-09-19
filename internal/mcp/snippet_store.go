package mcp

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// snippetDirs lists where reusable page scripts are looked up, in priority
// order: $BROWSER_MCP_SNIPPETS, then ~/.config/browser-mcp/snippets, then
// <exe-dir>/snippets, then ./snippets.
func snippetDirs() []string {
	var dirs []string
	if d := os.Getenv("BROWSER_MCP_SNIPPETS"); d != "" {
		dirs = append(dirs, d)
	}
	if cd, err := os.UserConfigDir(); err == nil {
		dirs = append(dirs, filepath.Join(cd, "browser-mcp", "snippets"))
	}
	if exe, err := os.Executable(); err == nil {
		dirs = append(dirs, filepath.Join(filepath.Dir(exe), "snippets"))
	}
	dirs = append(dirs, filepath.Join("snippets"))
	return dirs
}

func safeSnippetName(name string) bool {
	if name == "" || strings.HasPrefix(name, "/") || strings.Contains(name, "..") {
		return false
	}
	for _, r := range name {
		ok := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') ||
			r == '.' || r == '-' || r == '_' || r == '/'
		if !ok {
			return false
		}
	}
	return true
}

// resolveSnippet loads a snippet by name (e.g. "atproto-qr.app/generate") from
// the first matching directory and returns its source and absolute path.
func resolveSnippet(name string) (string, string, error) {
	if !safeSnippetName(name) {
		return "", "", fmt.Errorf("invalid snippet name %q", name)
	}
	rel := name + ".js"
	for _, dir := range snippetDirs() {
		p := filepath.Join(dir, rel)
		if b, err := os.ReadFile(p); err == nil {
			return string(b), p, nil
		}
	}
	return "", "", fmt.Errorf("snippet %q not found", name)
}

func snippetDescription(src string) string {
	for _, l := range strings.Split(src, "\n") {
		t := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(l), "//"))
		if t != "" {
			return t
		}
	}
	return ""
}

// listSnippets enumerates every *.js snippet in all configured directories,
// deduped by name (first directory wins) and sorted.
func listSnippets() ([]map[string]string, error) {
	seen := make(map[string]bool)
	var out []map[string]string
	var walk func(dir, prefix string)
	walk = func(dir, prefix string) {
		ents, err := os.ReadDir(dir)
		if err != nil {
			return
		}
		for _, e := range ents {
			name := e.Name()
			rel := name
			if prefix != "" {
				rel = prefix + "/" + name
			}
			if e.IsDir() {
				walk(filepath.Join(dir, name), rel)
				continue
			}
			if !strings.HasSuffix(name, ".js") {
				continue
			}
			key := strings.TrimSuffix(rel, ".js")
			if seen[key] {
				continue
			}
			seen[key] = true
			if b, err := os.ReadFile(filepath.Join(dir, name)); err == nil {
				out = append(out, map[string]string{"name": key, "description": snippetDescription(string(b))})
			}
		}
	}
	for _, dir := range snippetDirs() {
		walk(dir, "")
	}
	sort.Slice(out, func(i, j int) bool { return out[i]["name"] < out[j]["name"] })
	return out, nil
}
