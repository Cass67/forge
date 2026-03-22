package tui

import (
	"strings"
	"testing"

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
