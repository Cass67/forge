package react

import (
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
