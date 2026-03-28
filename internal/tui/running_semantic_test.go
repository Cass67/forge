package tui

import "testing"

func TestRunningViewHighlightsSemanticTokens(t *testing.T) {
	withTrueColorProfile(t)

	theme := lookupThemeForTest(t, "default")
	m := NewRunningModel(2, 2, "gpt-5.4", "claude-sonnet-4-6")
	m.Width = 80
	m.Height = 24
	m.WriterBuf = "tool_call: forge -d\nstatus: approved in 1.2s"
	m.AuditorBuf = "review ./internal/tui"

	view := m.View()

	assertStyledSubstring(t, view, "tool_call:", theme.TextDim)
	assertStyledSubstring(t, view, "forge -d", theme.AccentSecondary)
	assertStyledSubstring(t, view, "./internal/tui", theme.TextDim)
}
