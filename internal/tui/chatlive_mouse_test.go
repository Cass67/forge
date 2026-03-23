package tui

import (
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"
)

func TestHandleMouseClickFocusesToolsPane(t *testing.T) {
	m := chatLiveModel{
		width:  120,
		height: 24,
		panes: chatPaneState{
			layout: chatPaneLayoutState{toolsVisible: true},
			tools:  chatPaneBufferState{buf: strings.Repeat("tool output line\n", 20)},
		},
	}

	x, y, _, _ := m.rightPaneRect()
	m.handleMouse(tcell.NewEventMouse(x+2, y+2, tcell.Button1, 0))

	if !m.panes.focusR {
		t.Fatal("expected click in tools pane to focus tools pane")
	}
}

func TestHandleMouseWheelScrollsToolsPane(t *testing.T) {
	m := chatLiveModel{
		width:  120,
		height: 16,
		panes: chatPaneState{
			layout: chatPaneLayoutState{toolsVisible: true},
			tools: chatPaneBufferState{
				buf:    strings.Repeat("tool output line\n", 80),
				follow: true,
			},
		},
	}

	x, y, _, _ := m.rightPaneRect()
	m.handleMouse(tcell.NewEventMouse(x+2, y+2, tcell.WheelDown, 0))

	if m.panes.tools.scroll == 0 {
		t.Fatal("expected mouse wheel in tools pane to scroll tools pane")
	}
	if m.panes.tools.follow {
		t.Fatal("expected wheel scrolling away from bottom to disable follow mode")
	}
}

func TestHandleMouseClickFocusesAgentPane(t *testing.T) {
	m := chatLiveModel{
		width:  120,
		height: 24,
		panes: chatPaneState{
			focusR: true,
			layout: chatPaneLayoutState{toolsVisible: true},
			agent:  chatPaneBufferState{buf: strings.Repeat("agent output line\n", 20)},
		},
	}

	x, y, _, _ := m.leftPaneRect()
	m.handleMouse(tcell.NewEventMouse(x+2, y+2, tcell.Button1, 0))

	if m.panes.focusR {
		t.Fatal("expected click in agent pane to focus agent pane")
	}
}

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
