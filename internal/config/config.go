package config

import (
	"os"
	"path/filepath"
	"strings"

	"forge/internal/auth"
	"forge/internal/fsutil"

	"github.com/BurntSushi/toml"
)

type ModelParams struct {
	MaxTokens   int     `toml:"max_tokens"`  // 0 = use provider default
	Temperature float64 `toml:"temperature"` // -1 = use provider default
}

type Session struct {
	OutputDir string `toml:"output_dir"`
}

type Keys struct {
	Anthropic  string `toml:"anthropic"`
	OpenAI     string `toml:"openai"`
	Groq       string `toml:"groq"`
	Mistral    string `toml:"mistral"`
	XAI        string `toml:"xai"`
	ZAI        string `toml:"zai"`
	NVIDIA     string `toml:"nvidia"`
	OpenRouter string `toml:"openrouter"`
	Together   string `toml:"together"`
	Perplexity string `toml:"perplexity"`
	DeepInfra  string `toml:"deepinfra"`
	Cerebras   string `toml:"cerebras"`
	OpenCode   string `toml:"opencode"`
	Brave      string `toml:"brave"`
}

type Copilot struct {
	ClientID string `toml:"client_id"`
}

type Log struct {
	Level string `toml:"level"` // "debug", "info", "warn", "error"; default "info"
	File  string `toml:"file"`  // path; empty means session dir / "session.log"
}

type Retry struct {
	MaxAttempts int `toml:"max_attempts"`
	InitialWait int `toml:"initial_wait_ms"`
	MaxWait     int `toml:"max_wait_ms"`
	Timeout     int `toml:"timeout_seconds"`
}

type Resilience struct {
	CompactionMaxFailures     int `toml:"compaction_max_failures"`
	TokenDiminishingThreshold int `toml:"token_diminishing_threshold"`
	TokenDiminishingChecks    int `toml:"token_diminishing_checks"`
	ToolThrashCircuitBreaker  int `toml:"tool_thrash_circuit_breaker"`
	StreamIdleTimeoutMS       int `toml:"stream_idle_timeout_ms"`
}

type SecretSecurityConfig struct {
	Read           string `toml:"read"`
	Write          string `toml:"write"`
	CommandOutput  string `toml:"command_output"`
	ApprovalDetail string `toml:"approval_detail"`
}

type SecurityConfig struct {
	Secrets SecretSecurityConfig `toml:"secrets"`
}

type ChatConfig struct {
	Model          string   `toml:"model"`
	LastModel      string   `toml:"last_model"`
	CommandTimeout int      `toml:"command_timeout"`
	Yolo           bool     `toml:"yolo"`
	IgnoreDirs     []string `toml:"ignore_dirs"`
	// ToolProfile: "lean" exposes a reduced tool-schema set (rest callable
	// via tool_help), "full" always sends every schema, "" auto-detects
	// (lean for local/self-hosted providers).
	ToolProfile string `toml:"tool_profile"`
}

type ApprovalRuleConfig struct {
	Tool          string   `toml:"tool"`
	CommandPrefix []string `toml:"command_prefix"`
	Command       string   `toml:"command"`
	Decision      string   `toml:"decision"`
}

type ApprovalConfig struct {
	DefaultPolicy     string               `toml:"default_policy"`
	SandboxPolicy     string               `toml:"sandbox_policy"`
	KnownSafePrefixes []string             `toml:"known_safe_prefixes"`
	Rules             []ApprovalRuleConfig `toml:"rules"`
}

type PermissionRuleConfig struct {
	Behavior string `toml:"behavior"`
	Tool     string `toml:"tool"`
	Pattern  string `toml:"pattern"`
}

type PermissionScopeConfig struct {
	Rules []PermissionRuleConfig `toml:"rules"`
}

type PermissionsConfig struct {
	Managed PermissionScopeConfig `toml:"managed"`
	User    PermissionScopeConfig `toml:"user"`
	Project PermissionScopeConfig `toml:"project"`
	Local   PermissionScopeConfig `toml:"local"`
	Session PermissionScopeConfig `toml:"session"`
	CLI     PermissionScopeConfig `toml:"cli"`
	Auto    PermissionAutoConfig  `toml:"auto"`
}

