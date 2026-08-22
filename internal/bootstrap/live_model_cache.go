package bootstrap

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"

	"forge/internal/fsutil"
)

// Live model discovery is a network round trip per configured provider, and it
// runs before the chat UI can paint. A cached list answers immediately and the
// refresh happens behind it, so the next launch is current.
const liveModelCacheTTL = 24 * time.Hour

// refreshedLiveModels keeps each provider to one background refresh per process.
var refreshedLiveModels sync.Map

type liveModelCache struct {
	Models []string `json:"models"`
}

func liveModelCachePath(provider string) string {
	if provider == "" {
		return ""
	}
	return fsutil.ForgeConfigPath(filepath.Join("providers", provider+"-live-models.json"))
}

func loadLiveModelCache(provider string) ([]string, bool) {
	path := liveModelCachePath(provider)
	if path == "" {
		return nil, false
	}
	info, err := os.Stat(path)
	if err != nil || time.Since(info.ModTime()) > liveModelCacheTTL {
		return nil, false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	var cache liveModelCache
	if err := json.Unmarshal(data, &cache); err != nil || len(cache.Models) == 0 {
		return nil, false
	}
	return cache.Models, true
}

func writeLiveModelCache(provider string, models []string) {
	path := liveModelCachePath(provider)
	if path == "" || len(models) == 0 {
		return
	}
	data, err := json.Marshal(liveModelCache{Models: models})
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return
	}
	_ = os.WriteFile(path, data, 0o600)
}

// refreshLiveModelsAsync updates the on-disk list in the background. Only a
// successful fetch is cached: a failed one must not pin the curated fallback
// in place for a day.
func refreshLiveModelsAsync(provider string, fetch func() []string) {
	if provider == "" || fetch == nil {
		return
	}
	if _, loaded := refreshedLiveModels.LoadOrStore(provider, true); loaded {
		return
	}
	go func() {
		if models := fetch(); len(models) > 0 {
			writeLiveModelCache(provider, models)
		}
	}()
}
