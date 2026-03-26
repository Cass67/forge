package agent

import (
	"testing"

	"forge/internal/llm"
)

func TestProgressRendererSuppressesRecoverableFailureProgress(t *testing.T) {
	events := make(chan llm.Event, 4)
	renderer := NewSubAgentRenderer(NewEventRenderer(events), "scout")

	renderer.ToolResult("read_file", "permission denied", "", true)

	var sawFailureProgress bool
	for len(events) > 0 {
		ev := <-events
		if ev.Kind == llm.EventProgress {
			sawFailureProgress = true
		}
	}
	if sawFailureProgress {
		t.Fatal("expected recoverable tool errors to stay out of transcript progress")
	}
}
