package tui

import (
	"strings"
	"testing"
)

func TestDefaultChatViewSingleColumnLayout(t *testing.T) {
	m := NewChatModel(ChatLiveConfig{Model: "test-model", WorkDir: "/tmp"})
	m.width = 100
	m.height = 24
	m.toolsVisible = true
	m.traceVisible = true
	m.AddMessage(ChatMessage{Kind: MsgAgent, Header: "Forge • 12:00:00", Content: "latest transcript line"})
	setToolsContent(&m, "tool output should stay hidden")

	view := strippedLine(m.View())
	if !strings.Contains(view, "latest transcript line") {
		t.Fatalf("expected transcript content in default view, got:\n%s", view)
	}
	if !strings.Contains(view, "Prompt") {
		t.Fatalf("expected composer at the bottom, got:\n%s", view)
	}
	if strings.Contains(view, "tool output should stay hidden") || strings.Contains(view, "Debug trace") {
		t.Fatalf("default view leaked debug chrome: %s", view)
	}
	if strings.ContainsAny(view, "╭╮╰╯") {
		t.Fatalf("default view should stay single-column without pane chrome, got:\n%s", view)
	}
}
