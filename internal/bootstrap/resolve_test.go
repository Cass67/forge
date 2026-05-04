package bootstrap

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"forge/internal/auth"
	"forge/internal/config"
	"forge/internal/llm"
	"forge/internal/llm/drivers"
)

func TestParseModelRef(t *testing.T) {
	ref := ParseModelRef("openrouter/openai/gpt-4o")
	if ref.Provider != "openrouter" || ref.Model != "openai/gpt-4o" {
		t.Fatalf("unexpected ref: %+v", ref)
	}
}

func TestResolveCompatProviderDetectsAmbiguity(t *testing.T) {
	cfg := testConfig()
	cfg.Keys.Together = "together-key"
	cfg.Keys.OpenRouter = "openrouter-key"

	p, ambiguous := ResolveCompatProvider(BuildCompatProviders(cfg, &auth.Tokens{}), "meta-llama/Llama-3.3-70B-Instruct-Turbo")
	if p != nil {
		t.Fatalf("expected nil provider for ambiguous match, got %s", p.Name)
	}
	if !ambiguous {
		t.Fatal("expected ambiguity to be reported")
	}
}

func TestDriverForExplicitCompatProvider(t *testing.T) {
	cfg := testConfig()
	cfg.Keys.OpenRouter = "openrouter-key"
	d := DriverForModel(cfg, &auth.Tokens{}, "openrouter/openai/gpt-4o")
	if d == nil {
		t.Fatal("expected explicit provider-qualified model to resolve")
	}
	if got := d.Name(); got != "openrouter/openai/gpt-4o" {
		t.Fatalf("driver.Name() = %q, want %q", got, "openrouter/openai/gpt-4o")
	}
}

func TestDriverForExplicitCompatProviderKeepsRegistryNameForNestedProviderModel(t *testing.T) {
	cfg := testConfig()
	cfg.Keys.OpenRouter = "openrouter-key"
	d := DriverForModel(cfg, &auth.Tokens{}, "openrouter/x-ai/grok-4.20-multi-agent-beta")
	if d == nil {
		t.Fatal("expected explicit provider-qualified model to resolve")
	}
	if got := d.Name(); got != "openrouter/x-ai/grok-4.20-multi-agent-beta" {
		t.Fatalf("driver.Name() = %q, want %q", got, "openrouter/x-ai/grok-4.20-multi-agent-beta")
	}
}

func TestDriverForExplicitZAICodingPlanProvider(t *testing.T) {
	cfg := testConfig()
	cfg.Keys.ZAI = "zai-key"
	d := DriverForModel(cfg, &auth.Tokens{}, "zai-coding-plan/glm-4.6")
	if d == nil {
		t.Fatal("expected explicit provider-qualified model to resolve")
	}
	if got := d.Name(); got != "zai-coding-plan/glm-4.6" {
		t.Fatalf("driver.Name() = %q, want %q", got, "zai-coding-plan/glm-4.6")
	}
	if _, ok := d.(*drivers.OpenAIDriver); !ok {
		t.Fatalf("expected OpenAIDriver, got %T", d)
	}
}

func TestCompatAPIModelPreservesOpenRouterFreeRouterAlias(t *testing.T) {
	// "free" is no longer a special-cased alias; it passes through unchanged
	// like any other model name. The curated model list no longer includes it.
	ref := ParseModelRef("openrouter/free")
	if got := compatAPIModel("openrouter", ref, ref.Model); got != "free" {
		t.Fatalf("compatAPIModel() = %q, want %q", got, "free")
	}
}

func TestDriverForExplicitNVIDIAProviderKeepsRegistryNameForNestedProviderModel(t *testing.T) {
	cfg := testConfig()
	cfg.Keys.NVIDIA = "nvidia-key"
	d := DriverForModel(cfg, &auth.Tokens{}, "nvidia/meta/llama-3.3-70b-instruct")
	if d == nil {
		t.Fatal("expected explicit provider-qualified model to resolve")
	}
	if got := d.Name(); got != "nvidia/meta/llama-3.3-70b-instruct" {
		t.Fatalf("driver.Name() = %q, want %q", got, "nvidia/meta/llama-3.3-70b-instruct")
	}
}

