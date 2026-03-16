package tui_test

import (
    "testing"
    "forge/internal/tui"
    tea "github.com/charmbracelet/bubbletea"
)

func TestInputValidRounds(t *testing.T) {
    m := tui.NewInputModel([]string{"claude-sonnet-4-6"}, []string{"gpt-4o"})
    if m.Rounds != 3 {
        t.Errorf("expected 3, got %d", m.Rounds)
    }
}

func TestInputTabShiftsFocus(t *testing.T) {
    m := tui.NewInputModel([]string{"claude-sonnet-4-6"}, []string{"gpt-4o"})
    if m.Focus != tui.FocusWriter {
        t.Error("expected writer focus initially")
    }
    m2, _ := m.Update(tea.KeyMsg{Type: tea.KeyTab})
    im := m2.(tui.InputModel)
    if im.Focus != tui.FocusAuditor {
        t.Error("expected auditor focus after tab")
    }
}

func TestInputRoundsClampedToRange(t *testing.T) {
    if tui.ClampRounds(0) != 1 {
        t.Error("expected 0 clamped to 1")
    }
    if tui.ClampRounds(11) != 10 {
        t.Error("expected 11 clamped to 10")
    }
    if tui.ClampRounds(5) != 5 {
        t.Error("expected 5 unchanged")
    }
}
