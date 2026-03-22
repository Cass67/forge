package bootstrap

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"forge/internal/auth"
	"forge/internal/config"
	"forge/internal/copilot"
	"forge/internal/llm"
	"forge/internal/llm/drivers"
)

type Runtime struct {
	Config   *config.Config
	Tokens   *auth.Tokens
	Registry *llm.Registry
	Models   []string
}

type CompatProvider struct {
	Name    string
	BaseURL string
	KeyFn   func() string
	IsModel func(string) bool
	Models  []string
}

func DefaultConfigPath() string {
	return filepath.Join(os.Getenv("HOME"), ".config", "forge", "config.toml")
}

func LoadConfig() (*config.Config, error) {
	cfg, err := config.Load(DefaultConfigPath())
	if err != nil {
		return nil, err
	}
	if issues := cfg.Validate(); len(issues) > 0 {
		return nil, fmt.Errorf("invalid config: %s: %s", issues[0].Field, issues[0].Message)
	}
	return cfg, nil
}

func LoadTokens() (*auth.Tokens, error) {
	tokens, err := auth.Load()
	if err != nil {
		return &auth.Tokens{}, err
	}
	return tokens, nil
}

func LoadRuntime() (*Runtime, error) {
	cfg, err := LoadConfig()
	if err != nil {
		return nil, err
	}
	tokens, _ := LoadTokens()
	return &Runtime{
		Config:   cfg,
		Tokens:   tokens,
		Registry: BuildRegistry(cfg, tokens, cfg.Models.Writer, cfg.Models.Auditor, cfg.Models.Summarizer),
		Models:   AvailableModels(cfg, tokens),
	}, nil
}

func BuildRegistry(cfg *config.Config, tokens *auth.Tokens, models ...string) *llm.Registry {
	reg := llm.NewRegistry()
	registered := map[string]bool{}
	for _, model := range models {
		d := DriverForModel(cfg, tokens, model)
		if d == nil || registered[d.Name()] {
			continue
		}
		reg.Register(d)
		registered[d.Name()] = true
	}
	return reg
}

func EnsureDriver(cfg *config.Config, tokens *auth.Tokens, reg *llm.Registry, model string) {
	if _, err := reg.Lookup(model); err == nil {
		return
	}
	if d := DriverForModel(cfg, tokens, model); d != nil {
		reg.Register(d)
	}
}

func DriverForModel(cfg *config.Config, tokens *auth.Tokens, model string) llm.Driver {
	ref := ParseModelRef(model)
	resolvedModel := ref.Model
	if ref.Provider == "anthropic" || (ref.Provider == "" && IsAnthropicModel(model)) {
		if key := cfg.AnthropicKey(); key != "" {
			return drivers.NewClaude(key, resolvedModel)
		}
		return nil
	}
	if ref.Provider == "openai" || (ref.Provider == "" && IsOpenAIModel(model)) {
		if key := cfg.OpenAIKey(); key != "" {
			return drivers.NewOpenAI(key, resolvedModel)
		}
		return nil
	}
	if ref.Provider == "copilot" || (ref.Provider == "" && IsCopilotModel(model)) {
		if tokens.CopilotToken != "" {
			apiModel := resolvedModel
			if ref.Provider == "" {
				apiModel = CopilotAPIModel(model)
			}
			return drivers.NewCopilot(tokens.CopilotToken, model, apiModel)
		}
		return nil
	}
	if p, ambiguous := ResolveCompatProvider(BuildCompatProviders(cfg), model); p != nil {
		return drivers.NewOpenAICompatible(p.KeyFn(), p.BaseURL, resolvedModel)
	} else if ambiguous {
		return nil
	}
	return nil
}

func AvailableModels(cfg *config.Config, tokens *auth.Tokens) []string {
	var out []string
	if cfg.AnthropicKey() != "" {
		out = append(out, AnthropicModels()...)
	}
	if cfg.OpenAIKey() != "" {
		out = append(out, OpenAIModels()...)
	}
	for _, p := range BuildCompatProviders(cfg) {
		if p.KeyFn() != "" {
			out = append(out, p.Models...)
		}
	}
	if tokens.CopilotToken != "" {
		out = append(out, CopilotModels(tokens.CopilotToken)...)
	}
	if len(out) == 0 {
		out = AllModels()
	}
	return out
}

func FindCompatProvider(providers []CompatProvider, model string) *CompatProvider {
	for i := range providers {
		p := &providers[i]
		if p.KeyFn() != "" && p.IsModel(model) {
			return p
		}
	}
	return nil
}

func AnthropicModels() []string {
	return []string{
		"claude-3-7-sonnet-latest",
		"claude-3-7-sonnet-20250219",
		"claude-3-5-haiku-latest",
		"claude-3-5-haiku-20241022",
		"claude-sonnet-4-5",
		"claude-sonnet-4-5-20250929",
		"claude-haiku-4-5",
		"claude-haiku-4-5-20251001",
		"claude-opus-4-5-20251101",
		"claude-sonnet-4-6",
		"claude-opus-4-6",
	}
}