func TestCanonicalOpenAIModel(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{in: "gpt-5.4", want: "gpt-5"},
		{in: "gpt5.4", want: "gpt-5"},
		{in: " gpt-5 ", want: "gpt-5"},
		{in: "gpt-4o", want: "gpt-4o"},
	}

	for _, tt := range tests {
		if got := canonicalOpenAIModel(tt.in); got != tt.want {
			t.Fatalf("canonicalOpenAIModel(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestCanonicalAnthropicModel(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{in: "claude-sonnet-4-6", want: "claude-sonnet-4-6"},
		{in: "claude-sonnet-4-20250514", want: "claude-sonnet-4-6"},
		{in: "claude-3-7-sonnet-20250219", want: "claude-sonnet-4-6"},
		{in: "claude-opus-4-6", want: "claude-opus-4-6"},
		{in: "claude-opus-4-1-20250805", want: "claude-opus-4-6"},
		{in: "claude-haiku-4-5", want: "claude-haiku-4-5"},
		{in: "claude-3-5-haiku-20241022", want: "claude-haiku-4-5"},
		{in: "claude-3-5-haiku-latest", want: "claude-haiku-4-5"},
	}

	for _, tt := range tests {
		if got := canonicalAnthropicModel(tt.in); got != tt.want {
			t.Fatalf("canonicalAnthropicModel(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestAnthropicModelsUseStableAPINames(t *testing.T) {
	want := []string{
		"claude-opus-4-6",
		"claude-sonnet-4-6",
		"claude-haiku-4-5",
	}
	got := AnthropicModels()
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("AnthropicModels() = %#v, want %#v", got, want)
	}
}

func TestPreferredClaudeModelsKeepsLatestVisibleFamilies(t *testing.T) {
	got := preferredClaudeModels([]string{
		"claude-sonnet-4-6",
		"claude-opus-4-6",
		"claude-opus-4-5-20251101",
		"claude-sonnet-4-5-20250929",
		"claude-haiku-4-5",
	})
	want := []string{
		"claude-opus-4-6",
		"claude-sonnet-4-6",
		"claude-haiku-4-5",
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("preferredClaudeModels() = %#v, want %#v", got, want)
	}
}

func TestDriverForModelMapsLegacyOpenAIAlias(t *testing.T) {
	cfg := testConfig()
	cfg.Keys.OpenAI = "openai-key"

	d := DriverForModel(cfg, &auth.Tokens{}, "gpt-5.4")
	if d == nil {
		t.Fatal("expected legacy openai alias to resolve")
	}
	if got := d.Name(); got != "gpt-5.4" {
		t.Fatalf("driver.Name() = %q, want %q", got, "gpt-5.4")
	}
}

func TestDriverForUnqualifiedGPTPrefersCopilotWhenAvailable(t *testing.T) {
	cfg := testConfig()
	cfg.Keys.OpenAI = "openai-key"

	prevAvail := chatGPTAuthAvailable
	prevDriver := newChatGPTDriver
	chatGPTAuthAvailable = func() bool { return false }
	newChatGPTDriver = prevDriver
	defer func() {
		chatGPTAuthAvailable = prevAvail
		newChatGPTDriver = prevDriver
	}()

	d := DriverForModel(cfg, &auth.Tokens{CopilotToken: "copilot-token"}, "gpt-5.4")
	if d == nil {
		t.Fatal("expected driver")
	}
	if got := d.Name(); got != "gpt-5.4" {
		t.Fatalf("driver.Name() = %q, want %q", got, "gpt-5.4")
	}
	if _, ok := d.(*drivers.OpenAIDriver); !ok {
		t.Fatalf("expected OpenAIDriver-backed copilot driver, got %T", d)
	}
}

func TestDriverForExplicitOpenAIDoesNotPreferCopilot(t *testing.T) {
	cfg := testConfig()
	cfg.Keys.OpenAI = "openai-key"

	d := DriverForModel(cfg, &auth.Tokens{CopilotToken: "copilot-token"}, "openai/gpt-5.4")
	if d == nil {
		t.Fatal("expected driver")
	}
	if got := d.Name(); got != "openai/gpt-5.4" {
		t.Fatalf("driver.Name() = %q, want %q", got, "openai/gpt-5.4")
	}
}

func TestDriverForUnqualifiedGPTPrefersChatGPTWhenAvailable(t *testing.T) {
	cfg := testConfig()
	cfg.Keys.OpenAI = "openai-key"

	prevAvail := chatGPTAuthAvailable
	prevDriver := newChatGPTDriver
	defer func() {
		chatGPTAuthAvailable = prevAvail
		newChatGPTDriver = prevDriver
	}()
	chatGPTAuthAvailable = func() bool { return true }
	newChatGPTDriver = func(registryName, apiModel string) llm.Driver {
		return drivers.NewOpenAIAlias("chatgpt-test", registryName, apiModel)
	}

	d := DriverForModel(cfg, &auth.Tokens{CopilotToken: "copilot-token"}, "gpt-5.4")
	if d == nil {
		t.Fatal("expected driver")
	}
	if got := d.Name(); got != "gpt-5.4" {
		t.Fatalf("driver.Name() = %q, want %q", got, "gpt-5.4")
	}
}

func TestDriverForExplicitChatGPTUsesChatGPTProvider(t *testing.T) {
	cfg := testConfig()

	prevAvail := chatGPTAuthAvailable
	prevDriver := newChatGPTDriver
	defer func() {
		chatGPTAuthAvailable = prevAvail
		newChatGPTDriver = prevDriver
	}()
	chatGPTAuthAvailable = func() bool { return true }
	newChatGPTDriver = func(registryName, apiModel string) llm.Driver {
		return drivers.NewOpenAIAlias("chatgpt-test", registryName, apiModel)
	}

	d := DriverForModel(cfg, &auth.Tokens{}, "chatgpt/gpt-5.4")
	if d == nil {
		t.Fatal("expected driver")
	}
	if got := d.Name(); got != "chatgpt/gpt-5.4" {
		t.Fatalf("driver.Name() = %q, want %q", got, "chatgpt/gpt-5.4")
	}
}

func TestDriverForExplicitClaudeUsesClaudeProvider(t *testing.T) {
	cfg := testConfig()

	prevAvail := claudeAuthAvailable
	prevDriver := newClaudeOAuthDriver
	defer func() {
		claudeAuthAvailable = prevAvail
		newClaudeOAuthDriver = prevDriver
	}()
	claudeAuthAvailable = func() bool { return true }
	newClaudeOAuthDriver = func(registryName, apiModel string) llm.Driver {
		return drivers.NewClaudeOAuthAlias(registryName, apiModel, nil)
	}

	d := DriverForModel(cfg, &auth.Tokens{}, "claude/claude-sonnet-4-6")
	if d == nil {
		t.Fatal("expected driver")
	}
	if got := d.Name(); got != "claude/claude-sonnet-4-6" {
		t.Fatalf("driver.Name() = %q, want %q", got, "claude/claude-sonnet-4-6")
	}
}

func TestDriverForUnqualifiedClaudePrefersClaudeLoginWhenAvailable(t *testing.T) {
	cfg := testConfig()
	cfg.Keys.Anthropic = "anthropic-key"

	prevAvail := claudeAuthAvailable
	prevDriver := newClaudeOAuthDriver
	defer func() {
		claudeAuthAvailable = prevAvail
		newClaudeOAuthDriver = prevDriver
	}()
	claudeAuthAvailable = func() bool { return true }
	newClaudeOAuthDriver = func(registryName, apiModel string) llm.Driver {
		return drivers.NewClaudeOAuthAlias(registryName, apiModel, nil)
	}

	d := DriverForModel(cfg, &auth.Tokens{}, "claude-sonnet-4-6")
	if d == nil {
		t.Fatal("expected driver")
	}
	if got := d.Name(); got != "claude-sonnet-4-6" {
		t.Fatalf("driver.Name() = %q, want %q", got, "claude-sonnet-4-6")
	}
}

func TestSupportedProviderBackendsIncludesConfiguredAndLoginBackends(t *testing.T) {
	cfg := testConfig()
	cfg.Keys.OpenAI = "openai-key"
	cfg.Keys.Anthropic = "anthropic-key"

	prevClaudeAvail := claudeAuthAvailable
	prevAvail := chatGPTAuthAvailable
	defer func() {
		claudeAuthAvailable = prevClaudeAvail
		chatGPTAuthAvailable = prevAvail
	}()
	claudeAuthAvailable = func() bool { return true }
	chatGPTAuthAvailable = func() bool { return true }

	backends := SupportedProviderBackends(cfg, &auth.Tokens{CopilotToken: "copilot-token"})
	if len(backends) < 4 {
		t.Fatalf("expected at least core backends, got %#v", backends)
	}
	if backends[0].ID != "anthropic" {
		t.Fatalf("first backend = %q, want anthropic", backends[0].ID)
	}
	foundChatGPT := false
	foundClaude := false
	for _, backend := range backends {
		if backend.ID == "claude" {
			foundClaude = true
			if backend.Status != "ready" {
				t.Fatalf("claude status = %q, want ready", backend.Status)
			}
		}
		if backend.ID == "chatgpt" {
			foundChatGPT = true
			if backend.Status != "ready" {
				t.Fatalf("chatgpt status = %q, want ready", backend.Status)
			}
		}
	}
	if !foundChatGPT {
		t.Fatal("expected chatgpt backend to be present")
	}
	if !foundClaude {
		t.Fatal("expected claude backend to be present")
	}
}

func TestAvailableModelsIncludesQualifiedCompatProviderModels(t *testing.T) {
	prevDiscover := discoverCompatModels
	discoverCompatModels = func(_ string, _ string, _ string, curated []string, _ func(string) bool) []string {
		return qualifyCompatibleModelList("openrouter", curated)
	}
	defer func() { discoverCompatModels = prevDiscover }()

	cfg := testConfig()
	cfg.Keys.OpenRouter = "openrouter-key"

	models := AvailableModels(cfg, &auth.Tokens{})

	if !containsTestString(models, "openrouter/moonshotai/kimi-k2-0905") {
		t.Fatalf("expected qualified openrouter model in available models, got %#v", models)
	}
}

func TestAvailableModelsUsesCuratedCompatCatalogWhenLiveCompatDiscoveryDisabled(t *testing.T) {
	t.Setenv("FORGE_ENABLE_LIVE_COMPAT_MODELS", "0")
	cfg := testConfig()
	cfg.Keys.OpenRouter = "openrouter-key"

	models := AvailableModels(cfg, &auth.Tokens{})

	if containsTestString(models, "openrouter/x-ai/grok-4.20-multi-agent-beta") {
		t.Fatalf("unexpected live compat model in default catalog: %#v", models)
	}
	if !containsTestString(models, "openrouter/moonshotai/kimi-k2-0905") {
		t.Fatalf("expected curated openrouter kimi model in default catalog: %#v", models)
	}
}

func TestAvailableModelsIncludesQualifiedNVIDIAModels(t *testing.T) {
	prevDiscover := discoverCompatModels
	discoverCompatModels = func(_ string, _ string, provider string, curated []string, _ func(string) bool) []string {
		return qualifyCompatibleModelList(provider, curated)
	}
	defer func() { discoverCompatModels = prevDiscover }()

	cfg := testConfig()
	cfg.Keys.NVIDIA = "nvidia-key"

	models := AvailableModels(cfg, &auth.Tokens{})

	if !containsTestString(models, "nvidia/meta/llama-3.3-70b-instruct") {
		t.Fatalf("expected qualified nvidia meta model in available models, got %#v", models)
	}
	if !containsTestString(models, "nvidia/moonshotai/kimi-k2-instruct-0905") {
		t.Fatalf("expected curated nvidia kimi model in available models, got %#v", models)
	}
}

func TestAvailableModelsIncludesAuthBackedCompatProviderModels(t *testing.T) {
	prevDiscover := discoverCompatModels
	discoverCompatModels = func(_ string, _ string, _ string, curated []string, _ func(string) bool) []string {
		return qualifyCompatibleModelList("openrouter", curated)
	}
	defer func() { discoverCompatModels = prevDiscover }()

	home := filepath.Join(t.TempDir(), "home")
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatalf("mkdir home: %v", err)
	}
	t.Setenv("HOME", home)
	if err := auth.Save(&auth.Tokens{OpenRouterAPIKey: "openrouter-key"}); err != nil {
		t.Fatalf("save auth: %v", err)
	}

	cfg := testConfig()
	models := AvailableModels(cfg, &auth.Tokens{})

	if !containsTestString(models, "openrouter/moonshotai/kimi-k2-0905") {
		t.Fatalf("expected auth-backed openrouter model in available models, got %#v", models)
	}
}

func TestAvailableModelsIncludesAuthBackedNVIDIAModels(t *testing.T) {
	prevDiscover := discoverCompatModels
	discoverCompatModels = func(_ string, _ string, provider string, curated []string, _ func(string) bool) []string {
		return qualifyCompatibleModelList(provider, curated)
	}
	defer func() { discoverCompatModels = prevDiscover }()

	home := filepath.Join(t.TempDir(), "home")
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatalf("mkdir home: %v", err)
	}
	t.Setenv("HOME", home)
	if err := auth.Save(&auth.Tokens{NVIDIAAPIKey: "nvidia-key"}); err != nil {
		t.Fatalf("save auth: %v", err)
	}

	cfg := testConfig()
	models := AvailableModels(cfg, &auth.Tokens{})

	if !containsTestString(models, "nvidia/moonshotai/kimi-k2-instruct-0905") {
		t.Fatalf("expected auth-backed nvidia model in available models, got %#v", models)
	}
}

func TestAvailableModelsUsesCuratedCompatCatalogByDefault(t *testing.T) {
	t.Setenv("FORGE_ENABLE_LIVE_COMPAT_MODELS", "")
	prevDiscover := discoverCompatModels
	discoverCompatModels = func(_ string, _ string, provider string, curated []string, _ func(string) bool) []string {
		return append([]string{provider + "/live/provider-model"}, qualifyCompatibleModelList(provider, curated)...)
	}
	defer func() { discoverCompatModels = prevDiscover }()

	cfg := testConfig()
	cfg.Keys.OpenRouter = "openrouter-key"

	models := AvailableModels(cfg, &auth.Tokens{})

	if containsTestString(models, "openrouter/live/provider-model") {
		t.Fatalf("unexpected live compat model in default catalog: %#v", models)
	}
	if !containsTestString(models, "openrouter/moonshotai/kimi-k2-0905") {
		t.Fatalf("expected curated compat model in default catalog: %#v", models)
	}
}

func TestAvailableModelsUsesLiveCompatCatalogWhenExplicitlyEnabled(t *testing.T) {
	t.Setenv("FORGE_ENABLE_LIVE_COMPAT_MODELS", "1")
	prevDiscover := discoverCompatModels
	discoverCompatModels = func(_ string, _ string, provider string, curated []string, _ func(string) bool) []string {
		return append([]string{provider + "/live/provider-model"}, qualifyCompatibleModelList(provider, curated)...)
	}
	defer func() { discoverCompatModels = prevDiscover }()

	cfg := testConfig()
	cfg.Keys.OpenRouter = "openrouter-key"

	models := AvailableModels(cfg, &auth.Tokens{})

	if !containsTestString(models, "openrouter/live/provider-model") {
		t.Fatalf("expected live compat model when enabled: %#v", models)
	}
}

func TestAvailableModelsUsesLiveAnthropicModelsWhenAPIKeyPresent(t *testing.T) {
	prevDiscover := discoverAnthropicModels
	discoverAnthropicModels = func(_ string) []string {
		return []string{"claude-sonnet-4-6", "claude-haiku-4-5"}
	}
	defer func() { discoverAnthropicModels = prevDiscover }()

	cfg := testConfig()
	cfg.Keys.Anthropic = "anthropic-key"

	models := AvailableModels(cfg, &auth.Tokens{})

	if !containsTestString(models, "claude-sonnet-4-6") || !containsTestString(models, "anthropic/claude-sonnet-4-6") {
		t.Fatalf("expected live anthropic model in available models, got %#v", models)
	}
	if containsTestString(models, "anthropic/claude-opus-4-6") {
		t.Fatalf("unexpected fallback anthropic catalog entry when live discovery succeeded: %#v", models)
	}
}

func TestAvailableModelsUsesLiveClaudeModelsWhenLoginPresent(t *testing.T) {
	prevAvail := claudeAuthAvailable
	prevDiscover := discoverClaudeModels
	claudeAuthAvailable = func() bool { return true }
	discoverClaudeModels = func() []string {
		return []string{"claude-sonnet-4-6", "claude-haiku-4-5"}
	}
	defer func() {
		claudeAuthAvailable = prevAvail
		discoverClaudeModels = prevDiscover
	}()

	cfg := testConfig()
	models := AvailableModels(cfg, &auth.Tokens{})

	if !containsTestString(models, "claude-sonnet-4-6") || !containsTestString(models, "claude/claude-sonnet-4-6") {
		t.Fatalf("expected live claude model in available models, got %#v", models)
	}
	if containsTestString(models, "claude/claude-opus-4-6") {
		t.Fatalf("unexpected fallback claude catalog entry when live discovery succeeded: %#v", models)
	}
}

func TestModelDisplayLabelShowsResolvedProviderForUnqualifiedModel(t *testing.T) {
	cfg := testConfig()
	cfg.Keys.OpenAI = "openai-key"
	prevAvail := chatGPTAuthAvailable
	defer func() { chatGPTAuthAvailable = prevAvail }()
	chatGPTAuthAvailable = func() bool { return true }

	got := ModelDisplayLabel(cfg, &auth.Tokens{}, "gpt-5.4")
	if got != "gpt-5.4 [chatgpt]" {
		t.Fatalf("ModelDisplayLabel() = %q, want %q", got, "gpt-5.4 [chatgpt]")
	}
}

func TestModelDisplayLabelPreservesExplicitProvider(t *testing.T) {
	cfg := testConfig()
	cfg.Keys.OpenAI = "openai-key"

	got := ModelDisplayLabel(cfg, &auth.Tokens{}, "openai/gpt-5.4")
	if got != "gpt-5.4 [openai]" {
		t.Fatalf("ModelDisplayLabel() = %q, want %q", got, "gpt-5.4 [openai]")
	}
}

func TestDiscoverOpenAICompatibleModelsFiltersInvalidLiveIDs(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]string{
				{"id": "free"},
				{"id": "openai/gpt-4o"},
			},
		})
	}))
	defer srv.Close()

	models := DiscoverOpenAICompatibleModels(srv.URL, "token", "openrouter", []string{"openai/gpt-4o"}, func(m string) bool {
		return strings.Contains(m, "/")
	})

	if containsTestString(models, "openrouter/free") {
		t.Fatalf("unexpected invalid model in list: %#v", models)
	}
	if !containsTestString(models, "openrouter/openai/gpt-4o") {
		t.Fatalf("expected valid openrouter model in list: %#v", models)
	}
}

