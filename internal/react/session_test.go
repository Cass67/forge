package react

import (
	"testing"

	"forge/internal/llm"
)

func TestAppendAssistantWithToolCalls(t *testing.T) {
	s := NewSession()
	s.RecordInput("check the repo")
	calls := []llm.NativeToolCall{
		{ID: "c1", Name: "git_status", ArgsJSON: `{}`},
		{ID: "c2", Name: "run_command", ArgsJSON: `{"command":"ls"}`},
	}
	s.AppendAssistantWithToolCalls(calls)

	snap := s.Snapshot()
	if len(snap.History) != 2 {
		t.Fatalf("want 2 history entries, got %d", len(snap.History))
	}
	last := snap.History[1]
	if last.Role != llm.RoleAssistant {
		t.Fatalf("role = %q, want assistant", last.Role)
	}
	if len(last.ToolCalls) != 2 {
		t.Fatalf("tool calls = %d, want 2", len(last.ToolCalls))
	}
	if last.ToolCalls[0].ID != "c1" || last.ToolCalls[0].Name != "git_status" {
		t.Fatal("first tool call mismatch")
	}
}

func TestAppendNativeToolResult(t *testing.T) {
	s := NewSession()
	s.RecordInput("run ls")
	s.AppendAssistantWithToolCalls([]llm.NativeToolCall{
		{ID: "c1", Name: "run_command", ArgsJSON: `{"command":"ls"}`},
	})
	s.AppendNativeToolResult("c1", "file1.go\nfile2.go")

	snap := s.Snapshot()
	if len(snap.History) != 3 {
		t.Fatalf("want 3 history entries, got %d", len(snap.History))
	}
	result := snap.History[2]
	if result.Role != llm.RoleTool {
		t.Fatalf("role = %q, want tool", result.Role)
	}
	if result.ToolCallID != "c1" {
		t.Fatalf("tool_call_id = %q, want c1", result.ToolCallID)
	}
	if result.Content != "file1.go\nfile2.go" {
		t.Fatalf("content = %q", result.Content)
	}
}

func TestAppendNativeToolResultGuardsEmptyID(t *testing.T) {
	s := NewSession()
	s.RecordInput("check")
	s.AppendAssistantWithToolCalls([]llm.NativeToolCall{{ID: "c1", Name: "git_status", ArgsJSON: "{}"}})
	s.AppendNativeToolResult("", "result")
	snap := s.Snapshot()
	// Empty toolCallID should be ignored — only user+assistant in history
	if len(snap.History) != 2 {
		t.Fatalf("empty toolCallID should be ignored, got %d history entries", len(snap.History))
	}
}

func TestMessagesIncludesToolRoleMessages(t *testing.T) {
	s := NewSession()
	s.RecordInput("check status")
	s.AppendAssistantWithToolCalls([]llm.NativeToolCall{
		{ID: "c1", Name: "git_status", ArgsJSON: `{}`},
	})
	s.AppendNativeToolResult("c1", "nothing to commit")

	msgs := s.Messages("system prompt")
	// system + user + assistant(tool_calls) + tool(result)
	if len(msgs) != 4 {
		t.Fatalf("want 4 messages, got %d\nhistory: %+v", len(msgs), msgs)
	}
	if msgs[2].Role != llm.RoleAssistant || len(msgs[2].ToolCalls) != 1 {
		t.Fatal("message 3 should be assistant with tool calls")
	}
	if msgs[3].Role != llm.RoleTool || msgs[3].ToolCallID != "c1" {
		t.Fatal("message 4 should be tool result with correct ID")
	}
}
