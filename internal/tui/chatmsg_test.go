package tui

import (
	"regexp"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

func TestChatMessageRenderUser(t *testing.T) {
	m := ChatMessage{
		Kind:    MsgUser,
		Header:  "You • 22:59:50",
		Content: "hello world",
	}
	got := m.Render(60, lookupThemeForTest(t, "default"))
	if got == "" {
		t.Fatal("expected non-empty render")
	}
	if !strings.Contains(got, "You • 22:59:50") {
		t.Fatalf("render missing header: %s", got)
	}
	if !strings.Contains(got, "hello world") {
		t.Fatalf("render missing content: %s", got)
	}
	if strings.Contains(strippedLine(got), "▌ You") {
		t.Fatalf("expected user message header without rail, got: %s", strippedLine(got))
	}
}

func TestChatMessageRenderAgent(t *testing.T) {
	m := ChatMessage{
		Kind:    MsgAgent,
		Header:  "Forge • 23:00:00",
		Content: "I can help with that.",
	}
	got := m.Render(60, lookupThemeForTest(t, "default"))
	if got == "" {
		t.Fatal("expected non-empty render")
	}
	if !strings.Contains(got, "I can help with that.") {
		t.Fatalf("render missing content: %s", got)
	}
	if strings.Contains(strippedLine(got), "▌ Forge") {
		t.Fatalf("expected agent message header without rail, got: %s", strippedLine(got))
	}
}

func TestChatMessageRenderDoesNotIndentBodyUnderHeader(t *testing.T) {
	m := ChatMessage{
		Kind:    MsgUser,
		Header:  "You • 22:59:50",
		Content: "hello world",
	}
	got := m.Render(60, lookupThemeForTest(t, "default"))
	lines := strings.Split(got, "\n")
	if len(lines) < 2 {
		t.Fatalf("expected header and body lines, got %q", got)
	}
	bodyLine := strippedLine(lines[1])
	if strings.HasPrefix(bodyLine, "  ") {
		t.Fatalf("expected unindented body line, got %q", bodyLine)
	}
	if !strings.Contains(bodyLine, "hello world") {
		t.Fatalf("expected body content on body line, got %q", bodyLine)
	}
}

func TestChatMessageRenderStatus(t *testing.T) {
	m := ChatMessage{
		Kind:    MsgStatus,
		Content: "Agent complete • 22:59:51",
	}
	got := m.Render(60, lookupThemeForTest(t, "default"))
	if got == "" {
		t.Fatal("expected non-empty render")
	}
}

func TestChatMessageRenderWorking(t *testing.T) {
	m := ChatMessage{
		Kind:    MsgWorking,
		Content: "scout: reading repository structure",
	}
	got := m.Render(60, lookupThemeForTest(t, "default"))
	if got == "" {
		t.Fatal("expected non-empty render")
	}
	if !strings.Contains(got, "scout: reading repository structure") {
		t.Fatalf("working render missing content: %s", got)
	}
}

func TestChatMessageRenderPlan(t *testing.T) {
	m := ChatMessage{
		Kind:    MsgPlan,
		Header:  "Plan",
		Content: "Explanation: Runtime alignment\nPlan:\n- [completed] Inspect loop\n- Tighten prompt",
	}
	got := m.Render(70, lookupThemeForTest(t, "default"))
	if got == "" {
		t.Fatal("expected non-empty render")
	}
	if !strings.Contains(got, "Plan") || !strings.Contains(got, "Tighten prompt") {
		t.Fatalf("plan render missing content: %s", got)
	}
}

func TestChatMessageRenderStatusUsesSemanticStatusProfile(t *testing.T) {
	withTrueColorProfile(t)

	theme := lookupThemeForTest(t, "default")
	got := (ChatMessage{Kind: MsgStatus, Content: "status: approved in 1.2s"}).Render(60, theme)

	assertStyledSubstring(t, got, "status:", theme.TextDim)
	assertStyledSubstring(t, got, "approved", theme.Success)
	assertStyledSubstring(t, got, "1.2s", theme.Success)
}

func TestChatMessageRenderWorkingHighlightsHighSignalTokens(t *testing.T) {
	withTrueColorProfile(t)

	theme := lookupThemeForTest(t, "default")
	got := (ChatMessage{Kind: MsgWorking, Content: "running go test ./... in ./internal/tui"}).Render(80, theme)

	assertStyledSubstring(t, got, "go test ./...", theme.AccentSecondary)
	assertStyledSubstring(t, got, "./internal/tui", theme.TextDim)
}

func TestChatMessageRenderDoesNotForceTranscriptBackgroundFill(t *testing.T) {
	theme := lookupThemeForTest(t, "default")
	m := ChatMessage{
		Kind:    MsgUser,
		Header:  "You • 22:59:50",
		Content: "hello world",
	}

	got := m.Render(60, theme)
	lines := strings.Split(got, "\n")
	var sawHeader bool
	var sawBody bool
	for _, line := range lines {
		if strings.Contains(line, "You") {
			sawHeader = true
			if strings.Contains(line, ansiBackgroundFragment(theme.AppBG)) {
				t.Fatalf("header line should not force app background fill: %q", line)
			}
		}
		if strings.Contains(line, "hello world") {
			sawBody = true
			if strings.Contains(line, ansiBackgroundFragment(theme.AppBG)) {
				t.Fatalf("body line should not force app background fill: %q", line)
			}
		}
	}
	if !sawHeader || !sawBody {
		t.Fatalf("expected header and body lines in render: %q", got)
	}
}

func TestChatMessageStatusRenderDoesNotForceAppBackground(t *testing.T) {
	theme := lookupThemeForTest(t, "default")
	got := (ChatMessage{Kind: MsgStatus, Content: "Agent complete"}).Render(60, theme)
	if strings.Contains(got, ansiBackgroundFragment(theme.AppBG)) {
		t.Fatalf("status render should not force app background: %q", got)
	}
}

func TestChatMessageHeaderDoesNotForceAppBackground(t *testing.T) {
	theme := lookupThemeForTest(t, "default")
	got := (ChatMessage{Kind: MsgForge, Header: "Forge • 10:44:08", Content: "repo overview"}).Render(80, theme)

	lines := strings.Split(got, "\n")
	for _, line := range lines {
		if strings.Contains(line, "Forge") && strings.Contains(line, "10:44:08") && strings.Contains(line, ansiBackgroundFragment(theme.AppBG)) {
			t.Fatalf("message header should not force app background: %q", line)
		}
	}
}

func TestChatMessageForgeHeaderDoesNotInsertStandaloneDividerLine(t *testing.T) {
	m := ChatMessage{
		Kind:    MsgForge,
		Header:  "Forge • 10:44:08",
		Content: "thinking line",
	}
	rendered := m.Render(80, lookupThemeForTest(t, "default"))
	lines := strings.Split(rendered, "\n")
	if len(lines) < 2 {
		t.Fatalf("expected header and body lines, got %q", strippedLine(rendered))
	}
	for idx, line := range lines {
		trimmed := strings.TrimSpace(strippedLine(line))
		if idx > 0 && trimmed == "·" {
			t.Fatalf("unexpected standalone divider line at %d: %q", idx+1, strippedLine(rendered))
		}
	}
}

func TestChatMessageRenderKeepsWordsIntactAcrossWrapBoundaries(t *testing.T) {
	theme := lookupThemeForTest(t, "default")
	msg := ChatMessage{
		Kind:    MsgAgent,
		Header:  "Forge • 16:04:35",
		Content: "It also wasn’t really a harness limitation here, because I had the tools needed to create the file. The failure was that I responded conversationally instead of continuing the task flow and writing the plan into the repo.",
	}

	splitPattern := regexp.MustCompile(`instead o\s*\n\s*f continuing`)
	for width := 40; width <= 120; width++ {
		rendered := strippedLine(msg.Render(width, theme))
		if splitPattern.MatchString(rendered) {
			t.Fatalf("render split short word at width %d:\n%s", width, rendered)
		}
	}
}

func TestChatMessageRenderChangesAcrossThemes(t *testing.T) {
	prevProfile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() {
		lipgloss.SetColorProfile(prevProfile)
	})

	m := ChatMessage{
		Kind:    MsgUser,
		Header:  "You • 22:59:50",
		Content: "hello world",
	}
	defaultRendered := m.Render(60, lookupThemeForTest(t, "default"))
	lightRendered := m.Render(60, lookupThemeForTest(t, "light"))
	if defaultRendered == lightRendered {
		t.Fatal("expected different message rendering across themes")
	}
	if !strings.Contains(defaultRendered, "hello world") || !strings.Contains(lightRendered, "hello world") {
		t.Fatal("expected message content in both renders")
	}
}

func TestChatMessageAccentRoles(t *testing.T) {
	theme := lookupThemeForTest(t, "default")

	if got := (ChatMessage{Kind: MsgUser}).accentColor(theme); got != theme.Success {
		t.Fatalf("user accent = %q, want %q", got, theme.Success)
	}
	if got := (ChatMessage{Kind: MsgAgent}).accentColor(theme); got != theme.AccentPrimary {
		t.Fatalf("agent accent = %q, want %q", got, theme.AccentPrimary)
	}
	if got := (ChatMessage{Kind: MsgForge}).accentColor(theme); got != theme.AccentSecondary {
		t.Fatalf("forge accent = %q, want %q", got, theme.AccentSecondary)
	}
	if got := (ChatMessage{Kind: MsgWorking}).accentColor(theme); got != theme.TextDim {
		t.Fatalf("working accent = %q, want %q", got, theme.TextDim)
	}
}

func lookupThemeForTest(t *testing.T, name string) chatTheme {
	t.Helper()
	theme, ok := lookupChatTheme(name)
	if !ok {
		t.Fatalf("missing theme %q", name)
	}
	return theme
}
