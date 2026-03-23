package bootstrap

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"forge/internal/auth"
	"forge/internal/chatgptauth"
	"forge/internal/config"
	"forge/internal/copilot"
	"forge/internal/fsutil"
	"forge/internal/llm"
	"forge/internal/llm/drivers"
)

var (
	chatGPTAuthAvailable = chatgptauth.Available
	newChatGPTDriver     = func(registryName, apiModel string) llm.Driver { return drivers.NewChatGPT(registryName, apiModel) }
	discoverOpenAIModels = DiscoverOpenAIModels
	discoverCompatModels = DiscoverOpenAICompatibleModels
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

type ProviderBackend struct {
	ID           string
	Label        string
	Status       string
	DefaultModel string
}

func DefaultConfigPath() string {
	return fsutil.ForgeConfigPath("config.toml")
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
	if ref.Provider == "chatgpt" || (ref.Provider == "" && canUseChatGPTForUnqualifiedModel(resolvedModel)) {
		apiModel := canonicalChatGPTModel(resolvedModel)
		if apiModel == "" {
			return nil
		}
		registryName := model
		if ref.Provider == "chatgpt" {
			registryName = QualifyModel(ref)
		}
		if d := newChatGPTDriver(registryName, apiModel); d != nil {
			return d
		}
		return nil
	}
	if ref.Provider == "" && IsOpenAIModel(model) && canUseCopilotForUnqualifiedModel(tokens, resolvedModel) {
		return drivers.NewCopilot(tokens.CopilotToken, model, resolvedModel)
	}
	if ref.Provider == "openai" || (ref.Provider == "" && IsOpenAIModel(model)) {
		if key := cfg.OpenAIKey(); key != "" {
			apiModel := canonicalOpenAIModel(resolvedModel)
			if apiModel != resolvedModel {
				return drivers.NewOpenAIAlias(key, model, apiModel)
			}
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
		apiModel := compatAPIModel(p.Name, ref, resolvedModel)
		if apiModel != resolvedModel {
			return drivers.NewOpenAICompatibleProviderAlias(p.Name, p.KeyFn(), p.BaseURL, model, apiModel)
		}
		return drivers.NewOpenAICompatibleProviderAlias(p.Name, p.KeyFn(), p.BaseURL, model, resolvedModel)
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
	if chatGPTAuthAvailable() {
		out = append(out, ChatGPTModels()...)
		out = append(out, qualifyModels("chatgpt", ChatGPTModels())...)
	}
	if cfg.OpenAIKey() != "" {
		openAIModels := discoverOpenAIModels(cfg.OpenAIKey())
		out = append(out, openAIModels...)
		out = append(out, qualifyModels("openai", openAIModels)...)
	}
	for _, p := range BuildCompatProviders(cfg) {
		if p.KeyFn() != "" {
			if useLiveCompatModelDiscovery() {
				out = append(out, discoverCompatModels(p.BaseURL, p.KeyFn(), p.Name, p.Models, p.IsModel)...)
			} else {
				out = append(out, qualifyCompatibleModelList(p.Name, p.Models)...)
			}
		}
	}
	if tokens.CopilotToken != "" {
		out = append(out, CopilotModels(tokens.CopilotToken)...)
	}
	return sortModelsByHealth(uniqueStrings(out))
}

func useLiveCompatModelDiscovery() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("FORGE_ENABLE_LIVE_COMPAT_MODELS"))) {
	case "0", "false", "no", "off":
		return false
	default:
		return true
	}
}

