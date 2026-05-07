package plugins

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const ManifestFilename = "forge-plugin.json"

type Manifest struct {
	Name        string                       `json:"name"`
	Version     string                       `json:"version"`
	Description string                       `json:"description,omitempty"`
	Commands    map[string]CommandManifest   `json:"commands,omitempty"`
	Agents      []AgentManifest              `json:"agents,omitempty"`
	Skills      []SkillManifest              `json:"skills,omitempty"`
	Hooks       []HookManifest               `json:"hooks,omitempty"`
	MCPServers  map[string]MCPServerManifest `json:"mcp_servers,omitempty"`
}

type CommandManifest struct {
	Path string `json:"path"`
}

type AgentManifest struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

type SkillManifest struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

type HookManifest struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

type MCPServerManifest struct {
	Command []string          `json:"command,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
	URL     string            `json:"url,omitempty"`
}

func LoadManifest(root string) (Manifest, error) {
	path := filepath.Join(root, ManifestFilename)
	data, err := os.ReadFile(path)
	if err != nil {
		return Manifest{}, err
	}
	var manifest Manifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return Manifest{}, err
	}
	if err := manifest.Validate(root); err != nil {
		return Manifest{}, err
	}
	return manifest, nil
}

func (m Manifest) Validate(root string) error {
	if strings.TrimSpace(m.Name) == "" {
		return fmt.Errorf("manifest name is required")
	}
	if !asciiIdentifier(m.Name) {
		return fmt.Errorf("manifest name must be ASCII")
	}
	if strings.TrimSpace(m.Version) == "" {
		return fmt.Errorf("manifest version is required")
	}
	seen := map[string]struct{}{}
	for name, cmd := range m.Commands {
		if err := validateComponentName(seen, name); err != nil {
			return err
		}
		if err := validateManifestPath(root, cmd.Path); err != nil {
			return fmt.Errorf("command %q: %w", name, err)
		}
	}
	for _, agent := range m.Agents {
		if err := validateComponentName(seen, agent.Name); err != nil {
			return err
		}
		if err := validateManifestPath(root, agent.Path); err != nil {
			return fmt.Errorf("agent %q: %w", agent.Name, err)
		}
	}
	for _, skill := range m.Skills {
		if err := validateComponentName(seen, skill.Name); err != nil {
			return err
		}
		if err := validateManifestPath(root, skill.Path); err != nil {
			return fmt.Errorf("skill %q: %w", skill.Name, err)
		}
	}
	for _, hook := range m.Hooks {
		if err := validateComponentName(seen, hook.Name); err != nil {
			return err
		}
		if err := validateManifestPath(root, hook.Path); err != nil {
			return fmt.Errorf("hook %q: %w", hook.Name, err)
		}
	}
	for name := range m.MCPServers {
		if err := validateComponentName(seen, name); err != nil {
			return err
		}
	}
	return nil
}

func validateComponentName(seen map[string]struct{}, name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("component name is required")
	}
	if !asciiIdentifier(name) {
		return fmt.Errorf("component name %q must be ASCII", name)
	}
	key := strings.ToLower(name)
	if _, ok := seen[key]; ok {
		return fmt.Errorf("duplicate component name %q", name)
	}
	seen[key] = struct{}{}
	return nil
}

func validateManifestPath(root, rel string) error {
	rel = strings.TrimSpace(rel)
	if rel == "" {
		return fmt.Errorf("path is required")
	}
	if filepath.IsAbs(rel) {
		return fmt.Errorf("absolute paths are not allowed")
	}
	clean := filepath.Clean(rel)
	if clean == "." || strings.HasPrefix(clean, "..") || strings.Contains(clean, string(filepath.Separator)+".."+string(filepath.Separator)) {
		return fmt.Errorf("path traversal is not allowed")
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	absPath, err := filepath.Abs(filepath.Join(root, clean))
	if err != nil {
		return err
	}
	relToRoot, err := filepath.Rel(absRoot, absPath)
	if err != nil {
		return err
	}
	if strings.HasPrefix(relToRoot, "..") || filepath.IsAbs(relToRoot) {
		return fmt.Errorf("path traversal is not allowed")
	}
	return nil
}

func asciiIdentifier(value string) bool {
	for _, r := range value {
		if r > 127 {
			return false
		}
	}
	return true
}