func OpenAIModels() []string {
	return []string{
		"gpt-5.4",
		"gpt-5-mini",
		"gpt-4o",
		"gpt-4o-mini",
		"gpt-4-turbo",
		"o1",
		"o1-mini",
		"o3-mini",
	}
}

func AllModels() []string {
	return append(AnthropicModels(), OpenAIModels()...)
}

func IsAnthropicModel(name string) bool {
	return strings.HasPrefix(name, "claude")
}

func IsOpenAIModel(name string) bool {
	return strings.HasPrefix(name, "gpt") || strings.HasPrefix(name, "o1") || strings.HasPrefix(name, "o3")
}

func IsCopilotModel(name string) bool {
	return strings.HasPrefix(name, "copilot/")
}

func CopilotAPIModel(name string) string {
	return strings.TrimPrefix(name, "copilot/")
}

func CopilotModels(token string) []string {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	models, _ := copilot.DiscoverModels(ctx, token)
	return models
}

func BuildCompatProviders(cfg *config.Config) []CompatProvider {
	return []CompatProvider{
		{
			Name:    "xai",
			BaseURL: "https://api.x.ai/v1",
			KeyFn:   cfg.XAIKey,
			IsModel: func(m string) bool { return strings.HasPrefix(m, "grok-") },
			Models:  []string{"grok-3", "grok-3-mini", "grok-3-fast", "grok-2"},
		},
		{
			Name:    "mistral",
			BaseURL: "https://api.mistral.ai/v1",
			KeyFn:   cfg.MistralKey,
			IsModel: func(m string) bool {
				return strings.HasPrefix(m, "mistral-") ||
					strings.HasPrefix(m, "codestral-") ||
					strings.HasPrefix(m, "pixtral-") ||
					strings.HasPrefix(m, "magistral-")
			},
			Models: []string{"mistral-large-latest", "mistral-small-latest", "codestral-latest", "pixtral-large-latest"},
		},
		{
			Name:    "perplexity",
			BaseURL: "https://api.perplexity.ai",
			KeyFn:   cfg.PerplexityKey,
			IsModel: func(m string) bool { return strings.HasPrefix(m, "sonar") },
			Models:  []string{"sonar-pro", "sonar", "sonar-reasoning-pro", "sonar-reasoning"},
		},
		{
			Name:    "cerebras",
			BaseURL: "https://api.cerebras.ai/v1",
			KeyFn:   cfg.CerebrasKey,
			IsModel: func(m string) bool { return strings.HasPrefix(m, "llama3.") },
			Models:  []string{"llama3.1-8b", "llama3.3-70b"},
		},
		{
			Name:    "groq",
			BaseURL: "https://api.groq.com/openai/v1",
			KeyFn:   cfg.GroqKey,
			IsModel: func(m string) bool {
				return strings.HasPrefix(m, "llama-") ||
					strings.HasPrefix(m, "gemma") ||
					strings.HasPrefix(m, "mixtral") ||
					strings.HasPrefix(m, "qwen-") ||
					strings.HasPrefix(m, "compound") ||
					strings.HasPrefix(m, "deepseek-r1-")
			},
			Models: []string{
				"llama-3.3-70b-versatile",
				"llama-3.1-8b-instant",
				"gemma2-9b-it",
				"mixtral-8x7b-32768",
				"qwen-qwq-32b",
				"deepseek-r1-distill-llama-70b",
			},
		},
		{
			Name:    "nvidia",
			BaseURL: "https://integrate.api.nvidia.com/v1",
			KeyFn:   cfg.NVIDIAKey,
			IsModel: func(m string) bool { return strings.HasPrefix(m, "nvidia/") },
			Models:  []string{"nvidia/llama-3.1-nemotron-51b-instruct", "meta/llama-3.3-70b-instruct"},
		},
		{
			Name:    "together",
			BaseURL: "https://api.together.xyz/v1",
			KeyFn:   cfg.TogetherKey,
			IsModel: func(m string) bool { return strings.Contains(m, "/") },
			Models: []string{
				"meta-llama/Llama-3.3-70B-Instruct-Turbo",
				"deepseek-ai/DeepSeek-R1",
				"Qwen/QwQ-32B-Preview",
			},
		},
		{
			Name:    "deepinfra",
			BaseURL: "https://api.deepinfra.com/v1/openai",
			KeyFn:   cfg.DeepInfraKey,
			IsModel: func(m string) bool { return strings.Contains(m, "/") },
			Models: []string{
				"meta-llama/Meta-Llama-3.1-8B-Instruct",
				"deepseek-ai/DeepSeek-R1-Turbo",
				"microsoft/WizardLM-2-8x22B",
			},
		},
		{
			Name:    "openrouter",
			BaseURL: "https://openrouter.ai/api/v1",
			KeyFn:   cfg.OpenRouterKey,
			IsModel: func(m string) bool { return strings.Contains(m, "/") },
			Models: []string{
				"openai/gpt-4o",
				"anthropic/claude-sonnet-4-5",
				"meta-llama/llama-3.3-70b-instruct:free",
			},
		},
	}
}
