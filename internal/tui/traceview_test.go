package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
)

func TestRenderTraceOverlayPanelShowsContent(t *testing.T) {
	theme := lookupThemeForTest(t, "default")
	got := renderTraceOverlayPanel(theme, "tool_call read_file\nobserve complete", "", 100, 24)
	if !strings.Contains(got, "Debug trace") {
		t.Fatalf("missing title: %q", got)
	}
	if !strings.Contains(got, "tool_call read_file") {
		t.Fatalf("missing trace content: %q", got)
	}
}

func TestRenderTraceOverlayPanelHandlesEmptyContent(t *testing.T) {
	theme := lookupThemeForTest(t, "default")
	got := renderTraceOverlayPanel(theme, "", "", 100, 24)
	if !strings.Contains(got, "No trace captured yet.") {
		t.Fatalf("missing empty-state text: %q", got)
	}
}

func TestRenderTraceOverlayPanelShowsDebugLogPath(t *testing.T) {
	theme := lookupThemeForTest(t, "default")
	got := renderTraceOverlayPanel(theme, "", "/tmp/forge-chat-debug.jsonl", 100, 24)
	if !strings.Contains(got, "/tmp/forge-chat-debug.jsonl") {
		t.Fatalf("missing debug log path: %q", got)
	}
}

func TestRenderTraceDockPanelUsesAppBackground(t *testing.T) {
	withTrueColorProfile(t)

	theme := chatTheme{
		AppBG:         lipgloss.Color("#112233"),
		HeaderBG:      lipgloss.Color("#445566"),
		Text:          lipgloss.Color("#eef2f7"),
		TextDim:       lipgloss.Color("#8b97a8"),
		AccentPrimary: lipgloss.Color("#8fb4ff"),
		Border:        lipgloss.Color("#334455"),
	}

	rendered := renderTraceDockPanel(theme, "tool_call read_file", "/tmp/forge-debug.jsonl", 80, 8)

	if strings.Contains(rendered, ansiBackground(theme.HeaderBG)) {
		t.Fatalf("trace dock should not use header background as the dock surface: %q", rendered)
	}
	if !strings.Contains(rendered, ansiBackground(theme.AppBG)) {
		t.Fatalf("trace dock missing app background fill: %q", rendered)
	}
}
