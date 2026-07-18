package plugins

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"forge/internal/config"
	"forge/internal/fsutil"
)

// DiscoveredPlugin is a plugin found by scanning the filesystem.
type DiscoveredPlugin struct {
	ID      string
	Kind    string // "external" or "native" (from manifest)
	Command []string
	Source  string // path to the plugin directory or executable
}

// ScanPluginsDir scans a directory for plugins.
// It looks for:
//   - Subdirectories containing forge-plugin.json (external or native manifest)
//   - Executable files that can run as JSON-RPC plugins (external)
func ScanPluginsDir(dir string) ([]DiscoveredPlugin, error) {
	if dir == "" {
		dir = filepath.Join(fsutil.ForgeConfigDir(), "plugins")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("scan plugins dir: %w", err)
	}

	var plugins []DiscoveredPlugin
	for _, entry := range entries {
		entryPath := filepath.Join(dir, entry.Name())

		if entry.IsDir() {
			// Check for forge-plugin.json manifest inside
			manifestPath := filepath.Join(entryPath, ManifestFilename)
			if _, err := os.Stat(manifestPath); err == nil {
				p, err := readManifestPlugin(manifestPath)
				if err != nil {
					continue
				}
				if p.ID == "" {
					p.ID = entry.Name()
				}
				p.Source = entryPath
				plugins = append(plugins, p)
				continue
			}
		}

		// Check for a standalone executable (external JSON-RPC plugin)
		if !entry.IsDir() && isExecutable(entryPath) {
			name := strings.TrimSuffix(entry.Name(), filepath.Ext(entry.Name()))
			plugins = append(plugins, DiscoveredPlugin{
				ID:      name,
				Kind:    "external",
				Command: []string{entryPath},
				Source:  entryPath,
			})
		}
	}

	return plugins, nil
}

func readManifestPlugin(path string) (DiscoveredPlugin, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return DiscoveredPlugin{}, err
	}
	var manifest struct {
		ID      string   `json:"id"`
		Kind    string   `json:"kind"`
		Command []string `json:"command"`
		Source  string   `json:"source"`
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		return DiscoveredPlugin{}, err
	}
	kind := strings.ToLower(strings.TrimSpace(manifest.Kind))
	if kind == "" {
		// If manifest has a command, it's external. Otherwise assume native.
		if len(manifest.Command) > 0 {
			kind = "external"
		} else {
			kind = "native"
		}
	}
	return DiscoveredPlugin{
		ID:      manifest.ID,
		Kind:    kind,
		Command: manifest.Command,
		Source:  manifest.Source,
	}, nil
}

func isExecutable(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	if info.IsDir() {
		return false
	}
	// Check if any execute bit is set
	return info.Mode()&0111 != 0
}

// MergeDiscovered merges discovered plugins into the config's plugin list.
// Existing config entries with the same ID are kept; new entries are appended.
func MergeDiscovered(cfg *config.Config, discovered []DiscoveredPlugin) (added, removed, updated int) {
	if cfg == nil || len(discovered) == 0 {
		return 0, 0, 0
	}

	existing := make(map[string]int) // lower(id) -> index in cfg.Plugins
	for i, p := range cfg.Plugins {
		existing[strings.ToLower(strings.TrimSpace(p.ID))] = i
	}

	seen := make(map[string]bool)
	for _, d := range discovered {
		lower := strings.ToLower(strings.TrimSpace(d.ID))
		seen[lower] = true

		if idx, ok := existing[lower]; ok {
			// Already in config, check if command changed (external plugins)
			existingCfg := cfg.Plugins[idx]
			if d.Kind == "external" && !equalStringSlices(existingCfg.Command, d.Command) {
				cfg.Plugins[idx].Command = d.Command
				cfg.Plugins[idx].Source = d.Source
				updated++
			}
			continue
		}

		// New plugin: add it
		plugin := config.PluginConfig{
			ID:      d.ID,
			Kind:    d.Kind,
			Source:  d.Source,
			Command: d.Command,
		}
		if d.Kind == "external" {
			plugin.StartupTimeoutMS = 3000
			plugin.RequestTimeoutMS = 10000
		}
		cfg.Plugins = append(cfg.Plugins, plugin)
		added++
	}

	// Remove config entries for plugins no longer on disk
	// (only auto-discovered ones; user-configured ones stay)
	// # ponytail: no concept of "auto-discovered" marker on config entries yet.
	// Currently we only add, never remove. Add removal when auto-discovered
	// entries are tagged with a source="auto" flag.

	return added, removed, updated
}

func equalStringSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
