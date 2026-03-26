package tui

import "testing"

func TestLiveViewHighlightsSemanticTokens(t *testing.T) {
	withTrueColorProfile(t)

	theme := lookupThemeForTest(t, "default")
	m := liveModel{
		width:         80,
		height:        24,
		currentPass:   1,
		totalPasses:   2,
		currentRound:  1,
		totalRounds:   2,
		passName:      "build",
		phase:         "writer",
		writerModel:   "gpt-5.4",
		auditorModel:  "claude-sonnet-4-6",
		writerBuf:     "tool_call: forge -d\nstatus: approved in 1.2s",
		auditorBuf:    "check ./internal/tui",
		manualMode:    false,
		waitingAdvance: false,
	}

	view := m.View()

	assertStyledSubstring(t, view, "tool_call:", theme.TextDim)
	assertStyledSubstring(t, view, "forge -d", theme.AccentSecondary)
	assertStyledSubstring(t, view, "./internal/tui", theme.AccentPrimary)
}
