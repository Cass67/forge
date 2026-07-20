package modelcatalog

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestLookupFallsBackToBundledLimitsWhenLiveCatalogOmitsThem(t *testing.T) {
	origCatalog := catalog
	origBundled := bundledCatalog
	t.Cleanup(func() {
		catalog = origCatalog
		bundledCatalog = origBundled
	})

	bundledCatalog = map[string]providerData{
		"openrouter": {
			Models: map[string]modelEntry{
				"arcee-ai/trinity-large-preview:free": {
					Temperature: true,
					ToolCall:    true,
					Limit: struct {
						Context int `json:"context"`
						Output  int `json:"output"`
					}{Context: 131072, Output: 32768},
				},
			},
		},
	}
	catalog = map[string]providerData{
		"openrouter": {
			Models: map[string]modelEntry{
				"arcee-ai/trinity-large-preview:free": {
					Temperature: true,
					ToolCall:    true,
				},
			},
		},
	}

	info := Lookup("openrouter", "arcee-ai/trinity-large-preview:free")
	if info == nil {
		t.Fatal("Lookup returned nil")
	}
	if info.ContextWindow != 131072 {
		t.Fatalf("ContextWindow = %d, want %d", info.ContextWindow, 131072)
	}
	if info.OutputLimit != 32768 {
		t.Fatalf("OutputLimit = %d, want %d", info.OutputLimit, 32768)
	}
	if !info.Temperature || !info.ToolCall {
		t.Fatalf("capability flags lost: %+v", info)
	}
}

func TestMergeModelInfoPrefersLiveLimitsWhenPresent(t *testing.T) {
	info := mergeModelInfo(
		&ModelInfo{Temperature: true, ContextWindow: 8192, OutputLimit: 4096},
		&ModelInfo{Reasoning: true, ToolCall: true, ContextWindow: 131072, OutputLimit: 32768},
	)
	if info == nil {
		t.Fatal("mergeModelInfo returned nil")
	}
	if info.ContextWindow != 8192 {
		t.Fatalf("ContextWindow = %d, want %d", info.ContextWindow, 8192)
	}
	if info.OutputLimit != 4096 {
		t.Fatalf("OutputLimit = %d, want %d", info.OutputLimit, 4096)
	}
	if !info.Reasoning || !info.Temperature || !info.ToolCall {
		t.Fatalf("expected merged capability flags, got %+v", info)
	}
}

