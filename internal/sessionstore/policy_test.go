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
	item := protocol.Item{Kind: protocol.ItemToolCall, ToolCall: &protocol.ToolCallItem{Args: map[string]any{"token": strings.Join([]string{"sk", "secret", "value"}, "-")}}}
	out := policy.Apply(item)
	if out.ToolCall.Args["token"] == strings.Join([]string{"sk", "secret", "value"}, "-") {
		t.Fatalf("secret-looking arg was not redacted: %#v", out.ToolCall.Args)
	}
}

func TestPersistencePolicyRedactsSecretTextFields(t *testing.T) {
	secret := "API_" + "KEY" + "=" + strings.Repeat("a", 16)
	policy := DefaultPersistencePolicy()
	item := protocol.Item{
		Kind:        protocol.ItemToolResult,
		Message:     &protocol.MessageItem{Text: "user pasted " + secret},
		ToolResult:  &protocol.ToolResultItem{Text: "command printed " + secret, Diff: "diff contained " + secret},
		TurnContext: &protocol.TurnContextItem{Input: "queued " + secret},
		Compaction:  &protocol.CompactionItem{Summary: "summary " + secret},
		Failure:     &protocol.FailureItem{Decision: protocol.FailureDecision{Feedback: "failure " + secret}},
	}
	out := policy.Apply(item)
	for name, text := range map[string]string{
		"message":      out.Message.Text,
		"diff":         out.ToolResult.Diff,
		"tool_result":  out.ToolResult.Text,
		"turn_context": out.TurnContext.Input,
		"compaction":   out.Compaction.Summary,
		"failure":      out.Failure.Decision.Feedback,
	} {
		if strings.Contains(text, secret) {
			t.Fatalf("%s leaked secret: %q", name, text)
		}
	}
}

func TestPersistencePolicyDoesNotPanicWhenRedactionShortensLargeToolResult(t *testing.T) {
	policy := PersistencePolicy{MaxToolResultBytes: 30}
	item := protocol.Item{Kind: protocol.ItemToolResult, ToolResult: &protocol.ToolResultItem{Text: strings.Join([]string{"sk", "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMN"}, "-")}}
	out := policy.Apply(item)
	if strings.Contains(out.ToolResult.Text, "abcdefghijklmnopqrstuvwxyz") {
		t.Fatalf("secret leaked after redaction: %#v", out.ToolResult)
	}
}
