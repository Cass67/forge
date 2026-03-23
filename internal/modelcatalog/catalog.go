// Package modelcatalog provides model metadata from models.dev.
// It ships a bundled snapshot and refreshes it hourly in the background.
package modelcatalog

import (
	_ "embed"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"
)

//go:embed snapshot.json
var snapshotData []byte

// ModelInfo holds capability flags for a single model.
type ModelInfo struct {
	Reasoning   bool
	Temperature bool
	ToolCall    bool
}

// providerData mirrors the relevant fields from models.dev's JSON structure.
type providerData struct {
	Models map[string]modelEntry `json:"models"`
}

type modelEntry struct {
	Reasoning   bool `json:"reasoning"`
	Temperature bool `json:"temperature"`
	ToolCall    bool `json:"tool_call"`
}

// forgeToModelsDev maps forge provider labels to models.dev provider IDs.
var forgeToModelsDev = map[string]string{
	"openai":     "openai",
	"chatgpt":    "openai",
	"copilot":    "github-copilot",
	"openrouter": "openrouter",
	"nvidia":     "nvidia",
	"xai":        "xai",
	"mistral":    "mistral",
	"perplexity": "perplexity",
	"cerebras":   "cerebras",
	"groq":       "groq",
	"together":   "togetherai",
	"deepinfra":  "deepinfra",
	"anthropic":  "anthropic",
	"deepseek":   "deepseek",
	"google":     "google",
	"cohere":     "cohere",
	"fireworks":  "fireworks-ai",
	"novita":     "novita-ai",
}

var (
	mu      sync.RWMutex
	catalog map[string]providerData // keyed by models.dev provider ID
)

func init() {
	catalog = parseSnapshot(snapshotData)
	go refreshLoop()
}

func parseSnapshot(data []byte) map[string]providerData {
	var raw map[string]providerData
	if err := json.Unmarshal(data, &raw); err != nil {
		return map[string]providerData{}
	}
	return raw
}

func refreshLoop() {
	// Attempt to load from disk cache first, then start periodic refresh.
	if cached := loadDiskCache(); cached != nil {
		mu.Lock()
		catalog = cached
		mu.Unlock()
	}
	for {
		time.Sleep(time.Hour)
		refresh()
	}
}

func refresh() {
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Get("https://models.dev/api.json")
	if err != nil {
		return
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return
	}

	var raw map[string]providerData
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return
	}

	mu.Lock()
	catalog = raw
	mu.Unlock()

	writeDiskCache(raw)
}

func cacheFilePath() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "forge", "models.json")
}

func loadDiskCache() map[string]providerData {
	path := cacheFilePath()
	if path == "" {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	info, err := os.Stat(path)
	if err != nil || time.Since(info.ModTime()) > 24*time.Hour {
		return nil // stale
	}
	return parseSnapshot(data)
}

func writeDiskCache(data map[string]providerData) {
	path := cacheFilePath()
	if path == "" {
		return
	}
	b, err := json.Marshal(data)
	if err != nil {
		return
	}
	_ = os.WriteFile(path, b, 0o600)
}

// Lookup returns capability info for a model. providerID is the forge provider
// label (e.g. "openai", "copilot", "openrouter"). modelID is the bare model
// name as sent to the provider API (no provider prefix).
// Returns nil when the model is not found in the catalog.
func Lookup(providerID, modelID string) *ModelInfo {
	mdevID, ok := forgeToModelsDev[providerID]
	if !ok {
		mdevID = providerID
	}

	mu.RLock()
	provider, ok := catalog[mdevID]
	mu.RUnlock()
	if !ok {
		return nil
	}

	if entry, ok := provider.Models[modelID]; ok {
		return &ModelInfo{
			Reasoning:   entry.Reasoning,
			Temperature: entry.Temperature,
			ToolCall:    entry.ToolCall,
		}
	}
	return nil
}

// ProviderModels returns all tool-callable model IDs for a forge provider.
// Model IDs are returned bare (no provider prefix).
func ProviderModels(providerID string) []string {
	mdevID, ok := forgeToModelsDev[providerID]
	if !ok {
		mdevID = providerID
	}

	mu.RLock()
	provider, ok := catalog[mdevID]
	mu.RUnlock()
	if !ok {
		return nil
	}

	out := make([]string, 0, len(provider.Models))
	for id, m := range provider.Models {
		if m.ToolCall {
			out = append(out, id)
		}
	}
	return out
}
