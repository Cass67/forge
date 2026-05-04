package react

import (
	"context"
	"fmt"
	"strings"
	"testing"
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
	// Simulate a session that has already run many turns so compaction
	// threshold checking fires via the else-if branch (compactionMaxFailures > 0
	// and Turn > 50).
	_ = session.RecordInput("first")
	for i := 0; i < 60; i++ {
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
