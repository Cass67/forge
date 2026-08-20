package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolvedOutputDirStaysOutOfTheWorkspace(t *testing.T) {
	workspace := t.TempDir()
	state := t.TempDir()
	t.Setenv("XDG_STATE_HOME", state)
	t.Chdir(workspace)

	cfg := &Config{}
	setDefaults(cfg)
	dir := cfg.ResolvedOutputDir()
	if !strings.HasPrefix(dir, filepath.Join(state, "forge", "projects")) {
		t.Fatalf("default output dir = %q, want under state dir", dir)
	}

	cfg.Session.OutputDir = "./output"
	if got := cfg.ResolvedOutputDir(); got != dir {
		t.Fatalf("legacy default output dir = %q, want %q", got, dir)
	}

	cfg.Session.OutputDir = "/tmp/forge-out"
	if got := cfg.ResolvedOutputDir(); got != "/tmp/forge-out" {
		t.Fatalf("explicit output dir = %q", got)
	}
}

func TestResolvedOutputDirKeepsExistingWorkspaceOutput(t *testing.T) {
	workspace := t.TempDir()
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Chdir(workspace)
	if err := os.MkdirAll(filepath.Join(workspace, "output", "threads"), 0o755); err != nil {
		t.Fatal(err)
	}

	cfg := &Config{}
	setDefaults(cfg)
	if got := cfg.ResolvedOutputDir(); got != filepath.Join(workspace, "output") {
		t.Fatalf("output dir = %q, want existing workspace output", got)
	}
}

func TestWorkspaceSlugSeparatesSameNamedProjects(t *testing.T) {
	a := workspaceSlug("/one/forge")
	b := workspaceSlug("/two/forge")
	if a == b {
		t.Fatalf("slugs collide: %q", a)
	}
	if !strings.HasPrefix(a, "forge-") {
		t.Fatalf("slug = %q, want readable base name", a)
	}
}
