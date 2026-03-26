package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
)

func TestRenderMessageContentRendersFencedCodeBlocks(t *testing.T) {
	theme := lookupThemeForTest(t, "default")
	got := renderMessageContent("Before\n```go\nfmt.Println(\"hi\")\n```\nAfter", 60, theme)
	if !strings.Contains(got, "GO") {
		t.Fatalf("missing code label: %q", got)
	}
	if !strings.Contains(got, "fmt.Println(\"hi\")") {
		t.Fatalf("missing code content: %q", got)
	}
	if !strings.Contains(got, "Before") || !strings.Contains(got, "After") {
		t.Fatalf("missing surrounding prose: %q", got)
	}
}

func TestRenderMessageContentRendersDiffBlocks(t *testing.T) {
	theme := lookupThemeForTest(t, "default")
	got := renderMessageContent("```diff\n+ added line\n- removed line\n```", 60, theme)
	if !strings.Contains(got, "DIFF") {
		t.Fatalf("missing diff label: %q", got)
	}
	if !strings.Contains(got, "+ added line") || !strings.Contains(got, "- removed line") {
		t.Fatalf("missing diff content: %q", got)
	}
}

func TestRenderCodeBlockUsesAppBackground(t *testing.T) {
	withTrueColorProfile(t)

	theme := chatTheme{
		AppBG:           lipgloss.Color("#112233"),
		HeaderBG:        lipgloss.Color("#445566"),
		Text:            lipgloss.Color("#eef2f7"),
		BorderFocus:     lipgloss.Color("#8fb4ff"),
		AccentSecondary: lipgloss.Color("#79d2ff"),
		Warning:         lipgloss.Color("#f0c674"),
	}

	rendered := renderCodeBlock("go", "fmt.Println(\"hi\")", 60, theme)

	if strings.Contains(rendered, ansiBackground(theme.HeaderBG)) {
		t.Fatalf("code block should not use header background as the body surface: %q", rendered)
	}
	if !strings.Contains(rendered, ansiBackground(theme.AppBG)) {
		t.Fatalf("code block missing app background fill: %q", rendered)
	}
}
