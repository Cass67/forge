package chatgptauth

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
	"strings"
	"sync"
	"time"

	"forge/internal/auth"
)

const (
	DefaultBaseURL       = "https://chatgpt.com/backend-api/codex"
	refreshEndpoint      = "https://auth.openai.com/oauth/token"
	refreshClientID      = "app_EMoamEEZ73f0CkXaXp7hrann"
	deviceCodeEndpoint   = "https://auth.openai.com/api/accounts/deviceauth/usercode"
	deviceTokenEndpoint  = "https://auth.openai.com/api/accounts/deviceauth/token"
	deviceRedirectURI    = "https://auth.openai.com/deviceauth/callback"
	deviceVerifyURL      = "https://auth.openai.com/codex/device"
	refreshSafetyWindow  = 60 * time.Second
	defaultTokenLifetime = 55 * time.Minute
	defaultAuthUserAgent = "forge"
	pollingSafetyMargin  = 3 * time.Second
)

type fileTokens struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	IDToken      string `json:"id_token"`
	AccountID    string `json:"account_id"`
}

type authFile struct {
	AuthMode    string     `json:"auth_mode"`
	Tokens      fileTokens `json:"tokens"`
	LastRefresh string     `json:"last_refresh"`
}

type tokenClaims struct {
	Exp              int64  `json:"exp"`
	ChatGPTAccountID string `json:"chatgpt_account_id"`
	Organizations    []struct {
		ID string `json:"id"`
	} `json:"organizations"`
	OpenAIAuth struct {
		ChatGPTAccountID string `json:"chatgpt_account_id"`
		AccountID        string `json:"account_id"`
	} `json:"https://api.openai.com/auth"`
}

type refreshResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	IDToken      string `json:"id_token"`
	ExpiresIn    int64  `json:"expires_in"`
}

type deviceAuthResponse struct {
	DeviceAuthID string `json:"device_auth_id"`
	UserCode     string `json:"user_code"`
	Interval     string `json:"interval"`
}

type deviceTokenResponse struct {
	AuthorizationCode string `json:"authorization_code"`
	CodeVerifier      string `json:"code_verifier"`
}

type Session struct {
	AccessToken  string
	RefreshToken string
	AccountID    string
	ExpiresAt    time.Time
}

type Manager struct {
	client  *http.Client
	baseURL string
	now     func() time.Time
	mu      sync.Mutex
	session Session
}

type authTransport struct {
	base http.RoundTripper
	mgr  *Manager
}

type DeviceFlow struct {
	client       *http.Client
	deviceAuthID string
	userCode     string
	interval     time.Duration
}

func Available() bool {
	_, err := Load()
	return err == nil
}

func Load() (Session, error) {
	if tokens, err := auth.Load(); err == nil {
		if session, ok := sessionFromTokens(tokens); ok {
			return session, nil
		}
	}
	path, err := AuthFilePath()
	if err != nil {
		return Session{}, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return Session{}, errors.New("ChatGPT auth not found; sign in with ChatGPT/Codex first")
		}
		return Session{}, fmt.Errorf("reading ChatGPT auth: %w", err)
	}
	var auth authFile
	if err := json.Unmarshal(data, &auth); err != nil {
		return Session{}, fmt.Errorf("parsing ChatGPT auth: %w", err)
	}
	access := strings.TrimSpace(auth.Tokens.AccessToken)
	refresh := strings.TrimSpace(auth.Tokens.RefreshToken)
	if access == "" || refresh == "" {
		return Session{}, errors.New("ChatGPT auth is missing required OAuth tokens")
	}
	expiresAt := tokenExpiry(access)
	if expiresAt.IsZero() {
		expiresAt = fallbackExpiry(auth.LastRefresh)
	}
	accountID := strings.TrimSpace(auth.Tokens.AccountID)
	if accountID == "" {
		accountID = extractAccountID(auth.Tokens.IDToken)
	}
	if accountID == "" {
		accountID = extractAccountID(access)
	}
	return Session{
		AccessToken:  access,
		RefreshToken: refresh,
		AccountID:    accountID,
		ExpiresAt:    expiresAt,
	}, nil
}

