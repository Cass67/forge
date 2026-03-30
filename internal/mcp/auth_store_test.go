package mcp

import (
	"os"
	"path/filepath"
	"testing"
)

func TestBearerTokenStoreLifecycle(t *testing.T) {
	oldXDG := os.Getenv("XDG_CONFIG_HOME")
	tmp := t.TempDir()
	if err := os.Setenv("XDG_CONFIG_HOME", tmp); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = os.Setenv("XDG_CONFIG_HOME", oldXDG)
	}()

	if err := SaveBearerToken("context7", "secret-token"); err != nil {
		t.Fatalf("SaveBearerToken() error = %v", err)
	}
	token, ok, err := BearerToken("context7")
	if err != nil {
		t.Fatalf("BearerToken() error = %v", err)
	}
	if !ok || token != "secret-token" {
		t.Fatalf("BearerToken() = (%q, %t)", token, ok)
	}
	info, err := os.Stat(filepath.Join(tmp, "forge", "mcp_tokens.json"))
	if err != nil {
		t.Fatalf("os.Stat() error = %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("token store perms = %#o", info.Mode().Perm())
	}

	if err := DeleteBearerToken("context7"); err != nil {
		t.Fatalf("DeleteBearerToken() error = %v", err)
	}
	_, ok, err = BearerToken("context7")
	if err != nil {
		t.Fatalf("BearerToken() after delete error = %v", err)
	}
	if ok {
		t.Fatal("expected token to be deleted")
	}
}
