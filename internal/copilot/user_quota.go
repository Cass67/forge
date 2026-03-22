package copilot

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"forge/internal/llm"
)

const githubAPIBase = "https://api.github.com"

// UserQuota contains live Copilot allowance snapshots from the GitHub internal
// Copilot user endpoint.
type UserQuota struct {
	ResetAt string
	Windows map[string]llm.CopilotQuota
}

type userQuotaPayload struct {
	QuotaSnapshots map[string]userQuotaSnapshot `json:"quota_snapshots"`
	QuotaResetDate string                       `json:"quota_reset_date"`
}

type userQuotaSnapshot struct {
	Entitlement any `json:"entitlement"`
	Remaining   any `json:"remaining"`
}

// FetchUserQuota returns live Copilot quota information for the authenticated user.
func FetchUserQuota(ctx context.Context, token string) (*UserQuota, error) {
	return fetchUserQuota(ctx, http.DefaultClient, githubAPIBase, token)
}

func fetchUserQuota(ctx context.Context, client *http.Client, baseURL, token string) (*UserQuota, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/copilot_internal/user", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "token "+token)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Editor-Version", "vscode/1.96.2")
	req.Header.Set("X-Github-Api-Version", "2025-04-01")

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetching Copilot user quota: %s", resp.Status)
	}

	var payload userQuotaPayload
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, err
	}
	return normalizeUserQuota(payload), nil
}

func normalizeUserQuota(payload userQuotaPayload) *UserQuota {
	out := &UserQuota{ResetAt: payload.QuotaResetDate, Windows: map[string]llm.CopilotQuota{}}
	add := func(name, typ string, snap userQuotaSnapshot) {
		entitlement, okEnt := toFloat64(snap.Entitlement)
		remaining, okRem := toFloat64(snap.Remaining)
		if !okEnt && !okRem {
			return
		}
		included := int(entitlement)
		rem := int(remaining)
		used := 0
		if okEnt && okRem {
			used = included - rem
			if used < 0 {
				used = 0
			}
		}
		percent := 0.0
		if included > 0 {
			percent = (float64(rem) / float64(included)) * 100
		}
		out.Windows[name] = llm.CopilotQuota{
			Type:             typ,
			Included:         included,
			Used:             used,
			Remaining:        rem,
			PercentRemaining: percent,
			ResetAt:          normalizeReset(payload.QuotaResetDate),
		}
	}
	add("chat", "chat", payload.QuotaSnapshots["chat"])
	add("completions", "completions", payload.QuotaSnapshots["completions"])
	add("premium", "premium_interactions", payload.QuotaSnapshots["premium_interactions"])
	return out
}

func toFloat64(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case int32:
		return float64(n), true
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	default:
		return 0, false
	}
}

func normalizeReset(v string) string {
	if v == "" {
		return ""
	}
	if t, err := time.Parse(time.RFC3339, v); err == nil {
		return t.Format(time.RFC3339)
	}
	return v
}