type PermissionAutoConfig struct {
	Enabled               bool   `toml:"enabled"`
	Posture               string `toml:"posture"`
	Model                 string `toml:"model"`
	MaxConsecutiveDenials int    `toml:"max_consecutive_denials"`
	MaxTotalDenials       int    `toml:"max_total_denials"`
	FailureBehavior       string `toml:"failure_behavior"`
	TimeoutMS             int    `toml:"timeout_ms"`
}

type MCPServerConfig struct {
	Type      string            `toml:"type"`
	Command   []string          `toml:"command"`
	Env       map[string]string `toml:"env"`
	URL       string            `toml:"url"`
	Headers   map[string]string `toml:"headers"`
	Enabled   *bool             `toml:"enabled"`
	TimeoutMS int               `toml:"timeout_ms"`
}

type PluginConfig struct {
	ID               string                   `toml:"id"`
	Kind             string                   `toml:"kind"`
	Source           string                   `toml:"source"`
	Enabled          *bool                    `toml:"enabled"`
	Command          []string                 `toml:"command"`
	Env              map[string]string        `toml:"env"`
	InheritEnv       []string                 `toml:"inherit_env"`
	AutoApproveTools []string                 `toml:"auto_approve_tools"`
	AgentOverrides   map[string]AgentOverride `toml:"agent_overrides,omitempty"`
	StartupTimeoutMS int                      `toml:"startup_timeout_ms"`
	RequestTimeoutMS int                      `toml:"request_timeout_ms"`
}

type AgentOverride struct {
	Model     string   `toml:"model"`
	Fallbacks []string `toml:"fallbacks,omitempty"`
}

type Config struct {
	Session          Session                    `toml:"session"`
	Keys             Keys                       `toml:"keys"`
	Copilot          Copilot                    `toml:"copilot"`
	Log              Log                        `toml:"log"`
	Retry            Retry                      `toml:"retry"`
	Resilience       Resilience                 `toml:"resilience"`
	Security         SecurityConfig             `toml:"security"`
	Chat             ChatConfig                 `toml:"chat"`
	Approval         ApprovalConfig             `toml:"approval"`
	Permissions      PermissionsConfig          `toml:"permissions"`
	MCPServers       map[string]MCPServerConfig `toml:"mcp_servers"`
	Plugins          []PluginConfig             `toml:"plugins"`
	LiveCompatModels bool                       `toml:"live_compat_models"`
}

// defaultCopilotClientID is the bundled GitHub OAuth App client ID used for
// Copilot device-flow auth when the user does not provide an override.
const defaultCopilotClientID = "Ov23liEz8seIOGdwNY9R"

const (
	defaultStreamIdleTimeoutMS       = 120000
	legacyDefaultStreamIdleTimeoutMS = 30000
)

func Load(path string) (*Config, error) {
	cfg := &Config{}
	setDefaults(cfg)

	if _, err := toml.DecodeFile(path, cfg); err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	normalizeLegacyDefaults(cfg)

	expandTilde(&cfg.Session.OutputDir)
	return cfg, nil
}

func normalizeLegacyDefaults(c *Config) {
	if c.Resilience.StreamIdleTimeoutMS == legacyDefaultStreamIdleTimeoutMS {
		c.Resilience.StreamIdleTimeoutMS = defaultStreamIdleTimeoutMS
	}
}

func Save(path string, cfg *Config) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	if err := toml.NewEncoder(f).Encode(cfg); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

// SaveChatLastModel updates only the [chat] last_model key in the config file.
func SaveChatLastModel(path, model string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}

	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}

	lines := []string{}
	if len(data) > 0 {
		lines = strings.Split(string(data), "\n")
	}

	sectionFound := false
	inserted := false
	filtered := make([]string, 0, len(lines)+2)
	inChatSection := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
			if inChatSection && !inserted {
				filtered = append(filtered, `last_model = "`+model+`"`)
				inserted = true
			}
			inChatSection = trimmed == "[chat]"
			if inChatSection {
				sectionFound = true
			}
			filtered = append(filtered, line)
			continue
		}
		if inChatSection && strings.HasPrefix(trimmed, "last_model") {
			if !inserted {
				filtered = append(filtered, `last_model = "`+model+`"`)
				inserted = true
			}
			continue
		}
		filtered = append(filtered, line)
	}

	if sectionFound && !inserted {
		filtered = append(filtered, `last_model = "`+model+`"`)
	}
	if !sectionFound {
		for len(filtered) > 0 && strings.TrimSpace(filtered[len(filtered)-1]) == "" {
			filtered = filtered[:len(filtered)-1]
		}
		if len(filtered) > 0 {
			filtered = append(filtered, "")
		}
		filtered = append(filtered, "[chat]", `last_model = "`+model+`"`)
	}

	content := strings.Join(filtered, "\n")
	if content != "" && !strings.HasSuffix(content, "\n") {
		content += "\n"
	}
	return os.WriteFile(path, []byte(content), 0o600)
}

