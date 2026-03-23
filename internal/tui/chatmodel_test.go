package tui

import (
	"os"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"forge/internal/llm"
)

func TestChatModelInit(t *testing.T) {
	m := NewChatModel(ChatLiveConfig{Model: "test-model", WorkDir: "/tmp"})
	cmd := m.Init()
	if cmd == nil {
		t.Fatal("Init should return a command")
	}
}

func TestChatModelAddMessage(t *testing.T) {
	m := NewChatModel(ChatLiveConfig{Model: "test-model", WorkDir: "/tmp"})
	m.AddMessage(ChatMessage{Kind: MsgUser, Header: "You • 12:00:00", Content: "hello"})
	if len(m.messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(m.messages))
	}
	if m.messages[0].Content != "hello" {
		t.Fatalf("message content = %q", m.messages[0].Content)
	}
}

func TestChatModelViewNotEmpty(t *testing.T) {
	m := NewChatModel(ChatLiveConfig{Model: "test-model", WorkDir: "/tmp"})
	m.width = 80
	m.height = 24
	m.AddMessage(ChatMessage{Kind: MsgUser, Header: "You • 12:00:00", Content: "hello"})
	v := m.View()
	if v == "" {
		t.Fatal("View() should not be empty")
	}
}

func TestChatModelHandlesTokenEvent(t *testing.T) {
	m := NewChatModel(ChatLiveConfig{Model: "test", WorkDir: "/tmp"})
	m.width = 80
	m.height = 24

	ev := llm.Event{Kind: llm.EventToken, Text: "Hello "}
	updated, _ := m.Update(ev)
	m = updated.(ChatModel)

	ev2 := llm.Event{Kind: llm.EventToken, Text: "world"}
	updated, _ = m.Update(ev2)
	m = updated.(ChatModel)

	if len(m.messages) != 1 {
		t.Fatalf("expected 1 agent message, got %d", len(m.messages))
	}
	if m.messages[0].Content != "Hello world" {
		t.Fatalf("content = %q, want %q", m.messages[0].Content, "Hello world")
	}
}

func TestChatModelHandlesDoneEvent(t *testing.T) {
	m := NewChatModel(ChatLiveConfig{Model: "test", WorkDir: "/tmp"})
	m.width = 80
	m.height = 24
	m.busy = true

	ev := llm.Event{Kind: llm.EventDone}
	updated, _ := m.Update(ev)
	m = updated.(ChatModel)

	if m.busy {
		t.Fatal("expected busy=false after done event")
	}
	found := false
	for _, msg := range m.messages {
		if msg.Kind == MsgStatus {
			found = true
		}
	}
	if !found {
		t.Fatal("expected status message after done")
	}
}

func TestChatModelHandlesToolCallEvent(t *testing.T) {
	m := NewChatModel(ChatLiveConfig{Model: "test", WorkDir: "/tmp"})
	m.width = 80
	m.height = 24

	ev := llm.Event{Kind: llm.EventToolCall, Text: "read_file", Content: `{"path":"main.go"}`}
	updated, _ := m.Update(ev)
	m = updated.(ChatModel)

	if m.toolsBuf == "" {
		t.Fatal("expected tools buffer to have content")
	}
}

func TestChatModelSlashClear(t *testing.T) {
	m := NewChatModel(ChatLiveConfig{Model: "test", WorkDir: "/tmp"})
	m.width = 80
	m.height = 24
	m.AddMessage(ChatMessage{Kind: MsgUser, Header: "You", Content: "hello"})
	m.AddMessage(ChatMessage{Kind: MsgAgent, Content: "hi"})

	m.inputBuf = "/clear"
	m.inputPos = 6
	updated, _ := m.submitInput()
	m = updated.(ChatModel)

	if len(m.messages) != 0 {
		t.Fatalf("expected 0 messages after /clear, got %d", len(m.messages))
	}
}

func TestChatModelSlashExit(t *testing.T) {
	m := NewChatModel(ChatLiveConfig{Model: "test", WorkDir: "/tmp"})
	m.inputBuf = "/exit"
	m.inputPos = 5
	_, cmd := m.submitInput()
	if cmd == nil {
		t.Fatal("expected quit command from /exit")
	}
}

