package bootstrap

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
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
	return append([]string(nil), hardcodedChatGPTModels...)
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

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	live, err := fetchCompatibleModels(ctx, http.DefaultClient, baseURL, apiKey, provider, accept)
	if err != nil || len(live) == 0 {
		return qualifyCompatibleModelList(provider, curated)
	}
	// Live fetch succeeded: use the provider's own model list exclusively.
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

func mergeOpenAIModelLists(groups ...[]string) []string {
	var out []string
	seen := map[string]struct{}{}
	for _, group := range groups {
		for _, model := range group {
			model = canonicalOpenAIModel(model)
			if model == "" {
				continue
			}
			if _, ok := seen[model]; ok {
				continue
			}
			seen[model] = struct{}{}
			out = append(out, model)
		}
	}
	return out
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
