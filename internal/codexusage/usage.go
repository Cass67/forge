package codexusage

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"forge/internal/auth"
)

const (
	usageEndpoint      = "https://chatgpt.com/backend-api/codex/usage"
	refreshEndpoint    = "https://auth.openai.com/oauth/token"
	refreshClientID    = "app_EMoamEEZ73f0CkXaXp7hrann"
	defaultHTTPTimeout = 10 * time.Second
)

type Window struct {
	UsedPercent float64 `json:"used_percent,omitempty"`
	ResetAt     string  `json:"reset_at,omitempty"`
	ResetIn     string  `json:"reset_in,omitempty"`
}

type Snapshot struct {
	Plan       string  `json:"plan,omitempty"`
	Primary    *Window `json:"primary_window,omitempty"`
	Secondary  *Window `json:"secondary_window,omitempty"`
	CodeReview *Window `json:"code_review_rate_limit,omitempty"`
	Source     string  `json:"source,omitempty"`
}

type authFile struct {
	Tokens struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		IDToken      string `json:"id_token"`
		AccountID    string `json:"account_id"`
	} `json:"tokens"`
	LastRefresh string `json:"last_refresh"`
}

type refreshResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	IDToken      string `json:"id_token"`
}

func FetchUsage(ctx context.Context) (*Snapshot, error) {
	client := &http.Client{Timeout: defaultHTTPTimeout}
	if tokens, err := auth.Load(); err == nil {
		access := strings.TrimSpace(tokens.ChatGPTAccessToken)
		if access != "" {
			snapshot, status, err := fetchUsageWithToken(ctx, client, access, tokens.ChatGPTAccountID)
			if err == nil {
				snapshot.Source = "forge chatgpt auth"
				return snapshot, nil
			}
			if status != http.StatusUnauthorized && status != http.StatusForbidden {
				return nil, err
			}
			if strings.TrimSpace(tokens.ChatGPTRefreshToken) != "" {
				forgeAuth := &authFile{}
				forgeAuth.Tokens.AccessToken = access
				forgeAuth.Tokens.RefreshToken = tokens.ChatGPTRefreshToken
				forgeAuth.Tokens.AccountID = tokens.ChatGPTAccountID
				accessToken, accountID, refreshErr := refreshAccessToken(ctx, client, forgeAuth)
				if refreshErr == nil {
					snapshot, _, err = fetchUsageWithToken(ctx, client, accessToken, accountID)
					if err == nil {
						snapshot.Source = "forge chatgpt auth (refreshed)"
						return snapshot, nil
					}
				}
			}
		}
	}
	auth, err := loadAuthFile()
	if err != nil {
		return nil, err
	}
	snapshot, status, err := fetchUsageWithToken(ctx, client, auth.Tokens.AccessToken, auth.Tokens.AccountID)
	if err == nil {
		snapshot.Source = "codex backend"
		return snapshot, nil
	}
	if status != http.StatusUnauthorized && status != http.StatusForbidden {
		return nil, err
	}
	if strings.TrimSpace(auth.Tokens.RefreshToken) == "" {
		return nil, err
	}
	accessToken, accountID, refreshErr := refreshAccessToken(ctx, client, auth)
	if refreshErr != nil {
		return nil, fmt.Errorf("%w; refresh failed: %v", err, refreshErr)
	}
	snapshot, _, err = fetchUsageWithToken(ctx, client, accessToken, accountID)
	if err != nil {
		return nil, err
	}
	snapshot.Source = "codex backend (refreshed)"
	return snapshot, nil
}

func loadAuthFile() (*authFile, error) {
	path, err := authFilePath()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, errors.New("codex not authenticated; run `codex login`")
		}
		return nil, fmt.Errorf("reading codex auth: %w", err)
	}
	var auth authFile
	if err := json.Unmarshal(data, &auth); err != nil {
		return nil, fmt.Errorf("parsing codex auth: %w", err)
	}
	if strings.TrimSpace(auth.Tokens.AccessToken) == "" {
		return nil, errors.New("codex auth is missing an access token")
	}
	return &auth, nil
}

func authFilePath() (string, error) {
	if home := strings.TrimSpace(os.Getenv("CODEX_HOME")); home != "" {
		return filepath.Join(home, "auth.json"), nil
	}
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	paths := []string{
		filepath.Join(homeDir, ".config", "codex", "auth.json"),
		filepath.Join(homeDir, ".codex", "auth.json"),
	}
	for _, path := range paths {
		if _, err := os.Stat(path); err == nil {
			return path, nil
		}
	}
	return paths[len(paths)-1], nil
}

func fetchUsageWithToken(ctx context.Context, client *http.Client, accessToken, accountID string) (*Snapshot, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, usageEndpoint, nil)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(accessToken))
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "forge")
	if strings.TrimSpace(accountID) != "" {
		req.Header.Set("ChatGPT-Account-Id", strings.TrimSpace(accountID))
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("fetching codex usage: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	var payload map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, resp.StatusCode, fmt.Errorf("decoding codex usage response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, resp.StatusCode, fmt.Errorf("fetching codex usage: %s", resp.Status)
	}
	snapshot := parseSnapshot(payload)
	if snapshot == nil {
		return nil, resp.StatusCode, errors.New("codex usage response did not include recognizable limits")
	}
	return snapshot, resp.StatusCode, nil
}

