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

func TestZAIKeySupportsZhipuEnv(t *testing.T) {
	setTempHome(t)
	cfg := &config.Config{}
	t.Setenv("ZHIPU_API_KEY", "zhipu-env-key")
	if got := cfg.ZAIKey(); got != "zhipu-env-key" {
		t.Fatalf("ZAIKey() = %q, want %q", got, "zhipu-env-key")
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

func TestLoadApprovalConfigSectionApprovalRules(t *testing.T) {
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

func TestLoadPermissionsConfigSectionRules(t *testing.T) {
	toml := `
[[permissions.project.rules]]
behavior = "deny"
tool = "run_command"
pattern = "rm:*"

[[permissions.user.rules]]
behavior = "allow"
tool = "run_command"
pattern = "go test:*"
`
	path := writeTemp(t, toml)
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Permissions.Project.Rules) != 1 {
		t.Fatalf("project rules = %d, want 1", len(cfg.Permissions.Project.Rules))
	}
	if got := cfg.Permissions.Project.Rules[0]; got.Behavior != "deny" || got.Tool != "run_command" || got.Pattern != "rm:*" {
		t.Fatalf("project rule = %#v", got)
	}
	if len(cfg.Permissions.User.Rules) != 1 {
		t.Fatalf("user rules = %d, want 1", len(cfg.Permissions.User.Rules))
	}
	if got := cfg.Permissions.User.Rules[0]; got.Behavior != "allow" || got.Tool != "run_command" || got.Pattern != "go test:*" {
		t.Fatalf("user rule = %#v", got)
	}
}

func TestLoadApprovalConfigSectionApprovalSupportsCommandRules(t *testing.T) {
	toml := `
[approval]

[[approval.rules]]
tool = "run_command"
command = "git status --short"
decision = "allow"

[[approval.rules]]
tool = "run_command"
command = "git * status"
decision = "prompt"
`
	path := writeTemp(t, toml)
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Approval.Rules) != 2 {
		t.Fatalf("rules = %d, want 2", len(cfg.Approval.Rules))
	}
	if got := cfg.Approval.Rules[0].Command; got != "git status --short" {
		t.Fatalf("rules[0].Command = %q", got)
	}
	if got := cfg.Approval.Rules[1].Command; got != "git * status" {
		t.Fatalf("rules[1].Command = %q", got)
	}
}

func TestLoadPluginConfig(t *testing.T) {
	toml := `
[[plugins]]
id = "omo"
kind = "opencode"
source = "oh-my-openagent"
enabled = true
command = ["node", "/tmp/plugin.js"]
env = { OMO_MODE = "forge" }
inherit_env = ["OMO_API_KEY"]
auto_approve_tools = ["search_docs"]
startup_timeout_ms = 3000
request_timeout_ms = 10000
`
	path := writeTemp(t, toml)
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Plugins) != 1 {
		t.Fatalf("plugins = %d, want 1", len(cfg.Plugins))
	}
	plugin := cfg.Plugins[0]
	if plugin.ID != "omo" {
		t.Fatalf("plugin.ID = %q", plugin.ID)
	}
	if plugin.Kind != "opencode" {
		t.Fatalf("plugin.Kind = %q", plugin.Kind)
	}
	if plugin.Source != "oh-my-openagent" {
		t.Fatalf("plugin.Source = %q", plugin.Source)
	}
	if !plugin.IsEnabled() {
		t.Fatal("expected plugin to be enabled")
	}
	if got := strings.Join(plugin.Command, " "); got != "node /tmp/plugin.js" {
		t.Fatalf("plugin.Command = %q", got)
	}
	if plugin.Env["OMO_MODE"] != "forge" {
		t.Fatalf("plugin.Env[OMO_MODE] = %q", plugin.Env["OMO_MODE"])
	}
	if got := strings.Join(plugin.InheritEnv, ","); got != "OMO_API_KEY" {
		t.Fatalf("plugin.InheritEnv = %q", got)
	}
	if got := strings.Join(plugin.AutoApproveTools, ","); got != "search_docs" {
		t.Fatalf("plugin.AutoApproveTools = %q", got)
	}
	if plugin.StartupTimeoutMS != 3000 || plugin.RequestTimeoutMS != 10000 {
		t.Fatalf("timeouts = %d/%d", plugin.StartupTimeoutMS, plugin.RequestTimeoutMS)
	}
}

