package bootstrap

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"forge/internal/modelcatalog"
)

const openAIModelsBaseURL = "https://api.openai.com/v1"

// hardcodedOpenAIModels is the fallback when the catalog has no data.
var hardcodedOpenAIModels = []string{
	"gpt-5",
	"gpt-5-mini",
	"gpt-4o",
	"gpt-4o-mini",
	"gpt-4-turbo",
	"o1",
	"o1-mini",
	"o3-mini",
}

// hardcodedChatGPTModels is the fallback when the catalog has no data.
var hardcodedChatGPTModels = []string{
	"gpt-5.6",
	"gpt-5.6-sol",
	"gpt-5.6-terra",
	"gpt-5.6-luna",
	"gpt-5.5-pro",
	"gpt-5.4",
	"gpt-5.4-mini",
	"gpt-5.5",
	"gpt-5.3-codex",
	"gpt-5.2",
	"gpt-5.2-codex",
	"gpt-5.1-codex",
	"gpt-5.1-codex-max",
	"gpt-5.1-codex-mini",
}

type openAIModelsResponse struct {
	Data []openAIModelEntry `json:"data"`
}

type openAIModelEntry struct {
	ID string `json:"id"`
}

func OpenAIModels() []string {
	return append([]string(nil), hardcodedOpenAIModels...)
}

func ChatGPTModels() []string {
	out := append([]string(nil), hardcodedChatGPTModels...)
	seen := make(map[string]struct{}, len(out))
	for _, m := range out {
		seen[m] = struct{}{}
	}
	catalog := modelcatalog.ProviderModels("chatgpt")
	sort.Strings(catalog)
	for _, m := range catalog {
		// ponytail: ChatGPT/Codex backend only serves the gpt-5 family
		if !strings.HasPrefix(m, "gpt-5") {
			continue
		}
		if _, ok := seen[m]; ok {
			continue
		}
		seen[m] = struct{}{}
		out = append(out, m)
	}
	return out
}

func DiscoverOpenAIModels(apiKey string) []string {
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		return OpenAIModels()
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	live, err := fetchOpenAIModels(ctx, http.DefaultClient, openAIModelsBaseURL, apiKey)
	if err != nil || len(live) == 0 {
		return OpenAIModels()
	}
	// Live fetch succeeded: trust the provider's own model list exclusively.
	// The /models endpoint only returns currently available models, so stale
	// or removed models never appear. Fall back to curated only on failure.
	return live
}

func DiscoverOpenAICompatibleModels(baseURL, apiKey, provider string, curated []string, accept func(string) bool) []string {
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		return qualifyCompatibleModelList(provider, curated)
	}

	fetch := func() []string {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		live, err := fetchCompatibleModels(ctx, http.DefaultClient, baseURL, apiKey, provider, accept)
		if err != nil {
			return nil
		}
		return live
	}

	if cached, ok := loadLiveModelCache(provider); ok {
		refreshLiveModelsAsync(provider, fetch)
		return cached
	}

	// Nothing cached yet. A curated list is good enough to open the session
	// with, so the fetch runs in the background and the next launch has the
	// provider's own list. Without a curated list there is nothing to show,
	// so that one still waits.
	if fallback := qualifyCompatibleModelList(provider, curated); len(fallback) > 0 {
		refreshLiveModelsAsync(provider, fetch)
		return fallback
	}

	live := fetch()
	if len(live) == 0 {
		return nil
	}
	// Live fetch succeeded: use the provider's own model list exclusively.
	writeLiveModelCache(provider, live)
	refreshedLiveModels.Store(provider, true)
	return live
}

func fetchOpenAIModels(ctx context.Context, client *http.Client, baseURL, apiKey string) ([]string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(baseURL, "/")+"/models", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(apiKey))
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetching OpenAI models: %s", resp.Status)
	}

	var payload openAIModelsResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, err
	}

	out := make([]string, 0, len(payload.Data))
	seen := make(map[string]struct{}, len(payload.Data))
	for _, model := range payload.Data {
		id := canonicalOpenAIModel(model.ID)
		if id == "" || !IsOpenAIModel(id) {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out, nil
}

func applyProviderDiscoveryHeaders(req *http.Request, provider string) {
	if req == nil {
		return
	}
	switch strings.TrimSpace(strings.ToLower(provider)) {
	case "openrouter":
		req.Header.Set("HTTP-Referer", "https://github.com/cass/forge")
		req.Header.Set("X-Title", "forge")
		req.Header.Set("X-OpenRouter-Title", "forge")
	}
}

func fetchCompatibleModels(ctx context.Context, client *http.Client, baseURL, apiKey, provider string, accept func(string) bool) ([]string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(baseURL, "/")+"/models", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(apiKey))
	req.Header.Set("Accept", "application/json")
	applyProviderDiscoveryHeaders(req, provider)

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetching %s models: %s", provider, resp.Status)
	}

	var payload openAIModelsResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, err
	}

	out := make([]string, 0, len(payload.Data))
	seen := make(map[string]struct{}, len(payload.Data))
	for _, model := range payload.Data {
		id := strings.TrimSpace(model.ID)
		if id == "" {
			continue
		}
		if accept != nil && !accept(id) {
			continue
		}
		name := explicitBackendModel(provider, id)
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		out = append(out, name)
	}
	return out, nil
}

func qualifyCompatibleModelList(provider string, models []string) []string {
	out := make([]string, 0, len(models))
	for _, model := range models {
		model = strings.TrimSpace(model)
		if model == "" {
			continue
		}
		out = append(out, explicitBackendModel(provider, model))
	}
	return out
}