func DefaultPath() string {
	return fsutil.ForgeConfigPath("config.toml")
}

func setDefaults(c *Config) {
	c.Session.OutputDir = "./output"
	c.Log.Level = "info"
	c.Retry.MaxAttempts = 3
	c.Retry.InitialWait = 1000
	c.Retry.MaxWait = 30000
	c.Retry.Timeout = 600
	c.Resilience.CompactionMaxFailures = 3
	c.Resilience.TokenDiminishingThreshold = 500
	c.Resilience.TokenDiminishingChecks = 2
	c.Resilience.ToolThrashCircuitBreaker = 8
	c.Resilience.StreamIdleTimeoutMS = defaultStreamIdleTimeoutMS
	c.Security.Secrets.Read = "redact"
	c.Security.Secrets.Write = "block"
	c.Security.Secrets.CommandOutput = "redact"
	c.Security.Secrets.ApprovalDetail = "redact"
	c.Permissions.Auto.Posture = "balanced"
	c.Permissions.Auto.MaxConsecutiveDenials = 3
	c.Permissions.Auto.MaxTotalDenials = 20
	c.Permissions.Auto.FailureBehavior = "ask"
	c.Permissions.Auto.TimeoutMS = 5000
	c.Chat.CommandTimeout = 60
	c.Chat.IgnoreDirs = []string{".git", "node_modules", "__pycache__", ".venv", "vendor"}
	c.LiveCompatModels = true
	c.Approval.DefaultPolicy = "on_request"
	c.Approval.SandboxPolicy = "workspace_write"
	c.Approval.KnownSafePrefixes = []string{
		"git status",
		"git diff",
		"git log",
		"ls",
		"pwd",
		"cat",
		"head",
		"tail",
		"sed -n",
		"rg",
		"go test",
		"go build",
		"npm test",
		"npm run lint",
	}
}

func (c MCPServerConfig) IsEnabled() bool {
	if c.Enabled == nil {
		return true
	}
	return *c.Enabled
}

func (c PluginConfig) IsEnabled() bool {
	if c.Enabled == nil {
		return true
	}
	return *c.Enabled
}

func (c *Config) AnthropicKey() string {
	if v := strings.TrimSpace(os.Getenv("ANTHROPIC_API_KEY")); v != "" {
		return v
	}
	if v := strings.TrimSpace(c.Keys.Anthropic); v != "" {
		return v
	}
	tokens, _ := auth.Load()
	return strings.TrimSpace(tokens.AnthropicAPIKey)
}

func (c *Config) OpenAIKey() string {
	if v := strings.TrimSpace(os.Getenv("OPENAI_API_KEY")); v != "" {
		return v
	}
	if v := strings.TrimSpace(c.Keys.OpenAI); v != "" {
		return v
	}
	tokens, _ := auth.Load()
	return strings.TrimSpace(tokens.OpenAIAPIKey)
}

func (c *Config) GroqKey() string {
	if v := strings.TrimSpace(os.Getenv("GROQ_API_KEY")); v != "" {
		return v
	}
	if v := strings.TrimSpace(c.Keys.Groq); v != "" {
		return v
	}
	tokens, _ := auth.Load()
	return strings.TrimSpace(tokens.GroqAPIKey)
}

func (c *Config) MistralKey() string {
	if v := strings.TrimSpace(os.Getenv("MISTRAL_API_KEY")); v != "" {
		return v
	}
	if v := strings.TrimSpace(c.Keys.Mistral); v != "" {
		return v
	}
	tokens, _ := auth.Load()
	return strings.TrimSpace(tokens.MistralAPIKey)
}

func (c *Config) XAIKey() string {
	if v := strings.TrimSpace(os.Getenv("XAI_API_KEY")); v != "" {
		return v
	}
	if v := strings.TrimSpace(c.Keys.XAI); v != "" {
		return v
	}
	tokens, _ := auth.Load()
	return strings.TrimSpace(tokens.XAIAPIKey)
}

