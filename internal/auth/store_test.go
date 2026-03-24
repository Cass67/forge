package auth

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestCustomProviderAPIKeys(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmp)

	authDir := filepath.Join(tmp, "forge")
	if err := os.MkdirAll(authDir, 0700); err != nil {
		t.Fatal(err)
	}

	t.Run("save and load custom provider key", func(t *testing.T) {
		tok := &Tokens{}
		tok.SetCustomProviderKey("my-ollama", "sk-ollama-123")

		if err := SaveExact(tok); err != nil {
			t.Fatal(err)
		}

		loaded, err := Load()
		if err != nil {
			t.Fatal(err)
		}
		got := loaded.CustomProviderKey("my-ollama")
		if got != "sk-ollama-123" {
			t.Errorf("expected sk-ollama-123, got %q", got)
		}
	})

	t.Run("merge does not clobber built-in keys", func(t *testing.T) {
		base := &Tokens{
			AnthropicAPIKey: "ant-key-existing",
			OpenAIAPIKey:    "oai-key-existing",
		}
		base.SetCustomProviderKey("custom-a", "key-a")
		if err := SaveExact(base); err != nil {
			t.Fatal(err)
		}

		update := &Tokens{}
		update.SetCustomProviderKey("custom-b", "key-b")

		if err := Save(update); err != nil {
			t.Fatal(err)
		}

		loaded, err := Load()
		if err != nil {
			t.Fatal(err)
		}

		if loaded.AnthropicAPIKey != "ant-key-existing" {
			t.Errorf("built-in AnthropicAPIKey clobbered: got %q", loaded.AnthropicAPIKey)
		}
		if loaded.OpenAIAPIKey != "oai-key-existing" {
			t.Errorf("built-in OpenAIAPIKey clobbered: got %q", loaded.OpenAIAPIKey)
		}
		if loaded.CustomProviderKey("custom-a") != "key-a" {
			t.Errorf("existing custom key clobbered: got %q", loaded.CustomProviderKey("custom-a"))
		}
		if loaded.CustomProviderKey("custom-b") != "key-b" {
			t.Errorf("new custom key not merged: got %q", loaded.CustomProviderKey("custom-b"))
		}
	})

	t.Run("merge new custom keys without clobbering existing ones", func(t *testing.T) {
		base := &Tokens{}
		base.SetCustomProviderKey("provider-1", "key-1")
		base.SetCustomProviderKey("provider-2", "key-2")
		if err := SaveExact(base); err != nil {
			t.Fatal(err)
		}

		update := &Tokens{}
		update.SetCustomProviderKey("provider-3", "key-3")

		if err := Save(update); err != nil {
			t.Fatal(err)
		}

		loaded, err := Load()
		if err != nil {
			t.Fatal(err)
		}

		for _, tc := range []struct{ id, want string }{
			{"provider-1", "key-1"},
			{"provider-2", "key-2"},
			{"provider-3", "key-3"},
		} {
			if got := loaded.CustomProviderKey(tc.id); got != tc.want {
				t.Errorf("CustomProviderKey(%q) = %q, want %q", tc.id, got, tc.want)
			}
		}
	})

	t.Run("clear custom provider key with SaveExact", func(t *testing.T) {
		base := &Tokens{AnthropicAPIKey: "ant-key"}
		base.SetCustomProviderKey("to-keep", "keep-val")
		base.SetCustomProviderKey("to-remove", "remove-val")
		if err := SaveExact(base); err != nil {
			t.Fatal(err)
		}

		loaded, err := Load()
		if err != nil {
			t.Fatal(err)
		}
		loaded.ClearCustomProviderKey("to-remove")
		if err := SaveExact(loaded); err != nil {
			t.Fatal(err)
		}

		final, err := Load()
		if err != nil {
			t.Fatal(err)
		}
		if final.CustomProviderKey("to-keep") != "keep-val" {
			t.Errorf("kept key missing: got %q", final.CustomProviderKey("to-keep"))
		}
		if final.CustomProviderKey("to-remove") != "" {
			t.Errorf("removed key still present: got %q", final.CustomProviderKey("to-remove"))
		}
		if final.AnthropicAPIKey != "ant-key" {
			t.Errorf("built-in key lost: got %q", final.AnthropicAPIKey)
		}
	})

	t.Run("helper methods on nil map", func(t *testing.T) {
		tok := &Tokens{}
		if got := tok.CustomProviderKey("nonexistent"); got != "" {
			t.Errorf("expected empty string for nil map, got %q", got)
		}
		tok.ClearCustomProviderKey("nonexistent") // should not panic
	})

	t.Run("JSON round-trip preserves provider_api_keys", func(t *testing.T) {
		tok := &Tokens{AnthropicAPIKey: "ant"}
		tok.SetCustomProviderKey("local-llm", "llm-key")

		data, err := json.Marshal(tok)
		if err != nil {
			t.Fatal(err)
		}

		var decoded Tokens
		if err := json.Unmarshal(data, &decoded); err != nil {
			t.Fatal(err)
		}

		if decoded.CustomProviderKey("local-llm") != "llm-key" {
			t.Errorf("JSON round-trip lost key: got %q", decoded.CustomProviderKey("local-llm"))
		}
		if decoded.AnthropicAPIKey != "ant" {
			t.Errorf("JSON round-trip lost built-in: got %q", decoded.AnthropicAPIKey)
		}
	})
}
