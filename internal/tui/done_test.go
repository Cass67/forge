package tui_test

import (
	"forge/internal/tui"
	tea "github.com/charmbracelet/bubbletea"
	"strings"
	"testing"
)

func TestDoneQuit(t *testing.T) {
	m := tui.NewDoneModel("/tmp/output", false, "")
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	if cmd == nil {
		t.Error("expected quit command")
	}
}

func TestAbortShowsReason(t *testing.T) {
	m := tui.NewDoneModel("/tmp/output", true, "openai 429")
	view := m.View()
	if !strings.Contains(view, "openai 429") {
		t.Errorf("expected abort reason in view: %s", view)
	}
}

func TestDoneFixKey(t *testing.T) {
	m := tui.NewDoneModel("/tmp/output", false, "")
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("f")})
	if cmd == nil {
		t.Fatal("expected a command from pressing f")
	}
	msg := cmd()
	if _, ok := msg.(tui.FixRequested); !ok {
		t.Errorf("expected FixRequested, got %T", msg)
	}
}

func TestDoneFixKeyIgnoredWhenAborted(t *testing.T) {
	m := tui.NewDoneModel("/tmp/output", true, "some error")
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("f")})
	if cmd != nil {
		t.Error("expected no command when session is aborted, but got one")
	}
}
