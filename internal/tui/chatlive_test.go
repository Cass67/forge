package tui

import (
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
)

func TestHandleKeyBusySlashCommandHandledLocally(t *testing.T) {
	inputCh := make(chan string, 1)
	m := chatLiveModel{
		busy:     true,
		inputBuf: "/stats",
		inputPos: len([]rune("/stats")),
		display: chatDisplayState{
			statsDuration: time.Second,
		},
	}

	result, done := m.handleKey(tcell.NewEventKey(tcell.KeyEnter, 0, 0), inputCh)
	if done {
		t.Fatal("expected chat to remain open")
	}
	if result != (ChatLiveResult{}) {
		t.Fatalf("unexpected result: %+v", result)
	}
	if !m.overlays.statsVisible {
		t.Fatal("expected /stats to open stats overlay while busy")
	}
	if got := m.display.flash; got != "stats opened" {
		t.Fatalf("flash = %q, want %q", got, "stats opened")
	}
	select {
	case got := <-inputCh:
		t.Fatalf("expected no steering input for slash command, got %q", got)
	default:
	}
}

func TestHandleKeyBusyQueuesNonCommandInput(t *testing.T) {
	inputCh := make(chan string, 1)
	m := chatLiveModel{
		busy:     true,
		inputBuf: "check the last tool error",
		inputPos: len([]rune("check the last tool error")),
	}

	result, done := m.handleKey(tcell.NewEventKey(tcell.KeyEnter, 0, 0), inputCh)
	if done {
		t.Fatal("expected chat to remain open")
	}
	if result != (ChatLiveResult{}) {
		t.Fatalf("unexpected result: %+v", result)
	}
	if got := m.display.flash; got != "forge input sent" {
		t.Fatalf("flash = %q, want %q", got, "forge input sent")
	}
	select {
	case got := <-inputCh:
		if got != "check the last tool error" {
			t.Fatalf("queued input = %q, want %q", got, "check the last tool error")
		}
	default:
		t.Fatal("expected steering input to be queued")
	}
}
