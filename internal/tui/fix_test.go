package tui_test

import (
	"strings"
	"testing"

	"forge/internal/tui"
	tea "github.com/charmbracelet/bubbletea"
)

func TestFixModelEnterEmitsFixStarted(t *testing.T) {
	m := tui.NewFixModel("/tmp/out", tui.SessionStarted{
		WriterModel:  "claude-sonnet-4-6",
		AuditorModel: "gpt-4o",
	}, []string{"claude-sonnet-4-6", "gpt-4o"}, []string{"claude-sonnet-4-6", "gpt-4o"})

	// Type an issue
	for _, ch := range "login breaks" {
		m2, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{ch}})
		m = m2.(tui.FixModel)
	}

	// Press enter
	m2, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	_ = m2
	if cmd == nil {
		t.Fatal("expected command on enter")
	}
	msg := cmd()
	fs, ok := msg.(tui.FixStarted)
	if !ok {
		t.Fatalf("expected FixStarted, got %T", msg)
	}
	if fs.Issue != "login breaks" {
		t.Errorf("issue = %q, want %q", fs.Issue, "login breaks")
	}
	if fs.WriterModel != "claude-sonnet-4-6" {
		t.Errorf("writer = %q, want claude-sonnet-4-6", fs.WriterModel)
	}
}

func TestFixModelEnterNoopWhenEmpty(t *testing.T) {
	m := tui.NewFixModel("/tmp/out", tui.SessionStarted{
		WriterModel:  "claude-sonnet-4-6",
		AuditorModel: "gpt-4o",
	}, []string{"claude-sonnet-4-6"}, []string{"gpt-4o"})

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil {
		t.Error("expected no command when issue is empty")
	}
}

func TestFixModelTabSwitchesFocus(t *testing.T) {
	m := tui.NewFixModel("/tmp/out", tui.SessionStarted{
		WriterModel:  "claude-sonnet-4-6",
		AuditorModel: "gpt-4o",
	}, []string{"claude-sonnet-4-6"}, []string{"gpt-4o"})
	if m.ModelFocus != 0 {
		t.Fatal("expected initial focus on writer")
	}
	m2, _ := m.Update(tea.KeyMsg{Type: tea.KeyTab})
	fm := m2.(tui.FixModel)
	if fm.ModelFocus != 1 {
		t.Errorf("expected focus to shift to auditor after tab")
	}
}

func TestFixModelViewContainsOutputDir(t *testing.T) {
	m := tui.NewFixModel("/my/output/dir", tui.SessionStarted{
		WriterModel:  "claude-sonnet-4-6",
		AuditorModel: "gpt-4o",
	}, []string{"claude-sonnet-4-6"}, []string{"gpt-4o"})
	view := m.View()
	if !strings.Contains(view, "/my/output/dir") {
		t.Errorf("view should show output dir, got:\n%s", view)
	}
}
