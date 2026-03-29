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
		session.RecordInput(fmt.Sprintf("prompt %d", i))
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
}

func TestRunnerRunEmitsCompactionProgress(t *testing.T) {
	driver := &scriptedDriver{responses: []string{"done 1", "done 2", "done 3"}}
	var progress []string
	r := NewRunner(Config{
		Driver:          driver,
		Session:         NewSession(),
		Progress:        func(text string) { progress = append(progress, text) },
		MaxSessionTurns: 2,
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
		if strings.Contains(strings.ToLower(msg), "compacted session context") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected compaction progress message, got %#v", progress)
	}
}
