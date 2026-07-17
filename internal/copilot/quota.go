package copilot

import (
	"encoding/json"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"forge/internal/llm"
)

const quotaHeaderPrefix = "x-ratelimit-remaining-"

func applyHeaders(req *http.Request, token string) {
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(token))
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Editor-Version", userAgent)
	req.Header.Set("Copilot-Integration-Id", integrationID)
	req.Header.Set("OpenAI-Intent", "conversation-agent")
	req.Header.Set("X-Initiator", "user")
	req.Header.Set("X-GitHub-Api-Version", apiVersion)
}

// ExtractQuotaJSON parses Copilot quota information from JSON response bodies/events.
func ExtractQuotaJSON(raw string) *llm.CopilotQuota {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	var payload any
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return nil
	}
	objs := make([]map[string]any, 0)
	collectObjects(payload, &objs)
	for _, obj := range objs {
		if quota := quotaFromObject(obj); quota != nil {
			return quota
		}
	}
	return nil
}

// ExtractQuotaHeaders parses Copilot quota snapshots from response headers.
// Copilot CLI reads quota from headers prefixed with x-ratelimit-remaining-*
// and then exposes them as quotaSnapshots on model call success events.
func ExtractQuotaHeaders(h http.Header) *llm.CopilotQuota {
	if len(h) == 0 {
		return nil
	}
	types := make([]string, 0)
	seen := map[string]bool{}
	for k := range h {
		lk := strings.ToLower(k)
		if !strings.HasPrefix(lk, quotaHeaderPrefix) {
			continue
		}
		typeName := strings.TrimPrefix(lk, quotaHeaderPrefix)
		if strings.HasPrefix(typeName, "percentage-") {
			typeName = strings.TrimPrefix(typeName, "percentage-")
		}
		if typeName == "" || seen[typeName] {
			continue
		}
		seen[typeName] = true
		types = append(types, typeName)
	}
	if len(types) == 0 {
		return nil
	}
	sort.Strings(types)
	for _, typeName := range types {
		q := &llm.CopilotQuota{
			Type:             typeName,
			Remaining:        headerInt(h, "X-RateLimit-Remaining-"+typeName),
			Included:         headerInt(h, "X-RateLimit-Limit-"+typeName),
			Used:             headerInt(h, "X-RateLimit-Used-"+typeName),
			PercentRemaining: headerFloat(h, "X-RateLimit-Remaining-Percentage-"+typeName),
			ResetAt:          headerString(h, "X-RateLimit-Reset-"+typeName),
		}
		if q.PercentRemaining == 0 && q.Included > 0 && q.Remaining >= 0 {
			q.PercentRemaining = float64(q.Remaining) * 100 / float64(q.Included)
		}
		if q.Included == 0 && q.Used == 0 && q.Remaining == 0 && q.PercentRemaining == 0 && q.ResetAt == "" {
			continue
		}
		return q
	}
	return nil
}

func collectObjects(v any, out *[]map[string]any) {
	switch x := v.(type) {
	case map[string]any:
		*out = append(*out, x)
		for _, child := range x {
			collectObjects(child, out)
		}
	case []any:
		for _, child := range x {
			collectObjects(child, out)
		}
	}
}

