package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteFileNew(t *testing.T) {
	dir := t.TempDir()
	tool := NewWriteFile(dir, func(a Action) (bool, error) { return true, nil })

	result, err := tool.Execute(context.Background(), map[string]any{
		"path": "new.go", "content": "package main\n",
	})
	if err != nil {
		t.Fatal(err)
	}

	data, _ := os.ReadFile(filepath.Join(dir, "new.go"))
	if string(data) != "package main\n" {
		t.Errorf("file content = %q", string(data))
	}
	if !strings.Contains(result, "wrote") || !strings.Contains(result, "new.go") {
		t.Errorf("unexpected result: %s", result)
	}
}

func TestWriteFileCreatesDirs(t *testing.T) {
	dir := t.TempDir()
	tool := NewWriteFile(dir, func(a Action) (bool, error) { return true, nil })

	_, err := tool.Execute(context.Background(), map[string]any{
		"path": "pkg/sub/file.go", "content": "package sub\n",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "pkg", "sub", "file.go")); err != nil {
		t.Error("file not created with parent dirs")
	}
}

func TestWriteFileDenied(t *testing.T) {
	dir := t.TempDir()
	tool := NewWriteFile(dir, func(a Action) (bool, error) { return false, nil })

	result, err := tool.Execute(context.Background(), map[string]any{
		"path": "denied.go", "content": "package main\n",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "denied") {
		t.Errorf("expected denied message, got: %s", result)
	}
	if _, err := os.Stat(filepath.Join(dir, "denied.go")); err == nil {
		t.Error("file should not exist after denial")
	}
}

func TestWriteFileEscape(t *testing.T) {
	dir := t.TempDir()
	tool := NewWriteFile(dir, func(a Action) (bool, error) { return true, nil })
	_, err := tool.Execute(context.Background(), map[string]any{
		"path": "../escape.go", "content": "bad",
	})
	if err == nil {
		t.Error("expected error for path escape")
	}
}
