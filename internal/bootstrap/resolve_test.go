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

	p, ambiguous := ResolveCompatProvider(BuildCompatProviders(cfg), "meta-llama/Llama-3.3-70B-Instruct-Turbo")
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
	cfg.Chat.MaxTurns = 50
	cfg.Chat.CommandTimeout = 60
	cfg.Log.Level = "info"
	cfg.Models.WriterParams.Temperature = -1
	cfg.Models.AuditorParams.Temperature = -1
	return cfg
}
