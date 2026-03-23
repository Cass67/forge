package auth

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"forge/internal/fsutil"
)

type Tokens struct {
	CopilotToken        string    `json:"copilot_token,omitempty"`
	ChatGPTAccessToken  string    `json:"chatgpt_access_token,omitempty"`
	ChatGPTRefreshToken string    `json:"chatgpt_refresh_token,omitempty"`
	ChatGPTAccountID    string    `json:"chatgpt_account_id,omitempty"`
	ChatGPTExpiresAt    time.Time `json:"chatgpt_expires_at,omitempty"`
	AnthropicAPIKey     string    `json:"anthropic_api_key,omitempty"`
	OpenAIAPIKey        string    `json:"openai_api_key,omitempty"`
	GroqAPIKey          string    `json:"groq_api_key,omitempty"`
	MistralAPIKey       string    `json:"mistral_api_key,omitempty"`
	XAIAPIKey           string    `json:"xai_api_key,omitempty"`
	NVIDIAAPIKey        string    `json:"nvidia_api_key,omitempty"`
	OpenRouterAPIKey    string    `json:"openrouter_api_key,omitempty"`
	TogetherAPIKey      string    `json:"together_api_key,omitempty"`
	PerplexityAPIKey    string    `json:"perplexity_api_key,omitempty"`
	DeepInfraAPIKey     string    `json:"deepinfra_api_key,omitempty"`
	CerebrasAPIKey      string    `json:"cerebras_api_key,omitempty"`
	BraveAPIKey         string    `json:"brave_api_key,omitempty"`
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

func Save(t *Tokens) error {
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
