package bootstrap

import (
	"testing"

	"forge/internal/auth"
	"forge/internal/config"
)

func TestParseModelRef(t *testing.T) {
	ref := ParseModelRef("openrouter/openai/gpt-4o")
	if ref.Provider != "openrouter" || ref.Model != "openai/gpt-4o" {
		t.Fatalf("unexpected ref: %+v", ref)
	}
}

func TestResolveCompatProviderDetectsAmbiguity(t *testing.T) {
	cfg := testConfig()
	cfg.Keys.Together = "together-key"
	cfg.Keys.OpenRouter = "openrouter-key"

	p, ambiguous := ResolveCompatProvider(BuildCompatProviders(cfg), "meta-llama/Llama-3.3-70B-Instruct-Turbo")
	if p != nil {
		t.Fatalf("expected nil provider for ambiguous match, got %s", p.Name)
	}
	if !ambiguous {
		t.Fatal("expected ambiguity to be reported")
	}
}

func TestDriverForExplicitCompatProvider(t *testing.T) {
	cfg := testConfig()
	cfg.Keys.OpenRouter = "openrouter-key"
	d := DriverForModel(cfg, &auth.Tokens{}, "openrouter/openai/gpt-4o")
	if d == nil {
		t.Fatal("expected explicit provider-qualified model to resolve")
	}
}

func TestCanonicalOpenAIModel(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{in: "gpt-5.4", want: "gpt-5"},
		{in: "gpt5.4", want: "gpt-5"},
		{in: " gpt-5 ", want: "gpt-5"},
		{in: "gpt-4o", want: "gpt-4o"},
	}

	for _, tt := range tests {
		if got := canonicalOpenAIModel(tt.in); got != tt.want {
			t.Fatalf("canonicalOpenAIModel(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestDriverForModelMapsLegacyOpenAIAlias(t *testing.T) {
	cfg := testConfig()
	cfg.Keys.OpenAI = "openai-key"

	d := DriverForModel(cfg, &auth.Tokens{}, "gpt-5.4")
	if d == nil {
		t.Fatal("expected legacy openai alias to resolve")
	}
	if got := d.Name(); got != "gpt-5.4" {
		t.Fatalf("driver.Name() = %q, want %q", got, "gpt-5.4")
	}
}

func testConfig() *config.Config {
	cfg := &config.Config{}
	cfg.Models.Writer = "claude-3-7-sonnet-latest"
	cfg.Models.Auditor = "gpt-4o"
	cfg.Models.Summarizer = "claude-3-5-haiku-latest"
	cfg.Session.OutputDir = "."
	cfg.Retry.MaxAttempts = 3
	cfg.Retry.InitialWait = 1000
	cfg.Retry.MaxWait = 30000
	cfg.Retry.Timeout = 300
	cfg.Chat.MaxTurns = 50
	cfg.Chat.CommandTimeout = 60
	cfg.Log.Level = "info"
	cfg.Models.WriterParams.Temperature = -1
	cfg.Models.AuditorParams.Temperature = -1
	return cfg
}