func TestChatModelSlashTheme(t *testing.T) {
	m := NewChatModel(ChatLiveConfig{Model: "test", WorkDir: "/tmp"})
	m.width = 80
	m.height = 24

	m.inputBuf = "/theme"
	m.inputPos = 6
	updated, _ := m.submitInput()
	m = updated.(ChatModel)

	if !m.lowContrast {
		t.Fatal("expected lowContrast=true after /theme")
	}
}

func TestChatModelSlashHelpOpensOverlay(t *testing.T) {
	m := NewChatModel(ChatLiveConfig{Model: "test", WorkDir: "/tmp"})
	m.width = 100
	m.height = 30

	m.inputBuf = "/help"
	m.inputPos = len("/help")
	updated, _ := m.submitInput()
	m = updated.(ChatModel)

	if !m.helpVisible {
		t.Fatal("expected help overlay to be visible after /help")
	}
	if len(m.messages) != 0 {
		t.Fatalf("expected /help not to append chat messages, got %d", len(m.messages))
	}
	v := m.View()
	if !strings.Contains(v, "Chat Commands") || !strings.Contains(v, "CLI Skills") {
		t.Fatalf("help overlay missing tabs: %s", v)
	}
}

func TestChatModelF1OpensAndEscClosesHelpOverlay(t *testing.T) {
	m := NewChatModel(ChatLiveConfig{Model: "test", WorkDir: "/tmp"})
	m.width = 100
	m.height = 30

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyF1})
	m = updated.(ChatModel)
	if !m.helpVisible {
		t.Fatal("expected F1 to open help overlay")
	}

	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEscape})
	m = updated.(ChatModel)
	if m.helpVisible {
		t.Fatal("expected Esc to close help overlay")
	}
}

func TestChatModelHelpOverlayTabNavigation(t *testing.T) {
	m := NewChatModel(ChatLiveConfig{Model: "test", WorkDir: "/tmp"})
	m.width = 100
	m.height = 30
	m.helpVisible = true

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRight})
	m = updated.(ChatModel)
	if m.helpTab != 1 {
		t.Fatalf("expected helpTab=1, got %d", m.helpTab)
	}
	if !strings.Contains(m.View(), "/model <name>") {
		t.Fatal("expected chat commands help content on second tab")
	}

	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRight})
	m = updated.(ChatModel)
	if m.helpTab != 2 {
		t.Fatalf("expected helpTab=2, got %d", m.helpTab)
	}
	if !strings.Contains(strings.Join(m.helpLines(), "\n"), "forge skills install") {
		t.Fatal("expected CLI skills help content on third tab")
	}
}

func TestChatModelApprovalFlow(t *testing.T) {
	m := NewChatModel(ChatLiveConfig{Model: "test", WorkDir: "/tmp"})
	m.width = 80
	m.height = 24

	// Simulate approval request arriving
	updated, _ := m.Update(chatApprovalMsg{Tool: "write_file", Summary: "Write test.go"})
	m = updated.(ChatModel)

	if m.pendingApproval == nil {
		t.Fatal("expected pending approval")
	}

	v := m.View()
	if !strings.Contains(v, "write_file") {
		t.Fatalf("view should show pending approval tool name, got: %s", v)
	}

	// Approve with 'y'
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
	m = updated.(ChatModel)
	if m.pendingApproval != nil {
		t.Fatal("approval should be cleared after y")
	}
}

func TestChatModelApprovalDeny(t *testing.T) {
	m := NewChatModel(ChatLiveConfig{Model: "test", WorkDir: "/tmp"})
	m.width = 80
	m.height = 24

	updated, _ := m.Update(chatApprovalMsg{Tool: "write_file", Summary: "Write test.go"})
	m = updated.(ChatModel)

	// Deny with 'n'
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("n")})
	m = updated.(ChatModel)
	if m.pendingApproval != nil {
		t.Fatal("approval should be cleared after n")
	}
}

