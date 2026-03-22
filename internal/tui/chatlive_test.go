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

func TestHandleKeyBusyEnterQueuesNonCommandInput(t *testing.T) {
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

func TestHandleKeyShiftEnterInsertsNewline(t *testing.T) {
	inputCh := make(chan string, 1)
	m := chatLiveModel{
		inputBuf: "first line",
		inputPos: len([]rune("first line")),
	}

	ev := tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModShift)
	result, done := m.handleKey(ev, inputCh)
	if done {
		t.Fatal("expected chat to remain open")
	}
	if result != (ChatLiveResult{}) {
		t.Fatalf("unexpected result: %+v", result)
	}
	if got := m.inputBuf; got != "first line\n" {
		t.Fatalf("inputBuf = %q, want %q", got, "first line\n")
	}
	select {
	case got := <-inputCh:
		t.Fatalf("expected no submitted input, got %q", got)
	default:
	}
}

func TestHandleKeyEnterSubmitsMultilineInput(t *testing.T) {
	inputCh := make(chan string, 1)
	m := chatLiveModel{
		inputBuf: "first line\nsecond line",
		inputPos: len([]rune("first line\nsecond line")),
	}

	result, done := m.handleKey(tcell.NewEventKey(tcell.KeyEnter, 0, 0), inputCh)
	if done {
		t.Fatal("expected chat to remain open")
	}
	if result != (ChatLiveResult{}) {
		t.Fatalf("unexpected result: %+v", result)
	}
	if !m.busy {
		t.Fatal("expected submit to start a running turn")
	}
	select {
	case got := <-inputCh:
		if got != "first line\nsecond line" {
			t.Fatalf("submitted input = %q, want multiline text", got)
		}
	default:
		t.Fatal("expected multiline input to be submitted")
	}
}

func TestInputLayoutWrapsAndTracksCursor(t *testing.T) {
	m := chatLiveModel{height: 20, inputBuf: "abcdefghij", inputPos: len([]rune("abcdefghij"))}
	layout := m.inputLayout(12)
	if len(layout.lines) < 2 {
		t.Fatalf("expected wrapped input lines, got %#v", layout.lines)
	}
	if layout.cursorLine != len(layout.lines)-1 {
		t.Fatalf("cursorLine = %d, want last line", layout.cursorLine)
	}
}

func TestHandleKeyArrowMovesInputCursorHorizontally(t *testing.T) {
	inputCh := make(chan string, 1)
	m := chatLiveModel{
		width:    80,
		inputBuf: "hello",
		inputPos: len([]rune("hello")),
	}

	_, _ = m.handleKey(tcell.NewEventKey(tcell.KeyLeft, 0, 0), inputCh)
	if got := m.inputPos; got != 4 {
		t.Fatalf("inputPos after left = %d, want 4", got)
	}

	_, _ = m.handleKey(tcell.NewEventKey(tcell.KeyRight, 0, 0), inputCh)
	if got := m.inputPos; got != 5 {
		t.Fatalf("inputPos after right = %d, want 5", got)
	}
}

func TestHandleKeyArrowMovesInputCursorVertically(t *testing.T) {
	inputCh := make(chan string, 1)
	m := chatLiveModel{
		width:    20,
		height:   20,
		inputBuf: "abcd\nefgh",
		inputPos: 2,
	}

	_, _ = m.handleKey(tcell.NewEventKey(tcell.KeyDown, 0, 0), inputCh)
	if got := m.inputPos; got != 7 {
		t.Fatalf("inputPos after down = %d, want 7", got)
	}

	_, _ = m.handleKey(tcell.NewEventKey(tcell.KeyUp, 0, 0), inputCh)
	if got := m.inputPos; got != 2 {
		t.Fatalf("inputPos after up = %d, want 2", got)
	}
}

func TestHandleKeyEscapeRequiresConfirmationWhenIdle(t *testing.T) {
	m := chatLiveModel{status: "ready"}
	inputCh := make(chan string, 1)

	result, done := m.handleKey(tcell.NewEventKey(tcell.KeyEscape, 0, 0), inputCh)
	if done {
		t.Fatal("first escape should not exit")
	}
	if result.Aborted {
		t.Fatal("first escape should not abort")
	}
	if !m.exitPending {
		t.Fatal("expected exit to be pending after first escape")
	}
	if got := m.display.flash; got != "press Esc again to exit" {
		t.Fatalf("flash = %q, want %q", got, "press Esc again to exit")
	}

	result, done = m.handleKey(tcell.NewEventKey(tcell.KeyEscape, 0, 0), inputCh)
	if !done {
		t.Fatal("second escape should exit")
	}
	if !result.Aborted {
		t.Fatal("second escape should abort")
	}
	if m.exitPending {
		t.Fatal("expected pending exit to clear after exiting")
	}
}

func TestHandleKeyEscapePendingClearsOnOtherKey(t *testing.T) {
	m := chatLiveModel{status: "ready"}
	inputCh := make(chan string, 1)

	_, done := m.handleKey(tcell.NewEventKey(tcell.KeyEscape, 0, 0), inputCh)
	if done {
		t.Fatal("first escape should not exit")
	}
	if !m.exitPending {
		t.Fatal("expected exit to be pending after first escape")
	}

	_, done = m.handleKey(tcell.NewEventKey(tcell.KeyRune, 'a', 0), inputCh)
	if done {
		t.Fatal("typing should not exit")
	}
	if m.exitPending {
		t.Fatal("expected pending exit to clear on other key")
	}

	result, done := m.handleKey(tcell.NewEventKey(tcell.KeyEscape, 0, 0), inputCh)
	if done {
		t.Fatal("escape after clearing should warn first, not exit")
	}
	if result.Aborted {
		t.Fatal("escape after clearing should not abort")
	}
	if !m.exitPending {
		t.Fatal("expected exit to be pending again")
	}
}
