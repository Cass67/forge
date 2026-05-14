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

func TestRenderMessageContentDoesNotInsertBlankRowsInsideFencedCodeBlock(t *testing.T) {
	theme := lookupThemeForTest(t, "default")
	content := "```go\nfirst()\nsecond()\n```"

	got := strippedLine(renderMessageContent(content, 80, theme))
	lines := strings.Split(got, "\n")
	firstLine, secondLine := -1, -1
	for i, line := range lines {
		if strings.Contains(line, "first()") {
			firstLine = i
		}
		if strings.Contains(line, "second()") {
			secondLine = i
		}
	}

	if firstLine < 0 || secondLine < 0 {
		t.Fatalf("expected both code lines, got:\n%s", got)
	}
	if secondLine != firstLine+1 {
		t.Fatalf("unexpected blank visual row between code lines:\n%s", got)
	}
	for i := firstLine + 1; i < secondLine; i++ {
		if strings.TrimSpace(lines[i]) == "" {
			t.Fatalf("unexpected blank visual row between code lines:\n%s", got)
		}
	}
}

func TestRenderMessageContentDoesNotInsertBlankSeparatorsAdjacentToCodeBlocks(t *testing.T) {
	theme := lookupThemeForTest(t, "default")
	content := strings.Join([]string{
		"Before",
		"```go",
		"fmt.Println(\"hi\")",
		"```",
		"After",
	}, "\n")

	got := strippedLine(renderMessageContent(content, 80, theme))

	if strings.Contains(got, "Before\n\nGO") {
		t.Fatalf("unexpected blank separator before code block:\n%s", got)
	}
	if strings.Contains(got, "fmt.Println(\"hi\")\n\nAfter") {
		t.Fatalf("unexpected blank separator after code block:\n%s", got)
	}
}

func TestRenderMessageContentPreservesReadableBlankRowsBetweenProseBlocks(t *testing.T) {
	theme := lookupThemeForTest(t, "default")
	content := strings.Join([]string{
		"### TUI large-session validation gap",
		"",
		"Transcript cache and virtual window helpers exist, but user-facing performance needs proof.",
		"",
		"Remaining likely work:",
		"",
		"- load a large transcript/session and test scroll/render behavior",
		"",
		"- verify cache invalidation on width/theme/content changes",
	}, "\n")

	got := strippedLine(renderMessageContent(content, 64, theme))
	foundBlank := false
	for _, line := range strings.Split(got, "\n") {
		if strings.TrimSpace(line) == "" {
			foundBlank = true
			break
		}
	}
	if !foundBlank {
		t.Fatalf("expected readable blank row between prose blocks:\n%s", got)
	}
}

func TestWrapProseLinesPreservesConsecutiveBlankLines(t *testing.T) {
	got := strings.Join(wrapProseLines("First paragraph.\n\n\nSecond paragraph.", 80), "\n")
	want := "First paragraph.\n\n\nSecond paragraph."
	if got != want {
		t.Fatalf("wrapped prose = %q, want %q", got, want)
	}
}

func TestRenderMessageContentDropsBlankRowsInsideTextOutputBlocks(t *testing.T) {
	theme := lookupThemeForTest(t, "default")
	content := strings.Join([]string{
		"Targeted verification passed:",
		"```text",
		"ok forge/internal/permissions",
		"",
		"ok forge/internal/secscan",
		"",
		"ok forge/internal/react",
		"```",
	}, "\n")

	got := strippedLine(renderMessageContent(content, 80, theme))
	lines := strings.Split(got, "\n")
	first, second, third := -1, -1, -1
	for i, line := range lines {
		switch {
		case strings.Contains(line, "ok forge/internal/permissions"):
			first = i
		case strings.Contains(line, "ok forge/internal/secscan"):
			second = i
		case strings.Contains(line, "ok forge/internal/react"):
			third = i
		}
	}
	if first < 0 || second < 0 || third < 0 {
		t.Fatalf("expected all output lines, got:\n%s", got)
	}
	if second != first+1 || third != second+1 {
		t.Fatalf("unexpected blank visual row in text output block:\n%s", got)
	}
}

func TestRenderMessageContentDoesNotBoxFencedBlocks(t *testing.T) {
	theme := lookupThemeForTest(t, "default")
	content := strings.Join([]string{
		"```text",
		"ok forge/internal/permissions",
		"ok forge/internal/secscan",
		"```",
	}, "\n")

	got := strippedLine(renderMessageContent(content, 80, theme))
	for _, glyph := range []string{"┌", "┐", "└", "┘", "│", "─"} {
		if strings.Contains(got, glyph) {
			t.Fatalf("fenced block should not render as a bordered box, found %q in:\n%s", glyph, got)
		}
	}
	if !strings.Contains(got, "TEXT") || !strings.Contains(got, "ok forge/internal/permissions") {
		t.Fatalf("missing text block label/content:\n%s", got)
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

func TestRenderMessageContentStylesBareHeaders(t *testing.T) {
	withTrueColorProfile(t)

	theme := lookupThemeForTest(t, "default")
	// Weaker models often emit standalone title-case lines as section headers
	// with no ## prefix and no trailing colon.  The context-aware path also
	// catches long headers that reference file paths when they are immediately
	// followed by a list item (the pattern seen in debug session output).
	content := strings.Join([]string{
		"Summary",
		"- Forge is a terminal-first coding agent.",
		"",
		"What I inspected (source-grounded)",
		"- README.md covers project intent.",
		"",
		"High-level architecture and flow",
		"- CLI entrypoint is cmd/forge/main.go.",
		"",
		// Long header with path reference — only caught via context signal.
		"Summary (grounded to README.md and cmd/forge/main.go)",
		"- Purpose: Forge is a terminal-first coding agent.",
	}, "\n")

	got := renderMessageContent(content, 80, theme)

	assertStyledSubstring(t, got, "Summary", theme.AccentPrimary)
	assertStyledSubstring(t, got, "What I inspected (source-grounded)", theme.AccentPrimary)
	assertStyledSubstring(t, got, "High-level architecture and flow", theme.AccentPrimary)
	assertStyledSubstring(t, got, "Summary (grounded to README.md and cmd/forge/main.go)", theme.AccentPrimary)
}

func TestRenderMessageContentStylesWeakerModelOutput(t *testing.T) {
	withTrueColorProfile(t)

	theme := lookupThemeForTest(t, "default")
	// Weaker models often produce colon-style headers and N) numbered lists
	// instead of ## headings and 1. items.
	content := strings.Join([]string{
		"Architecture:",
		"1) Entry point is cmd/forge/main.go",
		"2) Runtime lives under internal/react",
		"**Key files:**",
		"- internal/tui/chatmodel.go",
	}, "\n")

	got := renderMessageContent(content, 80, theme)

	assertStyledSubstring(t, got, "Architecture:", theme.AccentPrimary)
	assertStyledSubstring(t, got, "Key files:", theme.AccentPrimary)
	assertStyledSubstring(t, got, "1)", theme.AccentSecondary)
	assertStyledSubstring(t, got, "2)", theme.AccentSecondary)
	assertStyledSubstring(t, got, "-", theme.AccentSecondary)
	if strings.Contains(got, "**Key files:**") {
		t.Fatalf("expected bold markers stripped from colon header, got: %q", got)
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
