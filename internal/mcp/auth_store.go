package mcp

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"forge/internal/fsutil"
)

type tokenStore struct {
	Servers map[string]string `json:"servers"`
}

func tokenStorePath() string {
	return fsutil.ForgeConfigPath("mcp_tokens.json")
}

func loadTokenStore() (*tokenStore, error) {
	path := tokenStorePath()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &tokenStore{Servers: map[string]string{}}, nil
		}
		return nil, err
	}
	var store tokenStore
	if err := json.Unmarshal(data, &store); err != nil {
		return nil, err
	}
	if store.Servers == nil {
		store.Servers = map[string]string{}
	}
	return &store, nil
}

func saveTokenStore(store *tokenStore) error {
	if store == nil {
		store = &tokenStore{Servers: map[string]string{}}
	}
	if store.Servers == nil {
		store.Servers = map[string]string{}
	}
	path := tokenStorePath()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.Marshal(store)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

func SaveBearerToken(serverName, token string) error {
	store, err := loadTokenStore()
	if err != nil {
		return err
	}
	if store.Servers == nil {
		store.Servers = map[string]string{}
	}
	store.Servers[serverName] = strings.TrimSpace(token)
	return saveTokenStore(store)
}

func DeleteBearerToken(serverName string) error {
	store, err := loadTokenStore()
	if err != nil {
		return err
	}
	delete(store.Servers, serverName)
	return saveTokenStore(store)
}

func BearerToken(serverName string) (string, bool, error) {
	store, err := loadTokenStore()
	if err != nil {
		return "", false, err
	}
	token := strings.TrimSpace(store.Servers[serverName])
	if token == "" {
		return "", false, nil
	}
	return token, true, nil
}
