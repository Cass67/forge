package auth

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"forge/internal/fsutil"
)

type Tokens struct {
	CopilotToken        string            `json:"copilot_token,omitempty"`
	ChatGPTAccessToken  string            `json:"chatgpt_access_token,omitempty"`
	ChatGPTRefreshToken string            `json:"chatgpt_refresh_token,omitempty"`
	ChatGPTAccountID    string            `json:"chatgpt_account_id,omitempty"`
	ChatGPTExpiresAt    time.Time         `json:"chatgpt_expires_at,omitempty"`
	ClaudeAccessToken   string            `json:"claude_access_token,omitempty"`
	ClaudeRefreshToken  string            `json:"claude_refresh_token,omitempty"`
	ClaudeExpiresAt     time.Time         `json:"claude_expires_at,omitempty"`
	AnthropicAPIKey     string            `json:"anthropic_api_key,omitempty"`
	OpenAIAPIKey        string            `json:"openai_api_key,omitempty"`
	GroqAPIKey          string            `json:"groq_api_key,omitempty"`
	MistralAPIKey       string            `json:"mistral_api_key,omitempty"`
	XAIAPIKey           string            `json:"xai_api_key,omitempty"`
	ZAIAPIKey           string            `json:"zai_api_key,omitempty"`
	NVIDIAAPIKey        string            `json:"nvidia_api_key,omitempty"`
	OpenRouterAPIKey    string            `json:"openrouter_api_key,omitempty"`
	TogetherAPIKey      string            `json:"together_api_key,omitempty"`
	PerplexityAPIKey    string            `json:"perplexity_api_key,omitempty"`
	DeepInfraAPIKey     string            `json:"deepinfra_api_key,omitempty"`
	CerebrasAPIKey      string            `json:"cerebras_api_key,omitempty"`
	BraveAPIKey         string            `json:"brave_api_key,omitempty"`
	ProviderAPIKeys     map[string]string `json:"provider_api_keys,omitempty"`
}

func (t *Tokens) CustomProviderKey(id string) string {
	if t.ProviderAPIKeys == nil {
		return ""
	}
	return t.ProviderAPIKeys[id]
}

func (t *Tokens) SetCustomProviderKey(id, key string) {
	if t.ProviderAPIKeys == nil {
		t.ProviderAPIKeys = make(map[string]string)
	}
	t.ProviderAPIKeys[id] = key
}

func (t *Tokens) ClearCustomProviderKey(id string) {
	delete(t.ProviderAPIKeys, id)
}

func defaultPath() string {
	return fsutil.ForgeConfigPath("auth.json")
}

func Load() (*Tokens, error) {
	data, err := os.ReadFile(defaultPath())
	if os.IsNotExist(err) {
		return &Tokens{}, nil
	}
	if err != nil {
		return nil, err
	}
	var t Tokens
	if err := json.Unmarshal(data, &t); err != nil {
		return nil, err
	}
	return &t, nil
}

// Save merges the provided tokens into the existing auth.json file.
// Non-empty fields in t overwrite existing values; empty fields are preserved
// from the current file. This prevents stale in-memory copies from wiping keys
// that were added by another process or manually.
func Save(t *Tokens) error {
	existing, _ := Load()
	if existing == nil {
		existing = &Tokens{}
	}
	merged := merge(existing, t)
	return writeTokens(merged)
}

// SaveExact writes the token struct as-is without merging. Use this only when
// you need to explicitly remove fields (e.g., clearing a provider credential).
func SaveExact(t *Tokens) error {
	return writeTokens(t)
}

func writeTokens(t *Tokens) error {
	p := defaultPath()
	if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(t, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(p, data, 0600)
}

// merge copies non-empty fields from src into dst, returning dst.
func merge(dst, src *Tokens) *Tokens {
	if src.CopilotToken != "" {
		dst.CopilotToken = src.CopilotToken
	}
	if src.ChatGPTAccessToken != "" {
		dst.ChatGPTAccessToken = src.ChatGPTAccessToken
	}
	if src.ChatGPTRefreshToken != "" {
		dst.ChatGPTRefreshToken = src.ChatGPTRefreshToken
	}
	if src.ChatGPTAccountID != "" {
		dst.ChatGPTAccountID = src.ChatGPTAccountID
	}
	if !src.ChatGPTExpiresAt.IsZero() {
		dst.ChatGPTExpiresAt = src.ChatGPTExpiresAt
	}
	if src.ClaudeAccessToken != "" {
		dst.ClaudeAccessToken = src.ClaudeAccessToken
	}
	if src.ClaudeRefreshToken != "" {
		dst.ClaudeRefreshToken = src.ClaudeRefreshToken
	}
	if !src.ClaudeExpiresAt.IsZero() {
		dst.ClaudeExpiresAt = src.ClaudeExpiresAt
	}
	if src.AnthropicAPIKey != "" {
		dst.AnthropicAPIKey = src.AnthropicAPIKey
	}
	if src.OpenAIAPIKey != "" {
		dst.OpenAIAPIKey = src.OpenAIAPIKey
	}
	if src.GroqAPIKey != "" {
		dst.GroqAPIKey = src.GroqAPIKey
	}
	if src.MistralAPIKey != "" {
		dst.MistralAPIKey = src.MistralAPIKey
	}
	if src.XAIAPIKey != "" {
		dst.XAIAPIKey = src.XAIAPIKey
	}
	if src.ZAIAPIKey != "" {
		dst.ZAIAPIKey = src.ZAIAPIKey
	}
	if src.NVIDIAAPIKey != "" {
		dst.NVIDIAAPIKey = src.NVIDIAAPIKey
	}
	if src.OpenRouterAPIKey != "" {
		dst.OpenRouterAPIKey = src.OpenRouterAPIKey
	}
	if src.TogetherAPIKey != "" {
		dst.TogetherAPIKey = src.TogetherAPIKey
	}
	if src.PerplexityAPIKey != "" {
		dst.PerplexityAPIKey = src.PerplexityAPIKey
	}
	if src.DeepInfraAPIKey != "" {
		dst.DeepInfraAPIKey = src.DeepInfraAPIKey
	}
	if src.CerebrasAPIKey != "" {
		dst.CerebrasAPIKey = src.CerebrasAPIKey
	}
	if src.BraveAPIKey != "" {
		dst.BraveAPIKey = src.BraveAPIKey
	}
	if len(src.ProviderAPIKeys) > 0 {
		if dst.ProviderAPIKeys == nil {
			dst.ProviderAPIKeys = make(map[string]string)
		}
		for k, v := range src.ProviderAPIKeys {
			if v != "" {
				dst.ProviderAPIKeys[k] = v
			}
		}
	}
	return dst
}
