package tui_test

import (
    "testing"
    "forge/internal/llm"
    "forge/internal/tui"
    tea "github.com/charmbracelet/bubbletea"
)

func TestRunningAppendToken(t *testing.T) {
    m := tui.NewRunningModel(4, 3)
    m2, _ := m.Update(llm.Event{Kind: llm.EventToken, Agent: "writer", Text: "hello"})
    rm := m2.(tui.RunningModel)
    if rm.WriterBuf != "hello" {
        t.Errorf("expected 'hello', got %q", rm.WriterBuf)
    }
}

func TestRunningToggleView(t *testing.T) {
    m := tui.NewRunningModel(4, 3)
    if m.YoloMode {
        t.Error("expected split pane by default")
    }
    m2, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("v")})
    rm := m2.(tui.RunningModel)
    if !rm.YoloMode {
        t.Error("expected yolo mode after v")
    }
}

func TestRunningRoundEndClearsBuffers(t *testing.T) {
    m := tui.NewRunningModel(4, 3)
    m.WriterBuf = "some writer text"
    m.AuditorBuf = "some auditor text"
    m2, _ := m.Update(llm.Event{Kind: llm.EventRoundEnd, Pass: 1, Round: 1})
    rm := m2.(tui.RunningModel)
    if rm.WriterBuf != "" || rm.AuditorBuf != "" {
        t.Error("expected buffers cleared on round end")
    }
}
