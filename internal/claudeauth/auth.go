package claudeauth

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"forge/internal/auth"
)

const (
	authorizeEndpoint    = "https://claude.ai/oauth/authorize"
	tokenEndpoint        = "https://console.anthropic.com/v1/oauth/token"
	clientID             = "9d1c250a-e61b-44d9-88ed-5944d1962f5e"
	redirectURI          = "https://console.anthropic.com/oauth/code/callback"
	scope                = "org:create_api_key user:profile user:inference"
	refreshSafetyWindow  = 60 * time.Second
	defaultTokenLifetime = 55 * time.Minute
	defaultAuthUserAgent = "forge"
)

type tokenClaims struct {
	Exp int64 `json:"exp"`
}

type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int64  `json:"expires_in"`
}

type Session struct {
	AccessToken  string
	RefreshToken string
	ExpiresAt    time.Time
}

type Flow struct {
	AuthorizationURL string
	Verifier         string
	State            string
}

type Manager struct {
	client  *http.Client
	now     func() time.Time
	mu      sync.Mutex
	session Session
}

func Available() bool {
	_, err := Load()
	return err == nil
}

func StartAuth() (*Flow, error) {
	verifier, err := randomURLSafe(32)
	if err != nil {
		return nil, err
	}
	state, err := randomURLSafe(32)
	if err != nil {
		return nil, err
	}
	challenge := pkceChallenge(verifier)
	u, err := url.Parse(authorizeEndpoint)
	if err != nil {
		return nil, err
	}
	q := u.Query()
	q.Set("code", "true")
	q.Set("client_id", clientID)
	q.Set("response_type", "code")
	q.Set("redirect_uri", redirectURI)
	q.Set("scope", scope)
	q.Set("code_challenge", challenge)
	q.Set("code_challenge_method", "S256")
	q.Set("state", state)
	u.RawQuery = q.Encode()
	return &Flow{
		AuthorizationURL: u.String(),
		Verifier:         verifier,
		State:            state,
	}, nil
}

func ParseCallbackInput(flow *Flow, input string) (code string, state string, err error) {
	trimmed := strings.TrimSpace(input)
	if trimmed == "" {
		return "", "", errors.New("paste the callback URL or authorization code")
	}
	if u, parseErr := url.Parse(trimmed); parseErr == nil && u.Scheme != "" && u.Host != "" {
		code = strings.TrimSpace(u.Query().Get("code"))
		state = strings.TrimSpace(u.Query().Get("state"))
		if code != "" {
			if state == "" && flow != nil {
				state = strings.TrimSpace(flow.State)
			}
			return code, state, nil
		}
		if frag := strings.TrimSpace(u.Fragment); frag != "" {
			return ParseCallbackInput(flow, frag)
		}
	}
	parts := strings.SplitN(trimmed, "#", 2)
	code = strings.TrimSpace(parts[0])
	if len(parts) == 2 {
		state = strings.TrimSpace(parts[1])
	}
	if code == "" {
		return "", "", errors.New("paste the callback URL or authorization code")
	}
	if state == "" && flow != nil {
		state = strings.TrimSpace(flow.State)
	}
	return code, state, nil
}

func Exchange(ctx context.Context, client *http.Client, flow *Flow, pasted string) (Session, error) {
	if flow == nil {
		return Session{}, errors.New("claude auth flow not started")
	}
	code, state, err := ParseCallbackInput(flow, pasted)
	if err != nil {
		return Session{}, err
	}
	body, _ := json.Marshal(map[string]string{
		"code":          code,
		"state":         state,
		"grant_type":    "authorization_code",
		"client_id":     clientID,
		"redirect_uri":  redirectURI,
		"code_verifier": flow.Verifier,
	})
	return exchangeRequest(ctx, client, body)
}

func Load() (Session, error) {
	if tokens, err := auth.Load(); err == nil {
		if session, ok := sessionFromTokens(tokens); ok {
			return session, nil
		}
	}
	return Session{}, errors.New("claude auth not found; sign in with Forge first")
}