func containsTestString(list []string, want string) bool {
	for _, item := range list {
		if item == want {
			return true
		}
	}
	return false
}

func testConfig() *config.Config {
	cfg := &config.Config{}
	cfg.Models.Writer = "claude-3-7-sonnet-latest"
	cfg.Models.Auditor = "gpt-4o"
	cfg.Models.Summarizer = "claude-3-5-haiku-latest"
	cfg.Session.OutputDir = "."
	cfg.Retry.MaxAttempts = 3
	cfg.Retry.InitialWait = 1000
	cfg.Retry.MaxWait = 30000
	cfg.Retry.Timeout = 300
	cfg.Chat.CommandTimeout = 60
	cfg.Log.Level = "info"
	cfg.Models.WriterParams.Temperature = -1
	cfg.Models.AuditorParams.Temperature = -1
	return cfg
}

func writeCustomProviderTOML(t *testing.T, dir string) {
	t.Helper()
	providersDir := filepath.Join(dir, "forge", "providers")
	if err := os.MkdirAll(providersDir, 0o700); err != nil {
		t.Fatalf("mkdir providers: %v", err)
	}
	tomlContent := `
[model_providers.oca]
name = "Omni Cloud AI"
base_url = "https://api.omnicloud.test/v1"
wire_api = "chat"
models = ["gpt-5.4", "llama-4"]

[model_providers.localbox]
name = "Local Box"
base_url = "http://localhost:11434/v1"
wire_api = "chat"
models = ["phi-4", "qwen-3"]
`
	if err := os.WriteFile(filepath.Join(providersDir, "custom.toml"), []byte(tomlContent), 0o644); err != nil {
		t.Fatalf("write toml: %v", err)
	}
}

