package bootstrap

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEnsureConfigScaffoldCreatesProvidersTemplate(t *testing.T) {
	configDir := filepath.Join(t.TempDir(), "forge")

	if err := ensureConfigScaffold(configDir); err != nil {
		t.Fatalf("ensureConfigScaffold: %v", err)
	}

	if info, err := os.Stat(configDir); err != nil || !info.IsDir() {
		t.Fatalf("expected config dir to exist, err=%v", err)
	}

	providersDir := filepath.Join(configDir, "providers")
	if info, err := os.Stat(providersDir); err != nil || !info.IsDir() {
		t.Fatalf("expected providers dir to exist, err=%v", err)
	}

	templatePath := filepath.Join(providersDir, "example.toml")
	data, err := os.ReadFile(templatePath)
	if err != nil {
		t.Fatalf("ReadFile(%q): %v", templatePath, err)
	}

	content := string(data)
	if !strings.Contains(content, "base_url") {
		t.Fatalf("expected template content, got:\n%s", content)
	}
	for _, line := range strings.Split(content, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "[model_providers.") {
			t.Fatalf("template should stay commented so it is not loaded as a real provider, got:\n%s", content)
		}
	}

	defs, err := LoadCustomCompatProviders(configDir)
	if err != nil {
		t.Fatalf("LoadCustomCompatProviders: %v", err)
	}
	if len(defs) != 0 {
		t.Fatalf("expected no active providers from template, got %d", len(defs))
	}
}

func TestEnsureConfigScaffoldDoesNotOverwriteExistingTemplate(t *testing.T) {
	configDir := filepath.Join(t.TempDir(), "forge")
	providersDir := filepath.Join(configDir, "providers")
	if err := os.MkdirAll(providersDir, 0o700); err != nil {
		t.Fatal(err)
	}

	templatePath := filepath.Join(providersDir, "example.toml")
	if err := os.WriteFile(templatePath, []byte("keep-me\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := ensureConfigScaffold(configDir); err != nil {
		t.Fatalf("ensureConfigScaffold: %v", err)
	}

	data, err := os.ReadFile(templatePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "keep-me\n" {
		t.Fatalf("template was overwritten: %q", string(data))
	}
}