func (c *Config) ZAIKey() string {
	if v := strings.TrimSpace(os.Getenv("ZAI_API_KEY")); v != "" {
		return v
	}
	if v := strings.TrimSpace(os.Getenv("ZHIPU_API_KEY")); v != "" {
		return v
	}
	if v := strings.TrimSpace(c.Keys.ZAI); v != "" {
		return v
	}
	tokens, _ := auth.Load()
	return strings.TrimSpace(tokens.ZAIAPIKey)
}

func (c *Config) NVIDIAKey() string {
	if v := strings.TrimSpace(os.Getenv("NVIDIA_API_KEY")); v != "" {
		return v
	}
	if v := strings.TrimSpace(c.Keys.NVIDIA); v != "" {
		return v
	}
	tokens, _ := auth.Load()
	return strings.TrimSpace(tokens.NVIDIAAPIKey)
}

func (c *Config) OpenRouterKey() string {
	if v := strings.TrimSpace(os.Getenv("OPENROUTER_API_KEY")); v != "" {
		return v
	}
	if v := strings.TrimSpace(c.Keys.OpenRouter); v != "" {
		return v
	}
	tokens, _ := auth.Load()
	return strings.TrimSpace(tokens.OpenRouterAPIKey)
}

func (c *Config) TogetherKey() string {
	if v := strings.TrimSpace(os.Getenv("TOGETHER_AI_API_KEY")); v != "" {
		return v
	}
	if v := strings.TrimSpace(c.Keys.Together); v != "" {
		return v
	}
	tokens, _ := auth.Load()
	return strings.TrimSpace(tokens.TogetherAPIKey)
}

func (c *Config) PerplexityKey() string {
	if v := strings.TrimSpace(os.Getenv("PERPLEXITY_API_KEY")); v != "" {
		return v
	}
	if v := strings.TrimSpace(c.Keys.Perplexity); v != "" {
		return v
	}
	tokens, _ := auth.Load()
	return strings.TrimSpace(tokens.PerplexityAPIKey)
}

func (c *Config) DeepInfraKey() string {
	if v := strings.TrimSpace(os.Getenv("DEEPINFRA_API_KEY")); v != "" {
		return v
	}
	if v := strings.TrimSpace(c.Keys.DeepInfra); v != "" {
		return v
	}
	tokens, _ := auth.Load()
	return strings.TrimSpace(tokens.DeepInfraAPIKey)
}

func (c *Config) CerebrasKey() string {
	if v := strings.TrimSpace(os.Getenv("CEREBRAS_API_KEY")); v != "" {
		return v
	}
	if v := strings.TrimSpace(c.Keys.Cerebras); v != "" {
		return v
	}
	tokens, _ := auth.Load()
	return strings.TrimSpace(tokens.CerebrasAPIKey)
}

func (c *Config) OpenCodeKey() string {
	if v := strings.TrimSpace(os.Getenv("OPENCODE_API_KEY")); v != "" {
		return v
	}
	if v := strings.TrimSpace(c.Keys.OpenCode); v != "" {
		return v
	}
	tokens, _ := auth.Load()
	return strings.TrimSpace(tokens.OpenCodeAPIKey)
}

func (c *Config) BraveKey() string {
	if v := strings.TrimSpace(os.Getenv("BRAVE_API_KEY")); v != "" {
		return v
	}
	if v := strings.TrimSpace(c.Keys.Brave); v != "" {
		return v
	}
	tokens, _ := auth.Load()
	return strings.TrimSpace(tokens.BraveAPIKey)
}

// CopilotClientID returns the GitHub OAuth App client ID used for device flow
// authentication. FORGE_COPILOT_CLIENT_ID and [copilot] client_id override the
// bundled default.
func (c *Config) ChatModel() string {
	if v := os.Getenv("FORGE_CHAT_MODEL"); v != "" {
		return v
	}
	if c.Chat.LastModel != "" {
		return c.Chat.LastModel
	}
	if c.Chat.Model != "" {
		return c.Chat.Model
	}
	return ""
}

func (c *Config) CopilotClientID() string {
	if v := os.Getenv("FORGE_COPILOT_CLIENT_ID"); v != "" {
		return v
	}
	if c.Copilot.ClientID != "" {
		return c.Copilot.ClientID
	}
	return defaultCopilotClientID
}

func expandTilde(s *string) {
	if strings.HasPrefix(*s, "~/") {
		home, _ := os.UserHomeDir()
		*s = filepath.Join(home, (*s)[2:])
	}
}
