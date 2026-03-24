package config_test

import (
	"forge/internal/config"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func setTempHome(t *testing.T) {
	t.Helper()
	home := filepath.Join(t.TempDir(), "home")
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
}

func writeTemp(t *testing.T, content string) string {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "forge-*.toml")
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString(content)
	f.Close()
	return f.Name()
}

func TestLoadDefaults(t *testing.T) {
	path := writeTemp(t, "")
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Models.Writer == "" {
		t.Error("expected default writer model")
	}
	if cfg.Session.RoundsPerPass < 1 {
		t.Error("expected positive default rounds")
	}
}

func TestLoadExplicit(t *testing.T) {
	toml := `
[models]
writer     = "claude-opus-4-6"
auditor    = "gpt-4o"
summarizer = "claude-haiku-4-5-20251001"

[session]
rounds_per_pass = 5
output_dir = "/tmp/forge-out"
`
	path := writeTemp(t, toml)
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Models.Writer != "claude-opus-4-6" {
		t.Errorf("got %s", cfg.Models.Writer)
	}
	if cfg.Session.RoundsPerPass != 5 {
		t.Errorf("got %d", cfg.Session.RoundsPerPass)
	}
}

func TestEnvOverridesKeys(t *testing.T) {
	setTempHome(t)
	t.Setenv("ANTHROPIC_API_KEY", "env-key")
	path := writeTemp(t, "[keys]\nanthropic = \"file-key\"")
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.AnthropicKey() != "env-key" {
		t.Errorf("expected env-key, got %s", cfg.AnthropicKey())
	}
}

func TestProviderKeysTrimWhitespace(t *testing.T) {
	setTempHome(t)
	cfg := &config.Config{}
	cfg.Keys.OpenAI = "  sk-test  \n"
	if got := cfg.OpenAIKey(); got != "sk-test" {
		t.Fatalf("OpenAIKey() = %q, want %q", got, "sk-test")
	}

	t.Setenv("OPENAI_API_KEY", "  env-openai  \n")
	if got := cfg.OpenAIKey(); got != "env-openai" {
		t.Fatalf("OpenAIKey() env = %q, want %q", got, "env-openai")
	}
}

func TestCopilotClientIDUsesBundledDefault(t *testing.T) {
	path := writeTemp(t, "")
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.CopilotClientID() != "Ov23liEz8seIOGdwNY9R" {
		t.Fatalf("unexpected bundled client id: %q", cfg.CopilotClientID())
	}
}

func TestCopilotClientIDPrefersConfigAndEnv(t *testing.T) {
	path := writeTemp(t, "[copilot]\nclient_id = \"from-config\"")
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.CopilotClientID() != "from-config" {
		t.Fatalf("expected config client id, got %q", cfg.CopilotClientID())
	}

	t.Setenv("FORGE_COPILOT_CLIENT_ID", "from-env")
	if cfg.CopilotClientID() != "from-env" {
		t.Fatalf("expected env client id, got %q", cfg.CopilotClientID())
	}
}

func TestTildeExpansion(t *testing.T) {
	home, _ := os.UserHomeDir()
	toml := "[session]\noutput_dir = \"~/forge-out\""
	path := writeTemp(t, toml)
	cfg, _ := config.Load(path)
	expected := filepath.Join(home, "forge-out")
	if cfg.Session.OutputDir != expected {
		t.Errorf("expected %s, got %s", expected, cfg.Session.OutputDir)
	}
}

func TestChatConfigDefaults(t *testing.T) {
	cfg, err := config.Load("/nonexistent/path.toml")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Chat.MaxTurns != 50 {
		t.Errorf("MaxTurns = %d, want 50", cfg.Chat.MaxTurns)
	}
	if cfg.Chat.CommandTimeout != 60 {
		t.Errorf("CommandTimeout = %d, want 60", cfg.Chat.CommandTimeout)
	}
	if cfg.Chat.Yolo {
		t.Error("Yolo should default to false")
	}
	if len(cfg.Chat.IgnoreDirs) == 0 {
		t.Error("IgnoreDirs should have defaults")
	}
	if cfg.Chat.AutoSkills != "suggest" {
		t.Errorf("AutoSkills = %q, want %q", cfg.Chat.AutoSkills, "suggest")
	}
}

