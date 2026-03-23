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
