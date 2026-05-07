package react

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"forge/internal/hooks"
)

func TestCompactSessionHistoryTrimsOldTurns(t *testing.T) {
	session := NewSession()
	for i := 1; i <= 6; i++ {
		turn := session.RecordInput(fmt.Sprintf("prompt %d", i))
		session.AppendAssistantMessage(fmt.Sprintf("answer %d", i))
		session.CompleteTurn(turn, fmt.Sprintf("answer %d", i), nil, nil)
	}

	changed := CompactSessionHistory(session, 3)
	if !changed {
		t.Fatal("expected compaction to trigger")
	}
	snap := session.Snapshot()
	if snap.CompactedTurns != 3 {
		t.Fatalf("CompactedTurns = %d, want 3", snap.CompactedTurns)
	}
	if len(snap.Turns) != 3 {
		t.Fatalf("Turns = %d, want 3", len(snap.Turns))
	}
	if len(snap.RecentInputs) != 3 {
		t.Fatalf("RecentInputs = %d, want 3", len(snap.RecentInputs))
	}
	if !strings.Contains(snap.CompactionSummary, "prompt 1") {
		t.Fatalf("CompactionSummary = %q", snap.CompactionSummary)
	}
	if !strings.Contains(snap.CompactionSummary, "answer 1") {
		t.Fatalf("expected semantic summary to include prior outcome, got %q", snap.CompactionSummary)
	}
}

func TestCompactSessionHistorySummarizesToolsAndErrors(t *testing.T) {
	session := NewSession()
	turn := session.RecordInput("inspect repo")
	session.AppendUserMessage("[list_dir] README.md\ninternal")
	session.AppendAssistantMessage("repo overview")
	session.CompleteTurn(turn, "repo overview", []TurnToolCall{{Name: "list_dir"}}, fmt.Errorf("tool timeout"))

	next := session.RecordInput("follow up")
	session.AppendAssistantMessage("done")
	session.CompleteTurn(next, "done", nil, nil)

	if !CompactSessionHistory(session, 1) {
		t.Fatal("expected compaction")
	}

	snap := session.Snapshot()
	if !strings.Contains(snap.CompactionSummary, "inspect repo") {
		t.Fatalf("summary = %q", snap.CompactionSummary)
	}
	if !strings.Contains(snap.CompactionSummary, "tools: list_dir") {
		t.Fatalf("expected tool usage in summary, got %q", snap.CompactionSummary)
	}
	if !strings.Contains(snap.CompactionSummary, "outcome: repo overview") {
		t.Fatalf("expected final outcome in summary, got %q", snap.CompactionSummary)
	}
	if !strings.Contains(snap.CompactionSummary, "error: tool timeout") {
		t.Fatalf("expected error in summary, got %q", snap.CompactionSummary)
	}
}

func TestRunnerRunEmitsCompactionProgress(t *testing.T) {
	driver := &nativeScriptedDriver{responses: []string{"done 1", "done 2", "done 3"}}
	var progress []string
	session := NewSession()
	// Pad so recentInputs exceeds the compaction threshold (40), triggering
	// CompactSessionHistory to return true on the first run.
	for i := 0; i < 40; i++ {
		_ = session.RecordInput("padding")
	}
	r := NewRunner(Config{
		Driver:                driver,
		Session:               session,
		Progress:              func(text string) { progress = append(progress, text) },
		CompactionMaxFailures: 1,
	})

	if err := r.Run(context.Background(), "first"); err != nil {
		t.Fatal(err)
	}
	if err := r.Run(context.Background(), "second"); err != nil {
		t.Fatal(err)
	}
	if err := r.Run(context.Background(), "third"); err != nil {
		t.Fatal(err)
	}

	found := false
	for _, msg := range progress {
		if strings.Contains(strings.ToLower(msg), "compaction circuit breaker") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected compaction progress message, got %#v", progress)
	}
}

