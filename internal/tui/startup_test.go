package tui_test

import (
    "testing"
    "forge/internal/tui"
    tea "github.com/charmbracelet/bubbletea"
)

func TestStartupCheckPass(t *testing.T) {
    m := tui.NewStartupModel()
    m2, _ := m.Update(tui.CheckResult{Name: "ANTHROPIC_API_KEY", OK: true})
    sm := m2.(tui.StartupModel)
    if len(sm.Results) != 1 {
        t.Fatalf("expected 1 result, got %d", len(sm.Results))
    }
    if !sm.Results[0].OK {
        t.Error("expected OK")
    }
}

func TestStartupCheckFail(t *testing.T) {
    m := tui.NewStartupModel()
    m2, _ := m.Update(tui.CheckResult{Name: "OPENAI_API_KEY", OK: false, Detail: "401 unauthorized"})
    sm := m2.(tui.StartupModel)
    if sm.Results[0].OK {
        t.Error("expected not OK")
    }
}

// Satisfy unused import
var _ tea.KeyMsg
