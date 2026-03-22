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

var curatedOpenAIModels = []string{
	"gpt-5",
	"gpt-5-mini",
	"gpt-4o",
	"gpt-4o-mini",
	"gpt-4-turbo",
	"o1",
	"o1-mini",
	"o3-mini",
}

type openAIModelsResponse struct {
	Data []openAIModelEntry `json:"data"`
}

type openAIModelEntry struct {
	ID string `json:"id"`
}

func OpenAIModels() []string {
	return append([]string(nil), curatedOpenAIModels...)
}

func DiscoverOpenAIModels(apiKey string) []string {
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		return OpenAIModels()
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	live, err := fetchOpenAIModels(ctx, http.DefaultClient, openAIModelsBaseURL, apiKey)
	if err != nil {
		return OpenAIModels()
	}
	return mergeOpenAIModelLists(live, curatedOpenAIModels)
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
