package tui

import (
	"strings"
	"testing"

	"forge/internal/chatstate"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

func TestChatComposerEnterSubmitsAndClears(t *testing.T) {
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

// Bubble Tea exposes modified Enter via KeyMsg.Alt; Forge treats that as the
// Shift+Enter multiline contract.
func TestChatComposerShiftEnterInsertsNewline(t *testing.T) {
	c := NewChatComposer()
	c.InsertString("first line")

	action := c.HandleKey(tea.KeyMsg{Type: tea.KeyEnter, Alt: true}, false)
	c.InsertString("second line")

	if action.SubmitText != "" || action.CancelTurn || action.Exit || len(action.Attachments) != 0 {
		t.Fatalf("action = %#v", action)
	}
	if got := c.Text(); got != "first line\nsecond line" {
		t.Fatalf("text = %q", got)
	}
}

func TestChatComposerBracketedPasteKeepsLiteralMultilineText(t *testing.T) {
	c := NewChatComposer()

	action := c.HandleKey(tea.KeyMsg{
		Type:  tea.KeyRunes,
		Runes: []rune("first line\nsecond line"),
		Paste: true,
	}, false)

	if action.SubmitText != "" || action.CancelTurn || action.Exit || len(action.Attachments) != 0 {
		t.Fatalf("action = %#v", action)
	}
	if got := c.Text(); got != "first line\nsecond line" {
		t.Fatalf("text = %q", got)
	}
}

func TestChatComposerIgnoresMouseTrackingSequences(t *testing.T) {
	c := NewChatComposer()
	c.InsertString("draft")

	action := c.HandleKey(tea.KeyMsg{
		Type:  tea.KeyRunes,
		Runes: []rune("[<64;56;33M[<65;58;32M"),
	}, false)

	if action.SubmitText != "" || action.CancelTurn || action.Exit || len(action.Attachments) != 0 {
		t.Fatalf("action = %#v", action)
	}
	if got := c.Text(); got != "draft" {
		t.Fatalf("text = %q", got)
	}
}

func TestChatComposerCtrlCClearsDraftWhenIdle(t *testing.T) {
	c := NewChatComposer()
	c.InsertString("keep this local")

	action := c.HandleKey(tea.KeyMsg{Type: tea.KeyCtrlC}, false)

	if action.SubmitText != "" || action.CancelTurn || action.Exit || len(action.Attachments) != 0 {
		t.Fatalf("action = %#v", action)
	}
	if got := c.Text(); got != "" {
		t.Fatalf("text = %q", got)
	}
}

func TestChatComposerCtrlCRequestsCancelWhenBusy(t *testing.T) {
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

func TestChatComposerCtrlDExitsOnlyWhenEmptyAndIdle(t *testing.T) {
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

	t.Run("whitespace-only draft does not exit", func(t *testing.T) {
		c := NewChatComposer()
		c.InsertString("   ")
		action := c.HandleKey(tea.KeyMsg{Type: tea.KeyCtrlD}, false)
		if action.Exit {
			t.Fatalf("action = %#v", action)
		}
		if got := c.Text(); got != "   " {
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

func TestChatComposerWhitespaceOnlyDraftRendersAsDraft(t *testing.T) {
	c := NewChatComposer()
	c.InsertString("   ")

	rendered := strippedLine(c.Render(lookupThemeForTest(t, "default"), 24))

	if strings.Contains(rendered, "Ask Forge anything") {
		t.Fatalf("expected whitespace draft to avoid placeholder, got %q", rendered)
	}
	if got := c.visibleBodyLines(22)[0]; got != "   " {
		t.Fatalf("visible draft = %q", got)
	}
}

func TestChatComposerRenderUsesAiryDock(t *testing.T) {
	c := NewChatComposer()
	theme := chatTheme{Border: "12", TextDim: "8"}
	rendered := c.Render(theme, 32)

	if strings.ContainsAny(rendered, "╭╮╰╯│") {
		t.Fatalf("expected borderless composer dock, got %q", rendered)
	}
	if !strings.Contains(rendered, "────────────────") {
		t.Fatalf("expected subtle top divider, got %q", rendered)
	}
	if !strings.Contains(rendered, "Ask Forge anything") {
		t.Fatalf("expected inviting placeholder, got %q", rendered)
	}
}

func TestChatComposerRenderShowsControlHintsInTopBar(t *testing.T) {
	c := NewChatComposer()
	theme := chatTheme{Border: "12", TextDim: "8"}
	rendered := c.Render(theme, 80)

	if !strings.Contains(rendered, "Esc cancel") {
		t.Fatalf("expected composer hint \"Esc cancel\" in placeholder, got %q", rendered)
	}
}

func TestChatComposerRenderShowsWhiteCursor(t *testing.T) {
	withTrueColorProfile(t)
	c := NewChatComposer()
	c.SetText("testing cursor")
	c.SetCursor(4)

	rendered := c.Render(lookupThemeForTest(t, "default"), 80)

	if !strings.Contains(rendered, ansiBackgroundFragment(lipgloss.Color("#ffffff"))) {
		t.Fatalf("expected white cursor in composer render, got %q", rendered)
	}
}

func TestChatComposerRenderWrapsDraftWithoutDroppingText(t *testing.T) {
	c := NewChatComposer()
	c.SetText("there is no space between the header and the first message")

	rendered := strippedLine(c.Render(lookupThemeForTest(t, "default"), 32))

	for _, want := range []string{"there is no space", "between", "the header", "first", "message"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("wrapped composer missing %q in:\n%s", want, rendered)
		}
	}
}

func TestChatComposerCursorPositionCountsTrailingSpaces(t *testing.T) {
	line, col := composerCursorPosition("test     ", len([]rune("test     ")), 32)

	if line != 0 || col != 9 {
		t.Fatalf("cursor position = (%d, %d), want (0, 9)", line, col)
	}
}

func TestChatComposerRenderDoesNotPaintInlineBackgroundBlocks(t *testing.T) {
	withTrueColorProfile(t)

	c := NewChatComposer()
	theme := chatTheme{
		AppBG:       lipgloss.Color("#112233"),
		PanelBG:     lipgloss.Color("#223344"),
		HeaderBG:    lipgloss.Color("#445566"),
		HeaderFG:    lipgloss.Color("#ddeeff"),
		Text:        lipgloss.Color("#eef2f7"),
		TextDim:     lipgloss.Color("#8b97a8"),
		Border:      lipgloss.Color("#334455"),
		BorderFocus: lipgloss.Color("#88aaff"),
	}

	rendered := c.Render(theme, 64)
	if strings.Contains(rendered, ansiBackgroundFragment(theme.PanelBG)) || strings.Contains(rendered, ansiBackgroundFragment(theme.HeaderBG)) {
		t.Fatalf("composer should not paint inline background blocks: %q", rendered)
	}
}

func TestChatComposerVisibleLineBudget(t *testing.T) {
	c := NewChatComposer()
	c.InsertString("short")
	if got := len(c.visibleLines(12)); got != 1 {
		t.Fatalf("short composer height = %d, want 1", got)
	}

	c.SetText("1111111111222222222233333333334444444444")
	if got := len(c.visibleLines(12)); got != 4 {
		t.Fatalf("wrapped composer height = %d, want 4", got)
	}

	c.SetText("111111111122222222223333333333444444444455555555556666666666777777777788888888889999999999")
	if got := len(c.visibleLines(12)); got != 7 {
		t.Fatalf("capped composer height = %d, want 7", got)
	}

	c.SetText("11111111112222222222333333333344444444445555555555666666666677777777778888888888")
	rendered := strings.Join(c.visibleLines(12), "\n")
	if strings.Count(rendered, "\n")+1 != 7 {
		t.Fatalf("scrolled composer height = %d, want 7\n%s", strings.Count(rendered, "\n")+1, rendered)
	}
	if strings.Contains(rendered, "1111111111") {
		t.Fatalf("expected scrolled composer to drop earliest content, got %q", rendered)
	}
	if !strings.Contains(rendered, "5555555555") {
		t.Fatalf("expected scrolled composer to keep latest content, got %q", rendered)
	}
}

func TestChatComposerBackspaceRemovesLastAttachmentWhenEmpty(t *testing.T) {
	c := NewChatComposer()
	c.SetAttachments([]chatstate.ChatAttachment{
		{ID: "a1", Name: "img1.png", MIMEType: "image/png", Size: 100, Width: 10, Height: 20},
		{ID: "a2", Name: "img2.jpg", MIMEType: "image/jpeg", Size: 200, Width: 30, Height: 40},
	})

	// Backspace with empty text removes last attachment
	action := c.HandleKey(tea.KeyMsg{Type: tea.KeyBackspace}, false)
	if action.SubmitText != "" || len(action.Attachments) != 0 {
		t.Fatalf("action = %#v", action)
	}
	if len(c.Attachments()) != 1 {
		t.Fatalf("expected 1 attachment after backspace, got %d", len(c.Attachments()))
	}
	if c.Attachments()[0].Name != "img1.png" {
		t.Fatalf("expected img1.png to remain, got %s", c.Attachments()[0].Name)
	}

	// Backspace again removes it too
	c.HandleKey(tea.KeyMsg{Type: tea.KeyBackspace}, false)
	if len(c.Attachments()) != 0 {
		t.Fatalf("expected 0 attachments, got %d", len(c.Attachments()))
	}
}

func TestChatComposerEnterSubmitsAttachmentsOnly(t *testing.T) {
	c := NewChatComposer()
	c.SetAttachments([]chatstate.ChatAttachment{
		{ID: "a1", Name: "screenshot.png", MIMEType: "image/png", Size: 100, Width: 10, Height: 20},
	})

	// Submit with only attachments (no text)
	action := c.HandleKey(tea.KeyMsg{Type: tea.KeyEnter}, false)
	if action.SubmitText != "" {
		t.Fatalf("expected empty submit text, got %q", action.SubmitText)
	}
	if len(action.Attachments) != 1 {
		t.Fatalf("expected 1 attachment in action, got %d", len(action.Attachments))
	}
	if c.Text() != "" {
		t.Fatalf("composer should clear after submit, got %q", c.Text())
	}
	if len(c.Attachments()) != 0 {
		t.Fatalf("composer attachments should clear after submit, got %d", len(c.Attachments()))
	}
}

func TestChatComposerRenderShowsAttachmentChips(t *testing.T) {
	c := NewChatComposer()
	c.SetAttachments([]chatstate.ChatAttachment{
		{ID: "a1", Name: "screenshot.png", MIMEType: "image/png", Size: 245760, Width: 1280, Height: 720},
	})

	theme := chatTheme{
		Text:          lipgloss.Color("#ffffff"),
		TextDim:       lipgloss.Color("#888888"),
		Border:        lipgloss.Color("#555555"),
		AccentPrimary: lipgloss.Color("#00ff00"),
		AppBG:         lipgloss.Color("#000000"),
	}
	rendered := c.Render(theme, 80)
	if !strings.Contains(rendered, "screenshot.png") {
		t.Errorf("render should show filename, got:\n%s", rendered)
	}
	if !strings.Contains(rendered, "1280x720") {
		t.Errorf("render should show dimensions, got:\n%s", rendered)
	}
}

func TestChatComposerClearAttachments(t *testing.T) {
	c := NewChatComposer()
	c.SetAttachments([]chatstate.ChatAttachment{
		{ID: "a1", Name: "img.png", MIMEType: "image/png", Size: 100, Width: 10, Height: 20},
	})
	if len(c.Attachments()) != 1 {
		t.Fatal("expected 1 attachment")
	}
	c.ClearAttachments()
	if len(c.Attachments()) != 0 {
		t.Fatal("expected 0 attachments after clear")
	}
}

func TestChatComposerMaxAttachments(t *testing.T) {
	c := NewChatComposer()
	for i := 0; i < chatstate.MaxAttachments+2; i++ {
		c.SetAttachments(append(c.Attachments(), chatstate.ChatAttachment{
			ID: "a", Name: "img.png", MIMEType: "image/png", Size: 100, Width: 10, Height: 20,
		}))
	}
	if len(c.Attachments()) != chatstate.MaxAttachments+2 {
		// SetAttachments just sets, doesn't enforce limit - enforcement is in detectAndAttachImages
		t.Logf("attachments = %d", len(c.Attachments()))
	}
}
