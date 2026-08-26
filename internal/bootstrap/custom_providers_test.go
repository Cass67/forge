package bootstrap

import (
	"cmp"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestLoadCustomCompatProviders(t *testing.T) {
	t.Run("providers dir with dedicated file", func(t *testing.T) {
		dir := t.TempDir()
		providersDir := filepath.Join(dir, "providers")
		if err := os.MkdirAll(providersDir, 0o755); err != nil {
			t.Fatal(err)
		}

		content := `
[model_providers.oca]
name = "My New Provider"
base_url = "https://example.com/v1"
wire_api = "responses"
model_info_url = "https://example.com/v1/model/info"
http_headers = { client = "codex-cli" }
default_model = "gpt-5.4"
models = ["gpt-5.4", "gpt-5.4-mini"]
image_models = ["gpt-5.4"]
`
		if err := os.WriteFile(filepath.Join(providersDir, "oca.toml"), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}

		defs, err := LoadCustomCompatProviders(dir)
		if err != nil {
			t.Fatal(err)
		}
		if len(defs) != 1 {
			t.Fatalf("expected 1 provider, got %d", len(defs))
		}

		d := defs[0]
		if d.ID != "oca" {
			t.Errorf("ID = %q, want %q", d.ID, "oca")
		}
		if d.Name != "My New Provider" {
			t.Errorf("Name = %q, want %q", d.Name, "My New Provider")
		}
		if d.BaseURL != "https://example.com/v1" {
			t.Errorf("BaseURL = %q, want %q", d.BaseURL, "https://example.com/v1")
		}
		if d.WireAPI != "responses" {
			t.Errorf("WireAPI = %q, want %q", d.WireAPI, "responses")
		}
		if d.ModelInfoURL != "https://example.com/v1/model/info" {
			t.Errorf("ModelInfoURL = %q, want %q", d.ModelInfoURL, "https://example.com/v1/model/info")
		}
		if d.DefaultModel != "gpt-5.4" {
			t.Errorf("DefaultModel = %q, want %q", d.DefaultModel, "gpt-5.4")
		}
		if len(d.ImageModels) != 1 || d.ImageModels[0] != "gpt-5.4" {
			t.Errorf("ImageModels = %v, want [gpt-5.4]", d.ImageModels)
		}
		if len(d.Models) != 2 || d.Models[0] != "gpt-5.4" || d.Models[1] != "gpt-5.4-mini" {
			t.Errorf("Models = %v, want [gpt-5.4 gpt-5.4-mini]", d.Models)
		}
		if d.HTTPHeaders["client"] != "codex-cli" {
			t.Errorf("HTTPHeaders = %v, want client=codex-cli", d.HTTPHeaders)
		}
	})

	t.Run("root-level toml with model_providers block", func(t *testing.T) {
		dir := t.TempDir()

		content := `
model = "some-model"
sandbox_mode = true

[model_providers.myhost]
name = "My Host"
base_url = "https://myhost.example.com/api"
wire_api = "chat"
default_model = "llama-3"
models = ["llama-3", "llama-3-small"]

[profiles.fast]
model = "fast-model"
`
		if err := os.WriteFile(filepath.Join(dir, "codex.toml"), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}

		defs, err := LoadCustomCompatProviders(dir)
		if err != nil {
			t.Fatal(err)
		}
		if len(defs) != 1 {
			t.Fatalf("expected 1 provider, got %d", len(defs))
		}

		d := defs[0]
		if d.ID != "myhost" {
			t.Errorf("ID = %q, want %q", d.ID, "myhost")
		}
		if d.Name != "My Host" {
			t.Errorf("Name = %q, want %q", d.Name, "My Host")
		}
		if d.WireAPI != "chat" {
			t.Errorf("WireAPI = %q, want %q", d.WireAPI, "chat")
		}
	})

	t.Run("ignores unrelated toml without provider blocks", func(t *testing.T) {
		dir := t.TempDir()

		content := `
model = "gpt-4o"
sandbox_mode = false

[profiles.default]
model = "gpt-4o"
`
		if err := os.WriteFile(filepath.Join(dir, "settings.toml"), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}

		defs, err := LoadCustomCompatProviders(dir)
		if err != nil {
			t.Fatal(err)
		}
		if len(defs) != 0 {
			t.Fatalf("expected 0 providers, got %d", len(defs))
		}
	})

	t.Run("bare base_url gets https scheme prepended", func(t *testing.T) {
		dir := t.TempDir()
		providersDir := filepath.Join(dir, "providers")
		if err := os.MkdirAll(providersDir, 0o755); err != nil {
			t.Fatal(err)
		}

		content := `
[model_providers.bare]
name = "Bare URL"
base_url = "example.com/v1"
models = ["m1"]
`
		if err := os.WriteFile(filepath.Join(providersDir, "bare.toml"), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}

		defs, err := LoadCustomCompatProviders(dir)
		if err != nil {
			t.Fatal(err)
		}
		if len(defs) != 1 {
			t.Fatalf("expected 1 provider, got %d", len(defs))
		}
		if defs[0].BaseURL != "https://example.com/v1" {
			t.Errorf("BaseURL = %q, want %q", defs[0].BaseURL, "https://example.com/v1")
		}
	})

	t.Run("multiple providers in single file", func(t *testing.T) {
		dir := t.TempDir()
		providersDir := filepath.Join(dir, "providers")
		if err := os.MkdirAll(providersDir, 0o755); err != nil {
			t.Fatal(err)
		}

		content := `
[model_providers.alpha]
name = "Alpha"
base_url = "https://alpha.example.com"
models = ["a1"]

[model_providers.beta]
name = "Beta"
base_url = "https://beta.example.com"
models = ["b1"]
`
		if err := os.WriteFile(filepath.Join(providersDir, "multi.toml"), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}

		defs, err := LoadCustomCompatProviders(dir)
		if err != nil {
			t.Fatal(err)
		}
		if len(defs) != 2 {
			t.Fatalf("expected 2 providers, got %d", len(defs))
		}

		slices.SortFunc(defs, func(a, b CustomProviderDef) int { return cmp.Compare(a.ID, b.ID) })
		if defs[0].ID != "alpha" || defs[1].ID != "beta" {
			t.Errorf("IDs = [%q, %q], want [alpha, beta]", defs[0].ID, defs[1].ID)
		}
	})

	t.Run("both locations are scanned", func(t *testing.T) {
		dir := t.TempDir()
		providersDir := filepath.Join(dir, "providers")
		if err := os.MkdirAll(providersDir, 0o755); err != nil {
			t.Fatal(err)
		}

		providerContent := `
[model_providers.from_providers_dir]
name = "From Providers Dir"
base_url = "https://providers.example.com"
models = ["p1"]
`
		rootContent := `
[model_providers.from_root]
name = "From Root"
base_url = "https://root.example.com"
models = ["r1"]
`
		if err := os.WriteFile(filepath.Join(providersDir, "a.toml"), []byte(providerContent), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "b.toml"), []byte(rootContent), 0o644); err != nil {
			t.Fatal(err)
		}

		defs, err := LoadCustomCompatProviders(dir)
		if err != nil {
			t.Fatal(err)
		}
		if len(defs) != 2 {
			t.Fatalf("expected 2 providers, got %d", len(defs))
		}

		ids := map[string]bool{}
		for _, d := range defs {
			ids[d.ID] = true
		}
		if !ids["from_providers_dir"] || !ids["from_root"] {
			t.Errorf("expected both locations scanned, got IDs: %v", ids)
		}
	})

	t.Run("missing config dir returns empty", func(t *testing.T) {
		defs, err := LoadCustomCompatProviders("/nonexistent/path/that/does/not/exist")
		if err != nil {
			t.Fatal(err)
		}
		if len(defs) != 0 {
			t.Fatalf("expected 0 providers, got %d", len(defs))
		}
	})

	t.Run("http scheme is preserved", func(t *testing.T) {
		dir := t.TempDir()
		providersDir := filepath.Join(dir, "providers")
		if err := os.MkdirAll(providersDir, 0o755); err != nil {
			t.Fatal(err)
		}

		content := `
[model_providers.local]
name = "Local"
base_url = "http://localhost:8080/v1"
models = ["m1"]
`
		if err := os.WriteFile(filepath.Join(providersDir, "local.toml"), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}

		defs, err := LoadCustomCompatProviders(dir)
		if err != nil {
			t.Fatal(err)
		}
		if len(defs) != 1 {
			t.Fatalf("expected 1 provider, got %d", len(defs))
		}
		if defs[0].BaseURL != "http://localhost:8080/v1" {
			t.Errorf("BaseURL = %q, want %q", defs[0].BaseURL, "http://localhost:8080/v1")
		}
	})
}
