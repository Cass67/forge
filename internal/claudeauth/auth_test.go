package claudeauth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"forge/internal/auth"
)

func TestStartAuthBuildsAuthorizeURL(t *testing.T) {
	flow, err := StartAuth()
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(flow.Verifier) == "" {
		t.Fatal("expected verifier")
	}
	if strings.TrimSpace(flow.State) == "" {
		t.Fatal("expected state")
	}
	if flow.AuthorizationURL == "" {
		t.Fatal("expected authorization URL")
	}

	u, err := url.Parse(flow.AuthorizationURL)
	if err != nil {
		t.Fatal(err)
	}
	if got := u.Scheme + "://" + u.Host + u.Path; got != authorizeEndpoint {
		t.Fatalf("authorize URL = %q, want %q", got, authorizeEndpoint)
	}
	query := u.Query()
	if got := query.Get("client_id"); got != clientID {
		t.Fatalf("client_id = %q, want %q", got, clientID)
	}
	if got := query.Get("response_type"); got != "code" {
		t.Fatalf("response_type = %q, want code", got)
	}
	if got := query.Get("code_challenge_method"); got != "S256" {
		t.Fatalf("code_challenge_method = %q, want S256", got)
	}
	if got := query.Get("state"); got != flow.State {
		t.Fatalf("state = %q, want %q", got, flow.State)
	}
	if got := query.Get("redirect_uri"); got != redirectURI {
		t.Fatalf("redirect_uri = %q, want %q", got, redirectURI)
	}
}

func TestParseCallbackInputSupportsURLAndRawCode(t *testing.T) {
	flow := &Flow{State: "state-123"}

	tests := []struct {
		name      string
		input     string
		wantCode  string
		wantState string
	}{
		{
			name:      "callback url",
			input:     redirectURI + "?code=abc123&state=returned-state",
			wantCode:  "abc123",
			wantState: "returned-state",
		},
		{
			name:      "raw code with state suffix",
			input:     "abc123#returned-state",
			wantCode:  "abc123",
			wantState: "returned-state",
		},
		{
			name:      "raw code falls back to flow state",
			input:     "abc123",
			wantCode:  "abc123",
			wantState: "state-123",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code, state, err := ParseCallbackInput(flow, tt.input)
			if err != nil {
				t.Fatal(err)
			}
			if code != tt.wantCode {
				t.Fatalf("code = %q, want %q", code, tt.wantCode)
			}
			if state != tt.wantState {
				t.Fatalf("state = %q, want %q", state, tt.wantState)
			}
		})
	}
}

func TestLoadUsesForgeAuthStore(t *testing.T) {
	xdg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", xdg)
	t.Setenv("HOME", filepath.Join(t.TempDir(), "home"))

	expiresAt := time.Now().Add(time.Hour).UTC().Truncate(time.Second)
	err := auth.SaveExact(&auth.Tokens{
		ClaudeAccessToken:  testJWT(map[string]any{"exp": expiresAt.Unix()}),
		ClaudeRefreshToken: "refresh-token",
		ClaudeExpiresAt:    expiresAt,
	})
	if err != nil {
		t.Fatal(err)
	}

	session, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if session.RefreshToken != "refresh-token" {
		t.Fatalf("RefreshToken = %q, want %q", session.RefreshToken, "refresh-token")
	}
}

func TestAuthorizationRefreshesExpiredToken(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	mgr := &Manager{
		client: &http.Client{
			Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				if got := req.URL.String(); got != tokenEndpoint {
					t.Fatalf("refresh URL = %q", got)
				}
				body, _ := io.ReadAll(req.Body)
				if !strings.Contains(string(body), `"grant_type":"refresh_token"`) {
					t.Fatalf("unexpected refresh body: %s", string(body))
				}
				respBody := `{"access_token":"` + testJWT(map[string]any{
					"exp": now.Add(time.Hour).Unix(),
				}) + `","refresh_token":"refresh_new","expires_in":3600}`
				return jsonResponse(respBody), nil
			}),
		},
		now: func() time.Time { return now },
		session: Session{
			AccessToken:  "expired",
			RefreshToken: "refresh_old",
			ExpiresAt:    now.Add(-time.Minute),
		},
	}

	token, err := mgr.Authorization(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if token == "expired" {
		t.Fatal("expected refreshed access token")
	}
	if mgr.session.RefreshToken != "refresh_new" {
		t.Fatalf("RefreshToken = %q, want %q", mgr.session.RefreshToken, "refresh_new")
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
