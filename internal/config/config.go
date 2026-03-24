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

type Models struct {
	Writer        string      `toml:"writer"`
	Auditor       string      `toml:"auditor"`
	Summarizer    string      `toml:"summarizer"`
	WriterParams  ModelParams `toml:"writer_params"`
	AuditorParams ModelParams `toml:"auditor_params"`
}

type Session struct {
	RoundsPerPass int    `toml:"rounds_per_pass"`
	OutputDir     string `toml:"output_dir"`
}

type Keys struct {
	Anthropic  string `toml:"anthropic"`
	OpenAI     string `toml:"openai"`
	Groq       string `toml:"groq"`
	Mistral    string `toml:"mistral"`
	XAI        string `toml:"xai"`
	NVIDIA     string `toml:"nvidia"`
	OpenRouter string `toml:"openrouter"`
	Together   string `toml:"together"`
	Perplexity string `toml:"perplexity"`
	DeepInfra  string `toml:"deepinfra"`
	Cerebras   string `toml:"cerebras"`
	Brave      string `toml:"brave"`
}

type Copilot struct {
	ClientID string `toml:"client_id"`
}

type Passes struct {
	Correctness string `toml:"correctness"`
	Refactor    string `toml:"refactor"`
	Security    string `toml:"security"`
	Prod        string `toml:"prod"`
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

type Git struct {
	Enabled    bool `toml:"enabled"`
	AutoCommit bool `toml:"auto_commit"`
}

type PipelinePass struct {
	Name          string `toml:"name"`
	WriterPrompt  string `toml:"writer_prompt"`  // path to prompt file; empty = use built-in
	AuditorPrompt string `toml:"auditor_prompt"` // path to prompt file; empty = use built-in
	Rounds        int    `toml:"rounds"`         // 0 = use default
}

type AgentModels struct {
	Dispatch  string `toml:"dispatch"`
	Scout     string `toml:"scout"`
	Builder   string `toml:"builder"`
	Doctor    string `toml:"doctor"`
	Architect string `toml:"architect"`
}

type AgentsConfig struct {
	Enabled bool        `toml:"enabled"`
	Models  AgentModels `toml:"models"`
}

type ChatConfig struct {
	Model          string       `toml:"model"`
	MaxTurns       int          `toml:"max_turns"`
	CommandTimeout int          `toml:"command_timeout"`
	Yolo           bool         `toml:"yolo"`
	IgnoreDirs     []string     `toml:"ignore_dirs"`
	AutoSkills     string       `toml:"auto_skills"`
	Agents         AgentsConfig `toml:"agents"`
}

type Config struct {
	Models   Models         `toml:"models"`
	Session  Session        `toml:"session"`
	Keys     Keys           `toml:"keys"`
	Passes   Passes         `toml:"passes"`
	Pipeline []PipelinePass `toml:"pipeline"`
	Copilot  Copilot        `toml:"copilot"`
	Log      Log            `toml:"log"`
	Retry    Retry          `toml:"retry"`
	Git      Git            `toml:"git"`
	Chat     ChatConfig     `toml:"chat"`
}

// defaultCopilotClientID is the bundled GitHub OAuth App client ID used for
// Copilot device-flow auth when the user does not provide an override.
const defaultCopilotClientID = "Ov23liEz8seIOGdwNY9R"

func Load(path string) (*Config, error) {
	cfg := &Config{}
	setDefaults(cfg)

	if _, err := toml.DecodeFile(path, cfg); err != nil && !os.IsNotExist(err) {
		return nil, err
	}

	expandTilde(&cfg.Session.OutputDir)
	return cfg, nil
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

// SaveAgentModels updates only the [chat.agents.models] section in the config file.
func SaveAgentModels(path string, models AgentModels) error {
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

	filtered := make([]string, 0, len(lines))
	inSection := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
			if trimmed == "[chat.agents.models]" {
				inSection = true
				continue
			}
			inSection = false
		}
		if inSection {
			continue
		}
		filtered = append(filtered, line)
	}

	for len(filtered) > 0 && strings.TrimSpace(filtered[len(filtered)-1]) == "" {
		filtered = filtered[:len(filtered)-1]
	}

	section := renderAgentModelsSection(models)
	if section != "" {
		if len(filtered) > 0 {
			filtered = append(filtered, "")
		}
		filtered = append(filtered, strings.Split(section, "\n")...)
	}

	content := strings.Join(filtered, "\n")
	if content != "" && !strings.HasSuffix(content, "\n") {
		content += "\n"
	}
	return os.WriteFile(path, []byte(content), 0o600)
}

func renderAgentModelsSection(models AgentModels) string {
	type pair struct {
		key string
		val string
	}
	pairs := []pair{
		{key: "dispatch", val: models.Dispatch},
		{key: "scout", val: models.Scout},
		{key: "builder", val: models.Builder},
		{key: "doctor", val: models.Doctor},
		{key: "architect", val: models.Architect},
	}
	lines := []string{"[chat.agents.models]"}
	for _, pair := range pairs {
		if strings.TrimSpace(pair.val) == "" {
			continue
		}
		lines = append(lines, pair.key+` = "`+pair.val+`"`)
	}
	if len(lines) == 1 {
		return ""
	}
	return strings.Join(lines, "\n")
}

func DefaultPath() string {
	return fsutil.ForgeConfigPath("config.toml")
}

func setDefaults(c *Config) {
	c.Models.Writer = "claude-sonnet-4-6"
	c.Models.Auditor = "gpt-4o"
	c.Models.Summarizer = "claude-haiku-4-5"
	c.Session.RoundsPerPass = 5
	c.Session.OutputDir = "./output"
	c.Log.Level = "info"
	c.Retry.MaxAttempts = 3
	c.Retry.InitialWait = 1000
	c.Retry.MaxWait = 30000
	c.Retry.Timeout = 300
	c.Git.AutoCommit = true
	c.Chat.MaxTurns = 50
	c.Chat.CommandTimeout = 60
	c.Chat.IgnoreDirs = []string{".git", "node_modules", "__pycache__", ".venv", "vendor"}
	c.Chat.AutoSkills = "suggest"
	c.Models.WriterParams.Temperature = -1
	c.Models.AuditorParams.Temperature = -1
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
	if c.Chat.Model != "" {
		return c.Chat.Model
	}
	return c.Models.Writer
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

// AgentRoleModels returns a map of role name to model name for multi-agent mode.
func (c *Config) AgentRoleModels() map[string]string {
	m := make(map[string]string)
	if v := c.Chat.Agents.Models.Dispatch; v != "" {
		m["dispatch"] = v
	}
	if v := c.Chat.Agents.Models.Scout; v != "" {
		m["scout"] = v
	}
	if v := c.Chat.Agents.Models.Builder; v != "" {
		m["builder"] = v
	}
	if v := c.Chat.Agents.Models.Doctor; v != "" {
		m["doctor"] = v
	}
	if v := c.Chat.Agents.Models.Architect; v != "" {
		m["architect"] = v
	}
	return m
}

func ValidRounds(n int) bool {
	return n >= 1 && n <= 10
}

func expandTilde(s *string) {
	if strings.HasPrefix(*s, "~/") {
		home, _ := os.UserHomeDir()
		*s = filepath.Join(home, (*s)[2:])
	}
}
