package tui

import "testing"

func TestChatThemeLookupSupportsNamedThemes(t *testing.T) {
	names := []string{"default", "low", "light", "dusk"}
	for _, name := range names {
		if _, ok := lookupChatTheme(name); !ok {
			t.Fatalf("missing theme %q", name)
		}
	}
}

func TestChatThemeLookupSupportsLegacyAliases(t *testing.T) {
	theme, ok := lookupChatTheme("default")
	if !ok {
		t.Fatal("default theme missing")
	}
	alias, ok := lookupChatTheme("low")
	if !ok || alias.ID != "low" {
		t.Fatalf("low alias = %#v, ok=%v", alias, ok)
	}
	if theme.ID != "default" {
		t.Fatalf("default theme = %#v", theme)
	}
}