func SupportedProviderBackends(cfg *config.Config, tokens *auth.Tokens) []ProviderBackend {
	backends := []ProviderBackend{
		{
			ID:           "anthropic",
			Label:        "Anthropic",
			Status:       providerBackendStatus(cfg.AnthropicKey() != "", "configure API key"),
			DefaultModel: "anthropic/" + AnthropicModels()[0],
		},
		{
			ID:           "chatgpt",
			Label:        "ChatGPT subscription",
			Status:       providerBackendStatus(chatGPTAuthAvailable(), "sign in"),
			DefaultModel: "chatgpt/" + ChatGPTModels()[0],
		},
		{
			ID:           "copilot",
			Label:        "GitHub Copilot",
			Status:       providerBackendStatus(tokens != nil && strings.TrimSpace(tokens.CopilotToken) != "", "sign in"),
			DefaultModel: "copilot/gpt-5",
		},
		{
			ID:           "openai",
			Label:        "OpenAI API",
			Status:       providerBackendStatus(cfg.OpenAIKey() != "", "configure API key"),
			DefaultModel: "openai/" + OpenAIModels()[0],
		},
	}
	for _, provider := range BuildCompatProviders(cfg) {
		defaultModel := provider.Name
		if len(provider.Models) > 0 {
			defaultModel = explicitBackendModel(provider.Name, provider.Models[0])
		}
		backends = append(backends, ProviderBackend{
			ID:           provider.Name,
			Label:        strings.ToUpper(provider.Name[:1]) + provider.Name[1:],
			Status:       providerBackendStatus(provider.KeyFn() != "", "configure API key"),
			DefaultModel: defaultModel,
		})
	}
	return backends
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

func AllModels() []string {
	return append(AnthropicModels(), OpenAIModels()...)
}

func IsAnthropicModel(name string) bool {
	return strings.HasPrefix(name, "claude")
}

func IsOpenAIModel(name string) bool {
	return strings.HasPrefix(name, "gpt") || strings.HasPrefix(name, "o1") || strings.HasPrefix(name, "o3") || strings.HasPrefix(name, "o4")
}

func canonicalOpenAIModel(name string) string {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "gpt-5.4", "gpt5.4":
		return "gpt-5"
	default:
		return strings.TrimSpace(name)
	}
}

func compatAPIModel(provider string, ref ModelRef, resolvedModel string) string {
	return canonicalOpenAIModel(strings.TrimSpace(resolvedModel))
}

func IsCopilotModel(name string) bool {
	return strings.HasPrefix(name, "copilot/")
}

func canonicalChatGPTModel(name string) string {
	model := strings.TrimSpace(name)
	switch strings.ToLower(model) {
	case "gpt-5":
		return "gpt-5.4"
	default:
		return model
	}
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

func canUseCopilotForUnqualifiedModel(tokens *auth.Tokens, model string) bool {
	if tokens == nil || strings.TrimSpace(tokens.CopilotToken) == "" {
		return false
	}
	target := "copilot/" + strings.TrimSpace(model)
	for _, known := range copilot.KnownModels() {
		if known == target {
			return true
		}
	}
	return false
}

func canUseChatGPTForUnqualifiedModel(model string) bool {
	if !chatGPTAuthAvailable() {
		return false
	}
	target := canonicalChatGPTModel(model)
	if target == "" {
		return false
	}
	for _, known := range ChatGPTModels() {
		if known == target {
			return true
		}
	}
	return false
}

func qualifyModels(provider string, models []string) []string {
	out := make([]string, 0, len(models))
	for _, model := range models {
		model = strings.TrimSpace(model)
		if model == "" {
			continue
		}
		out = append(out, provider+"/"+model)
	}
	return out
}

func uniqueStrings(in []string) []string {
	out := make([]string, 0, len(in))
	seen := make(map[string]struct{}, len(in))
	for _, item := range in {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if _, ok := seen[item]; ok {
			continue
		}
		seen[item] = struct{}{}
		out = append(out, item)
	}
	return out
}

func providerBackendStatus(available bool, missing string) string {
	if available {
		return "ready"
	}
	return missing
}

func explicitBackendModel(providerID, model string) string {
	ref := ParseModelRef(model)
	if ref.Provider == providerID {
		return QualifyModel(ref)
	}
	return providerID + "/" + strings.TrimSpace(model)
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
			IsModel: func(m string) bool { return strings.Contains(m, "/") },
			Models: []string{
				"moonshotai/kimi-k2-instruct-0905",
				"moonshotai/kimi-k2.5",
				"nvidia/llama-3.1-nemotron-51b-instruct",
				"meta/llama-3.3-70b-instruct",
				"google/gemma-3-27b-it",
			},
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
			IsModel: func(m string) bool {
				return strings.Contains(m, "/") && !strings.HasPrefix(m, "openrouter/")
			},
			Models: []string{
				"deepseek/deepseek-r1-0528",
				"moonshotai/kimi-k2-0905",
				"openai/gpt-4o",
				"anthropic/claude-sonnet-4-5",
				"meta-llama/llama-3.3-70b-instruct:free",
			},
		},
	}
}
