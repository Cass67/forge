package config

import (
	"os"
	"path/filepath"
	"strings"

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

type ChatConfig struct {
	Model          string   `toml:"model"`
	MaxTurns       int      `toml:"max_turns"`
	CommandTimeout int      `toml:"command_timeout"`
	Yolo           bool     `toml:"yolo"`
	IgnoreDirs     []string `toml:"ignore_dirs"`
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

func setDefaults(c *Config) {
	c.Models.Writer = "claude-3-7-sonnet-latest"
	c.Models.Auditor = "gpt-4o"
	c.Models.Summarizer = "claude-3-5-haiku-latest"
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
	c.Models.WriterParams.Temperature = -1
	c.Models.AuditorParams.Temperature = -1
}

func (c *Config) AnthropicKey() string {
	if v := os.Getenv("ANTHROPIC_API_KEY"); v != "" {
		return v
	}
	return c.Keys.Anthropic
}

func (c *Config) OpenAIKey() string {
	if v := os.Getenv("OPENAI_API_KEY"); v != "" {
		return v
	}
	return c.Keys.OpenAI
}

func (c *Config) GroqKey() string {
	if v := os.Getenv("GROQ_API_KEY"); v != "" {
		return v
	}
	return c.Keys.Groq
}

func (c *Config) MistralKey() string {
	if v := os.Getenv("MISTRAL_API_KEY"); v != "" {
		return v
	}
	return c.Keys.Mistral
}

func (c *Config) XAIKey() string {
	if v := os.Getenv("XAI_API_KEY"); v != "" {
		return v
	}
	return c.Keys.XAI
}

func (c *Config) NVIDIAKey() string {
	if v := os.Getenv("NVIDIA_API_KEY"); v != "" {
		return v
	}
	return c.Keys.NVIDIA
}

func (c *Config) OpenRouterKey() string {
	if v := os.Getenv("OPENROUTER_API_KEY"); v != "" {
		return v
	}
	return c.Keys.OpenRouter
}

func (c *Config) TogetherKey() string {
	if v := os.Getenv("TOGETHER_AI_API_KEY"); v != "" {
		return v
	}
	return c.Keys.Together
}

func (c *Config) PerplexityKey() string {
	if v := os.Getenv("PERPLEXITY_API_KEY"); v != "" {
		return v
	}
	return c.Keys.Perplexity
}

func (c *Config) DeepInfraKey() string {
	if v := os.Getenv("DEEPINFRA_API_KEY"); v != "" {
		return v
	}
	return c.Keys.DeepInfra
}

func (c *Config) CerebrasKey() string {
	if v := os.Getenv("CEREBRAS_API_KEY"); v != "" {
		return v
	}
	return c.Keys.Cerebras
}

func (c *Config) BraveKey() string {
	if v := os.Getenv("BRAVE_API_KEY"); v != "" {
		return v
	}
	return c.Keys.Brave
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

func ValidRounds(n int) bool {
	return n >= 1 && n <= 10
}

func expandTilde(s *string) {
	if strings.HasPrefix(*s, "~/") {
		home, _ := os.UserHomeDir()
		*s = filepath.Join(home, (*s)[2:])
	}
}
