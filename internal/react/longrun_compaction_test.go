package react

import (
	"fmt"
	"strings"
	"testing"

	"forge/internal/llm"
)

// buildLongSingleTurnSession models autonomous agent work: ONE user input
// followed by many tool call/result steps. This is the shape that long-running
// sessions actually take, and the shape no existing compaction test covers.
func buildLongSingleTurnSession(t testing.TB, steps int, resultBytes int) (*Session, *fakeDurableSink) {
	t.Helper()
	sink := &fakeDurableSink{}
	session := NewSession()
	session.SetDurableSink(sink)
	session.RecordInput("audit the whole repository and fix every issue you find")
	body := strings.Repeat("x", resultBytes)
	for i := range steps {
		callID := fmt.Sprintf("call-%d", i)
		if err := session.AppendAssistantToolTurn("", []llm.NativeToolCall{
			{ID: callID, Name: "read_file", ArgsJSON: fmt.Sprintf(`{"path":"file-%d.go"}`, i)},
		}); err != nil {
			t.Fatal(err)
		}
		if err := session.AppendNativeToolResult(callID, body); err != nil {
			t.Fatal(err)
		}
	}
	return session, sink
}

// TestCompactSessionHistoryIsNoOpWithinOneTurn pins the core defect: compaction
// counts user turns, but an autonomous run is a single turn, so summarize
// compaction can never drop anything -- and it reports success anyway.
func TestCompactSessionHistoryIsNoOpWithinOneTurn(t *testing.T) {
	session, _ := buildLongSingleTurnSession(t, 200, 512)

	before := len(session.Snapshot().History)
	changed := CompactSessionHistory(session, 20)
	after := len(session.Snapshot().History)

	if after >= before && changed {
		t.Fatalf("compaction reported changed=true but history stayed at %d messages "+
			"(before=%d): summarize compaction is a no-op inside one turn, and the "+
			"false success resets the failure circuit breaker", after, before)
	}
	if after >= before {
		t.Fatalf("compaction did not shrink history: before=%d after=%d", before, after)
	}
}

// TestLongSingleTurnPromptStaysBounded is the end-to-end reliability property:
// an hours-long single-turn run must keep its prompt under budget.
func TestLongSingleTurnPromptStaysBounded(t *testing.T) {
	const budget = 256 * 1024
	session, _ := buildLongSingleTurnSession(t, 300, 4096)

	manager := NewCompactionManager(CompactionConfig{
		KeepTurns:         20,
		PromptBudgetBytes: budget,
		MaxFailures:       3,
	})

	// Give compaction every chance: drive it to a fixed point.
	for range 10 {
		messages := session.Messages("system prompt")
		decision := manager.DecidePromptPressure(messages)
		if decision.Mode == CompactionNone {
			break
		}
		if !manager.Apply(session, decision) {
			break
		}
	}

	got := estimatePromptBytes(session.Messages("system prompt"))
	if got > budget {
		t.Fatalf("prompt is %d bytes after compaction, over the %d budget: "+
			"a long single-turn run cannot be compacted below its context limit", got, budget)
	}
}

// TestReplayPreservesCompaction pins the drift between the two representations:
// the live session is compacted, but replaying the durable log rebuilds the
// full pre-compaction history, so resuming a session inflates its context.
func TestReplayPreservesCompaction(t *testing.T) {
	session, sink := buildLongSingleTurnSession(t, 40, 4096)

	manager := NewCompactionManager(CompactionConfig{
		KeepTurns:             20,
		PromptBudgetBytes:     64 * 1024,
		PromptToolResultBytes: 1024,
		MaxFailures:           3,
	})
	for range 10 {
		decision := manager.DecidePromptPressure(session.Messages("system prompt"))
		if decision.Mode == CompactionNone || !manager.Apply(session, decision) {
			break
		}
	}

	live := estimatePromptBytes(session.Messages("system prompt"))

	resumed := NewSession()
	if _, err := resumed.AdoptReplayItems(sink.Items()); err != nil {
		t.Fatal(err)
	}
	restored := estimatePromptBytes(resumed.Messages("system prompt"))

	if restored > live {
		t.Fatalf("resumed session is larger than the live one it replaced: "+
			"live=%d bytes, resumed=%d bytes. ReplayItems ignores compaction, so "+
			"resuming a compacted session restores the full context it just shed",
			live, restored)
	}
}

// TestLivePromptEqualsReplayedPrompt is the invariant that keeps the two
// representations from drifting again: the prompt a running session sends must
// be exactly the prompt its own log reproduces. Every past regression in this
// area was a divergence between those two, so assert them equal directly.
func TestLivePromptEqualsReplayedPrompt(t *testing.T) {
	session, sink := buildLongSingleTurnSession(t, 60, 8192)
	session.SetLastAssistantReasoning("thinking about the audit")

	manager := NewCompactionManager(CompactionConfig{
		KeepTurns:             20,
		PromptBudgetBytes:     96 * 1024,
		PromptToolResultBytes: 2048,
		MaxFailures:           3,
	})
	for range 10 {
		decision := manager.DecidePromptPressure(session.Messages("system prompt"))
		if decision.Mode == CompactionNone || !manager.Apply(session, decision) {
			break
		}
	}

	live := session.Messages("system prompt")

	resumed := NewSession()
	if _, err := resumed.AdoptReplayItems(sink.Items()); err != nil {
		t.Fatal(err)
	}
	replayed := resumed.Messages("system prompt")

	if len(live) != len(replayed) {
		t.Fatalf("live prompt has %d messages, replayed has %d", len(live), len(replayed))
	}
	for i := range live {
		if live[i].Role != replayed[i].Role {
			t.Fatalf("message %d role: live=%q replayed=%q", i, live[i].Role, replayed[i].Role)
		}
		if live[i].Content != replayed[i].Content {
			t.Fatalf("message %d content diverged:\nlive=%q\nreplayed=%q", i, live[i].Content, replayed[i].Content)
		}
		if live[i].ToolCallID != replayed[i].ToolCallID {
			t.Fatalf("message %d tool_call_id: live=%q replayed=%q", i, live[i].ToolCallID, replayed[i].ToolCallID)
		}
		if len(live[i].ToolCalls) != len(replayed[i].ToolCalls) {
			t.Fatalf("message %d tool call count: live=%d replayed=%d", i, len(live[i].ToolCalls), len(replayed[i].ToolCalls))
		}
		for j := range live[i].ToolCalls {
			if live[i].ToolCalls[j].ArgsJSON != replayed[i].ToolCalls[j].ArgsJSON {
				t.Fatalf("message %d call %d args diverged:\nlive=%q\nreplayed=%q",
					i, j, live[i].ToolCalls[j].ArgsJSON, replayed[i].ToolCalls[j].ArgsJSON)
			}
		}
	}
}
