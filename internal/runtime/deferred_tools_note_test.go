package runtime

import (
	"context"
	"strings"
	"testing"

	"forge/internal/agent/tools"
	"forge/internal/config"
)

func noteRegistry(names ...string) *tools.Registry {
	reg := tools.NewRegistry()
	for _, name := range names {
		reg.Register(tools.Tool{
			Name:    name,
			Execute: func(context.Context, map[string]any) (string, error) { return "", nil },
		})
	}
	return reg
}

// A server connecting mid-session must not rewrite the system prompt: the
// prompt sits ahead of the whole conversation, so an edit costs a full
// re-prefill of history on every provider that caches by prefix.
func TestDeferredToolsNoteIsStableWhenMCPConnects(t *testing.T) {
	servers := []string{"context7"}
	before := deferredToolsNote(noteRegistry("read_file", "apply_patch"), servers)
	after := deferredToolsNote(noteRegistry("read_file", "apply_patch",
		"mcp__context7__query-docs", "mcp__context7__resolve-library-id"), servers)
	if before != after {
		t.Fatalf("note changed when MCP tools registered:\n before: %q\n after:  %q", before, after)
	}
	if !strings.Contains(before, "context7") {
		t.Errorf("configured server should be named up front: %q", before)
	}
	if strings.Contains(before, "query-docs") {
		t.Errorf("individual MCP tools must not be listed: %q", before)
	}
	if !strings.Contains(before, "apply_patch") {
		t.Errorf("non-MCP deferred tools should still be listed: %q", before)
	}
}

func TestDeferredToolsNoteWithoutMCP(t *testing.T) {
	if note := deferredToolsNote(noteRegistry("read_file"), nil); note != "" {
		t.Errorf("core-only registry should produce no note, got %q", note)
	}
	note := deferredToolsNote(noteRegistry("read_file"), []string{"context7"})
	if !strings.Contains(note, "Additional Tools") || !strings.Contains(note, "context7") {
		t.Errorf("MCP-only note malformed: %q", note)
	}
}

func TestMCPServerNamesSorted(t *testing.T) {
	off := false
	cfg := &config.Config{MCPServers: map[string]config.MCPServerConfig{
		"zeta":    {},
		"alpha":   {},
		"skipped": {Enabled: &off},
	}}
	got := mcpServerNames(cfg)
	if len(got) != 2 || got[0] != "alpha" || got[1] != "zeta" {
		t.Fatalf("got %v, want [alpha zeta]", got)
	}
	if mcpServerNames(nil) != nil {
		t.Error("nil config should yield no names")
	}
}
