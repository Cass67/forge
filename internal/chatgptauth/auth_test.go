package chatgptauth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"forge/internal/auth"
)

func TestExtractAccountID(t *testing.T) {
	jwt := testJWT(map[string]any{
		"exp":                time.Now().Add(time.Hour).Unix(),
		"chatgpt_account_id": "acct_123",
	})
	if got := extractAccountID(jwt); got != "acct_123" {
		t.Fatalf("extractAccountID() = %q, want %q", got, "acct_123")
	}
}

func TestAuthorizationRefreshesExpiredToken(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	mgr := &Manager{
		client: &http.Client{
			Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				body, _ := io.ReadAll(req.Body)
				if got := req.URL.String(); got != refreshEndpoint {
					t.Fatalf("refresh URL = %q", got)
				}
				if !strings.Contains(string(body), "grant_type=refresh_token") {
					t.Fatalf("unexpected refresh body: %s", string(body))
				}
				respBody := `{"access_token":"` + testJWT(map[string]any{
					"exp":                now.Add(time.Hour).Unix(),
					"chatgpt_account_id": "acct_new",
				}) + `","refresh_token":"refresh_new","expires_in":3600}`
				return jsonResponse(respBody), nil
			}),
		},
		now: func() time.Time { return now },
		session: Session{
			AccessToken:  "expired",
			RefreshToken: "refresh_old",
			AccountID:    "acct_old",
			ExpiresAt:    now.Add(-time.Minute),
		},
	}
	token, accountID, err := mgr.Authorization(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if token == "expired" {
		t.Fatal("expected refreshed access token")
	}
	if accountID != "acct_new" {
		t.Fatalf("accountID = %q, want %q", accountID, "acct_new")
	}
}

func TestTransportAddsAuthorizationHeaders(t *testing.T) {
	now := time.Now()
	mgr := &Manager{
		now: func() time.Time { return now },
		session: Session{
			AccessToken:  "access_token",
			RefreshToken: "refresh_token",
			AccountID:    "acct_123",
			ExpiresAt:    now.Add(time.Hour),
		},
	}
	rt := &authTransport{
		mgr: mgr,
		base: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			if got := req.Header.Get("Authorization"); got != "Bearer access_token" {
				t.Fatalf("Authorization = %q", got)
			}
			if got := req.Header.Get("ChatGPT-Account-Id"); got != "acct_123" {
				t.Fatalf("ChatGPT-Account-Id = %q", got)
			}
			return jsonResponse(`{"ok":true}`), nil
		}),
	}
	req, err := http.NewRequest(http.MethodGet, "https://example.com", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rt.RoundTrip(req); err != nil {
		t.Fatal(err)
	}
}

func TestSessionFromTokens(t *testing.T) {
	session, ok := sessionFromTokens(&auth.Tokens{
		ChatGPTAccessToken:  "access",
		ChatGPTRefreshToken: "refresh",
		ChatGPTAccountID:    "acct",
		ChatGPTExpiresAt:    time.Now().Add(time.Hour),
	})
	if !ok {
		t.Fatal("expected session")
	}
	if session.AccountID != "acct" {
		t.Fatalf("AccountID = %q", session.AccountID)
	}
}

func TestLoadUsesForgeAuthStore(t *testing.T) {
	xdg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", xdg)
	t.Setenv("HOME", filepath.Join(t.TempDir(), "home"))
	t.Setenv("CODEX_HOME", filepath.Join(t.TempDir(), "codex"))

	expiresAt := time.Now().Add(time.Hour).UTC().Truncate(time.Second)
	err := auth.SaveExact(&auth.Tokens{
		ChatGPTAccessToken:  testJWT(map[string]any{"exp": expiresAt.Unix(), "chatgpt_account_id": "acct_forge"}),
		ChatGPTRefreshToken: "refresh_forge",
		ChatGPTAccountID:    "acct_forge",
		ChatGPTExpiresAt:    expiresAt,
	})
	if err != nil {
		t.Fatal(err)
	}

	session, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if session.AccountID != "acct_forge" {
		t.Fatalf("AccountID = %q, want %q", session.AccountID, "acct_forge")
	}
	if session.RefreshToken != "refresh_forge" {
		t.Fatalf("RefreshToken = %q, want %q", session.RefreshToken, "refresh_forge")
	}
}

func TestLoadDoesNotFallBackToExternalAuthFiles(t *testing.T) {
	xdg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", xdg)
	t.Setenv("HOME", filepath.Join(t.TempDir(), "home"))

	codexHome := t.TempDir()
	t.Setenv("CODEX_HOME", codexHome)

	err := os.WriteFile(filepath.Join(codexHome, "auth.json"), []byte(`{
  "tokens": {
    "access_token": "external_access",
    "refresh_token": "external_refresh"
  }
}`), 0600)
	if err != nil {
		t.Fatal(err)
	}

	_, err = Load()
	if err == nil {
		t.Fatal("expected Load to require Forge auth")
	}
	if !strings.Contains(err.Error(), "sign in with Forge first") {
		t.Fatalf("Load() error = %q, want Forge sign-in guidance", err.Error())
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return fn(req) }

func jsonResponse(body string) *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func testJWT(claims map[string]any) string {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none","typ":"JWT"}`))
	payloadBytes, _ := json.Marshal(claims)
	payload := base64.RawURLEncoding.EncodeToString(payloadBytes)
	return header + "." + payload + "."
}
