package copilot

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

const apiBase = "https://api.githubcopilot.com"
const userAgent = "forge"
const integrationID = "copilot-developer-cli"
const apiVersion = "2025-05-01"

// knownModelIDs covers the stable aliases users typically expect to see in
// Copilot UIs. The live /models endpoint often returns only a partial or
// version-pinned subset, so forge merges those live results with this catalog.
var knownModelIDs = []string{
	"gpt-4o",
	"gpt-4o-mini",
	"gpt-4.1",
	"gpt-4.1-mini",
	"gpt-5.4",
	"gpt-5.3-codex",
	"gpt-5.2",
	"gpt-5.1-codex-mini",
	"gpt-5-mini",
	"o1",
	"o1-mini",
	"o3",
	"o3-mini",
	"o4-mini",
	"claude-3.5-sonnet",
	"claude-3.7-sonnet",
	"claude-sonnet-4",
	"claude-sonnet-4.5",
	"claude-opus-4.1",
	"gemini-2.0-flash-001",
	"gemini-2.5-pro",
}

type modelEntry struct {
	ID string `json:"id"`
}

type modelsResponse struct {
	Data []modelEntry `json:"data"`
}

// KnownModels returns forge's curated Copilot model catalog with the "copilot/"
// prefix applied for use in the registry and TUI.
func KnownModels() []string {
	out := make([]string, 0, len(knownModelIDs))
	for _, id := range knownModelIDs {
		out = append(out, prefixModel(id))
	}
	return out
}

// FetchModels calls the Copilot /models endpoint and returns the IDs prefixed
// with "copilot/" for use in forge's model registry.
func FetchModels(ctx context.Context, token string) ([]string, error) {
	return fetchModels(ctx, http.DefaultClient, apiBase, token)
}

// DiscoverModels returns the models available to the authenticated user from
// the live Copilot /models endpoint. The curated catalog is used only when the
// API call fails or returns an empty list.
func DiscoverModels(ctx context.Context, token string) ([]string, error) {
	live, err := FetchModels(ctx, token)
	if err == nil && len(live) > 0 {
		return live, nil
	}
	return KnownModels(), err
}

func fetchModels(ctx context.Context, client *http.Client, baseURL, token string) ([]string, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", baseURL+"/models", nil)
	if err != nil {
		return nil, err
	}
	applyHeaders(req, token)

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetching Copilot models: %s", resp.Status)
	}

	var mr modelsResponse
	if err := json.NewDecoder(resp.Body).Decode(&mr); err != nil {
		return nil, err
	}

	out := make([]string, 0, len(mr.Data))
	seen := make(map[string]struct{}, len(mr.Data))
	for _, m := range mr.Data {
		name := prefixModel(m.ID)
		if name == "" {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		out = append(out, name)
	}
	return out, nil
}

func prefixModel(id string) string {
	id = strings.TrimSpace(id)
	if id == "" {
		return ""
	}
	if strings.HasPrefix(id, "copilot/") {
		return id
	}
	return "copilot/" + id
}
