package providerauth

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"forge/internal/auth"
	"forge/internal/chatgptauth"
	"forge/internal/claudeauth"
	"forge/internal/copilot"
)

var (
	// ErrUnsupported is returned for a provider with no interactive sign-in.
	ErrUnsupported = errors.New("this provider does not support interactive sign-in")
	// ErrNoClientID is returned when Copilot has no OAuth client id configured.
	ErrNoClientID = errors.New("no GitHub OAuth client id configured")
	// ErrNoFlow is returned when a sign-in is completed without being started.
	ErrNoFlow = errors.New("no sign-in is in progress for this provider")
	// ErrNotSignedIn is returned when signing out a provider that has no
	// stored credentials.
	ErrNotSignedIn = errors.New("no stored credential for this provider")
)

// Login describes an in-progress sign-in for the UI to present.
type Login struct {
	// Provider is the provider id the flow belongs to.
	Provider string `json:"provider"`
	// VerifyURL is the page the user must open.
	VerifyURL string `json:"verify_url"`
	// UserCode is the code to enter there, when the flow uses one.
	UserCode string `json:"user_code,omitempty"`
	// NeedsPaste is true when the user must paste a callback URL or code back
	// (Claude), rather than the flow completing by itself (device codes).
	NeedsPaste bool `json:"needs_paste"`
}

// Flows tracks sign-ins that have been started but not yet finished. A single
// value is safe for concurrent use.
type Flows struct {
	mu sync.Mutex
	m  map[string]any
}

func NewFlows() *Flows { return &Flows{m: map[string]any{}} }

func (f *Flows) put(id string, flow any) {
	f.mu.Lock()
	f.m[id] = flow
	f.mu.Unlock()
}

func (f *Flows) take(id string) any {
	f.mu.Lock()
	defer f.mu.Unlock()
	flow := f.m[id]
	delete(f.m, id)
	return flow
}

// Start begins an interactive sign-in. Device-code providers (ChatGPT,
// Copilot) then need Await; Claude needs Complete with the pasted callback.
func (f *Flows) Start(ctx context.Context, providerID, copilotClientID string) (Login, error) {
	id := normalize(providerID)
	switch id {
	case "chatgpt":
		flow, err := chatgptauth.StartDeviceAuth(ctx)
		if err != nil {
			return Login{}, err
		}
		f.put(id, flow)
		return Login{Provider: id, VerifyURL: flow.VerificationURL(), UserCode: flow.UserCode()}, nil
	case "claude":
		flow, err := claudeauth.StartAuth()
		if err != nil {
			return Login{}, err
		}
		f.put(id, flow)
		return Login{Provider: id, VerifyURL: flow.AuthorizationURL, NeedsPaste: true}, nil
	case "copilot":
		if strings.TrimSpace(copilotClientID) == "" {
			return Login{}, ErrNoClientID
		}
		dc, err := copilot.RequestDeviceCode(ctx, copilotClientID)
		if err != nil {
			return Login{}, err
		}
		f.put(id, dc)
		return Login{Provider: id, VerifyURL: dc.VerificationURI, UserCode: dc.UserCode}, nil
	default:
		return Login{}, fmt.Errorf("%w: %s", ErrUnsupported, providerID)
	}
}

// Await blocks until a device-code sign-in completes, then stores the
// credentials. It is a no-op for providers that complete by pasting.
func (f *Flows) Await(ctx context.Context, providerID, copilotClientID string) error {
	id := normalize(providerID)
	flow := f.take(id)
	if flow == nil {
		return ErrNoFlow
	}
	switch id {
	case "chatgpt":
		device, ok := flow.(*chatgptauth.DeviceFlow)
		if !ok {
			return ErrNoFlow
		}
		session, err := device.Wait(ctx)
		if err != nil {
			return err
		}
		return update(func(t *auth.Tokens) {
			t.ChatGPTAccessToken = session.AccessToken
			t.ChatGPTRefreshToken = session.RefreshToken
			t.ChatGPTAccountID = session.AccountID
			t.ChatGPTExpiresAt = session.ExpiresAt
		})
	case "copilot":
		dc, ok := flow.(*copilot.DeviceCode)
		if !ok {
			return ErrNoFlow
		}
		token, err := copilot.PollForToken(ctx, copilotClientID, dc)
		if err != nil {
			return err
		}
		return update(func(t *auth.Tokens) { t.CopilotToken = token })
	default:
		return fmt.Errorf("%w: %s", ErrUnsupported, providerID)
	}
}

// Complete finishes a paste-based sign-in (Claude), or stores an API key for
// providers that use one.
func (f *Flows) Complete(ctx context.Context, providerID, pasted string) error {
	id := normalize(providerID)
	if strings.TrimSpace(pasted) == "" {
		return errors.New("nothing pasted")
	}
	if id == "claude" {
		flow, ok := f.take(id).(*claudeauth.Flow)
		if !ok {
			return ErrNoFlow
		}
		session, err := claudeauth.Exchange(ctx, nil, flow, pasted)
		if err != nil {
			return err
		}
		return update(func(t *auth.Tokens) {
			t.ClaudeAccessToken = session.AccessToken
			t.ClaudeRefreshToken = session.RefreshToken
			t.ClaudeExpiresAt = session.ExpiresAt
		})
	}
	if !UsesAPIKey(id) {
		return fmt.Errorf("%w: %s", ErrUnsupported, providerID)
	}
	return SaveKey(id, pasted)
}

// SaveKey stores an API key for a provider.
func SaveKey(providerID, key string) error {
	return update(func(t *auth.Tokens) { SetKey(t, providerID, key) })
}

// SignOut discards every stored credential for a provider. It writes with
// SaveExact: auth.Save merges non-empty fields onto what is already on disk,
// which would silently undo the clear.
func SignOut(providerID string) error {
	tokens, err := auth.Load()
	if err != nil {
		return err
	}
	if !HasCredential(tokens, providerID) {
		return ErrNotSignedIn
	}
	Clear(tokens, providerID)
	return auth.SaveExact(tokens)
}

// SignedIn reports whether a provider currently has stored credentials.
func SignedIn(providerID string) bool {
	tokens, err := auth.Load()
	if err != nil {
		return false
	}
	return HasCredential(tokens, providerID)
}

// Interactive reports whether a provider signs in through a browser flow
// rather than by pasting an API key.
func Interactive(providerID string) bool {
	switch normalize(providerID) {
	case "chatgpt", "claude", "copilot":
		return true
	default:
		return false
	}
}

// update applies a change to the stored tokens. Load/Save round-trips so a
// concurrent writer's fields are merged rather than dropped.
func update(fn func(*auth.Tokens)) error {
	tokens, err := auth.Load()
	if err != nil {
		return err
	}
	fn(tokens)
	return auth.Save(tokens)
}

func normalize(id string) string { return strings.ToLower(strings.TrimSpace(id)) }
