package agent

import (
	"testing"
	"time"

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

func TestEventRendererThrottlesBurstToolProgress(t *testing.T) {
	events := make(chan llm.Event, 16)
	renderer := NewEventRenderer(events)

	renderer.ToolCall("read_file", "README.md")
	renderer.ToolCall("read_file", "AGENTS.md")

	var progressCount int
	for len(events) > 0 {
		ev := <-events
		if ev.Kind == llm.EventProgress {
			progressCount++
		}
	}
	if progressCount != 1 {
		t.Fatalf("expected burst tool progress to be throttled to one line, got %d", progressCount)
	}

	renderer.lastToolProgressAt = time.Now().Add(-2 * time.Second)
	renderer.ToolCall("git_status", "")
	for len(events) > 0 {
		ev := <-events
		if ev.Kind == llm.EventProgress {
			progressCount++
		}
	}
	if progressCount != 2 {
		t.Fatalf("expected progress emission after throttle window, got %d total lines", progressCount)
	}
}
