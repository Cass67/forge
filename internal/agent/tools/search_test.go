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

func TestSearchRespectsIgnoreSecretBoundaries(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".ignore"), []byte("*.env\nsecrets/\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "secrets"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "app.env"), []byte("needle\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "secrets", "token.txt"), []byte("needle\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main // needle\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := NewSearch(dir)
	result, err := tool.Execute(context.Background(), map[string]any{"pattern": "needle"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "main.go") {
		t.Fatalf("expected allowed file match, got: %s", result)
	}
	if strings.Contains(result, "app.env") || strings.Contains(result, "secrets") {
		t.Fatalf("search result crossed ignored secret boundary: %s", result)
	}
}

func TestSearchRedactsSecretMatches(t *testing.T) {
	dir := t.TempDir()
	secret := dummySecret()
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n// token="+secret+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := NewSearch(dir)
	result, err := tool.Execute(context.Background(), map[string]any{"pattern": "token"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(result, secret) {
		t.Fatalf("search result leaked secret: %s", result)
	}
	if !strings.Contains(result, "<REDACTED:github-pat>") {
		t.Fatalf("search result missing redaction marker: %s", result)
	}
}

func TestSearchGrepRespectsIgnoreSecretDirectories(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".ignore"), []byte("secrets/\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "secrets"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "secrets", "token.txt"), []byte("needle\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main // needle\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := searchGrep(context.Background(), dir, "needle", ".", "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "main.go") {
		t.Fatalf("expected allowed file match, got: %s", result)
	}
	if strings.Contains(result, "secrets") {
		t.Fatalf("grep fallback crossed ignored secret directory: %s", result)
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
