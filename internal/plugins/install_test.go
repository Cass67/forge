package plugins

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallLocalPluginIntoVersionedCache(t *testing.T) {
	src := testPluginSource(t, "demo", "1.0.0")
	store := NewInstallStore(filepath.Join(t.TempDir(), "forge"))

	installed, err := store.InstallLocal(src, InstallOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if installed.Name != "demo" || installed.Version != "1.0.0" {
		t.Fatalf("installed = %#v", installed)
	}
	if _, err := os.Stat(filepath.Join(store.Root, "plugins", "cache", "demo", "1.0.0", ManifestFilename)); err != nil {
		t.Fatalf("cache manifest missing: %v", err)
	}
}

func TestInstallLocalPluginReusesSameSourceAndVersion(t *testing.T) {
	src := testPluginSource(t, "demo", "1.0.0")
	store := NewInstallStore(filepath.Join(t.TempDir(), "forge"))

	first, err := store.InstallLocal(src, InstallOptions{})
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.InstallLocal(src, InstallOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if first.CachePath != second.CachePath {
		t.Fatalf("cache paths differ: %q %q", first.CachePath, second.CachePath)
	}
}

func TestInstallLocalPluginRejectsChangedSourceSameVersionWithoutForce(t *testing.T) {
	src1 := testPluginSource(t, "demo", "1.0.0")
	src2 := testPluginSource(t, "demo", "1.0.0")
	store := NewInstallStore(filepath.Join(t.TempDir(), "forge"))
	if _, err := store.InstallLocal(src1, InstallOptions{}); err != nil {
		t.Fatal(err)
	}

	_, err := store.InstallLocal(src2, InstallOptions{})
	if err == nil || !strings.Contains(err.Error(), "already installed") {
		t.Fatalf("expected same-version source error, got %v", err)
	}
	if _, err := store.InstallLocal(src2, InstallOptions{Force: true}); err != nil {
		t.Fatalf("force install failed: %v", err)
	}
}

func TestInstallMetadataDoesNotStoreEnvValues(t *testing.T) {
	src := testPluginSource(t, "demo", "1.0.0")
	writeManifest(t, src, `{"name":"demo","version":"1.0.0","mcp_servers":{"demo":{"command":["node","server.js"],"env":{"TOKEN":"should-not-persist"}}}}`)
	store := NewInstallStore(filepath.Join(t.TempDir(), "forge"))
	if _, err := store.InstallLocal(src, InstallOptions{}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(store.MetadataPath())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "should-not-persist") {
		t.Fatalf("metadata persisted env value: %s", string(data))
	}
}

func testPluginSource(t *testing.T, name, version string) string {
	t.Helper()
	dir := t.TempDir()
	writeManifest(t, dir, `{"name":"`+name+`","version":"`+version+`"}`)
	return dir
}
