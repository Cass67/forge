package hooks

import (
	"testing"

	"forge/internal/agent/promptcomposer"
)

func TestToPromptOverlaysPreservesPriorityAndProvenance(t *testing.T) {
	got := ToPromptOverlays([]Overlay{
		{
			Key:        "plan_blocker",
			Content:    "Resolve the blocker before continuing broad work.",
			Priority:   PriorityHigh,
			Provenance: "runtime",
		},
	})

	if len(got) != 1 {
		t.Fatalf("overlays = %#v", got)
	}
	if got[0].Priority != promptcomposer.PriorityHigh {
		t.Fatalf("priority = %v", got[0].Priority)
	}
	if got[0].Key != "hook:plan_blocker" {
		t.Fatalf("key = %q", got[0].Key)
	}
	if got[0].Content != "[hook:runtime]\nResolve the blocker before continuing broad work." {
		t.Fatalf("content = %q", got[0].Content)
	}
}