func TestCustomCompatProviderBuildCompatProvidersAppendsCustomProviders(t *testing.T) {
	dir := t.TempDir()
	writeCustomProviderTOML(t, dir)
	t.Setenv("XDG_CONFIG_HOME", dir)

	cfg := testConfig()
	tokens := &auth.Tokens{}
	providers := BuildCompatProviders(cfg, tokens)

	foundOCA := false
	foundLocalbox := false
	for _, p := range providers {
		if p.Name == "oca" {
			foundOCA = true
			if p.Label != "Omni Cloud AI" {
				t.Fatalf("oca label = %q, want %q", p.Label, "Omni Cloud AI")
			}
			if p.BaseURL != "https://api.omnicloud.test/v1" {
				t.Fatalf("oca base_url = %q", p.BaseURL)
			}
		}
		if p.Name == "localbox" {
			foundLocalbox = true
		}
	}
	if !foundOCA {
		t.Fatal("expected oca custom provider in BuildCompatProviders output")
	}
	if !foundLocalbox {
		t.Fatal("expected localbox custom provider in BuildCompatProviders output")
	}

	// Verify built-in providers are still present
	foundXAI := false
	for _, p := range providers {
		if p.Name == "xai" {
			foundXAI = true
		}
	}
	if !foundXAI {
		t.Fatal("expected built-in xai provider to still be present")
	}
}

