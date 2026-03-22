package auth

import (
	"encoding/json"
	"os"
	"path/filepath"
)

type Tokens struct {
	CopilotToken string `json:"copilot_token,omitempty"`
}

func defaultPath() string {
	return filepath.Join(os.Getenv("HOME"), ".config", "forge", "auth.json")
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
