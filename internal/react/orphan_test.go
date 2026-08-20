package react

import (
	"testing"

	"forge/internal/llm"
)

// A partially answered assistant message is dropped whole, which strands the
// results that did arrive. Providers reject a tool result with no preceding
// tool call just as firmly as the reverse.
func TestPromptDropsResultsOrphanedByDroppingTheirCall(t *testing.T) {
	messages := []llm.Message{
		{Role: llm.RoleUser, Content: "do two things"},
		{Role: llm.RoleAssistant, ToolCalls: []llm.NativeToolCall{
			{ID: "a", Name: "read_file"},
			{ID: "b", Name: "read_file"},
		}},
		{Role: llm.RoleTool, ToolCallID: "a", Content: "first result"},
	}

	got := dropOrphanedToolCalls(messages)

	for _, msg := range got {
		if msg.Role == llm.RoleTool {
			t.Fatalf("tool result %q survived without its call: %#v", msg.ToolCallID, got)
		}
	}
}

func TestPromptKeepsFullyPairedToolCalls(t *testing.T) {
	messages := []llm.Message{
		{Role: llm.RoleUser, Content: "do two things"},
		{Role: llm.RoleAssistant, ToolCalls: []llm.NativeToolCall{
			{ID: "a", Name: "read_file"},
			{ID: "b", Name: "read_file"},
		}},
		{Role: llm.RoleTool, ToolCallID: "a", Content: "first"},
		{Role: llm.RoleTool, ToolCallID: "b", Content: "second"},
	}
	if got := dropOrphanedToolCalls(messages); len(got) != len(messages) {
		t.Fatalf("paired conversation was altered: %#v", got)
	}
}
