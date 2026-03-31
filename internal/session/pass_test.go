package session_test

import (
	"forge/internal/session"
	"testing"
)

func TestDefaultPasses(t *testing.T) {
	passes := session.DefaultPasses(3)
	if len(passes) != 4 {
		t.Fatalf("expected 4 passes, got %d", len(passes))
	}
	names := []string{"correctness", "refactor", "security", "prod"}
	for i, p := range passes {
		if p.Name != names[i] {
			t.Errorf("pass %d: expected %s, got %s", i, names[i], p.Name)
		}
	}
}

func TestShouldContinueAlwaysTrue(t *testing.T) {
	p := session.Pass{Name: "test", Rounds: 3}
	for round := 1; round <= 5; round++ {
		if !p.ShouldContinue(round, "any audit text") {
			t.Errorf("expected ShouldContinue true at round %d", round)
		}
	}
}
