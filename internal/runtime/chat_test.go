package runtime

import "testing"

func TestResolveModelExactAndIndexed(t *testing.T) {
	models := []string{"alpha/model-a", "beta/model-b", "gamma/model-c"}
	if got := ResolveModel(models, "2"); got != "beta/model-b" {
		t.Fatalf("expected indexed model, got %q", got)
	}
	if got := ResolveModel(models, "gamma/model-c"); got != "gamma/model-c" {
		t.Fatalf("expected exact model, got %q", got)
	}
}

func TestResolveModelAmbiguousSubstringReturnsEmpty(t *testing.T) {
	models := []string{"openai/gpt-4o", "openai/gpt-4o-mini"}
	if got := ResolveModel(models, "gpt-4o"); got != "openai/gpt-4o" {
		t.Fatalf("expected exact match to win, got %q", got)
	}
	if got := ResolveModel(models, "openai"); got != "" {
		t.Fatalf("expected ambiguous substring to return empty, got %q", got)
	}
}
