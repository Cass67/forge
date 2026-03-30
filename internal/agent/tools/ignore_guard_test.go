package tools

import (
	"os"
	"path/filepath"
	"testing"
)

func TestIgnoreGuardBlocksMatchedFiles(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".ignore"), []byte("config.toml\n*.env\nauth.json\n"), 0600); err != nil {
		t.Fatal(err)
	}

	guard := newIgnoreGuard(dir)

	blocked := []string{
		filepath.Join(dir, "config.toml"),
		filepath.Join(dir, ".env"),
		filepath.Join(dir, "prod.env"),
		filepath.Join(dir, "auth.json"),
	}
	for _, p := range blocked {
		if !guard.blocked(p) {
			t.Errorf("expected %q to be blocked", p)
		}
	}

	allowed := []string{
		filepath.Join(dir, "main.go"),
		filepath.Join(dir, "README.md"),
		filepath.Join(dir, "internal/runtime/chat.go"),
	}
	for _, p := range allowed {
		if guard.blocked(p) {
			t.Errorf("expected %q to be allowed", p)
		}
	}
}

func TestReadFileRejectsIgnoredPath(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".ignore"), []byte("config.toml\n"), 0600); err != nil {
		t.Fatal(err)
	}
	secret := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(secret, []byte("API_KEY=secret\n"), 0600); err != nil {
		t.Fatal(err)
	}

	tool := NewReadFile(dir)
	result, err := tool.Execute(t.Context(), map[string]any{"path": "config.toml"})
	if err != nil {
		t.Fatal(err)
	}
	if result == "" || result[:5] != "error" {
		t.Fatalf("expected error result, got %q", result)
	}
	// The actual file content must not appear in the error.
	if containsStr(result, "API_KEY=secret") {
		t.Fatalf("secret content leaked in result: %q", result)
	}
}

func containsStr(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
