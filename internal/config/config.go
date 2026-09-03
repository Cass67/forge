package config

import (
	"bytes"
	"crypto/sha256"
	"fmt"
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

// Scratch is the throwaway-file area used by the GUI's "New Scratch" action:
// quick paste/search buffers that are not part of any repository. Files live
// outside the workspace by default; point Dir somewhere else when you want
// them on disk where you can find them.
type Scratch struct {
	Dir string `toml:"dir"`
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
	// ShowReasoning displays the model's thinking as it works. Reasoning is
	// captured either way; this only controls whether it is shown.
	ShowReasoning *bool `toml:"show_reasoning"`
	// ReasoningEffort pins the provider reasoning-effort level. Empty means
	// use the lowest level the model advertises; "none" sends none at all.
	ReasoningEffort string `toml:"reasoning_effort"`
}

// ReasoningVisible reports whether thinking should be displayed, defaulting to
// on: it is captured regardless, and hiding it by default is what made a
// working turn look like nothing but tool cards.
func (c ChatConfig) ReasoningVisible() bool {
	return c.ShowReasoning == nil || *c.ShowReasoning
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
	// Settings holds plugin-specific options (e.g. [plugins.settings] default_on = true).
	// Interpreted by the plugin itself via plugin.Configurable.
	Settings map[string]any `toml:"settings,omitempty"`
}

type LSPServerConfig struct {
	// Command is the server binary followed by its args, as in mcp_servers.
	Command    []string `toml:"command"`
	LanguageID string   `toml:"language_id"`
	Extensions []string `toml:"extensions"`
	Enabled    *bool    `toml:"enabled"`
}

type LSPConfig struct {
	// Servers is keyed by language name. Entries merge over the built-in
	// table: a known key overrides only the fields it sets, a new key adds a
	// language, and enabled = false drops one.
	Servers map[string]LSPServerConfig `toml:"servers"`
}

type AgentOverride struct {
	Model     string   `toml:"model"`
	Fallbacks []string `toml:"fallbacks,omitempty"`
}

type Config struct {
	Session     Session                    `toml:"session"`
	Scratch     Scratch                    `toml:"scratch"`
	Keys        Keys                       `toml:"keys"`
	Copilot     Copilot                    `toml:"copilot"`
	Log         Log                        `toml:"log"`
	Retry       Retry                      `toml:"retry"`
	Resilience  Resilience                 `toml:"resilience"`
	Security    SecurityConfig             `toml:"security"`
	Chat        ChatConfig                 `toml:"chat"`
	Approval    ApprovalConfig             `toml:"approval"`
	Permissions PermissionsConfig          `toml:"permissions"`
	LSP         LSPConfig                  `toml:"lsp"`
	MCPServers  map[string]MCPServerConfig `toml:"mcp_servers"`
	Plugins     []PluginConfig             `toml:"plugins"`
	// Models routes work to a model by role, so cheap models can take the
	// bulk jobs while the expensive one stays on the main turn.
	Models           map[string]string `toml:"models"`
	LiveCompatModels bool              `toml:"live_compat_models"`
}

// defaultCopilotClientID is the bundled GitHub OAuth App client ID used for
// Copilot device-flow auth when the user does not provide an override.
const defaultCopilotClientID = "Ov23liEz8seIOGdwNY9R"

const (
	defaultStreamIdleTimeoutMS       = 120000
	legacyDefaultStreamIdleTimeoutMS = 30000
	legacyDefaultCommandTimeout      = 60
)

func Load(path string) (*Config, error) {
	cfg := &Config{}
	setDefaults(cfg)

	if _, err := toml.DecodeFile(path, cfg); err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	normalizeLegacyDefaults(cfg)

	expandTilde(&cfg.Session.OutputDir)
	expandTilde(&cfg.Scratch.Dir)
	return cfg, nil
}

func normalizeLegacyDefaults(c *Config) {
	if c.Resilience.StreamIdleTimeoutMS == legacyDefaultStreamIdleTimeoutMS {
		c.Resilience.StreamIdleTimeoutMS = defaultStreamIdleTimeoutMS
	}
	if c.Chat.CommandTimeout == legacyDefaultCommandTimeout {
		c.Chat.CommandTimeout = 0
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

// SaveScratchDir updates only dir in [scratch], preserving comments and all
// unrelated config, including credential sections.
func SaveScratchDir(path, dir string) error {
	var encoded bytes.Buffer
	if err := toml.NewEncoder(&encoded).Encode(struct {
		Dir string `toml:"dir"`
	}{Dir: dir}); err != nil {
		return err
	}
	line := strings.TrimSpace(encoded.String())

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
	inScratch := false
	out := make([]string, 0, len(lines)+2)
	for _, existing := range lines {
		trimmed := strings.TrimSpace(existing)
		if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
			if inScratch && !inserted {
				out = append(out, line)
				inserted = true
			}
			inScratch = trimmed == "[scratch]"
			sectionFound = sectionFound || inScratch
			out = append(out, existing)
			continue
		}
		if inScratch {
			key, _, found := strings.Cut(trimmed, "=")
			if found && strings.TrimSpace(key) == "dir" {
				if !inserted {
					out = append(out, line)
					inserted = true
				}
				continue
			}
		}
		out = append(out, existing)
	}
	if sectionFound && !inserted {
		out = append(out, line)
	}
	if !sectionFound {
		for len(out) > 0 && strings.TrimSpace(out[len(out)-1]) == "" {
			out = out[:len(out)-1]
		}
		if len(out) > 0 {
			out = append(out, "")
		}
		out = append(out, "[scratch]", line)
	}
	content := strings.Join(out, "\n")
	if content != "" && !strings.HasSuffix(content, "\n") {
		content += "\n"
	}
	return atomicWrite(path, []byte(content))
}

func atomicWrite(path string, data []byte) error {
	mode := os.FileMode(0o600)
	if info, err := os.Stat(path); err == nil {
		mode = info.Mode().Perm()
	} else if !os.IsNotExist(err) {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".config-*.tmp")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer func() { _ = os.Remove(tmp) }()
	if err := f.Chmod(mode); err != nil {
		_ = f.Close()
		return err
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func DefaultPath() string {
	return fsutil.ForgeConfigPath("config.toml")
}

// ScratchDir resolves the throwaway-file directory. Empty (unset) means the
// operating system's temp dir, since scratch files are junk by definition.
func (c *Config) ScratchDir() string {
	dir := strings.TrimSpace(c.Scratch.Dir)
	if dir == "" {
		return os.TempDir()
	}
	return dir
}

func setDefaults(c *Config) {
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
	c.Chat.CommandTimeout = 0
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

// ModelRoles are the routing intents RoleModel understands. Anything else in
// [models] is a typo, and Validate says so.
var ModelRoles = []string{"default", "smol", "slow", "commit"}

// RoleModel returns the model configured for a routing role. The "default"
// role falls back to the chat model; every other role falls back to empty so
// the caller can keep using whatever it would have used anyway.
func (c *Config) RoleModel(role string) string {
	role = strings.ToLower(strings.TrimSpace(role))
	if role == "" {
		role = "default"
	}
	if m := strings.TrimSpace(c.Models[role]); m != "" {
		return m
	}
	if role == "default" {
		return c.ChatModel()
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

// ResolvedOutputDir is where session threads and tool output blobs live.
//
// An unset value — or the historical "./output" default, which littered a
// directory into every repo forge was launched from — resolves to a
// per-workspace directory under the user's state dir. An existing ./output in
// the workspace still wins so older sessions stay readable.
func (c *Config) ResolvedOutputDir() string {
	cwd, err := os.Getwd()
	if err != nil {
		cwd = "."
	}
	return c.ResolvedOutputDirFor(cwd)
}

// ResolvedOutputDirFor resolves session storage for workspaceDir without
// depending on the process-wide current directory. GUI runtimes for different
// workspaces share one process, so using os.Getwd there can attach a thread to
// whichever workspace was opened most recently.
func (c *Config) ResolvedOutputDirFor(workspaceDir string) string {
	dir := strings.TrimSpace(c.Session.OutputDir)
	if dir != "" && !isLegacyOutputDir(dir) {
		return dir
	}
	if strings.TrimSpace(workspaceDir) == "" {
		workspaceDir = "."
	}
	if legacy := filepath.Join(workspaceDir, "output"); dirExists(legacy) {
		return legacy
	}
	return filepath.Join(fsutil.ForgeStateDir(), "projects", workspaceSlug(workspaceDir))
}

func isLegacyOutputDir(dir string) bool {
	switch filepath.ToSlash(strings.TrimSpace(dir)) {
	case "output", "./output":
		return true
	}
	return false
}

func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// workspaceSlug names a state directory after a workspace path: readable base
// name plus a hash so two projects with the same name stay separate.
func workspaceSlug(dir string) string {
	abs, err := filepath.Abs(dir)
	if err != nil {
		abs = dir
	}
	sum := sha256.Sum256([]byte(abs))
	base := filepath.Base(abs)
	if base == "." || base == string(filepath.Separator) {
		base = "workspace"
	}
	base = strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			return r
		default:
			return '-'
		}
	}, base)
	return fmt.Sprintf("%s-%x", base, sum[:4])
}
