package bootstrap

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"forge/internal/claudeauth"
)

const (
	anthropicModelsBaseURL   = "https://api.anthropic.com/v1"
	anthropicAPIVersion      = "2023-06-01"
	claudeOAuthDiscoveryBeta = "oauth-2025-04-20"
)

type anthropicModelsResponse struct {
	Data []anthropicModelEntry `json:"data"`
}

type anthropicModelEntry struct {
	ID string `json:"id"`
}

func DiscoverAnthropicModels(apiKey string) []string {
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		return AnthropicModels()
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	live, err := fetchAnthropicModels(ctx, http.DefaultClient, apiKey, "", false)
	if err != nil || len(live) == 0 {
		return AnthropicModels()
	}
	return live
}

func DiscoverClaudeModels() []string {
	mgr, err := claudeauth.NewManager()
	if err != nil {
		return AnthropicModels()
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	token, err := mgr.Authorization(ctx)
	if err != nil {
		return AnthropicModels()
	}

	live, err := fetchAnthropicModels(ctx, http.DefaultClient, "", token, true)
	if err != nil || len(live) == 0 {
		return AnthropicModels()
	}
	return live
}

func fetchAnthropicModels(ctx context.Context, client *http.Client, apiKey, bearer string, oauth bool) ([]string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, anthropicModelsBaseURL+"/models", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("anthropic-version", anthropicAPIVersion)
	if oauth {
		req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(bearer))
		req.Header.Set("anthropic-beta", claudeOAuthDiscoveryBeta)
	} else {
		req.Header.Set("x-api-key", strings.TrimSpace(apiKey))
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetching Anthropic models: %s", resp.Status)
	}

	var payload anthropicModelsResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, err
	}

	out := make([]string, 0, len(payload.Data))
	seen := make(map[string]struct{}, len(payload.Data))
	for _, model := range payload.Data {
		id := canonicalAnthropicModel(model.ID)
		if id == "" || !IsAnthropicModel(id) {
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
