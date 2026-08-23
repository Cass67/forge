package lsp

import (
	"testing"

	"forge/internal/config"
)

func boolPtr(v bool) *bool { return &v }

func TestServersFromConfig(t *testing.T) {
	servers := ServersFromConfig(config.LSPConfig{Servers: map[string]config.LSPServerConfig{
		// Pinning a binary must keep the extensions that made routing work.
		"go":   {Command: []string{"/opt/gopls", "-rpc.trace"}},
		"zig":  {Command: []string{"zls"}, Extensions: []string{"zig"}},
		"yaml": {Enabled: boolPtr(false)},
		// A new language with no command has nothing to spawn.
		"ruby": {Extensions: []string{".rb"}},
	}})

	goServer, ok := servers["go"]
	if !ok {
		t.Fatal("go server dropped")
	}
	if goServer.Command != "/opt/gopls" {
		t.Fatalf("go command = %q", goServer.Command)
	}
	if len(goServer.Args) != 1 || goServer.Args[0] != "-rpc.trace" {
		t.Fatalf("go args = %#v", goServer.Args)
	}
	if len(goServer.Extensions) != 1 || goServer.Extensions[0] != ".go" {
		t.Fatalf("go extensions = %#v, want the built-in set retained", goServer.Extensions)
	}

	if _, ok := servers["yaml"]; ok {
		t.Fatal("yaml stayed after enabled = false")
	}
	if _, ok := servers["ruby"]; ok {
		t.Fatal("commandless language was kept")
	}

	zig, ok := servers["zig"]
	if !ok {
		t.Fatal("zig server missing")
	}
	if zig.LanguageID != "zig" {
		t.Fatalf("zig language id = %q, want the map key", zig.LanguageID)
	}
	if len(zig.Extensions) != 1 || zig.Extensions[0] != ".zig" {
		t.Fatalf("zig extensions = %#v, want a leading dot added", zig.Extensions)
	}

	if cfg, ok := matchServer(servers, "main.zig"); !ok || cfg.Command != "zls" {
		t.Fatalf("zig routing = %#v, %v", cfg, ok)
	}
	if _, ok := matchServer(servers, "deploy.yaml"); ok {
		t.Fatal("disabled language still routes")
	}
}