func TestLookupLoadsCustomProviderMetadataFromConfiguredSource(t *testing.T) {
	origCatalog := catalog
	origBundled := bundledCatalog
	origSources := customSources
	t.Cleanup(func() {
		catalog = origCatalog
		bundledCatalog = origBundled
		customSources = origSources
	})

	configHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configHome)
	bundledCatalog = map[string]providerData{}
	catalog = map[string]providerData{}
	customSources = map[string]customSource{}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Fatalf("Authorization = %q, want Bearer test-key", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"model_name":"gpt-5.4-pro","model_info":{"supports_reasoning":true,"supports_function_calling":true,"supports_temperature":false,"max_input_tokens":200000,"max_output_tokens":50000}}]}`))
	}))
	defer srv.Close()

	RegisterCustomProviderSource("oca", srv.URL, nil, func() string { return "test-key" })

	info := Lookup("oca", "gpt-5.4-pro")
	if info == nil {
		t.Fatal("Lookup returned nil")
	}
	if !info.Reasoning || !info.ToolCall {
		t.Fatalf("expected reasoning and tool call metadata, got %+v", info)
	}
	if info.Temperature {
		t.Fatalf("Temperature = %v, want false", info.Temperature)
	}
	if info.ContextWindow != 200000 || info.OutputLimit != 50000 {
		t.Fatalf("unexpected limits: %+v", info)
	}

	cachePath := filepath.Join(configHome, "forge", "providers", "oca-models.json")
	if _, err := os.Stat(cachePath); err != nil {
		t.Fatalf("expected custom provider cache at %q: %v", cachePath, err)
	}
}

func TestCustomProviderRouteAndProviderModelsUseCachedMetadata(t *testing.T) {
	origSources := customSources
	t.Cleanup(func() {
		customSources = origSources
	})
	customSources = map[string]customSource{}

	configHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configHome)
	cacheDir := filepath.Join(configHome, "forge", "providers")
	if err := os.MkdirAll(cacheDir, 0o700); err != nil {
		t.Fatal(err)
	}
	cachePath := filepath.Join(cacheDir, "oca-models.json")
	content := `{
		"order": ["gpt-5.4-pro", "gpt-5.3-codex"],
		"models": {
			"gpt-5.4-pro": {"reasoning": false, "temperature": true, "tool_call": true, "limit": {"context": 922000, "output": 128000}},
			"gpt-5.3-codex": {"reasoning": false, "temperature": true, "tool_call": true, "limit": {"context": 272000, "output": 128000}}
		},
		"routes": {
			"gpt-5.4-pro": {"api_model": "openai/gpt-5.4-pro", "api_base": "https://example.com/v1"},
			"gpt-5.3-codex": {"api_model": "openai/gpt-5.3-codex", "api_base": "https://example.com/v1"}
		}
	}`
	if err := os.WriteFile(cachePath, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	models := ProviderModels("oca")
	if len(models) != 2 || models[0] != "gpt-5.4-pro" || models[1] != "gpt-5.3-codex" {
		t.Fatalf("ProviderModels() = %#v", models)
	}

	route := CustomProviderRouteForModel("oca", "gpt-5.4-pro")
	if route == nil {
		t.Fatal("CustomProviderRoute returned nil")
	}
	if route.APIModel != "openai/gpt-5.4-pro" {
		t.Fatalf("APIModel = %q", route.APIModel)
	}
	if route.APIBase != "https://example.com/v1" {
		t.Fatalf("APIBase = %q", route.APIBase)
	}
}

func TestCustomProviderLegacyCacheRefreshesWhenRoutesAreMissing(t *testing.T) {
	origSources := customSources
	t.Cleanup(func() {
		customSources = origSources
	})
	customSources = map[string]customSource{}

	configHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configHome)
	cacheDir := filepath.Join(configHome, "forge", "providers")
	if err := os.MkdirAll(cacheDir, 0o700); err != nil {
		t.Fatal(err)
	}
	cachePath := filepath.Join(cacheDir, "oca-models.json")
	legacy := `{
		"models": {
			"Grok 3": {"reasoning": false, "temperature": true, "tool_call": false, "limit": {"context": 131072, "output": 16000}}
		}
	}`
	if err := os.WriteFile(cachePath, []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Fatalf("Authorization = %q, want Bearer test-key", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"model_name":"Grok 3","litellm_params":{"model":"oca/grok3","max_tokens":131072},"model_info":{"supported_api_list":["CHAT_COMPLETIONS"],"context_window":131072,"max_output_tokens":16000,"is_reasoning_model":false}}]}`))
	}))
	defer srv.Close()

	RegisterCustomProviderSource("oca", srv.URL, nil, func() string { return "test-key" })

	models := CustomProviderModels("oca")
	if len(models) != 1 || models[0] != "Grok 3" {
		t.Fatalf("CustomProviderModels() = %#v", models)
	}

	route := CustomProviderRouteForModel("oca", "Grok 3")
	if route == nil {
		t.Fatal("CustomProviderRoute returned nil")
	}
	if route.APIModel != "oca/grok3" {
		t.Fatalf("APIModel = %q", route.APIModel)
	}
	if route.APIBase != "" {
		t.Fatalf("APIBase = %q", route.APIBase)
	}
	if route.WireAPI != "chat" {
		t.Fatalf("WireAPI = %q", route.WireAPI)
	}

	aliasRoute := CustomProviderRouteForModel("oca", "grok3")
	if aliasRoute == nil {
		t.Fatal("CustomProviderRoute alias returned nil")
	}
	if aliasRoute.APIModel != "oca/grok3" {
		t.Fatalf("alias APIModel = %q", aliasRoute.APIModel)
	}
}

func TestGPT55HasImageSupport(t *testing.T) {
	// GPT-5.5 via openai should have image support
	info := Lookup("openai", "gpt-5.5")
	if info != nil && !info.SupportsImages {
		t.Error("gpt-5.5 should support images via openai provider")
	}
}

func TestNonVisionModelLacksImageSupport(t *testing.T) {
	info := Lookup("openai", "gpt-4o-mini")
	if info != nil && info.SupportsImages {
		t.Error("gpt-4o-mini should not have image support in hardcoded list")
	}
}

func TestImageCapabilityMetadata(t *testing.T) {
	info := Lookup("openai", "gpt-5.5")
	if info == nil {
		t.Skip("gpt-5.5 not in catalog")
	}
	if info.MaxImageBytes <= 0 {
		t.Errorf("MaxImageBytes = %d, want positive", info.MaxImageBytes)
	}
	if len(info.SupportedImageMIMEs) == 0 {
		t.Error("SupportedImageMIMEs should not be empty")
	}
}

func TestAllowsImageParts(t *testing.T) {
	if !AllowsImageParts("openai", "gpt-4o-mini") {
		t.Error("non-gated providers should always allow image parts")
	}
	RegisterCustomProviderImageModels("testgated", []string{"visionmodel"})
	if !AllowsImageParts("testgated", "visionmodel") {
		t.Error("declared image model should allow image parts")
	}
	if AllowsImageParts("testgated", "textmodel") {
		t.Error("undeclared model on gated provider should not allow image parts")
	}
	RegisterCustomProviderImageModels("testgated2", nil)
	if AllowsImageParts("testgated2", "anymodel") {
		t.Error("gated provider with no image_models should not allow image parts")
	}
}
