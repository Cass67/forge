package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestDefaultChatViewSingleColumnLayout(t *testing.T) {
	m := NewChatModel(ChatLiveConfig{Model: "test-model", WorkDir: "/tmp"})
	m.width = 100
	m.height = 24
	m.toolsVisible = true
	m.traceVisible = true
	m.AddMessage(ChatMessage{Kind: MsgAgent, Header: "Forge • 12:00:00", Content: "latest transcript line"})
	setToolsContent(&m, "tool output should stay hidden")

	rawView := m.View()
	view := strippedLine(rawView)
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

	lines := strings.Split(rawView, "\n")
	lastNonEmpty := ""
	for i := len(lines) - 1; i >= 0; i-- {
		if trimmed := strings.TrimSpace(strippedLine(lines[i])); trimmed != "" {
			lastNonEmpty = trimmed
			break
		}
	}
	if !strings.Contains(lastNonEmpty, "Type a message or /help") {
		t.Fatalf("expected composer to be the bottom-most rendered element, got last line %q in:\n%s", lastNonEmpty, view)
	}
}

func TestDefaultChatViewShowsFlashAboveComposer(t *testing.T) {
	m := NewChatModel(ChatLiveConfig{Model: "test-model", WorkDir: "/tmp"})
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 16})
	m = updated.(ChatModel)
	m.AddMessage(ChatMessage{Kind: MsgAgent, Header: "Forge • 12:00:00", Content: "latest transcript line"})

	m.inputBuf = "/trace"
	m.inputPos = len(m.inputBuf)
	updated, _ = m.submitInput()
	m = updated.(ChatModel)

	if got := m.flash; got != "trace unavailable without -d" {
		t.Fatalf("flash = %q", got)
	}

	view := m.View()
	if count := strings.Count(strippedLine(view), "trace unavailable without -d"); count != 1 {
		t.Fatalf("expected flash once in default view, found %d in:\n%s", count, strippedLine(view))
	}

	lines := strings.Split(view, "\n")
	promptLine := -1
	flashLine := -1
	for i, line := range lines {
		stripped := strippedLine(line)
		if strings.Contains(stripped, "Prompt") {
			promptLine = i
		}
		if strings.Contains(stripped, "trace unavailable without -d") {
			flashLine = i
		}
	}
	if promptLine <= 0 || flashLine < 0 {
		t.Fatalf("expected flash slot and composer, got:\n%s", strippedLine(view))
	}
	if flashLine != promptLine-1 {
		t.Fatalf("expected flash directly above composer, got flash line %d prompt line %d in:\n%s", flashLine, promptLine, strippedLine(view))
	}
}
