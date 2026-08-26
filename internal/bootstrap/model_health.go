package bootstrap

import (
	"cmp"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"forge/internal/fsutil"
)

type modelHealthEntry struct {
	Successes     int       `json:"successes,omitempty"`
	Failures      int       `json:"failures,omitempty"`
	Quarantined   bool      `json:"quarantined,omitempty"`
	LastError     string    `json:"last_error,omitempty"`
	LastSuccessAt time.Time `json:"last_success_at,omitempty"`
	LastFailureAt time.Time `json:"last_failure_at,omitempty"`
}

type modelHealthStore struct {
	Models map[string]modelHealthEntry `json:"models"`
}

var modelHealthMu sync.Mutex

func ReportModelSuccess(model string) {
	model = strings.TrimSpace(model)
	if model == "" {
		return
	}
	modelHealthMu.Lock()
	defer modelHealthMu.Unlock()

	store := loadModelHealthLocked()
	entry := store.Models[model]
	entry.Successes++
	entry.Quarantined = false
	entry.LastError = ""
	entry.LastSuccessAt = time.Now().UTC()
	store.Models[model] = entry
	saveModelHealthLocked(store)
}

func ReportModelFailure(model string, err error) {
	model = strings.TrimSpace(model)
	if model == "" || err == nil {
		return
	}
	modelHealthMu.Lock()
	defer modelHealthMu.Unlock()

	store := loadModelHealthLocked()
	entry := store.Models[model]
	entry.Failures++
	entry.LastError = strings.TrimSpace(err.Error())
	entry.LastFailureAt = time.Now().UTC()
	if shouldQuarantineModelError(err) {
		entry.Quarantined = true
	}
	store.Models[model] = entry
	saveModelHealthLocked(store)
}

func sortModelsByHealth(models []string) []string {
	modelHealthMu.Lock()
	store := loadModelHealthLocked()
	modelHealthMu.Unlock()

	out := append([]string(nil), models...)
	index := make(map[string]int, len(out))
	for i, model := range out {
		index[model] = i
	}
	slices.SortStableFunc(out, func(a, b string) int {
		left := store.Models[a]
		right := store.Models[b]
		ls := modelHealthScore(left)
		rs := modelHealthScore(right)
		switch {
		case ls != rs:
			return cmp.Compare(rs, ls)
		case left.Successes != right.Successes:
			return cmp.Compare(right.Successes, left.Successes)
		case left.Failures != right.Failures:
			return cmp.Compare(left.Failures, right.Failures)
		default:
			return cmp.Compare(index[a], index[b])
		}
	})
	return out
}

func modelHealthScore(entry modelHealthEntry) int {
	switch {
	case entry.Quarantined:
		return 0
	case entry.Successes > 0:
		return 2
	default:
		return 1
	}
}

func shouldQuarantineModelError(err error) bool {
	msg := strings.ToLower(strings.TrimSpace(err.Error()))
	for _, needle := range []string{
		"404 not found",
		"410 gone",
		"not a valid model id",
		"no endpoints available matching your guardrail restrictions",
		"data policy",
		"privacy",
		"model_not_found",
		"does not exist",
	} {
		if strings.Contains(msg, needle) {
			return true
		}
	}
	return false
}

func loadModelHealthLocked() modelHealthStore {
	path := fsutil.ForgeConfigPath("model-health.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return modelHealthStore{Models: map[string]modelHealthEntry{}}
	}
	var store modelHealthStore
	if err := json.Unmarshal(data, &store); err != nil {
		return modelHealthStore{Models: map[string]modelHealthEntry{}}
	}
	if store.Models == nil {
		store.Models = map[string]modelHealthEntry{}
	}
	return store
}

func saveModelHealthLocked(store modelHealthStore) {
	if store.Models == nil {
		store.Models = map[string]modelHealthEntry{}
	}
	path := fsutil.ForgeConfigPath("model-health.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return
	}
	data, err := json.MarshalIndent(store, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(path, data, 0o600)
}
