package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCodeSearchFindsLiteralMatches(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n\nfunc helloForge() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := NewCodeSearch(dir)
	result, err := tool.Execute(context.Background(), map[string]any{"query": "helloForge"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "helloForge") || !strings.Contains(result, "main.go") {
		t.Fatalf("unexpected result: %s", result)
	}
}
