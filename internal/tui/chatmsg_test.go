package tui

import (
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
	if strings.ContainsAny(strippedLine(got), "│╭╮╰╯") {
		t.Fatalf("expected flat user message, got: %s", strippedLine(got))
	}
}

func TestChatMessageRenderAgent(t *testing.T) {
	m := ChatMessage{
		Kind:    MsgAgent,
		Content: "I can help with that.",
	}
	got := m.Render(60, lookupThemeForTest(t, "default"))
	if got == "" {
		t.Fatal("expected non-empty render")
	}
	if !strings.Contains(got, "I can help with that.") {
		t.Fatalf("render missing content: %s", got)
	}
	if strings.ContainsAny(strippedLine(got), "│╭╮╰╯") {
		t.Fatalf("expected flat agent message, got: %s", strippedLine(got))
	}
}

func TestChatMessageRenderIndentsBodyUnderHeader(t *testing.T) {
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
	if !strings.HasPrefix(strings.TrimRight(bodyLine, " "), "  ") {
		t.Fatalf("expected indented body line, got %q", bodyLine)
	}
	if !strings.Contains(bodyLine, "hello world") {
		t.Fatalf("expected body content on indented line, got %q", bodyLine)
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
