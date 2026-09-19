package mcp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveSnippet(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "site"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "site", "flow.js"), []byte("// My flow\ntrue;"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BROWSER_MCP_SNIPPETS", dir)

	src, path, err := resolveSnippet("site/flow")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(src, "My flow") {
		t.Fatalf("bad source: %q", src)
	}
	if path != filepath.Join(dir, "site", "flow.js") {
		t.Fatalf("bad path: %q", path)
	}

	if _, _, err := resolveSnippet("site/missing"); err == nil {
		t.Fatal("expected error for missing snippet")
	}
	if _, _, err := resolveSnippet("../evil"); err == nil {
		t.Fatal("expected error for path-traversal name")
	}
	if _, _, err := resolveSnippet("a b"); err == nil {
		t.Fatal("expected error for invalid name")
	}
}

func TestListSnippets(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(dir, "a.js"), []byte("// Alpha\n1;"), 0o644)
	os.WriteFile(filepath.Join(dir, "sub", "b.js"), []byte("// Beta\n2;"), 0o644)
	os.WriteFile(filepath.Join(dir, "ignored.txt"), []byte("x"), 0o644)
	t.Setenv("BROWSER_MCP_SNIPPETS", dir)

	got, err := listSnippets()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 snippets, got %d: %v", len(got), got)
	}
	if got[0]["name"] != "a" || got[0]["description"] != "Alpha" {
		t.Fatalf("bad first snippet: %v", got[0])
	}
	if got[1]["name"] != "sub/b" || got[1]["description"] != "Beta" {
		t.Fatalf("bad second snippet: %v", got[1])
	}
}

func TestSnippetDescription(t *testing.T) {
	if got := snippetDescription("// Hello world\nreturn 1;"); got != "Hello world" {
		t.Fatalf("got %q", got)
	}
	if got := snippetDescription("\n//\n// Doc\ncode"); got != "Doc" {
		t.Fatalf("got %q", got)
	}
}
