package bootstrap

import (
	"testing"

	"forge/internal/auth"
	"forge/internal/llm"
)

func TestPreflightFlagsAmbiguousCompatModel(t *testing.T) {
	cfg := testConfig()
	cfg.Models.Writer = "meta-llama/Llama-3.3-70B-Instruct-Turbo"
	cfg.Models.Auditor = "gpt-4o"
	cfg.Models.Summarizer = "gpt-4o-mini"
	cfg.Keys.Together = "together-key"
	cfg.Keys.OpenRouter = "openrouter-key"
	cfg.Keys.OpenAI = "openai-key"
	cfg.Session.OutputDir = t.TempDir()

	issues := Preflight(cfg, &auth.Tokens{}, llm.NewRegistry())
	found := false
	for _, issue := range issues {
		if issue.Name == cfg.Models.Writer && !issue.OK {
			found = true
		}
	}
	if !found {
		t.Fatal("expected ambiguous compat model issue")
	}
}
