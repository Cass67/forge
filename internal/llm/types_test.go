package llm_test

import (
	"forge/internal/llm"
	"testing"
)

func TestRoleConstants(t *testing.T) {
	if llm.RoleSystem != "system" {
		t.Fatalf("expected system, got %s", llm.RoleSystem)
	}
	if llm.RoleUser != "user" {
		t.Fatalf("expected user, got %s", llm.RoleUser)
	}
	if llm.RoleAssistant != "assistant" {
		t.Fatalf("expected assistant, got %s", llm.RoleAssistant)
	}
}

func TestNativeToolCallZeroValue(t *testing.T) {
	var tc llm.NativeToolCall
	if tc.ID != "" || tc.Name != "" || tc.ArgsJSON != "" {
		t.Fatal("NativeToolCall zero value should be empty")
	}
}

func TestTokenWithToolCall(t *testing.T) {
	tc := &llm.NativeToolCall{ID: "c1", Name: "read_file", ArgsJSON: `{"path":"go.mod"}`}
	tok := llm.Token{ToolCall: tc}
	if tok.Text != "" {
		t.Fatal("token text should be empty when carrying tool call")
	}
	if tok.ToolCall == nil || tok.ToolCall.Name != "read_file" {
		t.Fatal("token should carry tool call")
	}
}

func TestMessageRoleToolZeroValue(t *testing.T) {
	m := llm.Message{Role: llm.RoleTool, ToolCallID: "c1", Content: "result"}
	if m.Role != llm.RoleTool {
		t.Fatal("role mismatch")
	}
}

func TestMessageAssistantWithToolCalls(t *testing.T) {
	m := llm.Message{
		Role: llm.RoleAssistant,
		ToolCalls: []llm.NativeToolCall{
			{ID: "c1", Name: "run_command", ArgsJSON: `{"command":"ls"}`},
		},
	}
	if len(m.ToolCalls) != 1 {
		t.Fatalf("want 1 tool call, got %d", len(m.ToolCalls))
	}
}

func TestEventKindConstants(t *testing.T) {
	kinds := []llm.EventKind{
		llm.EventToken,
		llm.EventRetry,
		llm.EventRoundEnd,
		llm.EventPassEnd,
		llm.EventError,
		llm.EventDone,
		llm.EventAbort,
		llm.EventAgentTask,
	}
	for _, k := range kinds {
		if k == "" {
			t.Fatal("event kind must not be empty")
		}
	}
}
