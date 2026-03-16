package llm_test

import (
	"testing"
	"forge/internal/llm"
)

func TestRoleConstants(t *testing.T) {
	if llm.RoleSystem != "system" {
		t.Fatalf("expected system, got %s", llm.RoleSystem)
	}
	if llm.RoleUser != "user" {
		t.Fatalf("expected user, got %s", llm.RoleUser)
	}
	if llm.RoleAssistant != "assistant" {
		t.Fatalf("expected assistant, got %s", llm.RoleAssistant)
	}
}

func TestEventKindConstants(t *testing.T) {
	kinds := []llm.EventKind{
		llm.EventToken,
		llm.EventRoundEnd,
		llm.EventPassEnd,
		llm.EventError,
		llm.EventDone,
		llm.EventAbort,
	}
	for _, k := range kinds {
		if k == "" {
			t.Fatal("event kind must not be empty")
		}
	}
}
