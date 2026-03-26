package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestComposerEnterSubmitsAndClears(t *testing.T) {
	c := NewChatComposer()
	c.InsertString("review this repo")

	action := c.HandleKey(tea.KeyMsg{Type: tea.KeyEnter}, false)

	if action.SubmitText != "review this repo" {
		t.Fatalf("action = %#v", action)
	}
	if c.Text() != "" {
		t.Fatalf("composer should clear after submit, got %q", c.Text())
	}
}

func TestComposerModifiedEnterInsertsNewline(t *testing.T) {
	c := NewChatComposer()
	c.InsertString("first line")

	action := c.HandleKey(tea.KeyMsg{Type: tea.KeyEnter, Alt: true}, false)
	c.InsertString("second line")

	if action != (ComposerAction{}) {
		t.Fatalf("action = %#v", action)
	}
	if got := c.Text(); got != "first line\nsecond line" {
		t.Fatalf("text = %q", got)
	}
}

func TestComposerBracketedPasteKeepsLiteralMultilineText(t *testing.T) {
	c := NewChatComposer()

	action := c.HandleKey(tea.KeyMsg{
		Type:  tea.KeyRunes,
		Runes: []rune("first line\nsecond line"),
		Paste: true,
	}, false)

	if action != (ComposerAction{}) {
		t.Fatalf("action = %#v", action)
	}
	if got := c.Text(); got != "first line\nsecond line" {
		t.Fatalf("text = %q", got)
	}
}

func TestComposerCtrlCClearsDraftWhenIdle(t *testing.T) {
	c := NewChatComposer()
	c.InsertString("keep this local")

	action := c.HandleKey(tea.KeyMsg{Type: tea.KeyCtrlC}, false)

	if action != (ComposerAction{}) {
		t.Fatalf("action = %#v", action)
	}
	if got := c.Text(); got != "" {
		t.Fatalf("text = %q", got)
	}
}

func TestComposerCtrlCRequestsCancelWhenBusy(t *testing.T) {
	c := NewChatComposer()
	c.InsertString("still here")

	action := c.HandleKey(tea.KeyMsg{Type: tea.KeyCtrlC}, true)

	if !action.CancelTurn {
		t.Fatalf("action = %#v", action)
	}
	if got := c.Text(); got != "still here" {
		t.Fatalf("text = %q", got)
	}
}

func TestComposerCtrlDExitsOnlyWhenEmptyAndIdle(t *testing.T) {
	t.Run("empty and idle exits", func(t *testing.T) {
		c := NewChatComposer()
		action := c.HandleKey(tea.KeyMsg{Type: tea.KeyCtrlD}, false)
		if !action.Exit {
			t.Fatalf("action = %#v", action)
		}
	})

	t.Run("non-empty draft does not exit", func(t *testing.T) {
		c := NewChatComposer()
		c.InsertString("draft")
		action := c.HandleKey(tea.KeyMsg{Type: tea.KeyCtrlD}, false)
		if action.Exit {
			t.Fatalf("action = %#v", action)
		}
		if got := c.Text(); got != "draft" {
			t.Fatalf("text = %q", got)
		}
	})

	t.Run("busy does not exit", func(t *testing.T) {
		c := NewChatComposer()
		action := c.HandleKey(tea.KeyMsg{Type: tea.KeyCtrlD}, true)
		if action.Exit {
			t.Fatalf("action = %#v", action)
		}
	})
}

func TestComposerVisibleLineBudget(t *testing.T) {
	c := NewChatComposer()
	c.InsertString("short")
	if got := len(c.visibleLines(12)); got != 3 {
		t.Fatalf("short composer height = %d, want 3", got)
	}

	c.SetText("1111111111222222222233333333334444444444")
	if got := len(c.visibleLines(12)); got != 5 {
		t.Fatalf("wrapped composer height = %d, want 5", got)
	}

	c.SetText("11111111112222222222333333333344444444445555555555")
	rendered := strings.Join(c.visibleLines(12), "\n")
	if got := len(c.visibleLines(12)); got != 5 {
		t.Fatalf("scrolled composer height = %d, want 5", got)
	}
	if strings.Contains(rendered, "1111111111") {
		t.Fatalf("expected scrolled composer to drop earliest content, got %q", rendered)
	}
	if !strings.Contains(rendered, "5555555555") {
		t.Fatalf("expected scrolled composer to keep latest content, got %q", rendered)
	}
}
