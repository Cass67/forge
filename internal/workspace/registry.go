package workspace

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"forge/internal/fsutil"
)

// Entry is a workspace the user has opened.
type Entry struct {
	Path     string    `json:"path"`
	Pinned   bool      `json:"pinned"`
	LastUsed time.Time `json:"last_used"`
}

// Registry remembers which workspaces have been opened, so the app can offer
// them again on a later launch instead of starting from nothing.
type Registry struct {
	mu      sync.Mutex
	path    string
	entries []Entry
	roots   []string
}

type registryData struct {
	Entries []Entry  `json:"entries"`
	Roots   []string `json:"roots,omitempty"`
}

// LoadRegistry reads the stored workspaces. A missing or unreadable file is
// not an error: it just means nothing has been opened yet.
func LoadRegistry() *Registry {
	r := &Registry{path: fsutil.ForgeConfigPath("workspaces.json")}
	data, err := os.ReadFile(r.path)
	if err != nil {
		return r
	}
	var stored registryData
	if err := json.Unmarshal(data, &stored); err == nil && stored.Entries != nil {
		r.entries = stored.Entries
		r.roots = stored.Roots
		return r
	}
	// Older versions stored entries as a bare array.
	_ = json.Unmarshal(data, &r.entries)
	return r
}

// List returns the workspaces, pinned ones first and the rest most recent
// first. Directories that no longer exist are dropped.
func (r *Registry) List() []Entry {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.listLocked()
}

func (r *Registry) listLocked() []Entry {
	out := make([]Entry, 0, len(r.entries))
	for _, e := range r.entries {
		if st, err := os.Stat(e.Path); err == nil && st.IsDir() {
			out = append(out, e)
		}
	}
	slices.SortStableFunc(out, func(a, b Entry) int {
		if a.Pinned != b.Pinned {
			if a.Pinned {
				return -1
			}
			return 1
		}
		return b.LastUsed.Compare(a.LastUsed)
	})
	return out
}

// Remember records a workspace as opened now.
func (r *Registry) Remember(path string) error {
	clean, err := Clean(path)
	if err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := range r.entries {
		if r.entries[i].Path == clean {
			r.entries[i].LastUsed = time.Now().UTC()
			return r.saveLocked()
		}
	}
	r.entries = append(r.entries, Entry{Path: clean, LastUsed: time.Now().UTC()})
	return r.saveLocked()
}

// Ensure records a discovered workspace without changing existing pin or
// recency state.
func (r *Registry) Ensure(path string) error {
	clean, err := Clean(path)
	if err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, entry := range r.entries {
		if entry.Path == clean {
			return nil
		}
	}
	r.entries = append(r.entries, Entry{Path: clean, LastUsed: time.Now().UTC()})
	return r.saveLocked()
}

// RememberRoot records a folder whose immediate children can be rediscovered.
func (r *Registry) RememberRoot(path string) error {
	clean, err := Clean(path)
	if err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, root := range r.roots {
		if root == clean {
			return nil
		}
	}
	r.roots = append(r.roots, clean)
	return r.saveLocked()
}

// Roots returns existing top-level folders selected for expansion. Registries
// written before roots were stored are migrated by recognizing a remembered
// workspace that directly contains another remembered workspace.
func (r *Registry) Roots() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	seen := make(map[string]bool, len(r.roots))
	roots := make([]string, 0, len(r.roots))
	for _, root := range r.roots {
		if st, err := os.Stat(root); err == nil && st.IsDir() {
			roots = append(roots, root)
			seen[root] = true
		}
	}
	entries := make(map[string]bool, len(r.entries))
	for _, entry := range r.entries {
		entries[entry.Path] = true
	}
	for _, entry := range r.entries {
		parent := filepath.Dir(entry.Path)
		if !seen[parent] && entries[parent] {
			if st, err := os.Stat(parent); err == nil && st.IsDir() {
				roots = append(roots, parent)
				seen[parent] = true
			}
		}
	}
	return roots
}

// SetPinned pins a workspace so it survives regardless of how long ago it was
// last used.
func (r *Registry) SetPinned(path string, pinned bool) error {
	clean, err := Clean(path)
	if err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := range r.entries {
		if r.entries[i].Path == clean {
			r.entries[i].Pinned = pinned
			return r.saveLocked()
		}
	}
	r.entries = append(r.entries, Entry{Path: clean, Pinned: pinned, LastUsed: time.Now().UTC()})
	return r.saveLocked()
}

// Forget removes a workspace from the list. It does not touch the directory.
func (r *Registry) Forget(path string) error {
	clean, err := Clean(path)
	if err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	kept := r.entries[:0]
	for _, e := range r.entries {
		if e.Path != clean {
			kept = append(kept, e)
		}
	}
	r.entries = kept
	return r.saveLocked()
}

// MostRecent returns the workspace to reopen on a fresh launch: the most
// recently used one, preferring pinned entries. Empty when there is none.
func (r *Registry) MostRecent() string {
	list := r.List()
	if len(list) == 0 {
		return ""
	}
	return list[0].Path
}

func (r *Registry) saveLocked() error {
	if err := os.MkdirAll(filepath.Dir(r.path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(registryData{Entries: r.entries, Roots: r.roots}, "", "  ")
	if err != nil {
		return err
	}
	tmp := r.path + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, r.path)
}

// Clean resolves a workspace path and rejects anything that is not a usable
// directory.
func Clean(path string) (string, error) {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return "", errors.New("empty workspace path")
	}
	abs, err := filepath.Abs(trimmed)
	if err != nil {
		return "", err
	}
	st, err := os.Stat(abs)
	if err != nil || !st.IsDir() {
		return "", errors.New("not a directory: " + path)
	}
	return abs, nil
}

// Usable reports whether a directory is a sensible place to run an agent.
// The filesystem root is excluded: a bundled app launched from Finder inherits
// "/" as its working directory, which is neither writable nor meaningful.
func Usable(dir string) bool {
	clean, err := Clean(dir)
	if err != nil {
		return false
	}
	if clean == string(filepath.Separator) {
		return false
	}
	if home, err := os.UserHomeDir(); err == nil && clean == filepath.Clean(home) {
		return true
	}
	probe := filepath.Join(clean, ".forge-write-probe")
	f, err := os.OpenFile(probe, os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return false
	}
	_ = f.Close()
	_ = os.Remove(probe)
	return true
}