func refreshAccessToken(ctx context.Context, client *http.Client, auth *authFile) (accessToken, accountID string, err error) {
	form := url.Values{}
	form.Set("grant_type", "refresh_token")
	form.Set("client_id", refreshClientID)
	form.Set("refresh_token", strings.TrimSpace(auth.Tokens.RefreshToken))

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, refreshEndpoint, bytes.NewBufferString(form.Encode()))
	if err != nil {
		return "", "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "forge")

	resp, err := client.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("refreshing codex token: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", "", fmt.Errorf("refreshing codex token: %s", resp.Status)
	}
	var refreshed refreshResponse
	if err := json.NewDecoder(resp.Body).Decode(&refreshed); err != nil {
		return "", "", fmt.Errorf("decoding refreshed codex token: %w", err)
	}
	access := strings.TrimSpace(refreshed.AccessToken)
	if access == "" {
		return "", "", errors.New("refresh response did not include an access token")
	}
	account := auth.Tokens.AccountID
	if refreshed.IDToken != "" {
		if sub := claimString(refreshed.IDToken, "account_id"); sub != "" {
			account = sub
		}
	}
	if account == "" {
		if sub := claimString(access, "https://api.openai.com/auth"); sub != "" {
			account = sub
		}
	}
	return access, account, nil
}

func parseSnapshot(payload map[string]any) *Snapshot {
	if payload == nil {
		return nil
	}
	snapshot := &Snapshot{
		Plan: firstString(payload, "plan", "plan_type", "subscription_plan", "account_plan"),
	}
	root := payload
	if inner, ok := payload["rate_limits"].(map[string]any); ok {
		root = inner
	}
	snapshot.Primary = parseWindow(firstMap(root, "primary_window", "session_window", "five_hour_window"))
	snapshot.Secondary = parseWindow(firstMap(root, "secondary_window", "weekly_window", "seven_day_window"))
	snapshot.CodeReview = parseWindow(firstMap(root, "code_review_rate_limit", "code_review_window"))
	if snapshot.Primary == nil && snapshot.Secondary == nil && snapshot.CodeReview == nil {
		return nil
	}
	return snapshot
}

func parseWindow(obj map[string]any) *Window {
	if len(obj) == 0 {
		return nil
	}
	used := firstFloat(obj, "used_percent", "usedPercentage", "utilization_percentage", "percent_used", "usage_percent")
	resetAt := firstString(obj, "reset_at", "resets_at", "resetAt", "resetsAt")
	resetIn := firstString(obj, "reset_in", "resets_in", "reset_text", "resets_in_text")
	if resetIn == "" {
		if seconds := firstInt(obj, "seconds_until_reset", "reset_seconds", "resets_in_seconds", "time_until_reset_seconds"); seconds > 0 {
			resetIn = formatDuration(time.Duration(seconds) * time.Second)
		}
	}
	if used == 0 && resetAt == "" && resetIn == "" {
		return nil
	}
	return &Window{UsedPercent: used, ResetAt: resetAt, ResetIn: resetIn}
}

func firstMap(obj map[string]any, keys ...string) map[string]any {
	for _, key := range keys {
		if child, ok := obj[key].(map[string]any); ok {
			return child
		}
	}
	return nil
}

func firstString(obj map[string]any, keys ...string) string {
	for _, key := range keys {
		v, ok := obj[key]
		if !ok || v == nil {
			continue
		}
		switch val := v.(type) {
		case string:
			if strings.TrimSpace(val) != "" {
				return strings.TrimSpace(val)
			}
		case json.Number:
			return val.String()
		case float64:
			if val != 0 {
				return strconv.FormatFloat(val, 'f', -1, 64)
			}
		}
	}
	return ""
}

func firstFloat(obj map[string]any, keys ...string) float64 {
	for _, key := range keys {
		v, ok := obj[key]
		if !ok || v == nil {
			continue
		}
		switch val := v.(type) {
		case float64:
			return val
		case int:
			return float64(val)
		case json.Number:
			f, _ := val.Float64()
			return f
		case string:
			f, _ := strconv.ParseFloat(strings.TrimSpace(val), 64)
			if f != 0 {
				return f
			}
		}
	}
	return 0
}

func firstInt(obj map[string]any, keys ...string) int {
	for _, key := range keys {
		v, ok := obj[key]
		if !ok || v == nil {
			continue
		}
		switch val := v.(type) {
		case int:
			return val
		case float64:
			return int(val)
		case json.Number:
			i, _ := val.Int64()
			return int(i)
		case string:
			i, _ := strconv.Atoi(strings.TrimSpace(val))
			if i != 0 {
				return i
			}
		}
	}
	return 0
}

func claimString(jwtToken, key string) string {
	parts := strings.Split(jwtToken, ".")
	if len(parts) < 2 {
		return ""
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return ""
	}
	var claims map[string]any
	if err := json.Unmarshal(payload, &claims); err != nil {
		return ""
	}
	return firstString(claims, key)
}

func formatDuration(d time.Duration) string {
	if d <= 0 {
		return ""
	}
	hours := int(d / time.Hour)
	minutes := int((d % time.Hour) / time.Minute)
	if hours > 0 && minutes > 0 {
		return fmt.Sprintf("%dh%dm", hours, minutes)
	}
	if hours > 0 {
		return fmt.Sprintf("%dh", hours)
	}
	return fmt.Sprintf("%dm", minutes)
}
