package gui

import (
	"sync"
	"testing"

	"forge/internal/llm"
)

// Tokens reach the window as coalesced batches, but every byte arrives, in
// order, and a non-token event never overtakes buffered text.
func TestPumpEventsCoalescesTokens(t *testing.T) {
	var mu sync.Mutex
	var got []wireEvent
	_, c := New(func(name string, data any) {
		if name != EventChat {
			return
		}
		mu.Lock()
		defer mu.Unlock()
		got = append(got, data.(wireEvent))
	})

	events := make(chan llm.Event, 64)
	for _, part := range []string{"hel", "lo ", "wor", "ld"} {
		events <- llm.Event{Kind: llm.EventToken, Agent: "forge", Text: part}
	}
	events <- llm.Event{Kind: llm.EventToolCall, Agent: "read_file", Text: "read"}
	events <- llm.Event{Kind: llm.EventToken, Agent: "forge", Text: "done"}
	close(events)
	c.PumpEvents("s1", "/w", events)

	mu.Lock()
	defer mu.Unlock()
	var text string
	var kinds []string
	for _, e := range got {
		kinds = append(kinds, e.Kind)
		if e.Kind == string(llm.EventToken) {
			text += e.Text
		}
	}
	if text != "hello world"+"done" {
		t.Fatalf("token text = %q, want %q", text, "hello worlddone")
	}
	if len(got) != 3 {
		t.Fatalf("emitted %d events %v, want 3 (batch, tool_call, batch)", len(got), kinds)
	}
	if kinds[1] != string(llm.EventToolCall) {
		t.Fatalf("tool call overtook buffered text: %v", kinds)
	}
	for _, e := range got {
		if e.Session != "s1" || e.Workspace != "/w" {
			t.Fatalf("event lost its tags: %+v", e)
		}
	}
}

// Reasoning and answer text are separate streams and must not merge.
func TestPumpEventsKeepsKindsSeparate(t *testing.T) {
	var mu sync.Mutex
	var got []wireEvent
	_, c := New(func(name string, data any) {
		if name != EventChat {
			return
		}
		mu.Lock()
		defer mu.Unlock()
		got = append(got, data.(wireEvent))
	})

	events := make(chan llm.Event, 8)
	events <- llm.Event{Kind: llm.EventReasoning, Text: "think"}
	events <- llm.Event{Kind: llm.EventToken, Agent: "forge", Text: "answer"}
	close(events)
	c.PumpEvents("s1", "/w", events)

	mu.Lock()
	defer mu.Unlock()
	if len(got) != 2 || got[0].Kind != string(llm.EventReasoning) || got[1].Kind != string(llm.EventToken) {
		t.Fatalf("got %+v, want reasoning then token", got)
	}
}
