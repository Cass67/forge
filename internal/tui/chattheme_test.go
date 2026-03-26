package tui

import (
	"testing"

	"github.com/charmbracelet/lipgloss"
)

func TestChatThemeLookupSupportsNamedThemes(t *testing.T) {
	names := []string{"default", "low", "light", "dusk"}
	for _, name := range names {
		if _, ok := lookupChatTheme(name); !ok {
			t.Fatalf("missing theme %q", name)
		}
	}
}

func TestChatThemeLookupSupportsLegacyAliases(t *testing.T) {
	alias, ok := lookupChatTheme("dark")
	if !ok || alias.ID != "default" {
		t.Fatalf("dark alias = %#v, ok=%v", alias, ok)
	}
	alias, ok = lookupChatTheme("low-contrast")
	if !ok || alias.ID != "low" {
		t.Fatalf("low-contrast alias = %#v, ok=%v", alias, ok)
	}
	alias, ok = lookupChatTheme("lowcontrast")
	if !ok || alias.ID != "low" {
		t.Fatalf("lowcontrast alias = %#v, ok=%v", alias, ok)
	}
}

func TestChatThemeDefaultUsesGraphiteMutedBluePalette(t *testing.T) {
	theme, ok := lookupChatTheme("default")
	if !ok {
		t.Fatal("missing default theme")
	}
	if theme.AppBG != lipgloss.Color("#171a1f") {
		t.Fatalf("app background = %q", theme.AppBG)
	}
	if theme.PanelBG != lipgloss.Color("#1b2027") {
		t.Fatalf("panel background = %q", theme.PanelBG)
	}
	if theme.Text != lipgloss.Color("#c7d0da") {
		t.Fatalf("text = %q", theme.Text)
	}
	if theme.TextDim != lipgloss.Color("#8f98a3") {
		t.Fatalf("text dim = %q", theme.TextDim)
	}
	if theme.AccentPrimary != lipgloss.Color("#7cc7ff") {
		t.Fatalf("accent primary = %q", theme.AccentPrimary)
	}
	if theme.BorderFocus != theme.AccentPrimary || theme.AccentSecondary != theme.AccentPrimary {
		t.Fatalf("expected one shared cool accent, got focus=%q secondary=%q primary=%q", theme.BorderFocus, theme.AccentSecondary, theme.AccentPrimary)
	}
}