func TestPluginConfigEnabledDefaultsToTrue(t *testing.T) {
	plugin := config.PluginConfig{}
	if !plugin.IsEnabled() {
		t.Fatal("plugin without explicit enabled should default to true")
	}
}

func TestChatModel(t *testing.T) {
	cfg, _ := config.Load("/nonexistent/path.toml")
	if got := cfg.ChatModel(); got != "" {
		t.Errorf("ChatModel() = %q, want empty when chat config is unset", got)
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

func TestLoadMCPServers(t *testing.T) {
	toml := `
[mcp_servers.context7]
type = "remote"
url = "https://mcp.context7.com/mcp"
enabled = true
timeout_ms = 12000

[mcp_servers.context7.headers]
X-Test = "value"

[mcp_servers.files]
type = "stdio"
enabled = false
timeout_ms = 8000
command = ["node", "server.js", "--stdio"]

[mcp_servers.files.env]
NODE_ENV = "test"
`
	path := writeTemp(t, toml)
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.MCPServers) != 2 {
		t.Fatalf("MCPServers = %d, want 2", len(cfg.MCPServers))
	}
	context7, ok := cfg.MCPServers["context7"]
	if !ok {
		t.Fatal("missing context7 server")
	}
	if context7.Type != "remote" {
		t.Fatalf("context7.Type = %q", context7.Type)
	}
	if context7.URL != "https://mcp.context7.com/mcp" {
		t.Fatalf("context7.URL = %q", context7.URL)
	}
	if !context7.IsEnabled() {
		t.Fatal("context7.IsEnabled() = false, want true")
	}
	if context7.TimeoutMS != 12000 {
		t.Fatalf("context7.TimeoutMS = %d", context7.TimeoutMS)
	}
	if context7.Headers["X-Test"] != "value" {
		t.Fatalf("context7.Headers = %#v", context7.Headers)
	}
	files, ok := cfg.MCPServers["files"]
	if !ok {
		t.Fatal("missing files server")
	}
	if files.Type != "stdio" {
		t.Fatalf("files.Type = %q", files.Type)
	}
	if files.IsEnabled() {
		t.Fatal("files.IsEnabled() = true, want false")
	}
	if got := strings.Join(files.Command, " "); got != "node server.js --stdio" {
		t.Fatalf("files.Command = %q", got)
	}
	if files.Env["NODE_ENV"] != "test" {
		t.Fatalf("files.Env = %#v", files.Env)
	}
}

func TestSaveRoundTripsMCPServers(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	cfg := &config.Config{
		MCPServers: map[string]config.MCPServerConfig{
			"context7": {
				Type:      "remote",
				URL:       "https://mcp.context7.com/mcp",
				Enabled:   boolPtr(true),
				TimeoutMS: 15000,
			},
			"files": {
				Type:      "stdio",
				Command:   []string{"node", "server.js"},
				Env:       map[string]string{"NODE_ENV": "development"},
				Enabled:   boolPtr(true),
				TimeoutMS: 5000,
			},
		},
	}
	if err := config.Save(path, cfg); err != nil {
		t.Fatalf("Save: %v", err)
	}
	loaded, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(loaded.MCPServers) != 2 {
		t.Fatalf("loaded.MCPServers = %d, want 2", len(loaded.MCPServers))
	}
	if loaded.MCPServers["context7"].URL != "https://mcp.context7.com/mcp" {
		t.Fatalf("context7.URL = %q", loaded.MCPServers["context7"].URL)
	}
	if got := strings.Join(loaded.MCPServers["files"].Command, " "); got != "node server.js" {
		t.Fatalf("files.Command = %q", got)
	}
}

func boolPtr(v bool) *bool { return &v }
