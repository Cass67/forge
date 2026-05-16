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

func TestPersistencePolicyRedactsSecretLookingValues(t *testing.T) {
	policy := DefaultPersistencePolicy()
	item := protocol.Item{Kind: protocol.ItemToolCall, ToolCall: &protocol.ToolCallItem{Args: map[string]any{"token": "sk-secret-value"}}}
	out := policy.Apply(item)
	if out.ToolCall.Args["token"] == "sk-secret-value" {
		t.Fatalf("secret-looking arg was not redacted: %#v", out.ToolCall.Args)
	}
}
