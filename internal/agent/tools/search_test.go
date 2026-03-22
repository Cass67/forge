package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSearchBasic(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n\nfunc hello() {}\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "other.go"), []byte("package main\n\nfunc world() {}\n"), 0o644)

	tool := NewSearch(dir)
	result, err := tool.Execute(context.Background(), map[string]any{"pattern": "func"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "hello") {
		t.Error("missing hello match")
	}
	if !strings.Contains(result, "world") {
		t.Error("missing world match")
	}
}

func TestSearchWithGlob(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "readme.md"), []byte("# main package\n"), 0o644)

	tool := NewSearch(dir)
	result, err := tool.Execute(context.Background(), map[string]any{
		"pattern": "main", "glob": "*.go",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "main.go") {
		t.Error("should match main.go")
	}
	if strings.Contains(result, "readme.md") {
		t.Error("should not match readme.md with *.go glob")
	}
}

func TestSearchNoResults(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n"), 0o644)

	tool := NewSearch(dir)
	result, err := tool.Execute(context.Background(), map[string]any{"pattern": "zzzzzzz"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "no matches") {
		t.Errorf("expected 'no matches', got: %s", result)
	}
}
