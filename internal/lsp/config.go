package lsp

import (
	"strings"

	"forge/internal/config"
)

// ServersFromConfig merges user overrides onto the built-in server table. A
// key that already exists overrides only the fields it sets, so pinning a
// binary does not silently drop the extensions or args that made it work.
func ServersFromConfig(cfg config.LSPConfig) map[string]ServerConfig {
	servers := defaultServers()
	for name, override := range cfg.Servers {
		key := strings.ToLower(strings.TrimSpace(name))
		if key == "" {
			continue
		}
		if override.Enabled != nil && !*override.Enabled {
			delete(servers, key)
			continue
		}
		merged, known := servers[key]
		if !known {
			merged = ServerConfig{LanguageID: key}
		}
		if len(override.Command) > 0 {
			merged.Command = override.Command[0]
			merged.Args = append([]string(nil), override.Command[1:]...)
		}
		if id := strings.TrimSpace(override.LanguageID); id != "" {
			merged.LanguageID = id
		}
		if len(override.Extensions) > 0 {
			merged.Extensions = normalizeExtensions(override.Extensions)
		}
		// A new language with no command has nothing to spawn; keeping it
		// would route its files to a server that cannot start.
		if merged.Command == "" || len(merged.Extensions) == 0 {
			delete(servers, key)
			continue
		}
		servers[key] = merged
	}
	return servers
}

// normalizeExtensions accepts "go" or ".GO" and stores ".go", so a config
// typo does not silently stop matching every file of that language.
func normalizeExtensions(exts []string) []string {
	out := make([]string, 0, len(exts))
	for _, ext := range exts {
		trimmed := strings.ToLower(strings.TrimSpace(ext))
		if trimmed == "" {
			continue
		}
		if !strings.HasPrefix(trimmed, ".") {
			trimmed = "." + trimmed
		}
		out = append(out, trimmed)
	}
	return out
}
