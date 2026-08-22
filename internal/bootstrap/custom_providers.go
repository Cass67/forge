package bootstrap

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

type CustomProviderDef struct {
	ID           string
	Name         string
	BaseURL      string
	WireAPI      string
	ModelInfoURL string
	HTTPHeaders  map[string]string
	DefaultModel string
	Models       []string
	ImageModels  []string
	// ContextWindow is the models' context window in tokens. Custom providers
	// serve models the models.dev catalog has never heard of, and without this
	// the runtime treats their window as unknown and falls back to its most
	// conservative limits.
	ContextWindow int
}

type tomlProviderBlock struct {
	Name          string            `toml:"name"`
	BaseURL       string            `toml:"base_url"`
	WireAPI       string            `toml:"wire_api"`
	ModelInfoURL  string            `toml:"model_info_url"`
	HTTPHeaders   map[string]string `toml:"http_headers"`
	DefaultModel  string            `toml:"default_model"`
	Models        []string          `toml:"models"`
	ImageModels   []string          `toml:"image_models"`
	ContextWindow int               `toml:"context_window"`
}

type tomlProviderFile struct {
	ModelProviders map[string]tomlProviderBlock `toml:"model_providers"`
}

func LoadCustomCompatProviders(configDir string) ([]CustomProviderDef, error) {
	var defs []CustomProviderDef

	// Location 1: {configDir}/providers/*.toml
	providersDir := filepath.Join(configDir, "providers")
	if entries, err := os.ReadDir(providersDir); err == nil {
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".toml") {
				continue
			}
			found, err := parseProviderFile(filepath.Join(providersDir, e.Name()))
			if err != nil {
				return nil, err
			}
			defs = append(defs, found...)
		}
	}

	// Location 2: {configDir}/*.toml
	if entries, err := os.ReadDir(configDir); err == nil {
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".toml") {
				continue
			}
			found, err := parseProviderFile(filepath.Join(configDir, e.Name()))
			if err != nil {
				return nil, err
			}
			defs = append(defs, found...)
		}
	}

	return defs, nil
}

func parseProviderFile(path string) ([]CustomProviderDef, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var file tomlProviderFile
	if err := toml.Unmarshal(data, &file); err != nil {
		return nil, err
	}

	var defs []CustomProviderDef
	for id, block := range file.ModelProviders {
		defs = append(defs, CustomProviderDef{
			ID:            id,
			Name:          block.Name,
			BaseURL:       normalizeBaseURL(block.BaseURL),
			WireAPI:       block.WireAPI,
			ModelInfoURL:  normalizeBaseURL(block.ModelInfoURL),
			HTTPHeaders:   block.HTTPHeaders,
			DefaultModel:  block.DefaultModel,
			Models:        block.Models,
			ImageModels:   block.ImageModels,
			ContextWindow: block.ContextWindow,
		})
	}
	return defs, nil
}

func normalizeBaseURL(u string) string {
	if u == "" {
		return u
	}
	if !strings.Contains(u, "://") {
		return "https://" + u
	}
	return u
}
