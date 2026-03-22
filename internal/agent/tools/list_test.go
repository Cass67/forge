package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestListDirBasic(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "main.go"), []byte("x"), 0o644)
	os.Mkdir(filepath.Join(dir, "pkg"), 0o755)
	os.WriteFile(filepath.Join(dir, "pkg", "util.go"), []byte("x"), 0o644)

	tool := NewListDir(dir, nil)
	result, err := tool.Execute(context.Background(), map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "main.go") {
		t.Error("missing main.go")
	}
	if !strings.Contains(result, "pkg/") {
		t.Error("missing pkg/ dir")
	}
}

func TestListDirRecursive(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "a", "b"), 0o755)
	os.WriteFile(filepath.Join(dir, "a", "b", "deep.go"), []byte("x"), 0o644)

	tool := NewListDir(dir, nil)
	result, err := tool.Execute(context.Background(), map[string]any{
		"path": ".", "recursive": true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "deep.go") {
		t.Error("missing deep.go in recursive listing")
	}
}

func TestListDirIgnoresDotGit(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, ".git", "objects"), 0o755)
	os.WriteFile(filepath.Join(dir, "main.go"), []byte("x"), 0o644)

	tool := NewListDir(dir, []string{".git"})
	result, err := tool.Execute(context.Background(), map[string]any{"recursive": true})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(result, ".git") {
		t.Error("should not list .git")
	}
}
