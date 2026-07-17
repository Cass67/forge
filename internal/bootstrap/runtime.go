package bootstrap

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"forge/internal/auth"
	"forge/internal/chatgptauth"
	"forge/internal/claudeauth"
	"forge/internal/config"
	"forge/internal/copilot"
	"forge/internal/fsutil"
	"forge/internal/llm"
	"forge/internal/llm/drivers"
	"forge/internal/modelcatalog"
)

var (
	claudeAuthAvailable     = claudeauth.Available
	newClaudeOAuthDriver    = func(registryName, apiModel string) llm.Driver { return drivers.NewClaudeOAuth(registryName, apiModel) }
	chatGPTAuthAvailable    = chatgptauth.Available
	newChatGPTDriver        = func(registryName, apiModel string) llm.Driver { return drivers.NewChatGPT(registryName, apiModel) }
	discoverAnthropicModels = DiscoverAnthropicModels
	discoverClaudeModels    = DiscoverClaudeModels
	discoverOpenAIModels    = DiscoverOpenAIModels
	discoverCompatModels    = DiscoverOpenAICompatibleModels
)

type CompatProvider struct {
	Name         string
	Label        string
	BaseURL      string
	KeyFn        func() string
	IsModel      func(string) bool
	Models       []string
	WireAPI      string
	ModelInfoURL string
	HTTPHeaders  map[string]string
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
	if err := ensureDefaultConfigScaffold(); err != nil {
		return nil, err
	}
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
	if ref.Provider == "claude" || (ref.Provider == "" && canUseClaudeForUnqualifiedModel(resolvedModel)) {
		apiModel := canonicalAnthropicModel(resolvedModel)
		registryName := model
		if ref.Provider == "claude" {
			registryName = QualifyModel(ref)
		}
		if d := newClaudeOAuthDriver(registryName, apiModel); d != nil {
			return d
		}
		return nil
	}
	if ref.Provider == "anthropic" || (ref.Provider == "" && IsAnthropicModel(model)) {
		if key := cfg.AnthropicKey(); key != "" {
			return drivers.NewClaude(key, canonicalAnthropicModel(resolvedModel))
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
	if p, ambiguous := ResolveCompatProvider(BuildCompatProviders(cfg, tokens), model); p != nil {
		if p.Name == "opencode-go" && !modelcatalog.OpenCodeGoModelSupportedByOpenAICompatibleChat(ref.Model) {
			return nil
		}
		apiModel := compatAPIModel(p.Name, ref, resolvedModel)
		baseURL := p.BaseURL
		wireAPI := p.WireAPI
		if route := modelcatalog.CustomProviderRouteForModel(p.Name, ref.Model); route != nil {
			if strings.TrimSpace(route.APIModel) != "" {
				apiModel = strings.TrimSpace(route.APIModel)
			}
			if strings.TrimSpace(route.APIBase) != "" {
				baseURL = strings.TrimSpace(route.APIBase)
			}
			if strings.TrimSpace(route.WireAPI) != "" {
				wireAPI = strings.TrimSpace(route.WireAPI)
			}
		}
		supportsResponses := wireAPI == "responses"
		if len(p.HTTPHeaders) > 0 || supportsResponses || baseURL != p.BaseURL {
			return drivers.NewCustomCompatProvider(p.Name, p.KeyFn(), baseURL, model, apiModel, supportsResponses, p.HTTPHeaders)
		}
		if apiModel != resolvedModel {
			return drivers.NewOpenAICompatibleProviderAlias(p.Name, p.KeyFn(), baseURL, model, apiModel)
		}
		return drivers.NewOpenAICompatibleProviderAlias(p.Name, p.KeyFn(), baseURL, model, resolvedModel)
	} else if ambiguous {
		return nil
	}
	return nil
}

func ResolvedProviderID(cfg *config.Config, tokens *auth.Tokens, model string) string {
	ref := ParseModelRef(model)
	resolvedModel := ref.Model
	if ref.Provider == "claude" || (ref.Provider == "" && canUseClaudeForUnqualifiedModel(resolvedModel)) {
		return "claude"
	}
	if ref.Provider == "anthropic" || (ref.Provider == "" && IsAnthropicModel(model) && cfg.AnthropicKey() != "") {
		return "anthropic"
	}
	if ref.Provider == "chatgpt" || (ref.Provider == "" && canUseChatGPTForUnqualifiedModel(resolvedModel)) {
		return "chatgpt"
	}
	if ref.Provider == "" && IsOpenAIModel(model) && canUseCopilotForUnqualifiedModel(tokens, resolvedModel) {
		return "copilot"
	}
	if ref.Provider == "openai" || (ref.Provider == "" && IsOpenAIModel(model) && cfg.OpenAIKey() != "") {
		return "openai"
	}
	if ref.Provider == "copilot" || (ref.Provider == "" && IsCopilotModel(model) && tokens != nil && strings.TrimSpace(tokens.CopilotToken) != "") {
		return "copilot"
	}
	if p, ambiguous := ResolveCompatProvider(BuildCompatProviders(cfg, tokens), model); p != nil {
		return p.Name
	} else if ambiguous && ref.Provider != "" {
		return ref.Provider
	}
	return ref.Provider
}

func ModelDisplayLabel(cfg *config.Config, tokens *auth.Tokens, model string) string {
	model = strings.TrimSpace(model)
	if model == "" {
		return ""
	}
	ref := ParseModelRef(model)
	name := strings.TrimSpace(ref.Model)
	if name == "" {
		name = model
	}
	provider := ResolvedProviderID(cfg, tokens, model)
	if provider == "" {
		return name
	}
	return fmt.Sprintf("%s [%s]", name, provider)
}

func AvailableModels(cfg *config.Config, tokens *auth.Tokens) []string {
	var out []string
	if claudeAuthAvailable() {
		claudeModels := discoverClaudeModels()
		out = append(out, claudeModels...)
		out = append(out, qualifyModels("claude", claudeModels)...)
	}
	if cfg.AnthropicKey() != "" {
		anthropicModels := discoverAnthropicModels(cfg.AnthropicKey())
		out = append(out, anthropicModels...)
		out = append(out, qualifyModels("anthropic", anthropicModels)...)
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
	for _, p := range BuildCompatProviders(cfg, tokens) {
		if p.KeyFn() != "" {
			if customModels := modelcatalog.CustomProviderModels(p.Name); len(customModels) > 0 {
				customModels = filterCompatProviderModels(p.Name, customModels)
				out = append(out, qualifyCompatibleModelList(p.Name, customModels)...)
				continue
			}
			if useLiveCompatModelDiscovery(cfg) {
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

func filterCompatProviderModels(provider string, models []string) []string {
	if strings.TrimSpace(provider) != "opencode-go" {
		return models
	}
	out := make([]string, 0, len(models))
	for _, model := range models {
		if modelcatalog.OpenCodeGoModelSupportedByOpenAICompatibleChat(model) {
			out = append(out, model)
		}
	}
	return out
}

func useLiveCompatModelDiscoveryEnv() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("FORGE_ENABLE_LIVE_COMPAT_MODELS"))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

// useLiveCompatModelDiscovery returns true when live model discovery is enabled,
// either via config.toml [section] or the FORGE_ENABLE_LIVE_COMPAT_MODELS env var.
// The config file takes precedence.
func useLiveCompatModelDiscovery(cfg *config.Config) bool {
	if cfg != nil && cfg.LiveCompatModels {
		return true
	}
	return useLiveCompatModelDiscoveryEnv()
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
			ID:           "claude",
			Label:        "Claude.ai subscription",
			Status:       providerBackendStatus(claudeAuthAvailable(), "sign in"),
			DefaultModel: "claude/" + AnthropicModels()[0],
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
	for _, provider := range BuildCompatProviders(cfg, tokens) {
		defaultModel := provider.Name
		if len(provider.Models) > 0 {
			defaultModel = explicitBackendModel(provider.Name, provider.Models[0])
		}
		label := provider.Label
		if label == "" {
			label = strings.ToUpper(provider.Name[:1]) + provider.Name[1:]
		}
		backends = append(backends, ProviderBackend{
			ID:           provider.Name,
			Label:        label,
			Status:       providerBackendStatus(provider.KeyFn() != "", "configure API key"),
			DefaultModel: defaultModel,
		})
	}
	return backends
}

func AnthropicModels() []string {
	return []string{
		"claude-opus-4-6",
		"claude-sonnet-4-6",
		"claude-haiku-4-5",
	}
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

func canonicalAnthropicModel(name string) string {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "claude-opus-4-6", "claude-opus-4-1-20250805", "claude-opus-4-20250514", "claude-opus-4-5":
		return "claude-opus-4-6"
	case "claude-sonnet-4-6", "claude-sonnet-4-20250514", "claude-sonnet-4-5", "claude-3-7-sonnet-20250219", "claude-3-7-sonnet-latest", "claude-3-5-sonnet-20241022", "claude-3-5-sonnet-latest":
		return "claude-sonnet-4-6"
	case "claude-haiku-4-5", "claude-haiku-4-5-20251001", "claude-3-5-haiku-20241022", "claude-3-5-haiku-latest", "claude-3-haiku-20240307":
		return "claude-haiku-4-5"
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

func canUseClaudeForUnqualifiedModel(model string) bool {
	if !claudeAuthAvailable() {
		return false
	}
	return IsAnthropicModel(canonicalAnthropicModel(model))
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

func BuildCompatProviders(cfg *config.Config, tokens *auth.Tokens) []CompatProvider {
	providers := []CompatProvider{
		{
			Name:    "xai",
			BaseURL: "https://api.x.ai/v1",
			KeyFn:   cfg.XAIKey,
			IsModel: func(m string) bool { return strings.HasPrefix(m, "grok-") },
			Models:  []string{"grok-3", "grok-3-mini", "grok-3-fast", "grok-2"},
		},
		{
			Name:    "zai",
			BaseURL: "https://api.z.ai/api/paas/v4",
			KeyFn:   cfg.ZAIKey,
			IsModel: func(m string) bool { return strings.HasPrefix(m, "glm-") },
			Models: []string{
				"glm-5.1",
				"glm-5",
				"glm-5-turbo",
				"glm-4.7",
				"glm-4.7-flash",
				"glm-4.7-flashx",
				"glm-4.6",
				"glm-4.5",
				"glm-4.5-air",
				"glm-4.5-x",
				"glm-4.5-airx",
				"glm-4.5-flash",
				"glm-4-32b-0414-128k",
			},
		},
		{
			Name:    "zai-coding-plan",
			Label:   "Z.AI Coding Plan",
			BaseURL: "https://api.z.ai/api/coding/paas/v4",
			KeyFn:   cfg.ZAIKey,
			IsModel: func(m string) bool { return strings.HasPrefix(m, "glm-") },
			Models: []string{
				"glm-5.1",
				"glm-5",
				"glm-5-turbo",
				"glm-4.7",
				"glm-4.7-flash",
				"glm-4.7-flashx",
				"glm-4.6",
				"glm-4.5",
				"glm-4.5-air",
				"glm-4.5-flash",
				"glm-4.5v",
				"glm-4.6v",
			},
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
			Name:    "opencode-go",
			Label:   "OpenCode Go",
			BaseURL: "https://opencode.ai/zen/go/v1",
			KeyFn:   cfg.OpenCodeKey,
			IsModel: modelcatalog.OpenCodeGoModelSupportedByOpenAICompatibleChat,
			Models:  modelcatalog.OpenCodeGoSupportedChatModels(),
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

	// Register openrouter with modelcatalog so it can use models.dev catalog data
	modelcatalog.RegisterCustomProviderSource("openrouter", "https://models.dev/api.json", nil, cfg.OpenRouterKey)

	customDefs, _ := LoadCustomCompatProviders(fsutil.ForgeConfigDir())
	for _, def := range customDefs {
		RegisterCustomProviderName(def.ID)
		defID := def.ID
		modelcatalog.RegisterCustomProviderSource(defID, def.ModelInfoURL, def.HTTPHeaders, func() string {
			if tokens != nil {
				if k := tokens.CustomProviderKey(defID); k != "" {
					return k
				}
			}
			return os.Getenv(strings.ToUpper(defID) + "_API_KEY")
		})
		providers = append(providers, CompatProvider{
			Name:    def.ID,
			Label:   def.Name,
			BaseURL: def.BaseURL,
			KeyFn: func() string {
				if tokens != nil {
					if k := tokens.CustomProviderKey(defID); k != "" {
						return k
					}
				}
				return os.Getenv(strings.ToUpper(defID) + "_API_KEY")
			},
			IsModel:      func(string) bool { return false },
			Models:       def.Models,
			WireAPI:      def.WireAPI,
			ModelInfoURL: def.ModelInfoURL,
			HTTPHeaders:  def.HTTPHeaders,
		})
	}

	return providers
}