func TestChatModelToolsPaneVisible(t *testing.T) {
	m := NewChatModel(ChatLiveConfig{Model: "test", WorkDir: "/tmp"})
	m.width = 120
	m.height = 24
	m.toolsVisible = true
	m.toolsBuf = "● read_file {\"path\":\"main.go\"}\nstatus: ok\n"

	v := m.View()
	if !strings.Contains(v, "read_file") {
		t.Fatal("tools pane should show tool calls")
	}
}

func TestChatModelToolsPaneToggle(t *testing.T) {
	m := NewChatModel(ChatLiveConfig{Model: "test", WorkDir: "/tmp"})
	m.width = 120
	m.height = 24

	// Tools visible by default
	if !m.toolsVisible {
		t.Fatal("tools should be visible by default")
	}

	m.inputBuf = "/tools"
	m.inputPos = len("/tools")
	updated, _ := m.submitInput()
	m = updated.(ChatModel)

	if m.toolsVisible {
		t.Fatal("tools pane should be hidden after toggle")
	}
}

func TestChatModelViewShowsChatScrollbarWhenOverflowing(t *testing.T) {
	m := NewChatModel(ChatLiveConfig{Model: "test", WorkDir: "/tmp"})
	m.width = 100
	m.height = 16
	m.chatViewport.Width = m.chatPaneWidth()
	m.chatViewport.Height = 8

	for i := 0; i < 20; i++ {
		m.AddMessage(ChatMessage{Kind: MsgUser, Header: "You", Content: strings.Repeat("line ", 8)})
	}
	v := m.View()
	if !strings.Contains(v, "█") {
		t.Fatal("expected visible scrollbar thumb in chat pane")
	}
}

func TestChatModelViewShowsToolsScrollbarWhenOverflowing(t *testing.T) {
	m := NewChatModel(ChatLiveConfig{Model: "test", WorkDir: "/tmp"})
	m.width = 120
	m.height = 16
	m.toolsVisible = true
	m.toolsBuf = strings.Repeat("tool output line\n", 40)
	v := m.View()
	if !strings.Contains(v, "█") {
		t.Fatal("expected visible scrollbar thumb in tools pane")
	}
}

func TestChatModelAgentPaneStillRendersWhenToolsVisible(t *testing.T) {
	m := NewChatModel(ChatLiveConfig{Model: "test", WorkDir: "/tmp"})
	m.width = 120
	m.height = 24
	m.toolsVisible = true
	m.toolsBuf = "tool output"
	m.AddMessage(ChatMessage{Kind: MsgAgent, Header: "Forge", Content: "agent text should remain visible"})

	v := m.View()
	if !strings.Contains(v, "agent text should remain visible") {
		t.Fatalf("expected agent pane content to render with tools visible, got: %s", v)
	}
}

func TestChatModelAgentPaneStillRendersAfterToolsToggleOn(t *testing.T) {
	m := NewChatModel(ChatLiveConfig{Model: "test", WorkDir: "/tmp"})
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 24})
	m = updated.(ChatModel)
	m.toolsVisible = false
	m.toolsBuf = "tool output"
	m.AddMessage(ChatMessage{Kind: MsgAgent, Header: "Forge", Content: "agent text should remain visible after toggle"})

	m.inputBuf = "/tools"
	m.inputPos = len(m.inputBuf)
	updated, _ = m.submitInput()
	m = updated.(ChatModel)
	updated, _ = m.Update(chatTickMsg(time.Now()))
	m = updated.(ChatModel)

	v := m.View()
	if !strings.Contains(v, "agent text should remain visible after toggle") {
		t.Fatalf("expected agent pane content after tools toggle, got: %s", v)
	}
}

func TestChatModelSlashSessionsOpensOverlay(t *testing.T) {
	configDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configDir)
	m := NewChatModel(ChatLiveConfig{Model: "test", WorkDir: "/tmp"})
	m.width = 100
	m.height = 24
	if err := m.saveSession("example-session"); err != nil {
		t.Fatalf("saveSession: %v", err)
	}

	m.inputBuf = "/sessions"
	m.inputPos = len("/sessions")
	updated, _ := m.submitInput()
	m = updated.(ChatModel)

	if !m.sessionsVisible {
		t.Fatal("expected sessions overlay to be visible")
	}
	if len(m.sessionsList) == 0 || m.sessionsList[0].name != "example-session" {
		t.Fatalf("expected sessions picker to load saved session, got %#v", m.sessionsList)
	}
}

