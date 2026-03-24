package bootstrap

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
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
	return preferredClaudeModels(live)
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

func preferredClaudeModels(models []string) []string {
	type candidate struct {
		name    string
		family  string
		version []int
		dated   bool
	}

	parse := func(name string) (candidate, bool) {
		parts := strings.Split(strings.TrimSpace(name), "-")
		if len(parts) < 4 || parts[0] != "claude" {
			return candidate{}, false
		}
		family := parts[1]
		switch family {
		case "opus", "sonnet", "haiku":
		default:
			return candidate{}, false
		}
		versionParts := parts[2:]
		dated := false
		if len(versionParts) > 1 {
			last := versionParts[len(versionParts)-1]
			if len(last) == 8 {
				if _, err := strconv.Atoi(last); err == nil {
					dated = true
					versionParts = versionParts[:len(versionParts)-1]
				}
			}
		}
		version := make([]int, 0, len(versionParts))
		for _, part := range versionParts {
			n, err := strconv.Atoi(part)
			if err != nil {
				return candidate{}, false
			}
			version = append(version, n)
		}
		return candidate{name: strings.TrimSpace(name), family: family, version: version, dated: dated}, true
	}

	compareVersion := func(a, b []int) int {
		n := len(a)
		if len(b) > n {
			n = len(b)
		}
		for i := 0; i < n; i++ {
			av := 0
			if i < len(a) {
				av = a[i]
			}
			bv := 0
			if i < len(b) {
				bv = b[i]
			}
			switch {
			case av > bv:
				return 1
			case av < bv:
				return -1
			}
		}
		return 0
	}

	bestByFamily := map[string]candidate{}
	for _, raw := range uniqueStrings(models) {
		name := strings.TrimSpace(raw)
		cand, ok := parse(name)
		if !ok {
			continue
		}
		best, exists := bestByFamily[cand.family]
		if !exists {
			bestByFamily[cand.family] = cand
			continue
		}
		switch cmp := compareVersion(cand.version, best.version); {
		case cmp > 0:
			bestByFamily[cand.family] = cand
		case cmp == 0 && best.dated && !cand.dated:
			bestByFamily[cand.family] = cand
		}
	}

	order := map[string]int{"opus": 0, "sonnet": 1, "haiku": 2}
	families := make([]string, 0, len(bestByFamily))
	for family := range bestByFamily {
		families = append(families, family)
	}
	sort.Slice(families, func(i, j int) bool {
		oi, iok := order[families[i]]
		oj, jok := order[families[j]]
		if iok && jok {
			return oi < oj
		}
		if iok != jok {
			return iok
		}
		return families[i] < families[j]
	})

	out := make([]string, 0, len(families))
	for _, family := range families {
		out = append(out, bestByFamily[family].name)
	}
	if len(out) == 0 {
		return AnthropicModels()
	}
	return out
}
