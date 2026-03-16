package config

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

type Models struct {
	Writer     string `toml:"writer"`
	Auditor    string `toml:"auditor"`
	Summarizer string `toml:"summarizer"`
}

type Session struct {
	RoundsPerPass int    `toml:"rounds_per_pass"`
	OutputDir     string `toml:"output_dir"`
}

type Keys struct {
	Anthropic string `toml:"anthropic"`
	OpenAI    string `toml:"openai"`
}

type Passes struct {
	Correctness string `toml:"correctness"`
	Refactor    string `toml:"refactor"`
	Security    string `toml:"security"`
	Prod        string `toml:"prod"`
}

type Config struct {
	Models  Models  `toml:"models"`
	Session Session `toml:"session"`
	Keys    Keys    `toml:"keys"`
	Passes  Passes  `toml:"passes"`
}

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
	c.Models.Writer = "claude-sonnet-4-6"
	c.Models.Auditor = "gpt-4o"
	c.Models.Summarizer = "claude-haiku-4-5-20251001"
	c.Session.RoundsPerPass = 3
	c.Session.OutputDir = "./output"
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

func ValidRounds(n int) bool {
	return n >= 1 && n <= 10
}

func expandTilde(s *string) {
	if strings.HasPrefix(*s, "~/") {
		home, _ := os.UserHomeDir()
		*s = filepath.Join(home, (*s)[2:])
	}
}