func AuthFilePath() (string, error) {
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

func NewManager() (*Manager, error) {
	session, err := Load()
	if err != nil {
		return nil, err
	}
	return &Manager{
		client:  &http.Client{Timeout: 20 * time.Second},
		baseURL: DefaultBaseURL,
		now:     time.Now,
		session: session,
	}, nil
}

func (m *Manager) BaseURL() string {
	if m == nil || strings.TrimSpace(m.baseURL) == "" {
		return DefaultBaseURL
	}
	return strings.TrimRight(m.baseURL, "/")
}

func (m *Manager) HTTPClient() *http.Client {
	base := http.DefaultTransport
	if m.client != nil && m.client.Transport != nil {
		base = m.client.Transport
	}
	return &http.Client{
		Timeout:   0,
		Transport: &authTransport{base: base, mgr: m},
	}
}

func (m *Manager) Authorization(ctx context.Context) (token, accountID string, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.session.AccessToken == "" {
		return "", "", errors.New("ChatGPT auth is missing an access token")
	}
	if m.session.ExpiresAt.IsZero() || m.session.ExpiresAt.After(m.now().Add(refreshSafetyWindow)) {
		return m.session.AccessToken, m.session.AccountID, nil
	}
	if err := m.refreshLocked(ctx); err != nil {
		return "", "", err
	}
	return m.session.AccessToken, m.session.AccountID, nil
}

func (m *Manager) refreshLocked(ctx context.Context) error {
	form := url.Values{}
	form.Set("grant_type", "refresh_token")
	form.Set("client_id", refreshClientID)
	form.Set("refresh_token", strings.TrimSpace(m.session.RefreshToken))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, refreshEndpoint, bytes.NewBufferString(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", defaultAuthUserAgent)
	resp, err := m.client.Do(req)
	if err != nil {
		return fmt.Errorf("refreshing ChatGPT token: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("refreshing ChatGPT token: %s", resp.Status)
	}
	var refreshed refreshResponse
	if err := json.NewDecoder(resp.Body).Decode(&refreshed); err != nil {
		return fmt.Errorf("decoding refreshed ChatGPT token: %w", err)
	}
	access := strings.TrimSpace(refreshed.AccessToken)
	refresh := strings.TrimSpace(refreshed.RefreshToken)
	if access == "" || refresh == "" {
		return errors.New("refreshed ChatGPT token response was incomplete")
	}
	expiresAt := tokenExpiry(access)
	if expiresAt.IsZero() {
		lifetime := defaultTokenLifetime
		if refreshed.ExpiresIn > 0 {
			lifetime = time.Duration(refreshed.ExpiresIn) * time.Second
		}
		expiresAt = m.now().Add(lifetime)
	}
	accountID := m.session.AccountID
	if fresh := extractAccountID(strings.TrimSpace(refreshed.IDToken)); fresh != "" {
		accountID = fresh
	} else if fresh := extractAccountID(access); fresh != "" {
		accountID = fresh
	}
	m.session = Session{
		AccessToken:  access,
		RefreshToken: refresh,
		AccountID:    accountID,
		ExpiresAt:    expiresAt,
	}
	return nil
}

func (t *authTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	token, accountID, err := t.mgr.Authorization(req.Context())
	if err != nil {
		return nil, err
	}
	cloned := req.Clone(req.Context())
	cloned.Header = req.Header.Clone()
	cloned.Header.Set("Authorization", "Bearer "+token)
	if strings.TrimSpace(accountID) != "" {
		cloned.Header.Set("ChatGPT-Account-Id", strings.TrimSpace(accountID))
	} else {
		cloned.Header.Del("ChatGPT-Account-Id")
	}
	return t.base.RoundTrip(cloned)
}

func tokenExpiry(jwtToken string) time.Time {
	claims, ok := parseClaims(jwtToken)
	if !ok || claims.Exp <= 0 {
		return time.Time{}
	}
	return time.Unix(claims.Exp, 0)
}

func extractAccountID(jwtToken string) string {
	claims, ok := parseClaims(jwtToken)
	if !ok {
		return ""
	}
	switch {
	case strings.TrimSpace(claims.ChatGPTAccountID) != "":
		return strings.TrimSpace(claims.ChatGPTAccountID)
	case strings.TrimSpace(claims.OpenAIAuth.ChatGPTAccountID) != "":
		return strings.TrimSpace(claims.OpenAIAuth.ChatGPTAccountID)
	case strings.TrimSpace(claims.OpenAIAuth.AccountID) != "":
		return strings.TrimSpace(claims.OpenAIAuth.AccountID)
	case len(claims.Organizations) > 0:
		return strings.TrimSpace(claims.Organizations[0].ID)
	default:
		return ""
	}
}

func parseClaims(jwtToken string) (tokenClaims, bool) {
	var claims tokenClaims
	parts := strings.Split(jwtToken, ".")
	if len(parts) < 2 {
		return claims, false
	}
	data, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return claims, false
	}
	if err := json.Unmarshal(data, &claims); err != nil {
		return claims, false
	}
	return claims, true
}

func fallbackExpiry(lastRefresh string) time.Time {
	if strings.TrimSpace(lastRefresh) == "" {
		return time.Now().Add(defaultTokenLifetime)
	}
	if ts, err := time.Parse(time.RFC3339, strings.TrimSpace(lastRefresh)); err == nil {
		return ts.Add(defaultTokenLifetime)
	}
	return time.Now().Add(defaultTokenLifetime)
}

func sessionFromTokens(tokens *auth.Tokens) (Session, bool) {
	if tokens == nil {
		return Session{}, false
	}
	access := strings.TrimSpace(tokens.ChatGPTAccessToken)
	refresh := strings.TrimSpace(tokens.ChatGPTRefreshToken)
	if access == "" || refresh == "" {
		return Session{}, false
	}
	expiresAt := tokens.ChatGPTExpiresAt
	if expiresAt.IsZero() {
		expiresAt = tokenExpiry(access)
	}
	if expiresAt.IsZero() {
		expiresAt = time.Now().Add(defaultTokenLifetime)
	}
	return Session{
		AccessToken:  access,
		RefreshToken: refresh,
		AccountID:    strings.TrimSpace(tokens.ChatGPTAccountID),
		ExpiresAt:    expiresAt,
	}, true
}

func StoreSession(tokens *auth.Tokens, session Session) *auth.Tokens {
	if tokens == nil {
		tokens = &auth.Tokens{}
	}
	tokens.ChatGPTAccessToken = strings.TrimSpace(session.AccessToken)
	tokens.ChatGPTRefreshToken = strings.TrimSpace(session.RefreshToken)
	tokens.ChatGPTAccountID = strings.TrimSpace(session.AccountID)
	tokens.ChatGPTExpiresAt = session.ExpiresAt
	return tokens
}

func StartDeviceAuth(ctx context.Context) (*DeviceFlow, error) {
	body, _ := json.Marshal(map[string]string{"client_id": refreshClientID})
	client := &http.Client{Timeout: 20 * time.Second}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, deviceCodeEndpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", defaultAuthUserAgent)
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("starting ChatGPT device auth: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("starting ChatGPT device auth: %s", resp.Status)
	}
	var payload deviceAuthResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("decoding ChatGPT device auth: %w", err)
	}
	interval := 5 * time.Second
	if secs, err := time.ParseDuration(strings.TrimSpace(payload.Interval) + "s"); err == nil && secs > 0 {
		interval = secs
	}
	if strings.TrimSpace(payload.DeviceAuthID) == "" || strings.TrimSpace(payload.UserCode) == "" {
		return nil, errors.New("ChatGPT device auth response was incomplete")
	}
	return &DeviceFlow{
		client:       client,
		deviceAuthID: strings.TrimSpace(payload.DeviceAuthID),
		userCode:     strings.TrimSpace(payload.UserCode),
		interval:     interval,
	}, nil
}

func (f *DeviceFlow) VerificationURL() string { return deviceVerifyURL }

func (f *DeviceFlow) UserCode() string { return f.userCode }

func (f *DeviceFlow) Wait(ctx context.Context) (Session, error) {
	for {
		tokenResp, retry, err := f.poll(ctx)
		if err != nil {
			return Session{}, err
		}
		if retry {
			timer := time.NewTimer(f.interval + pollingSafetyMargin)
			select {
			case <-ctx.Done():
				timer.Stop()
				return Session{}, ctx.Err()
			case <-timer.C:
			}
			continue
		}
		return exchangeAuthorizationCode(ctx, f.client, tokenResp.AuthorizationCode, tokenResp.CodeVerifier)
	}
}

func (f *DeviceFlow) poll(ctx context.Context) (deviceTokenResponse, bool, error) {
	var out deviceTokenResponse
	body, _ := json.Marshal(map[string]string{
		"device_auth_id": f.deviceAuthID,
		"user_code":      f.userCode,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, deviceTokenEndpoint, bytes.NewReader(body))
	if err != nil {
		return out, false, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", defaultAuthUserAgent)
	resp, err := f.client.Do(req)
	if err != nil {
		return out, false, fmt.Errorf("polling ChatGPT device auth: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusNotFound {
		return out, true, nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return out, false, fmt.Errorf("polling ChatGPT device auth: %s", resp.Status)
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return out, false, fmt.Errorf("decoding ChatGPT device auth token: %w", err)
	}
	if strings.TrimSpace(out.AuthorizationCode) == "" || strings.TrimSpace(out.CodeVerifier) == "" {
		return out, false, errors.New("ChatGPT device auth token response was incomplete")
	}
	return out, false, nil
}

func exchangeAuthorizationCode(ctx context.Context, client *http.Client, code, verifier string) (Session, error) {
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", strings.TrimSpace(code))
	form.Set("redirect_uri", deviceRedirectURI)
	form.Set("client_id", refreshClientID)
	form.Set("code_verifier", strings.TrimSpace(verifier))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, refreshEndpoint, bytes.NewBufferString(form.Encode()))
	if err != nil {
		return Session{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", defaultAuthUserAgent)
	resp, err := client.Do(req)
	if err != nil {
		return Session{}, fmt.Errorf("exchanging ChatGPT authorization code: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return Session{}, fmt.Errorf("exchanging ChatGPT authorization code: %s", resp.Status)
	}
	var refreshed refreshResponse
	if err := json.NewDecoder(resp.Body).Decode(&refreshed); err != nil {
		return Session{}, fmt.Errorf("decoding ChatGPT authorization exchange: %w", err)
	}
	access := strings.TrimSpace(refreshed.AccessToken)
	refresh := strings.TrimSpace(refreshed.RefreshToken)
	if access == "" || refresh == "" {
		return Session{}, errors.New("ChatGPT authorization exchange was incomplete")
	}
	expiresAt := tokenExpiry(access)
	if expiresAt.IsZero() {
		lifetime := defaultTokenLifetime
		if refreshed.ExpiresIn > 0 {
			lifetime = time.Duration(refreshed.ExpiresIn) * time.Second
		}
		expiresAt = time.Now().Add(lifetime)
	}
	accountID := extractAccountID(strings.TrimSpace(refreshed.IDToken))
	if accountID == "" {
		accountID = extractAccountID(access)
	}
	return Session{
		AccessToken:  access,
		RefreshToken: refresh,
		AccountID:    accountID,
		ExpiresAt:    expiresAt,
	}, nil
}
