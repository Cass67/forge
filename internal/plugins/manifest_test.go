package plugins

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestManifestValidLocalManifest(t *testing.T) {
	dir := t.TempDir()
	writeManifest(t, dir, `{
  "name":"demo-plugin",
  "version":"1.0.0",
  "description":"demo",
  "commands":{"hello":{"path":"commands/hello.md"}},
  "agents":[{"name":"reviewer","path":"agents/reviewer.md"}],
  "skills":[{"name":"skill","path":"skills/skill.md"}],
  "hooks":[{"name":"pre_compact","path":"hooks/pre.sh"}],
  "mcp_servers":{"demo":{"command":["node","server.js"]}}
}`)

	manifest, err := LoadManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Name != "demo-plugin" || manifest.Version != "1.0.0" {
		t.Fatalf("manifest = %#v", manifest)
	}
	if len(manifest.Commands) != 1 || len(manifest.Agents) != 1 || len(manifest.Skills) != 1 || len(manifest.Hooks) != 1 || len(manifest.MCPServers) != 1 {
		t.Fatalf("manifest components = %#v", manifest)
	}
}

func TestManifestRejectsPathTraversal(t *testing.T) {
	dir := t.TempDir()
	writeManifest(t, dir, `{"name":"demo","version":"1.0.0","commands":{"bad":{"path":"../outside.md"}}}`)

	_, err := LoadManifest(dir)
	if err == nil || !strings.Contains(err.Error(), "path traversal") {
		t.Fatalf("expected traversal error, got %v", err)
	}
}

func TestManifestRejectsAbsolutePath(t *testing.T) {
	dir := t.TempDir()
	writeManifest(t, dir, `{"name":"demo","version":"1.0.0","commands":{"bad":{"path":"/tmp/outside.md"}}}`)

	_, err := LoadManifest(dir)
	if err == nil || !strings.Contains(err.Error(), "absolute") {
		t.Fatalf("expected absolute path error, got %v", err)
	}
}

func TestManifestRejectsNonASCIIName(t *testing.T) {
	dir := t.TempDir()
	writeManifest(t, dir, `{"name":"claude-插件","version":"1.0.0"}`)

	_, err := LoadManifest(dir)
	if err == nil || !strings.Contains(err.Error(), "ASCII") {
		t.Fatalf("expected ASCII error, got %v", err)
	}
}

func TestManifestRejectsDuplicateComponentNames(t *testing.T) {
	dir := t.TempDir()
	writeManifest(t, dir, `{"name":"demo","version":"1.0.0","commands":{"dup":{"path":"commands/a.md"}},"skills":[{"name":"dup","path":"skills/a.md"}]}`)

	_, err := LoadManifest(dir)
	if err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("expected duplicate error, got %v", err)
	}
}

func writeManifest(t *testing.T, dir, text string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, ManifestFilename), []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}
