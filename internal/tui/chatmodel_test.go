package tui

import (
	"testing"

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
