// Package providerauth holds the per-provider credential rules shared by the
// terminal UI and the desktop app: which providers use a plain API key, and
// how a provider's stored credentials are written, read and cleared.
package providerauth

import (
	"strings"
	"time"

	"forge/internal/auth"
)

func UsesAPIKey(id string) bool {
	switch strings.ToLower(strings.TrimSpace(id)) {
	case "chatgpt", "claude", "copilot":
		return false
	default:
		return true
	}
}

func SetKey(t *auth.Tokens, id, value string) {
	id = strings.ToLower(strings.TrimSpace(id))
	value = strings.TrimSpace(value)
	switch id {
	case "anthropic":
		t.AnthropicAPIKey = value
	case "openai":
		t.OpenAIAPIKey = value
	case "groq":
		t.GroqAPIKey = value
	case "mistral":
		t.MistralAPIKey = value
	case "xai":
		t.XAIAPIKey = value
	case "zai", "zai-coding-plan":
		t.ZAIAPIKey = value
	case "nvidia":
		t.NVIDIAAPIKey = value
	case "openrouter":
		t.OpenRouterAPIKey = value
	case "together":
		t.TogetherAPIKey = value
	case "perplexity":
		t.PerplexityAPIKey = value
	case "deepinfra":
		t.DeepInfraAPIKey = value
	case "cerebras":
		t.CerebrasAPIKey = value
	case "opencode", "opencode-go":
		t.OpenCodeAPIKey = value
	case "brave":
		t.BraveAPIKey = value
	default:
		if value == "" {
			t.ClearCustomProviderKey(id)
			return
		}
		t.SetCustomProviderKey(id, value)
	}
}

func Clear(t *auth.Tokens, id string) {
	id = strings.ToLower(strings.TrimSpace(id))
	switch id {
	case "chatgpt":
		t.ChatGPTAccessToken = ""
		t.ChatGPTRefreshToken = ""
		t.ChatGPTAccountID = ""
		t.ChatGPTExpiresAt = time.Time{}
	case "claude":
		t.ClaudeAccessToken = ""
		t.ClaudeRefreshToken = ""
		t.ClaudeExpiresAt = time.Time{}
	case "copilot":
		t.CopilotToken = ""
	default:
		if UsesAPIKey(id) {
			SetKey(t, id, "")
		}
	}
}

func HasCredential(t *auth.Tokens, id string) bool {
	if t == nil {
		return false
	}
	id = strings.ToLower(strings.TrimSpace(id))
	switch id {
	case "chatgpt":
		return strings.TrimSpace(t.ChatGPTAccessToken) != "" || strings.TrimSpace(t.ChatGPTRefreshToken) != ""
	case "claude":
		return strings.TrimSpace(t.ClaudeAccessToken) != "" || strings.TrimSpace(t.ClaudeRefreshToken) != ""
	case "copilot":
		return strings.TrimSpace(t.CopilotToken) != ""
	case "anthropic":
		return strings.TrimSpace(t.AnthropicAPIKey) != ""
	case "openai":
		return strings.TrimSpace(t.OpenAIAPIKey) != ""
	case "groq":
		return strings.TrimSpace(t.GroqAPIKey) != ""
	case "mistral":
		return strings.TrimSpace(t.MistralAPIKey) != ""
	case "xai":
		return strings.TrimSpace(t.XAIAPIKey) != ""
	case "nvidia":
		return strings.TrimSpace(t.NVIDIAAPIKey) != ""
	case "openrouter":
		return strings.TrimSpace(t.OpenRouterAPIKey) != ""
	case "together":
		return strings.TrimSpace(t.TogetherAPIKey) != ""
	case "perplexity":
		return strings.TrimSpace(t.PerplexityAPIKey) != ""
	case "deepinfra":
		return strings.TrimSpace(t.DeepInfraAPIKey) != ""
	case "cerebras":
		return strings.TrimSpace(t.CerebrasAPIKey) != ""
	case "opencode", "opencode-go":
		return strings.TrimSpace(t.OpenCodeAPIKey) != ""
	case "brave":
		return strings.TrimSpace(t.BraveAPIKey) != ""
	default:
		if !UsesAPIKey(id) {
			return false
		}
		return strings.TrimSpace(t.CustomProviderKey(id)) != ""
	}
}
