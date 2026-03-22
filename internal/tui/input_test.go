package tui_test

import (
	"testing"

	"forge/internal/tui"
	tea "github.com/charmbracelet/bubbletea"
)

func TestInputValidRounds(t *testing.T) {
	m := tui.NewInputModel([]string{"gpt-5.4"}, []string{"gpt-5-mini"}, "", "")
	if m.Rounds != 3 {
		t.Errorf("expected 3, got %d", m.Rounds)
	}
}

func TestInputTabTogglesFocus(t *testing.T) {
	m := tui.NewInputModel([]string{"gpt-5.4"}, []string{"gpt-5-mini"}, "", "")
	if m.ModelFocus != 0 {
		t.Error("expected writer focused initially")
	}
	m2, _ := m.Update(tea.KeyMsg{Type: tea.KeyTab})
	im := m2.(tui.InputModel)
	if im.ModelFocus != 1 {
		t.Error("expected auditor focused after tab")
	}
	m3, _ := im.Update(tea.KeyMsg{Type: tea.KeyTab})
	im = m3.(tui.InputModel)
	if im.ModelFocus != 0 {
		t.Error("expected writer focused after second tab")
	}
}

func TestInputArrowCyclesWriterModel(t *testing.T) {
	models := []string{"gpt-5.4", "gpt-5-mini", "gpt-4o"}
	m := tui.NewInputModel(models, models, "", "")
	// focus on writer (default), press right
	m2, _ := m.Update(tea.KeyMsg{Type: tea.KeyRight})
	im := m2.(tui.InputModel)
	if im.WriterIdx != 1 {
		t.Errorf("expected WriterIdx 1 after right, got %d", im.WriterIdx)
	}
}

func TestInputArrowCyclesAuditorModel(t *testing.T) {
	models := []string{"gpt-5.4", "gpt-5-mini", "gpt-4o"}
	m := tui.NewInputModel(models, models, "", "")
	// tab to auditor, then right
	m2, _ := m.Update(tea.KeyMsg{Type: tea.KeyTab})
	im := m2.(tui.InputModel)
	m3, _ := im.Update(tea.KeyMsg{Type: tea.KeyRight})
	im = m3.(tui.InputModel)
	if im.AuditorIdx != 1 {
		t.Errorf("expected AuditorIdx 1 after tab+right, got %d", im.AuditorIdx)
	}
}

func TestInputTypingAlwaysGoesToPrompt(t *testing.T) {
	m := tui.NewInputModel([]string{"gpt-5.4"}, []string{"gpt-5-mini"}, "", "")
	for _, ch := range "lets build something" {
		m2, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{ch}})
		m = m2.(tui.InputModel)
	}
	if m.Prompt != "lets build something" {
		t.Errorf("expected full prompt, got %q", m.Prompt)
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