func TestChatModel(t *testing.T) {
	cfg, _ := config.Load("/nonexistent/path.toml")
	if got := cfg.ChatModel(); got != cfg.Models.Writer {
		t.Errorf("ChatModel() = %q, want %q (writer default)", got, cfg.Models.Writer)
	}
	cfg.Chat.Model = "gpt-4o"
	if got := cfg.ChatModel(); got != "gpt-4o" {
		t.Errorf("ChatModel() = %q, want gpt-4o", got)
	}
}

func TestBraveKeyEnvOverride(t *testing.T) {
	setTempHome(t)
	cfg := &config.Config{}
	cfg.Keys.Brave = "from-config"
	t.Setenv("BRAVE_API_KEY", "from-env")
	if got := cfg.BraveKey(); got != "from-env" {
		t.Errorf("expected env override, got %q", got)
	}
}

func TestBraveKeyFromConfig(t *testing.T) {
	setTempHome(t)
	cfg := &config.Config{}
	cfg.Keys.Brave = "from-config"
	if got := cfg.BraveKey(); got != "from-config" {
		t.Errorf("expected config value, got %q", got)
	}
}

func TestRoundsValidation(t *testing.T) {
	for _, n := range []int{0, -1, 11} {
		if config.ValidRounds(n) {
			t.Errorf("expected %d to be invalid", n)
		}
	}
	for _, n := range []int{1, 3, 10} {
		if !config.ValidRounds(n) {
			t.Errorf("expected %d to be valid", n)
		}
	}
}

func TestSaveAgentModelsPreservesExistingConfig(t *testing.T) {
	path := writeTemp(t, "[models]\nwriter = \"openai/gpt-5\"\n\n[chat]\nmodel = \"openai/gpt-5\"\n")

	err := config.SaveAgentModels(path, config.AgentModels{
		Scout:   "anthropic/claude-sonnet-4-6",
		Builder: "groq/llama",
	})
	if err != nil {
		t.Fatalf("SaveAgentModels: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "[models]") || !strings.Contains(content, "writer = \"openai/gpt-5\"") {
		t.Fatalf("expected existing config to be preserved, got:\n%s", content)
	}
	if !strings.Contains(content, "[chat.agents.models]") {
		t.Fatalf("expected agent models section, got:\n%s", content)
	}
	if !strings.Contains(content, "scout = \"anthropic/claude-sonnet-4-6\"") || !strings.Contains(content, "builder = \"groq/llama\"") {
		t.Fatalf("expected saved agent models, got:\n%s", content)
	}
}

func TestSaveAgentModelsReplacesExistingSection(t *testing.T) {
	path := writeTemp(t, "[chat.agents.models]\nscout = \"old\"\ndoctor = \"stale\"\n\n[session]\nrounds_per_pass = 3\n")

	err := config.SaveAgentModels(path, config.AgentModels{
		Architect: "new-architect",
	})
	if err != nil {
		t.Fatalf("SaveAgentModels: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	content := string(data)
	if strings.Contains(content, "scout = \"old\"") || strings.Contains(content, "doctor = \"stale\"") {
		t.Fatalf("expected previous agent models to be replaced, got:\n%s", content)
	}
	if !strings.Contains(content, "architect = \"new-architect\"") {
		t.Fatalf("expected new architect model, got:\n%s", content)
	}
	if !strings.Contains(content, "[session]") || !strings.Contains(content, "rounds_per_pass = 3") {
		t.Fatalf("expected unrelated config to remain, got:\n%s", content)
	}
}
