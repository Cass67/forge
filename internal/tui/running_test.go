package tui_test

import (
	"forge/internal/llm"
	"forge/internal/tui"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

func TestRunningAppendToken(t *testing.T) {
	m := tui.NewRunningModel(4, 3, "gpt-5.4", "claude-sonnet-4-6")
	m2, _ := m.Update(llm.Event{Kind: llm.EventToken, Agent: "writer", Text: "hello"})
	rm := m2.(tui.RunningModel)
	if rm.WriterBuf != "hello" {
		t.Errorf("expected 'hello', got %q", rm.WriterBuf)
	}
}

func TestRunningToggleView(t *testing.T) {
	m := tui.NewRunningModel(4, 3, "gpt-5.4", "claude-sonnet-4-6")
	if m.YoloMode {
		t.Error("expected split pane by default")
	}
	m2, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("v")})
	rm := m2.(tui.RunningModel)
	if !rm.YoloMode {
		t.Error("expected yolo mode after v")
	}
}

func TestRunningRoundEndKeepsBuffersVisible(t *testing.T) {
	m := tui.NewRunningModel(4, 3, "gpt-5.4", "claude-sonnet-4-6")
	m.WriterBuf = "some writer text"
	m.AuditorBuf = "some auditor text"
	m2, _ := m.Update(llm.Event{Kind: llm.EventRoundEnd, Pass: 1, Round: 1})
	rm := m2.(tui.RunningModel)
	if rm.WriterBuf == "" || rm.AuditorBuf == "" {
		t.Error("expected buffers to stay visible on round end")
	}
	if rm.Phase != 3 {
		t.Errorf("expected summarizer phase, got %v", rm.Phase)
	}
}

func TestRunningRoundStartKeepsPriorTurnsVisible(t *testing.T) {
	m := tui.NewRunningModel(4, 3, "gpt-5.4", "claude-sonnet-4-6")
	m.WriterBuf = "writer round 1"
	m.AuditorBuf = "auditor round 1"

	m2, _ := m.Update(llm.Event{Kind: llm.EventRoundStart, Pass: 1, Round: 2})
	rm := m2.(tui.RunningModel)

	if rm.WriterBuf != "writer round 1" {
		t.Fatalf("expected writer buffer to be preserved, got %q", rm.WriterBuf)
	}
	if rm.AuditorBuf != "auditor round 1" {
		t.Fatalf("expected auditor buffer to be preserved, got %q", rm.AuditorBuf)
	}
}

func TestRunningAddsParagraphGapBetweenTurns(t *testing.T) {
	m := tui.NewRunningModel(4, 3, "gpt-5.4", "claude-sonnet-4-6")
	m.WriterBuf = "writer round 1"
	m.AuditorBuf = "auditor round 1"

	m2, _ := m.Update(llm.Event{Kind: llm.EventRoundStart, Pass: 1, Round: 2})
	rm := m2.(tui.RunningModel)

	m3, _ := rm.Update(llm.Event{Kind: llm.EventToken, Agent: "writer", Text: "writer round 2"})
	rm = m3.(tui.RunningModel)
	if rm.WriterBuf != "writer round 1\n\nwriter round 2" {
		t.Fatalf("expected paragraph gap before next writer turn, got %q", rm.WriterBuf)
	}

	m4, _ := rm.Update(llm.Event{Kind: llm.EventToken, Agent: "auditor", Text: "auditor round 2"})
	rm = m4.(tui.RunningModel)
	if rm.AuditorBuf != "auditor round 1\n\nauditor round 2" {
		t.Fatalf("expected paragraph gap before next auditor turn, got %q", rm.AuditorBuf)
	}
}

