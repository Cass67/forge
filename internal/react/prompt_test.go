package react

import (
	"fmt"
	"strings"
	"testing"

	"forge/internal/llm"
)

func TestBuildPromptTrimsInput(t *testing.T) {
	if got := BuildPrompt("  inspect repo  "); got != "inspect repo" {
		t.Fatalf("BuildPrompt() = %q", got)
	}
}

func TestSessionMessagesIncludeCompactionSummaryContext(t *testing.T) {
	session := NewSession()
	first := session.RecordInput("prompt 1")
	session.AppendAssistantMessage("answer 1")
	session.CompleteTurn(first, "answer 1", nil, nil)

	second := session.RecordInput("prompt 2")
	session.AppendAssistantMessage("answer 2")
	session.CompleteTurn(second, "answer 2", nil, nil)

	if !CompactSessionHistory(session, 1) {
		t.Fatal("expected compaction")
	}

	messages := session.Messages("system prompt")
	if len(messages) < 4 {
		t.Fatalf("messages = %#v", messages)
	}
	if messages[0].Role != llm.RoleSystem || messages[0].Content != "system prompt" {
		t.Fatalf("system message = %#v", messages[0])
	}
	if messages[1].Role != llm.RoleSystem {
		t.Fatalf("summary message role = %q, want system", messages[1].Role)
	}
	if !strings.Contains(messages[1].Content, "Earlier conversation summary") {
		t.Fatalf("summary message = %q", messages[1].Content)
	}
	if !strings.Contains(messages[1].Content, "user: prompt 1") {
		t.Fatalf("summary message missing compacted turn detail: %q", messages[1].Content)
	}
	if !strings.Contains(messages[1].Content, "outcome: answer 1") {
		t.Fatalf("summary message missing semantic outcome detail: %q", messages[1].Content)
	}
	if messages[2].Role != llm.RoleUser || messages[2].Content != "prompt 2" {
		t.Fatalf("remaining user message = %#v", messages[2])
	}
	if messages[3].Role != llm.RoleAssistant || messages[3].Content != "answer 2" {
		t.Fatalf("remaining assistant message = %#v", messages[3])
	}
}

func TestBuildMessages_LargeToolResultTruncated(t *testing.T) {
	lines := make([]string, 100)
	for i := range lines {
		lines[i] = fmt.Sprintf("output line %d", i+1)
	}
	bigResult := strings.Join(lines, "\n")

	snap := SessionSnapshot{
		History: []llm.Message{
			{Role: llm.RoleUser, Content: "run something"},
			{Role: llm.RoleAssistant, ToolCalls: []llm.NativeToolCall{{ID: "c1", Name: "run_command", ArgsJSON: `{}`}}},
			{Role: llm.RoleTool, ToolCallID: "c1", Content: bigResult},
		},
	}
	msgs := BuildMessages("sys", snap)

	var toolMsg *llm.Message
	for i := range msgs {
		if msgs[i].Role == llm.RoleTool {
			toolMsg = &msgs[i]
			break
		}
	}
	if toolMsg == nil {
		t.Fatal("no tool message in output")
	}
	// Middle lines must be absent from LLM context
	if strings.Contains(toolMsg.Content, "output line 50") {
		t.Error("middle lines should be truncated from LLM context")
	}
	// Head and tail must be preserved
	if !strings.Contains(toolMsg.Content, "output line 1") {
		t.Error("head lines should be present in LLM context")
	}
	if !strings.Contains(toolMsg.Content, "output line 100") {
		t.Error("tail lines should be present in LLM context")
	}
	// Truncation marker must appear
	if !strings.Contains(toolMsg.Content, "lines truncated)") {
		t.Error("truncation marker should be present")
	}
	// Original snapshot must not be mutated — truncateToolResults must copy
	if !strings.Contains(snap.History[2].Content, "output line 50") {
		t.Error("original snapshot history must not be mutated by BuildMessages")
	}
}

func TestBuildMessages_IncludesRuntimeNoteAsSystemMessage(t *testing.T) {
	snap := SessionSnapshot{
		RuntimeNote: "Git merge workflow active. Resolve unmerged files before retrying commit.",
		History: []llm.Message{
			{Role: llm.RoleUser, Content: "merge the branch"},
		},
	}

	msgs := BuildMessages("sys", snap)
	if len(msgs) < 3 {
		t.Fatalf("messages = %#v", msgs)
	}
	if msgs[1].Role != llm.RoleSystem {
		t.Fatalf("runtime note role = %q, want system", msgs[1].Role)
	}
	if !strings.Contains(msgs[1].Content, "Git merge workflow active") {
		t.Fatalf("runtime note = %q", msgs[1].Content)
	}
}
