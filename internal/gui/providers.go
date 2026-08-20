package gui

import (
	"context"
	"os/exec"
	"runtime"
	"strings"

	"forge/internal/providerauth"
	"forge/internal/tui"
)

// Providers lists the configured providers with their current sign-in state.
func (s *Service) Providers() []ProviderPayload {
	cfg, _, ready := s.snapshot()
	if !ready {
		return []ProviderPayload{}
	}
	return providerPayloads(refreshProviders(cfg))
}

// SignOutProvider discards a provider's stored credentials.
func (s *Service) SignOutProvider(providerID string) ([]ProviderPayload, error) {
	if err := providerauth.SignOut(providerID); err != nil {
		return s.Providers(), err
	}
	return s.Providers(), nil
}

// SetProviderKey stores an API key for a provider that uses one.
func (s *Service) SetProviderKey(providerID, key string) ([]ProviderPayload, error) {
	if strings.TrimSpace(key) == "" {
		return s.Providers(), providerauth.ErrNotSignedIn
	}
	if err := providerauth.SaveKey(providerID, key); err != nil {
		return s.Providers(), err
	}
	return s.Providers(), nil
}

// StartProviderLogin begins a browser sign-in and returns the URL (and code,
// for device flows) the user needs. Device flows are then finished by
// AwaitProviderLogin; Claude is finished by CompleteProviderLogin.
func (s *Service) StartProviderLogin(providerID string) (providerauth.Login, error) {
	cfg, _, ready := s.snapshot()
	if !ready {
		return providerauth.Login{}, errNotReady
	}
	login, err := s.flows.Start(context.Background(), providerID, cfg.CopilotClientID)
	if err != nil {
		return providerauth.Login{}, err
	}
	// Opening the page is a convenience; the URL is shown either way.
	_ = openURL(login.VerifyURL)
	return login, nil
}

// AwaitProviderLogin blocks until a device-code sign-in completes.
func (s *Service) AwaitProviderLogin(providerID string) ([]ProviderPayload, error) {
	cfg, _, ready := s.snapshot()
	if !ready {
		return []ProviderPayload{}, errNotReady
	}
	if err := s.flows.Await(context.Background(), providerID, cfg.CopilotClientID); err != nil {
		return s.Providers(), err
	}
	return s.Providers(), nil
}

// CompleteProviderLogin finishes a sign-in that needs a pasted callback URL.
func (s *Service) CompleteProviderLogin(providerID, pasted string) ([]ProviderPayload, error) {
	if err := s.flows.Complete(context.Background(), providerID, pasted); err != nil {
		return s.Providers(), err
	}
	return s.Providers(), nil
}

// OpenURL opens a link in the user's browser. The webview must not navigate
// away from the app itself.
func (s *Service) OpenURL(target string) error { return openURL(target) }

func refreshProviders(cfg tui.ChatLiveConfig) []tui.ProviderOption {
	if cfg.RefreshProviders != nil {
		return cfg.RefreshProviders()
	}
	return cfg.Providers
}

func openURL(target string) error {
	if strings.TrimSpace(target) == "" {
		return nil
	}
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", target)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", target)
	default:
		cmd = exec.Command("xdg-open", target)
	}
	return cmd.Start()
}
