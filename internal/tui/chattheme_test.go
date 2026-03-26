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
	if theme.AppBG != lipgloss.Color("#0b1016") {
		t.Fatalf("app background = %q", theme.AppBG)
	}
	if theme.PanelBG != theme.AppBG {
		t.Fatalf("panel background = %q", theme.PanelBG)
	}
	if theme.HeaderBG != theme.AppBG {
		t.Fatalf("header background = %q", theme.HeaderBG)
	}
	if theme.Text != lipgloss.Color("#eaf0f8") {
		t.Fatalf("text = %q", theme.Text)
	}
	if theme.TextDim != lipgloss.Color("#8d9aae") {
		t.Fatalf("text dim = %q", theme.TextDim)
	}
	if theme.AccentPrimary != lipgloss.Color("#79c0ff") {
		t.Fatalf("accent primary = %q", theme.AccentPrimary)
	}
	if theme.Border != lipgloss.Color("#1f2b38") {
		t.Fatalf("border = %q", theme.Border)
	}
	if theme.BorderFocus != theme.AccentPrimary {
		t.Fatalf("expected focus border to match primary accent, got focus=%q primary=%q", theme.BorderFocus, theme.AccentPrimary)
	}
}
