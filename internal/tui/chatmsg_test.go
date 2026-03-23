package tui

import (
	"strings"
	"testing"
)

func TestChatMessageRenderUser(t *testing.T) {
	m := ChatMessage{
		Kind:    MsgUser,
		Header:  "You • 22:59:50",
		Content: "hello world",
	}
	got := m.Render(60, false)
	if got == "" {
		t.Fatal("expected non-empty render")
	}
	if !strings.Contains(got, "You • 22:59:50") {
		t.Fatalf("render missing header: %s", got)
	}
	if !strings.Contains(got, "hello world") {
		t.Fatalf("render missing content: %s", got)
	}
}

func TestChatMessageRenderAgent(t *testing.T) {
	m := ChatMessage{
		Kind:    MsgAgent,
		Content: "I can help with that.",
	}
	got := m.Render(60, false)
	if got == "" {
		t.Fatal("expected non-empty render")
	}
	if !strings.Contains(got, "I can help with that.") {
		t.Fatalf("render missing content: %s", got)
	}
}

func TestChatMessageRenderStatus(t *testing.T) {
	m := ChatMessage{
		Kind:    MsgStatus,
		Content: "Agent complete • 22:59:51",
	}
	got := m.Render(60, false)
	if got == "" {
		t.Fatal("expected non-empty render")
	}
}
