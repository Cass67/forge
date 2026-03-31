package hooks

import (
	"context"
	"testing"
)

func TestHookRuntimeDispatchUsesRegistrationOrder(t *testing.T) {
	registry := NewRegistry()
	var calls []string

	registry.Register(PointPromptContext, "first", func(_ context.Context, event Event) []Result {
		payload, ok := event.Transient.(*testPayload)
		if !ok {
			t.Fatalf("transient payload type = %T", event.Transient)
		}
		if payload.Name != "caller-owned" {
			t.Fatalf("transient payload = %#v", payload)
		}
		calls = append(calls, "first")
		return []Result{OverlayResult{Key: "first", Content: "first overlay"}}
	})
	registry.Register(PointPromptContext, "second", func(_ context.Context, _ Event) []Result {
		calls = append(calls, "second")
		return []Result{OverlayResult{Key: "second", Content: "second overlay"}}
	})

	output := registry.Dispatch(context.Background(), Event{
		Point:     PointPromptContext,
		Transient: &testPayload{Name: "caller-owned"},
	})

	if got, want := len(calls), 2; got != want {
		t.Fatalf("calls = %v, want %d calls", calls, want)
	}
	if calls[0] != "first" || calls[1] != "second" {
		t.Fatalf("call order = %v", calls)
	}
	if got, want := len(output.Overlays), 2; got != want {
		t.Fatalf("overlay count = %d, want %d", got, want)
	}
	if output.Overlays[0].Key != "first" || output.Overlays[1].Key != "second" {
		t.Fatalf("overlays = %#v", output.Overlays)
	}
}

func TestHookRuntimeRegisterSupportsZeroValueRegistry(t *testing.T) {
	var registry Registry

	registry.Register(PointPromptContext, "handler", func(_ context.Context, _ Event) []Result {
		return []Result{OverlayResult{Key: "overlay", Content: "content"}}
	})

	output := registry.Dispatch(context.Background(), Event{Point: PointPromptContext})
	if got, want := len(output.Overlays), 1; got != want {
		t.Fatalf("overlay count = %d, want %d", got, want)
	}
	if output.Overlays[0].Key != "overlay" {
		t.Fatalf("overlays = %#v", output.Overlays)
	}
}

func TestHookRuntimeDispatchStopsAfterBlock(t *testing.T) {
	registry := NewRegistry()
	calledAfterBlock := false

	registry.Register(PointBeforeTool, "before", func(_ context.Context, _ Event) []Result {
		return []Result{OverlayResult{Key: "before", Content: "before overlay"}}
	})
	registry.Register(PointBeforeTool, "blocker", func(_ context.Context, _ Event) []Result {
		return []Result{
			OverlayResult{Key: "ignored", Content: "should not be collected"},
			BlockResult{Message: "tool execution is blocked", Provenance: "runtime"},
		}
	})
	registry.Register(PointBeforeTool, "after", func(_ context.Context, _ Event) []Result {
		calledAfterBlock = true
		return nil
	})

	output := registry.Dispatch(context.Background(), Event{Point: PointBeforeTool})

	if calledAfterBlock {
		t.Fatal("handler after block should not run")
	}
	if output.Block == nil {
		t.Fatal("expected block result")
	}
	if output.Block.Message != "tool execution is blocked" {
		t.Fatalf("block message = %q", output.Block.Message)
	}
	if got, want := len(output.Overlays), 1; got != want {
		t.Fatalf("overlay count = %d, want %d", got, want)
	}
	if output.Overlays[0].Key != "before" {
		t.Fatalf("overlays = %#v", output.Overlays)
	}
}

func TestHookRuntimeDispatchIgnoresBlankBlockResults(t *testing.T) {
	registry := NewRegistry()
	calledAfterBlankBlock := false

	registry.Register(PointBeforeTool, "blank-block", func(_ context.Context, _ Event) []Result {
		return []Result{BlockResult{}}
	})
	registry.Register(PointBeforeTool, "after", func(_ context.Context, _ Event) []Result {
		calledAfterBlankBlock = true
		return []Result{OverlayResult{Key: "after", Content: "still runs"}}
	})

	output := registry.Dispatch(context.Background(), Event{Point: PointBeforeTool})

	if output.Block != nil {
		t.Fatalf("unexpected block = %#v", output.Block)
	}
	if !calledAfterBlankBlock {
		t.Fatal("blank block should not stop later handlers")
	}
	if got, want := len(output.Overlays), 1; got != want {
		t.Fatalf("overlay count = %d, want %d", got, want)
	}
	if output.Overlays[0].Key != "after" {
		t.Fatalf("overlays = %#v", output.Overlays)
	}
}

func TestHookRuntimeDispatchContainsPanics(t *testing.T) {
	registry := NewRegistry()

	registry.Register(PointPromptContext, "panic", func(_ context.Context, _ Event) []Result {
		panic("boom")
	})
	registry.Register(PointPromptContext, "after", func(_ context.Context, _ Event) []Result {
		return []Result{OverlayResult{Key: "after", Content: "still runs"}}
	})

	output := registry.Dispatch(context.Background(), Event{Point: PointPromptContext})

	if got, want := len(output.Failures), 1; got != want {
		t.Fatalf("failure count = %d, want %d", got, want)
	}
	if output.Failures[0].Handler != "panic" {
		t.Fatalf("failure handler = %q", output.Failures[0].Handler)
	}
	if got, want := len(output.Overlays), 1; got != want {
		t.Fatalf("overlay count = %d, want %d", got, want)
	}
	if output.Overlays[0].Key != "after" {
		t.Fatalf("overlays = %#v", output.Overlays)
	}
}

func TestHookRuntimeDispatchNormalizesVisibleNoteByPriorityThenOrder(t *testing.T) {
	registry := NewRegistry()

	registry.Register(PointPromptContext, "low", func(_ context.Context, _ Event) []Result {
		return []Result{NoteResult{Message: "low note", Priority: PriorityLow, Provenance: "runtime"}}
	})
	registry.Register(PointPromptContext, "high-first", func(_ context.Context, _ Event) []Result {
		return []Result{NoteResult{Message: "winning note", Priority: PriorityHigh, Provenance: "runtime"}}
	})
	registry.Register(PointPromptContext, "high-second", func(_ context.Context, _ Event) []Result {
		return []Result{NoteResult{Message: "losing tie", Priority: PriorityHigh, Provenance: "runtime"}}
	})

	output := registry.Dispatch(context.Background(), Event{Point: PointPromptContext})

	if output.Note == nil {
		t.Fatal("expected normalized note")
	}
	if output.Note.Message != "winning note" {
		t.Fatalf("normalized note = %#v", output.Note)
	}
}

type testPayload struct {
	Name string
}
