package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEditFileBasic(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n\nfunc hello() {}\n"), 0o644)

	tool := NewEditFile(dir, func(a Action) (bool, error) { return true, nil })
	result, err := tool.Execute(context.Background(), map[string]any{
		"path":     "main.go",
		"old_text": "func hello() {}",
		"new_text": "func hello() {\n\tfmt.Println(\"hello\")\n}",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "edited") {
		t.Errorf("unexpected result: %s", result)
	}

	data, _ := os.ReadFile(filepath.Join(dir, "main.go"))
	if !strings.Contains(string(data), "Println") {
		t.Error("edit not applied")
	}
}

func TestEditFileNotFound(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n"), 0o644)

	tool := NewEditFile(dir, func(a Action) (bool, error) { return true, nil })
	result, err := tool.Execute(context.Background(), map[string]any{
		"path": "main.go", "old_text": "nonexistent text", "new_text": "new",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "not found") {
		t.Errorf("expected 'not found' error, got: %s", result)
	}
}

func TestEditFileReportsReplacementAlreadyPresentWhenOldTextIsStale(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "main.go"), []byte("package foo\n"), 0o644)

	tool := NewEditFile(dir, func(a Action) (bool, error) { return true, nil })
	result, err := tool.Execute(context.Background(), map[string]any{
		"path": "main.go", "old_text": "package main", "new_text": "package foo",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "already present") {
		t.Errorf("expected already-present result, got: %s", result)
	}
}

func TestEditFileMultipleMatches(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "main.go"), []byte("foo\nbar\nfoo\n"), 0o644)

	tool := NewEditFile(dir, func(a Action) (bool, error) { return true, nil })
	result, err := tool.Execute(context.Background(), map[string]any{
		"path": "main.go", "old_text": "foo", "new_text": "baz",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "matched 2") {
		t.Errorf("expected multiple match error, got: %s", result)
	}
}

func TestEditFileDenied(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n"), 0o644)

	tool := NewEditFile(dir, func(a Action) (bool, error) { return false, nil })
	result, err := tool.Execute(context.Background(), map[string]any{
		"path": "main.go", "old_text": "package main", "new_text": "package foo",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "denied") {
		t.Error("expected denied message")
	}
	data, _ := os.ReadFile(filepath.Join(dir, "main.go"))
	if !strings.Contains(string(data), "package main") {
		t.Error("file should be unchanged after denial")
	}
}

func TestEditFileBlocksSecretByDefault(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.go")
	if err := os.WriteFile(path, []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	approved := false
	tool := NewEditFile(dir, func(a Action) (bool, error) {
		approved = true
		return true, nil
	})

	result, err := tool.Execute(context.Background(), map[string]any{
		"path": "main.go", "old_text": "package main", "new_text": "package main\n// token=" + dummySecret(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "blocked") || strings.Contains(result, dummySecret()) {
		t.Fatalf("expected redacted block result, got: %s", result)
	}
	if approved {
		t.Fatal("edit containing secret should not request normal approval")
	}
	data, _ := os.ReadFile(path)
	if strings.Contains(string(data), dummySecret()) {
		t.Fatal("secret edit should not be applied")
	}
}