func TestRunningPaneFocusAndScrollAreIndependent(t *testing.T) {
	m := tui.NewRunningModel(4, 3, "gpt-5.4", "claude-sonnet-4-6")
	m.Width = 100
	m.Height = 12
	m.WriterBuf = "line1\nline2\nline3\nline4\nline5\nline6\nline7\nline8\nline9\nline10\nline11\nline12\nline13\nline14"
	m.AuditorBuf = m.WriterBuf
	m1, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 12})
	rm := m1.(tui.RunningModel)
	m2, _ := rm.Update(tea.KeyMsg{Type: tea.KeyDown})
	rm = m2.(tui.RunningModel)
	if rm.AI1Scroll == 0 {
		t.Fatal("expected AI-1 scroll to change")
	}
	m3, _ := rm.Update(tea.KeyMsg{Type: tea.KeyRight})
	rm = m3.(tui.RunningModel)
	m4, _ := rm.Update(tea.KeyMsg{Type: tea.KeyDown})
	rm = m4.(tui.RunningModel)
	if rm.AI2Scroll == 0 {
		t.Fatal("expected AI-2 scroll to change")
	}
	if rm.AI1Scroll == 0 {
		t.Fatal("expected AI-1 scroll to remain unchanged")
	}
}

func TestRunningLongAI1ContentDoesNotPushOutAI2Pane(t *testing.T) {
	m := tui.NewRunningModel(4, 3, "gpt-5.4", "claude-sonnet-4-6")
	m.WriterBuf = strings.Repeat("A", 400)
	m.AuditorBuf = "review text"
	mm, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	rm := mm.(tui.RunningModel)
	view := rm.View()
	if !strings.Contains(view, "AI-1") {
		t.Fatal("expected AI-1 pane in view")
	}
	if !strings.Contains(view, "AI-2") {
		t.Fatal("expected AI-2 pane to remain visible in split view")
	}
}

func TestRunningViewStaysWithinTerminalBounds(t *testing.T) {
	m := tui.NewRunningModel(4, 3, "gpt-5.4", "claude-sonnet-4-6")
	m.WriterBuf = strings.Repeat("A", 600) + "\n" + strings.Repeat("B", 600)
	m.AuditorBuf = strings.Repeat("review ", 100)
	mm, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	rm := mm.(tui.RunningModel)
	view := rm.View()
	lines := strings.Split(view, "\n")
	if len(lines) > 23 {
		t.Fatalf("expected rendered height <= 23, got %d", len(lines))
	}
	for i, line := range lines {
		if lipgloss.Width(line) > 80 {
			t.Fatalf("line %d exceeds terminal width: got %d", i+1, lipgloss.Width(line))
		}
	}
}

func TestRunningViewFitsShortTerminalHeights(t *testing.T) {
	for _, h := range []int{10, 12, 14, 16} {
		m := tui.NewRunningModel(4, 3, "gpt-5.4", "claude-sonnet-4-6")
		m.WriterBuf = strings.Repeat("A", 600) + "\n" + strings.Repeat("B", 600)
		m.AuditorBuf = strings.Repeat("review ", 100)
		mm, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: h})
		rm := mm.(tui.RunningModel)
		view := rm.View()
		lines := strings.Split(view, "\n")
		if len(lines) > h-1 {
			t.Fatalf("height %d: expected rendered height <= %d, got %d", h, h-1, len(lines))
		}
	}
}

func TestRunningStreamingViewStaysWithinTerminalBounds(t *testing.T) {
	m := tui.NewRunningModel(4, 3, "gpt-5.4", "claude-sonnet-4-6")
	mm, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	rm := mm.(tui.RunningModel)

	chunks := []string{
		strings.Repeat("A", 120),
		strings.Repeat("B", 120),
		"\n",
		strings.Repeat("C", 120),
		strings.Repeat("D", 120),
		"\n",
		strings.Repeat("E", 120),
	}

	for i, chunk := range chunks {
		mm, _ = rm.Update(llm.Event{Kind: llm.EventToken, Agent: "writer", Text: chunk})
		rm = mm.(tui.RunningModel)
		view := rm.View()
		lines := strings.Split(view, "\n")
		if len(lines) > 23 {
			t.Fatalf("step %d: expected rendered height <= 23, got %d", i, len(lines))
		}
		for lineNo, line := range lines {
			if lipgloss.Width(line) > 80 {
				t.Fatalf("step %d line %d exceeds terminal width: got %d", i, lineNo+1, lipgloss.Width(line))
			}
		}
	}
}
