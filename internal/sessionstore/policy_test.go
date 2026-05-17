package sessionstore

import (
	"strings"
	"testing"

	"forge/internal/protocol"
)

func TestPersistencePolicyTruncatesLargeToolResult(t *testing.T) {
	policy := DefaultPersistencePolicy()
	item := protocol.Item{Kind: protocol.ItemToolResult, ToolResult: &protocol.ToolResultItem{Text: strings.Repeat("x", policy.MaxToolResultBytes+10)}}
	out := policy.Apply(item)
	if !out.ToolResult.Truncated || len(out.ToolResult.Text) > policy.MaxToolResultBytes {
		t.Fatalf("tool result was not truncated: %#v", out.ToolResult)
	}
}

func TestPersistencePolicyPreservesOutputHandleMetadataWhenTruncating(t *testing.T) {
	policy := DefaultPersistencePolicy()
	item := protocol.Item{Kind: protocol.ItemToolResult, ToolResult: &protocol.ToolResultItem{
		Text:          strings.Repeat("x", policy.MaxToolResultBytes+10),
		Handle:        "thread-1/abc123",
		OriginalBytes: 42,
		SHA256:        "abc123",
	}}

	out := policy.Apply(item)

	if !out.ToolResult.Truncated {
		t.Fatalf("tool result was not truncated: %#v", out.ToolResult)
	}
	if out.ToolResult.Handle != "thread-1/abc123" || out.ToolResult.SHA256 != "abc123" || out.ToolResult.OriginalBytes != 42 {
		t.Fatalf("metadata changed during truncation: %#v", out.ToolResult)
	}
}

func TestPersistencePolicyRedactsSecretLookingValues(t *testing.T) {
	policy := DefaultPersistencePolicy()
	item := protocol.Item{Kind: protocol.ItemToolCall, ToolCall: &protocol.ToolCallItem{Args: map[string]any{"token": "sk-secret-value"}}}
	out := policy.Apply(item)
	if out.ToolCall.Args["token"] == "sk-secret-value" {
		t.Fatalf("secret-looking arg was not redacted: %#v", out.ToolCall.Args)
	}
}
