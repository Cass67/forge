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
	if !strings.ContainsAny(view, "╭╮╰╯") {
		t.Fatalf("expected designed composer chrome in default view, got:\n%s", view)
	}

	lines := strings.Split(rawView, "\n")
	promptLine := -1
	for i, line := range lines {
		if strings.Contains(strippedLine(line), "Prompt") {
			promptLine = i
		}
	}
	if promptLine <= 0 {
		t.Fatalf("expected composer prompt line in:\n%s", view)
	}
	if !strings.Contains(strippedLine(lines[promptLine+1]), "Type a message or /help") {
		t.Fatalf("expected composer body below prompt line in:\n%s", view)
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

func TestDebugSurfaceShowsVisibleTraceChromeByDefault(t *testing.T) {
	m := NewChatModel(ChatLiveConfig{
		Model:        "test-model",
		WorkDir:      "/tmp",
		DebugEnabled: true,
		DebugLogPath: "/tmp/forge-chat-debug.jsonl",
		SurfaceKind:  ChatSurfaceDebug,
	})
	m.width = 100
	m.height = 24
	setToolsContent(&m, "tool_call read_file\nstatus: complete\n")

	view := m.View()
	if !strings.Contains(view, "Debug trace") {
		t.Fatalf("expected debug surface to render visible trace chrome, got:\n%s", strippedLine(view))
	}
	if !strings.Contains(view, "tool_call read_file") {
		t.Fatalf("expected debug surface to render trace content, got:\n%s", strippedLine(view))
	}
	if !strings.Contains(view, "/tmp/forge-chat-debug.jsonl") {
		t.Fatalf("expected debug surface to show debug log path, got:\n%s", strippedLine(view))
	}
}

func TestDefaultChatViewEmptyStateOnlyShowsReadyCopy(t *testing.T) {
	m := NewChatModel(ChatLiveConfig{Model: "test-model", WorkDir: "/tmp"})
	m.width = 80
	m.height = 24

	view := strippedLine(m.View())
	if !strings.Contains(view, "Forge is ready.") {
		t.Fatalf("expected ready copy in default empty view:\n%s", view)
	}
	if strings.Contains(view, "Ask for a code change, bugfix, or investigation.") {
		t.Fatalf("old helper line should be gone:\n%s", view)
	}
}