func TestChatModelMouseClickFocusesToolsPane(t *testing.T) {
	m := NewChatModel(ChatLiveConfig{Model: "test", WorkDir: "/tmp"})
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 24})
	m = updated.(ChatModel)
	m.toolsVisible = true
	m.toolsBuf = strings.Repeat("tool output line\n", 20)

	x := m.chatPaneWidth() + 1
	y := 2
	updated, _ = m.Update(tea.MouseMsg{X: x, Y: y, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	m = updated.(ChatModel)

	if m.paneFocus != focusTools {
		t.Fatal("expected mouse click in tools pane to focus tools")
	}
}

func TestChatModelMouseWheelScrollsToolsPane(t *testing.T) {
	m := NewChatModel(ChatLiveConfig{Model: "test", WorkDir: "/tmp"})
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 16})
	m = updated.(ChatModel)
	m.toolsVisible = true
	m.toolsBuf = strings.Repeat("tool output line\n", 80)

	x := m.chatPaneWidth() + 1
	y := 2
	updated, _ = m.Update(tea.MouseMsg{X: x, Y: y, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	m = updated.(ChatModel)
	updated, _ = m.Update(tea.MouseMsg{X: x, Y: y, Action: tea.MouseActionPress, Button: tea.MouseButtonWheelDown})
	m = updated.(ChatModel)

	if m.toolsScroll == 0 {
		t.Fatal("expected mouse wheel in tools pane to scroll tools")
	}
}

func TestChatModelMouseClickScrollbarScrollsToolsPane(t *testing.T) {
	m := NewChatModel(ChatLiveConfig{Model: "test", WorkDir: "/tmp"})
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 16})
	m = updated.(ChatModel)
	m.toolsVisible = true
	m.toolsBuf = strings.Repeat("tool output line\n", 80)
	updated, _ = m.Update(tea.WindowSizeMsg{Width: 120, Height: 16})
	m = updated.(ChatModel)

	toolsX := m.chatPaneWidth()
	toolsW := m.width - m.chatPaneWidth()
	scrollbarX := toolsX + max(1, toolsW-2)
	clickY := m.chatViewport.Height
	updated, _ = m.Update(tea.MouseMsg{X: scrollbarX, Y: clickY, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	m = updated.(ChatModel)

	if m.toolsScroll == 0 {
		t.Fatal("expected clicking tools scrollbar to change tools scroll")
	}
}

func TestChatModelSaveAndRestoreSessionCommands(t *testing.T) {
	configDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configDir)
	m := NewChatModel(ChatLiveConfig{Model: "test", WorkDir: "/tmp"})
	m.width = 100
	m.height = 24
	m.toolsBuf = "tool output"
	m.AddMessage(ChatMessage{Kind: MsgUser, Header: "You", Content: "hello"})

	m.inputBuf = "/save named-session"
	m.inputPos = len(m.inputBuf)
	updated, _ := m.submitInput()
	m = updated.(ChatModel)

	path, err := chatSessionFile("named-session")
	if err != nil {
		t.Fatalf("chatSessionFile: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected saved session file at %s: %v", path, err)
	}

	m.messages = nil
	m.chatContent = ""
	m.chatViewport.SetContent("")
	m.toolsBuf = ""
	m.inputBuf = "/restore named-session"
	m.inputPos = len(m.inputBuf)
	updated, _ = m.submitInput()
	m = updated.(ChatModel)

	if !strings.Contains(m.chatContent, "hello") {
		t.Fatal("expected restored chat content to include saved conversation")
	}
	if m.toolsBuf != "tool output" {
		t.Fatalf("expected restored toolsBuf, got %q", m.toolsBuf)
	}
}