func TestRunnerDispatchesCompactionHookPayloads(t *testing.T) {
	driver := &nativeScriptedDriver{responses: []string{"done"}}
	session := NewSession()
	for i := 1; i <= 45; i++ {
		turn := session.RecordInput(fmt.Sprintf("prompt %d", i))
		session.AppendAssistantMessage(fmt.Sprintf("answer %d", i))
		session.CompleteTurn(turn, fmt.Sprintf("answer %d", i), nil, nil)
	}
	before := session.Snapshot()

	var prePayloads []CompactionHookPayload
	var postPayloads []CompactionHookPayload
	r := NewRunner(Config{
		Driver:  driver,
		Session: session,
		ConfigureHooks: func(registry *hooks.Registry) {
			registry.Register(hooks.PointPreCompact, "capture:pre", func(_ context.Context, event hooks.Event) []hooks.Result {
				payload, ok := event.Transient.(CompactionHookPayload)
				if !ok {
					t.Fatalf("pre_compact payload type = %T, want CompactionHookPayload", event.Transient)
				}
				prePayloads = append(prePayloads, payload)
				panic("pre hook failure should be non-fatal")
			})
			registry.Register(hooks.PointPostCompact, "capture:post", func(_ context.Context, event hooks.Event) []hooks.Result {
				payload, ok := event.Transient.(CompactionHookPayload)
				if !ok {
					t.Fatalf("post_compact payload type = %T, want CompactionHookPayload", event.Transient)
				}
				postPayloads = append(postPayloads, payload)
				return nil
			})
		},
	})

	if err := r.Run(context.Background(), "trigger compaction"); err != nil {
		t.Fatal(err)
	}
	after := session.Snapshot()

	if len(prePayloads) != 1 {
		t.Fatalf("pre payload count = %d, want 1", len(prePayloads))
	}
	pre := prePayloads[0]
	if pre.Mode != CompactionSummarize {
		t.Fatalf("pre Mode = %q, want %q", pre.Mode, CompactionSummarize)
	}
	if pre.Reason != "history pressure" {
		t.Fatalf("pre Reason = %q, want history pressure", pre.Reason)
	}
	if pre.KeepTurns != 40 {
		t.Fatalf("pre KeepTurns = %d, want 40", pre.KeepTurns)
	}
	if pre.SummaryLength != len(before.CompactionSummary) {
		t.Fatalf("pre SummaryLength = %d, want %d", pre.SummaryLength, len(before.CompactionSummary))
	}
	if pre.Changed {
		t.Fatal("pre Changed = true, want false before compaction")
	}
	if pre.CircuitOpen {
		t.Fatal("pre CircuitOpen = true, want false")
	}

	if len(postPayloads) != 1 {
		t.Fatalf("post payload count = %d, want 1", len(postPayloads))
	}
	post := postPayloads[0]
	if post.Mode != CompactionSummarize {
		t.Fatalf("post Mode = %q, want %q", post.Mode, CompactionSummarize)
	}
	if post.Reason != "history pressure" {
		t.Fatalf("post Reason = %q, want history pressure", post.Reason)
	}
	if post.KeepTurns != 40 {
		t.Fatalf("post KeepTurns = %d, want 40", post.KeepTurns)
	}
	if post.DroppedTurns != after.CompactedTurns-before.CompactedTurns {
		t.Fatalf("post DroppedTurns = %d, want compacted turn delta %d", post.DroppedTurns, after.CompactedTurns-before.CompactedTurns)
	}
	if post.DroppedTurns <= 0 {
		t.Fatalf("post DroppedTurns = %d, want meaningful positive count", post.DroppedTurns)
	}
	if post.SummaryLength != len(after.CompactionSummary) {
		t.Fatalf("post SummaryLength = %d, want %d", post.SummaryLength, len(after.CompactionSummary))
	}
	if !post.Changed {
		t.Fatal("post Changed = false, want true")
	}
	if post.CircuitOpen {
		t.Fatal("post CircuitOpen = true, want false")
	}
}