func TestCustomCompatProviderSupportedProviderBackendsShowsCustomProvider(t *testing.T) {
	dir := t.TempDir()
	writeCustomProviderTOML(t, dir)
	t.Setenv("XDG_CONFIG_HOME", dir)

	cfg := testConfig()
	tokens := &auth.Tokens{}

	backends := SupportedProviderBackends(cfg, tokens)

	foundOCA := false
	for _, b := range backends {
		if b.ID == "oca" {
			foundOCA = true
			if b.Label != "Omni Cloud AI" {
				t.Fatalf("oca label = %q, want %q", b.Label, "Omni Cloud AI")
			}
			if b.Status != "configure API key" {
				t.Fatalf("oca status = %q, want 'configure API key'", b.Status)
			}
		}
	}
	if !foundOCA {
		t.Fatal("expected oca in SupportedProviderBackends")
	}
}

func TestBuildCompatProvidersIncludesZAICodingPlan(t *testing.T) {
	cfg := testConfig()
	cfg.Keys.ZAI = "zai-key"
	providers := BuildCompatProviders(cfg, &auth.Tokens{})

	found := false
	for _, p := range providers {
		if p.Name != "zai-coding-plan" {
			continue
		}
		found = true
		if p.Label != "Z.AI Coding Plan" {
			t.Fatalf("label = %q, want %q", p.Label, "Z.AI Coding Plan")
		}
		if p.BaseURL != "https://api.z.ai/api/coding/paas/v4" {
			t.Fatalf("base_url = %q", p.BaseURL)
		}
		if p.KeyFn() != "zai-key" {
			t.Fatalf("KeyFn() = %q, want %q", p.KeyFn(), "zai-key")
		}
		if !p.IsModel("glm-4.6") {
			t.Fatal("expected glm-4.6 to match zai-coding-plan")
		}
		if !containsTestString(p.Models, "glm-5.1") {
			t.Fatalf("expected glm-5.1 in coding plan models, got %#v", p.Models)
		}
	}
	if !found {
		t.Fatal("expected zai-coding-plan in BuildCompatProviders output")
	}
}

