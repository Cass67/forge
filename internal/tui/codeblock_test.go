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

func TestRenderMessageContentStylesCommandsAndPaths(t *testing.T) {
	withTrueColorProfile(t)

	theme := lookupThemeForTest(t, "default")
	got := renderMessageContent("Run go test ./... from ./internal/tui", 60, theme)

	assertStyledSubstring(t, got, "go test ./...", theme.AccentSecondary)
	assertStyledSubstring(t, got, "./internal/tui", theme.TextDim)
}

func TestRenderMessageContentStylesInlineCodeSemantically(t *testing.T) {
	withTrueColorProfile(t)

	theme := lookupThemeForTest(t, "default")
	got := renderMessageContent("Use `go test ./...` from `./internal/tui` and inspect `runner.sh`.", 80, theme)

	assertStyledSubstring(t, got, "go test ./...", theme.AccentSecondary)
	assertStyledSubstring(t, got, "./internal/tui", theme.TextDim)
	assertStyledSubstring(t, got, "runner.sh", theme.TextDim)
}

func TestRenderMessageContentStylesHeadingsListsAndStrongEmphasis(t *testing.T) {
	withTrueColorProfile(t)

	theme := lookupThemeForTest(t, "default")
	content := strings.Join([]string{
		"## Forge overview",
		"- **Host-owned** completion checks",
		"1. `go test ./internal/tui` before merge",
	}, "\n")

	got := renderMessageContent(content, 80, theme)

	assertStyledSubstring(t, got, "##", theme.AccentPrimary)
	assertStyledSubstring(t, got, "Forge overview", theme.AccentPrimary)
	assertStyledSubstring(t, got, "-", theme.AccentSecondary)
	assertStyledSubstring(t, got, "Host-owned", theme.Text)
	assertStyledSubstring(t, got, "1.", theme.AccentSecondary)
	assertStyledSubstring(t, got, "go test ./internal/tui", theme.AccentSecondary)
	if strings.Contains(got, "**Host-owned**") {
		t.Fatalf("expected strong emphasis markers to be stripped, got: %q", got)
	}
}

func TestRenderMessageContentProseDoesNotPaintInlineBackgroundBlocks(t *testing.T) {
	withTrueColorProfile(t)

	theme := lookupThemeForTest(t, "default")
	got := renderMessageContent("Objective: explain this repo\nVerify: produce source-grounded findings", 80, theme)

	if strings.Contains(got, ansiBackgroundFragment(theme.AppBG)) {
		t.Fatalf("prose content should not paint app background blocks inline: %q", got)
	}
	if strings.Contains(got, ansiBackgroundFragment(theme.PanelBG)) {
		t.Fatalf("prose content should not paint panel background blocks inline: %q", got)
	}
	if strings.Contains(got, ansiBackgroundFragment(theme.HeaderBG)) {
		t.Fatalf("prose content should not paint header background blocks inline: %q", got)
	}
}

func TestRenderMessageContentWrapsProseWithoutSplittingShortWords(t *testing.T) {
	theme := lookupThemeForTest(t, "default")
	content := "It also wasn’t really a harness limitation here, because I had the tools needed to create the file. The failure was that I responded conversationally instead of continuing the task flow and writing the plan into the repo."

	got := strippedLine(renderMessageContent(content, 80, theme))
	if strings.Contains(got, "instead o\nf continuing") {
		t.Fatalf("expected word-safe wrap, got:\n%s", got)
	}
}

func TestRenderMessageContentNormalizesSplitShortFlagInCodeBlock(t *testing.T) {
	theme := lookupThemeForTest(t, "default")
	content := "```bash\ngit remote -\nv\n```"

	got := strippedLine(renderMessageContent(content, 80, theme))
	if strings.Contains(got, "git remote -\nv") {
		t.Fatalf("expected split short flag fragment to be normalized, got:\n%s", got)
	}
	if !strings.Contains(got, "git remote -v") {
		t.Fatalf("expected normalized flag form, got:\n%s", got)
	}
}
