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
	if cfg.Models.Writer != "claude-sonnet-4-6" {
		t.Fatalf("default writer = %q, want %q", cfg.Models.Writer, "claude-sonnet-4-6")
	}
	if cfg.Models.Summarizer != "claude-haiku-4-5" {
		t.Fatalf("default summarizer = %q, want %q", cfg.Models.Summarizer, "claude-haiku-4-5")
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
	if cfg.Approval.DefaultPolicy != "on_request" {
		t.Errorf("Approval.DefaultPolicy = %q, want %q", cfg.Approval.DefaultPolicy, "on_request")
	}
	if cfg.Approval.SandboxPolicy != "workspace_write" {
		t.Errorf("Approval.SandboxPolicy = %q, want %q", cfg.Approval.SandboxPolicy, "workspace_write")
	}
	if len(cfg.Approval.KnownSafePrefixes) == 0 {
		t.Error("Approval.KnownSafePrefixes should have defaults")
	}
}

func TestLoadApprovalConfigSection(t *testing.T) {
	toml := `
[approval]
default_policy = "unless_trusted"
sandbox_policy = "read_only"
known_safe_prefixes = ["git status", "go test"]

[[approval.rules]]
tool = "run_command"
command_prefix = ["git", "push"]
decision = "forbidden"
`
	path := writeTemp(t, toml)
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Approval.DefaultPolicy != "unless_trusted" {
		t.Fatalf("DefaultPolicy = %q", cfg.Approval.DefaultPolicy)
	}
	if cfg.Approval.SandboxPolicy != "read_only" {
		t.Fatalf("SandboxPolicy = %q", cfg.Approval.SandboxPolicy)
	}
	if len(cfg.Approval.Rules) != 1 {
		t.Fatalf("rules = %d, want 1", len(cfg.Approval.Rules))
	}
	rule := cfg.Approval.Rules[0]
	if rule.Tool != "run_command" {
		t.Fatalf("rule.Tool = %q", rule.Tool)
	}
	if got := strings.Join(rule.CommandPrefix, " "); got != "git push" {
		t.Fatalf("rule.CommandPrefix = %q", got)
	}
	if rule.Decision != "forbidden" {
		t.Fatalf("rule.Decision = %q", rule.Decision)
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
	cfg.Chat.LastModel = "openai/gpt-5"
	if got := cfg.ChatModel(); got != "openai/gpt-5" {
		t.Errorf("ChatModel() with last_model = %q, want openai/gpt-5", got)
	}
}

func TestChatModelEnvOverrideBeatsStoredLastModel(t *testing.T) {
	cfg, _ := config.Load("/nonexistent/path.toml")
	cfg.Chat.LastModel = "claude/claude-sonnet-4-6"
	t.Setenv("FORGE_CHAT_MODEL", "openai/gpt-5.4")
	if got := cfg.ChatModel(); got != "openai/gpt-5.4" {
		t.Fatalf("ChatModel() = %q, want openai/gpt-5.4", got)
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

func TestZAIKey(t *testing.T) {
	setTempHome(t)
	t.Setenv("ZAI_API_KEY", "test-zai-key")
	cfg := &config.Config{}
	got := cfg.ZAIKey()
	if got != "test-zai-key" {
		t.Fatalf("ZAIKey() = %q, want %q", got, "test-zai-key")
	}
}

func TestZAIKeyFromConfig(t *testing.T) {
	setTempHome(t)
	cfg := &config.Config{}
	cfg.Keys.ZAI = "from-config"
	if got := cfg.ZAIKey(); got != "from-config" {
		t.Fatalf("ZAIKey() = %q, want %q", got, "from-config")
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

func TestSaveChatLastModelPreservesExistingChatSection(t *testing.T) {
	path := writeTemp(t, "[models]\nwriter = \"claude-sonnet-4-6\"\n\n[chat]\nmodel = \"chatgpt/gpt-5.4\"\nauto_skills = \"auto\"\n\n[chat.agents]\nenabled = true\n")

	if err := config.SaveChatLastModel(path, "claude/claude-opus-4-6"); err != nil {
		t.Fatalf("SaveChatLastModel: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "[chat]") || !strings.Contains(content, "model = \"chatgpt/gpt-5.4\"") || !strings.Contains(content, "auto_skills = \"auto\"") {
		t.Fatalf("expected existing chat config to be preserved, got:\n%s", content)
	}
	if !strings.Contains(content, "last_model = \"claude/claude-opus-4-6\"") {
		t.Fatalf("expected last_model to be saved, got:\n%s", content)
	}
	if !strings.Contains(content, "[chat.agents]") || !strings.Contains(content, "enabled = true") {
		t.Fatalf("expected nested chat sections to remain, got:\n%s", content)
	}
}

func TestSaveChatLastModelAddsChatSectionWhenMissing(t *testing.T) {
	path := writeTemp(t, "[models]\nwriter = \"claude-sonnet-4-6\"\n")

	if err := config.SaveChatLastModel(path, "openai/gpt-5.4"); err != nil {
		t.Fatalf("SaveChatLastModel: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "[chat]") || !strings.Contains(content, "last_model = \"openai/gpt-5.4\"") {
		t.Fatalf("expected chat section with last_model, got:\n%s", content)
	}
}
