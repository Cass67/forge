package promptcomposer

import (
	"strings"
	"testing"
)

func TestComposeStaticSectionOrder(t *testing.T) {
	got := Compose(StaticInput{
		Identity: "identity",
	}, nil)

	wantOrder := []string{
		"identity",
	}
	assertOrder(t, got, wantOrder)
}

func TestComposeIncludesDynamicOverlaysAfterStaticSections(t *testing.T) {
	got := Compose(StaticInput{
		Identity: "identity",
		System:   "system",
	}, []Overlay{
		{Key: "task", Content: "task"},
		{Key: "plan", Content: "plan"},
	})

	wantOrder := []string{
		"identity",
		"system",
		"task",
		"plan",
	}
	assertOrder(t, got, wantOrder)
}

func TestComposeOmitsEmptySections(t *testing.T) {
	got := Compose(StaticInput{
		Identity: "identity",
		System:   "",
	}, []Overlay{
		{Key: "empty", Content: "   "},
		{Key: "task", Content: "task"},
	})

	if strings.Contains(got, "empty") {
		t.Fatalf("compose should omit empty overlay content: %q", got)
	}
	if strings.Contains(got, "\n\n\n") {
		t.Fatalf("compose should not introduce blank section gaps: %q", got)
	}
}

func assertOrder(t *testing.T, got string, parts []string) {
	t.Helper()
	last := -1
	for _, part := range parts {
		idx := strings.Index(got, part)
		if idx < 0 {
			t.Fatalf("compose missing %q in %q", part, got)
		}
		if idx < last {
			t.Fatalf("compose order wrong for %q in %q", part, got)
		}
		last = idx
	}
}
