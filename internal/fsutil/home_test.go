package fsutil

import (
	"path/filepath"
	"testing"
)

func TestForgeConfigPathPrefersXDGConfigHome(t *testing.T) {
	t.Setenv("HOME", "/tmp/home-do-not-use")
	t.Setenv("XDG_CONFIG_HOME", "/tmp/forge-test-config")

	got := ForgeConfigPath("auth.json")
	want := filepath.Join("/tmp/forge-test-config", "forge", "auth.json")
	if got != want {
		t.Fatalf("ForgeConfigPath() = %q, want %q", got, want)
	}
}
