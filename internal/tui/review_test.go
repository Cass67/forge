package tui_test

import (
	"os"
	"path/filepath"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"forge/internal/tui"
)

func setupReviewDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	codeDir := filepath.Join(dir, "code")
	os.MkdirAll(codeDir, 0o755)
	os.WriteFile(filepath.Join(codeDir, "main.go"), []byte("package main\n"), 0o644)
	os.MkdirAll(filepath.Join(codeDir, "pkg"), 0o755)
	os.WriteFile(filepath.Join(codeDir, "pkg", "util.go"), []byte("package pkg\n"), 0o644)
	return dir
}

func TestReviewModelNavigation(t *testing.T) {
	dir := setupReviewDir(t)
	m := tui.NewReviewModel(dir, "")
	if len(m.Files) != 2 {
		t.Fatalf("expected 2 files, got %d", len(m.Files))
	}
	if m.Cursor != 0 {
		t.Errorf("initial cursor should be 0")
	}

	// Move down
	m2, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	rm := m2.(tui.ReviewModel)
	if rm.Cursor != 1 {
		t.Errorf("cursor should be 1, got %d", rm.Cursor)
	}

	// Move up
	m3, _ := rm.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("k")})
	rm = m3.(tui.ReviewModel)
	if rm.Cursor != 0 {
		t.Errorf("cursor should be 0, got %d", rm.Cursor)
	}
}

func TestReviewModelSelect(t *testing.T) {
	dir := setupReviewDir(t)
	m := tui.NewReviewModel(dir, "")

	// Select first file
	m2, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(" ")})
	rm := m2.(tui.ReviewModel)
	if !rm.Selected[0] {
		t.Error("expected file 0 to be selected")
	}

	// Toggle off
	m3, _ := rm.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(" ")})
	rm = m3.(tui.ReviewModel)
	if rm.Selected[0] {
		t.Error("expected file 0 to be deselected")
	}
}

func TestReviewModelSelectAll(t *testing.T) {
	dir := setupReviewDir(t)
	m := tui.NewReviewModel(dir, "")

	m2, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")})
	rm := m2.(tui.ReviewModel)
	if len(rm.Selected) != 2 {
		t.Errorf("expected 2 selected, got %d", len(rm.Selected))
	}

	// Toggle all off
	m3, _ := rm.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")})
	rm = m3.(tui.ReviewModel)
	if len(rm.Selected) != 0 {
		t.Errorf("expected 0 selected, got %d", len(rm.Selected))
	}
}

func TestReviewModelViewFile(t *testing.T) {
	dir := setupReviewDir(t)
	m := tui.NewReviewModel(dir, "")

	m2, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	rm := m2.(tui.ReviewModel)
	if !rm.Viewing {
		t.Error("expected to be in viewing mode")
	}

	// Back to list
	m3, _ := rm.Update(tea.KeyMsg{Type: tea.KeyEscape})
	rm = m3.(tui.ReviewModel)
	if rm.Viewing {
		t.Error("expected to be back in list mode")
	}
}

func TestReviewModelQuit(t *testing.T) {
	dir := setupReviewDir(t)
	m := tui.NewReviewModel(dir, "")

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	if cmd == nil {
		t.Fatal("expected a command from q")
	}
	msg := cmd()
	if _, ok := msg.(tui.ReviewDone); !ok {
		t.Errorf("expected ReviewDone, got %T", msg)
	}
}
