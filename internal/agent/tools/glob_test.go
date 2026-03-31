package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGlobBasic(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "src"), 0o755)
	os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main"), 0o644)
	os.WriteFile(filepath.Join(dir, "src", "lib.go"), []byte("package src"), 0o644)
	os.WriteFile(filepath.Join(dir, "readme.md"), []byte("# hi"), 0o644)

	tool := NewGlob(dir, nil)
	result, err := tool.Execute(context.Background(), map[string]any{"pattern": "**/*.go"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "main.go") {
		t.Error("should match main.go")
	}
	if !strings.Contains(result, filepath.Join("src", "lib.go")) {
		t.Error("should match src/lib.go")
	}
	if strings.Contains(result, "readme.md") {
		t.Error("should not match readme.md")
	}
}

func TestGlobIgnoreDirs(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "node_modules"), 0o755)
	os.WriteFile(filepath.Join(dir, "node_modules", "pkg.js"), []byte(""), 0o644)
	os.WriteFile(filepath.Join(dir, "app.js"), []byte(""), 0o644)

	tool := NewGlob(dir, []string{"node_modules"})
	result, err := tool.Execute(context.Background(), map[string]any{"pattern": "**/*.js"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "app.js") {
		t.Error("should match app.js")
	}
	if strings.Contains(result, "node_modules") {
		t.Error("should skip node_modules")
	}
}

func TestGlobPathEscape(t *testing.T) {
	dir := t.TempDir()
	// glob now allows reading outside the workdir (read-only access)
	tool := NewGlob(dir, nil)
	result, err := tool.Execute(context.Background(), map[string]any{
		"pattern": "*.go",
		"path":    "../../etc",
	})
	if err != nil {
		t.Fatal(err)
	}
	// /etc typically has no .go files, so we expect "no matches found"
	if !strings.Contains(result, "no matches") {
		t.Errorf("expected 'no matches' for /etc, got: %s", result)
	}
}

func TestGlobNoMatches(t *testing.T) {
	dir := t.TempDir()
	tool := NewGlob(dir, nil)
	result, err := tool.Execute(context.Background(), map[string]any{"pattern": "**/*.xyz"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "no matches") {
		t.Errorf("expected 'no matches', got: %s", result)
	}
}