func NewManager() (*Manager, error) {
	session, err := Load()
	if err != nil {
		return nil, err
	}
	return &Manager{
		client:  &http.Client{Timeout: 20 * time.Second},
		now:     time.Now,
		session: session,
	}, nil
}

func (m *Manager) Authorization(ctx context.Context) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if strings.TrimSpace(m.session.AccessToken) == "" {
		return "", errors.New("claude auth is missing an access token")
	}
	if m.session.ExpiresAt.IsZero() || m.session.ExpiresAt.After(m.now().Add(refreshSafetyWindow)) {
		return m.session.AccessToken, nil
	}
	if err := m.refreshLocked(ctx); err != nil {
		return "", err
	}
	return m.session.AccessToken, nil
}

func (m *Manager) refreshLocked(ctx context.Context) error {
	body, _ := json.Marshal(map[string]string{
		"grant_type":    "refresh_token",
		"refresh_token": strings.TrimSpace(m.session.RefreshToken),
		"client_id":     clientID,
	})
	session, err := exchangeRequest(ctx, m.client, body)
	if err != nil {
		return err
	}
	m.session = session
	return nil
}

func StoreSession(tokens *auth.Tokens, session Session) *auth.Tokens {
	if tokens == nil {
		tokens = &auth.Tokens{}
	}
	tokens.ClaudeAccessToken = strings.TrimSpace(session.AccessToken)
	tokens.ClaudeRefreshToken = strings.TrimSpace(session.RefreshToken)
	tokens.ClaudeExpiresAt = session.ExpiresAt
	return tokens
}

func exchangeRequest(ctx context.Context, client *http.Client, body []byte) (Session, error) {
	if client == nil {
		client = &http.Client{Timeout: 20 * time.Second}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenEndpoint, bytes.NewReader(body))
	if err != nil {
		return Session{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", defaultAuthUserAgent)
	resp, err := client.Do(req)
	if err != nil {
		return Session{}, fmt.Errorf("claude code exchange failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return Session{}, fmt.Errorf("claude code exchange failed: %s", resp.Status)
	}
	var payload tokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return Session{}, fmt.Errorf("decoding Claude token response: %w", err)
	}
	access := strings.TrimSpace(payload.AccessToken)
	refresh := strings.TrimSpace(payload.RefreshToken)
	if access == "" || refresh == "" {
		return Session{}, errors.New("claude auth response was incomplete")
	}
	expiresAt := tokenExpiry(access)
	if expiresAt.IsZero() {
		lifetime := defaultTokenLifetime
		if payload.ExpiresIn > 0 {
			lifetime = time.Duration(payload.ExpiresIn) * time.Second
		}
		expiresAt = time.Now().Add(lifetime)
	}
	return Session{
		AccessToken:  access,
		RefreshToken: refresh,
		ExpiresAt:    expiresAt,
	}, nil
}

func sessionFromTokens(tokens *auth.Tokens) (Session, bool) {
	if tokens == nil {
		return Session{}, false
	}
	access := strings.TrimSpace(tokens.ClaudeAccessToken)
	refresh := strings.TrimSpace(tokens.ClaudeRefreshToken)
	if access == "" || refresh == "" {
		return Session{}, false
	}
	expiresAt := tokens.ClaudeExpiresAt
	if expiresAt.IsZero() {
		expiresAt = tokenExpiry(access)
	}
	if expiresAt.IsZero() {
		expiresAt = time.Now().Add(defaultTokenLifetime)
	}
	return Session{
		AccessToken:  access,
		RefreshToken: refresh,
		ExpiresAt:    expiresAt,
	}, true
}

func tokenExpiry(jwtToken string) time.Time {
	parts := strings.Split(jwtToken, ".")
	if len(parts) < 2 {
		return time.Time{}
	}
	data, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return time.Time{}
	}
	var claims tokenClaims
	if err := json.Unmarshal(data, &claims); err != nil || claims.Exp <= 0 {
		return time.Time{}
	}
	return time.Unix(claims.Exp, 0)
}

func randomURLSafe(n int) (string, error) {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func pkceChallenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}
