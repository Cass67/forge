package tui

import (
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"
)

func TestHandleMouseRightClickCopiesAgentSelection(t *testing.T) {
	var copied string
	m := chatLiveModel{
		width:        120,
		height:       40,
		toolsVisible: true,
		agentBuf:     "alpha beta gamma",
		copyFn: func(s string) error {
			copied = s
			return nil
		},
		selectionPane:   "left",
		selectionStart:  6,
		selectionEnd:    9,
		selectionActive: true,
	}

	x, y, _, _ := m.leftPaneRect()
	m.handleMouse(tcell.NewEventMouse(x+2, y+2, tcell.Button2, 0))

	if copied != "beta" {
		t.Fatalf("expected selected text copied, got %q", copied)
	}
	if m.flash != "agent selection copied" {
		t.Fatalf("expected selection flash, got %q", m.flash)
	}
}

func TestSelectedTextAcrossWrappedLines(t *testing.T) {
	m := chatLiveModel{
		width:           40,
		height:          20,
		toolsVisible:    true,
		leftPaneWidth:   20,
		agentBuf:        "hello world from forge",
		selectionPane:   "left",
		selectionActive: true,
	}

	wrapped := wrapPaneContent(m.agentBuf, m.leftContentWidth())
	joined := []rune("")
	_ = wrapped
	all := []rune(strings.Join(wrapPaneContent(m.agentBuf, m.leftContentWidth()), "\n"))
	var start, end int = -1, -1
	for i := 0; i < len(all); i++ {
		if string(all[i:min(len(all), i+5)]) == "world" && start == -1 {
			start = i
			end = i + 4
			break
		}
	}
	if start == -1 {
		t.Fatal("failed to locate wrapped selection target")
	}
	m.selectionStart = start
	m.selectionEnd = end

	if got := m.selectedText("left"); got != "world" {
		t.Fatalf("expected wrapped selection text %q, got %q", "world", got)
	}
	_ = joined
}
