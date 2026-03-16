package tui_test

import (
    "strings"
    "testing"
    "forge/internal/tui"
    tea "github.com/charmbracelet/bubbletea"
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