func quotaFromObject(obj map[string]any) *llm.CopilotQuota {
	if snapshots, ok := obj["quotaSnapshots"].(map[string]any); ok {
		for name, raw := range snapshots {
			child, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			q := quotaFromSnapshot(name, child)
			if q != nil {
				return q
			}
		}
	}
	keys := []string{"remaining", "included", "used", "percent_remaining", "unlimited", "usedRequests", "includedRequests", "remainingPercentage", "resetDate"}
	match := false
	for _, key := range keys {
		if _, ok := obj[key]; ok {
			match = true
			break
		}
	}
	if !match {
		return nil
	}
	q := &llm.CopilotQuota{
		Type:             firstString(obj, "type", "name", "kind", "metric"),
		Included:         firstInt(obj, "included", "included_count", "quota", "limit", "includedRequests"),
		Used:             firstInt(obj, "used", "used_count", "consumed", "usedRequests"),
		Remaining:        firstInt(obj, "remaining", "remaining_count", "available"),
		PercentRemaining: firstFloat(obj, "percent_remaining", "remaining_percent", "percentage_remaining", "remainingPercentage"),
		Unlimited:        firstBool(obj, "unlimited", "overageAllowedWithExhaustedQuota"),
		ResetAt:          firstString(obj, "reset_at", "resets_at", "resetAt", "resetDate"),
	}
	if q.Remaining == 0 && q.Included > 0 && q.Used > 0 {
		q.Remaining = q.Included - q.Used
		if q.Remaining < 0 {
			q.Remaining = 0
		}
	}
	if q.PercentRemaining == 0 && q.Included > 0 && q.Remaining >= 0 {
		q.PercentRemaining = float64(q.Remaining) * 100 / float64(q.Included)
	}
	if q.Type == "" {
		q.Type = "premium_interactions"
	}
	if q.Included == 0 && q.Used == 0 && q.Remaining == 0 && q.PercentRemaining == 0 && !q.Unlimited && q.ResetAt == "" {
		return nil
	}
	return q
}

func quotaFromSnapshot(name string, obj map[string]any) *llm.CopilotQuota {
	q := &llm.CopilotQuota{
		Type:             name,
		Included:         firstInt(obj, "includedRequests"),
		Used:             firstInt(obj, "usedRequests"),
		PercentRemaining: firstFloat(obj, "remainingPercentage"),
		Unlimited:        firstBool(obj, "overageAllowedWithExhaustedQuota"),
		ResetAt:          firstString(obj, "resetDate"),
	}
	if q.Included > 0 {
		q.Remaining = q.Included - q.Used
		if q.Remaining < 0 {
			q.Remaining = 0
		}
	}
	if q.PercentRemaining == 0 && q.Included > 0 {
		q.PercentRemaining = float64(q.Remaining) * 100 / float64(q.Included)
	}
	if q.Included == 0 && q.Used == 0 && q.Remaining == 0 && q.PercentRemaining == 0 && !q.Unlimited && q.ResetAt == "" {
		return nil
	}
	return q
}

func firstString(obj map[string]any, keys ...string) string {
	for _, k := range keys {
		if v, ok := obj[k]; ok {
			if s, ok := v.(string); ok {
				return s
			}
		}
	}
	return ""
}

func firstInt(obj map[string]any, keys ...string) int {
	for _, k := range keys {
		if v, ok := obj[k]; ok {
			switch n := v.(type) {
			case float64:
				return int(n)
			case int:
				return n
			case int64:
				return int(n)
			case string:
				i, err := strconv.Atoi(strings.TrimSpace(n))
				if err == nil {
					return i
				}
			}
		}
	}
	return 0
}

func firstFloat(obj map[string]any, keys ...string) float64 {
	for _, k := range keys {
		if v, ok := obj[k]; ok {
			switch n := v.(type) {
			case float64:
				return n
			case int:
				return float64(n)
			case int64:
				return float64(n)
			case string:
				f, err := strconv.ParseFloat(strings.TrimSpace(n), 64)
				if err == nil {
					return f
				}
			}
		}
	}
	return 0
}

func firstBool(obj map[string]any, keys ...string) bool {
	for _, k := range keys {
		if v, ok := obj[k]; ok {
			switch b := v.(type) {
			case bool:
				return b
			case string:
				parsed, err := strconv.ParseBool(strings.TrimSpace(b))
				if err == nil {
					return parsed
				}
			}
		}
	}
	return false
}

func headerString(h http.Header, key string) string {
	return strings.TrimSpace(h.Get(key))
}

func headerInt(h http.Header, key string) int {
	v := headerString(h, key)
	if v == "" {
		return 0
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0
	}
	return n
}

func headerFloat(h http.Header, key string) float64 {
	v := headerString(h, key)
	if v == "" {
		return 0
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return 0
	}
	return f
}
