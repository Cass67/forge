package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadFileBasic(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "hello.go"), []byte("package main\n\nfunc main() {}\n"), 0o644)

	tool := NewReadFile(dir)
	result, err := tool.Execute(context.Background(), map[string]any{"path": "hello.go"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "package main") {
		t.Error("result missing file content")
	}
	if !strings.Contains(result, "1 |") {
		t.Error("result missing line numbers")
	}
}

func TestReadFileLineRange(t *testing.T) {
	dir := t.TempDir()
	content := "line1\nline2\nline3\nline4\nline5\n"
	os.WriteFile(filepath.Join(dir, "test.txt"), []byte(content), 0o644)

	tool := NewReadFile(dir)
	result, err := tool.Execute(context.Background(), map[string]any{
		"path": "test.txt", "start_line": float64(2), "end_line": float64(4),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "line2") {
		t.Error("missing line2")
	}
	if strings.Contains(result, "line1") {
		t.Error("should not contain line1")
	}
	if strings.Contains(result, "line5") {
		t.Error("should not contain line5")
	}
}

func TestReadFileEscape(t *testing.T) {
	dir := t.TempDir()
	tool := NewReadFile(dir)
	_, err := tool.Execute(context.Background(), map[string]any{"path": "../etc/passwd"})
	if err == nil {
		t.Error("expected error for path escape")
	}
}

func TestReadFileBinary(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "bin"), []byte{0x00, 0x01, 0x02}, 0o644)

	tool := NewReadFile(dir)
	result, err := tool.Execute(context.Background(), map[string]any{"path": "bin"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "binary") {
		t.Error("expected binary file error message")
	}
}

func TestReadFileNotFound(t *testing.T) {
	dir := t.TempDir()
	tool := NewReadFile(dir)
	result, err := tool.Execute(context.Background(), map[string]any{"path": "nope.go"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "error") && !strings.Contains(result, "no such file") {
		t.Errorf("expected error message, got: %s", result)
	}
}