func TestSupportedProviderBackendsIncludesZAICodingPlan(t *testing.T) {
	cfg := testConfig()
	backends := SupportedProviderBackends(cfg, &auth.Tokens{})

	found := false
	for _, backend := range backends {
		if backend.ID != "zai-coding-plan" {
			continue
		}
		found = true
		if backend.Label != "Z.AI Coding Plan" {
			t.Fatalf("label = %q, want %q", backend.Label, "Z.AI Coding Plan")
		}
		if backend.DefaultModel != "zai-coding-plan/glm-5.1" {
			t.Fatalf("default model = %q, want %q", backend.DefaultModel, "zai-coding-plan/glm-5.1")
		}
	}
	if !found {
		t.Fatal("expected zai-coding-plan in SupportedProviderBackends")
	}
}

func TestCustomCompatProviderAvailableModelsExposesModelsOnlyWhenKeyExists(t *testing.T) {
	dir := t.TempDir()
	writeCustomProviderTOML(t, dir)
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("FORGE_ENABLE_LIVE_COMPAT_MODELS", "0")

	cfg := testConfig()

	// Without key, no models should appear
	tokensNoKey := &auth.Tokens{}
	models := AvailableModels(cfg, tokensNoKey)
	for _, m := range models {
		if strings.HasPrefix(m, "oca/") {
			t.Fatalf("unexpected oca model without key: %s", m)
		}
	}

	// With key via tokens, models should appear
	tokensWithKey := &auth.Tokens{}
	tokensWithKey.SetCustomProviderKey("oca", "test-key")
	models = AvailableModels(cfg, tokensWithKey)
	if !containsTestString(models, "oca/gpt-5.4") {
		t.Fatalf("expected oca/gpt-5.4 in available models, got %v", models)
	}
	if !containsTestString(models, "oca/llama-4") {
		t.Fatalf("expected oca/llama-4 in available models, got %v", models)
	}
}

