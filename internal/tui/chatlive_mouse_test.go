package tui

import (
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"
)

func TestHandleMouseRightClickCopiesAgentSelection(t *testing.T) {
	var copied string
	m := chatLiveModel{
		width:  120,
		height: 40,
		panes: chatPaneState{
			agent:  chatPaneBufferState{buf: "alpha beta gamma"},
			layout: chatPaneLayoutState{toolsVisible: true},
			selectn: chatSelectionState{
				pane:   "left",
				start:  6,
				end:    9,
				active: true,
			},
		},
		copyFn: func(s string) error {
			copied = s
			return nil
		},
	}

	x, y, _, _ := m.leftPaneRect()
	m.handleMouse(tcell.NewEventMouse(x+2, y+2, tcell.Button2, 0))

	if copied != "beta" {
		t.Fatalf("expected selected text copied, got %q", copied)
	}
	if m.display.flash != "agent selection copied" {
		t.Fatalf("expected selection flash, got %q", m.display.flash)
	}
}

func TestSelectedTextAcrossWrappedLines(t *testing.T) {
	m := chatLiveModel{
		width:  40,
		height: 20,
		panes: chatPaneState{
			agent:  chatPaneBufferState{buf: "hello world from forge"},
			layout: chatPaneLayoutState{toolsVisible: true, leftWidth: 20},
			selectn: chatSelectionState{
				pane:   "left",
				active: true,
			},
		},
	}

	wrapped := wrapPaneContent(m.panes.agent.buf, m.leftContentWidth())
	joined := []rune("")
	_ = wrapped
	all := []rune(strings.Join(wrapPaneContent(m.panes.agent.buf, m.leftContentWidth()), "\n"))
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
	m.panes.selectn.start = start
	m.panes.selectn.end = end

	if got := m.selectedText("left"); got != "world" {
		t.Fatalf("expected wrapped selection text %q, got %q", "world", got)
	}
	_ = joined
}