func TestCustomCompatProviderAvailableModelsUsesCachedModelList(t *testing.T) {
	dir := t.TempDir()
	writeCustomProviderTOML(t, dir)
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("FORGE_ENABLE_LIVE_COMPAT_MODELS", "0")

	providersDir := filepath.Join(dir, "forge", "providers")
	if err := os.MkdirAll(providersDir, 0o700); err != nil {
		t.Fatal(err)
	}
	cache := `{
		"order": ["gpt-5.4-pro", "gpt-5.3-codex"],
		"models": {
			"gpt-5.4-pro": {"reasoning": false, "temperature": true, "tool_call": true, "limit": {"context": 922000, "output": 128000}},
			"gpt-5.3-codex": {"reasoning": false, "temperature": true, "tool_call": true, "limit": {"context": 272000, "output": 128000}}
		},
		"routes": {
			"gpt-5.4-pro": {"api_model": "openai/gpt-5.4-pro", "api_base": "https://api.omnicloud.test/v1"},
			"gpt-5.3-codex": {"api_model": "openai/gpt-5.3-codex", "api_base": "https://api.omnicloud.test/v1"}
		}
	}`
	if err := os.WriteFile(filepath.Join(providersDir, "oca-models.json"), []byte(cache), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg := testConfig()
	tokens := &auth.Tokens{}
	tokens.SetCustomProviderKey("oca", "oca-key")
	models := AvailableModels(cfg, tokens)

	if !containsTestString(models, "oca/gpt-5.4-pro") || !containsTestString(models, "oca/gpt-5.3-codex") {
		t.Fatalf("AvailableModels() = %#v", models)
	}
}

func TestCustomCompatProviderAvailableModelsExposesModelsViaEnvVar(t *testing.T) {
	dir := t.TempDir()
	writeCustomProviderTOML(t, dir)
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("FORGE_ENABLE_LIVE_COMPAT_MODELS", "0")
	t.Setenv("OCA_API_KEY", "env-key")

	cfg := testConfig()
	tokens := &auth.Tokens{}
	models := AvailableModels(cfg, tokens)
	if !containsTestString(models, "oca/gpt-5.4") {
		t.Fatalf("expected oca/gpt-5.4 via env var in available models, got %v", models)
	}
}

func TestCustomCompatProviderDriverForModelResolvesCustomProvider(t *testing.T) {
	dir := t.TempDir()
	writeCustomProviderTOML(t, dir)
	t.Setenv("XDG_CONFIG_HOME", dir)

	cfg := testConfig()
	tokens := &auth.Tokens{}
	tokens.SetCustomProviderKey("oca", "test-key")

	d := DriverForModel(cfg, tokens, "oca/gpt-5.4")
	if d == nil {
		t.Fatal("expected DriverForModel to resolve oca/gpt-5.4")
	}
	if got := d.Name(); got != "oca/gpt-5.4" {
		t.Fatalf("driver.Name() = %q, want %q", got, "oca/gpt-5.4")
	}
}

func TestCustomCompatProviderResolvedProviderIDShowsCustomProviderID(t *testing.T) {
	dir := t.TempDir()
	writeCustomProviderTOML(t, dir)
	t.Setenv("XDG_CONFIG_HOME", dir)

	cfg := testConfig()
	tokens := &auth.Tokens{}
	tokens.SetCustomProviderKey("oca", "test-key")

	got := ResolvedProviderID(cfg, tokens, "oca/gpt-5.4")
	if got != "oca" {
		t.Fatalf("ResolvedProviderID() = %q, want %q", got, "oca")
	}
}

func TestCustomCompatProviderModelDisplayLabelShowsCustomProviderID(t *testing.T) {
	dir := t.TempDir()
	writeCustomProviderTOML(t, dir)
	t.Setenv("XDG_CONFIG_HOME", dir)

	cfg := testConfig()
	tokens := &auth.Tokens{}
	tokens.SetCustomProviderKey("oca", "test-key")

	got := ModelDisplayLabel(cfg, tokens, "oca/gpt-5.4")
	if got != "gpt-5.4 [oca]" {
		t.Fatalf("ModelDisplayLabel() = %q, want %q", got, "gpt-5.4 [oca]")
	}
}

func TestCustomCompatProviderIsProviderNameRecognizesCustomProviders(t *testing.T) {
	// Register a custom provider name
	RegisterCustomProviderName("testcustom")

	if !isProviderName("testcustom") {
		t.Fatal("expected isProviderName to recognize registered custom provider")
	}

	ref := ParseModelRef("testcustom/some-model")
	if ref.Provider != "testcustom" {
		t.Fatalf("ParseModelRef(testcustom/some-model).Provider = %q, want %q", ref.Provider, "testcustom")
	}
	if ref.Model != "some-model" {
		t.Fatalf("ParseModelRef(testcustom/some-model).Model = %q, want %q", ref.Model, "some-model")
	}
}
