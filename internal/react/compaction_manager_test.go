package react

import (
	"strings"
	"testing"

	"forge/internal/llm"
)

func TestCompactionManagerBelowThresholdNoCompaction(t *testing.T) {
	session := NewSession()
	session.RecordInput("small")
	mgr := NewCompactionManager(CompactionConfig{KeepTurns: 40, HistoryPressureTurns: 40, LargeToolResultBytes: 1024})

	decision := mgr.Decide(session.Snapshot())
	if decision.Mode != CompactionNone {
		t.Fatalf("Mode = %q, want %q", decision.Mode, CompactionNone)
	}
}

func TestCompactionManagerOldToolResultMicrocompacts(t *testing.T) {
	snap := SessionSnapshot{History: []llm.Message{{Role: llm.RoleTool, Content: strings.Repeat("x", 2048)}}}
	mgr := NewCompactionManager(CompactionConfig{KeepTurns: 40, LargeToolResultBytes: 1024})

	decision := mgr.Decide(snap)
	if decision.Mode != CompactionMicro {
		t.Fatalf("Mode = %q, want %q", decision.Mode, CompactionMicro)
	}
}

func TestCompactionManagerHighHistoryPressureSummarizes(t *testing.T) {
	session := NewSession()
	for i := range 45 {
		session.RecordInput("turn")
		session.CompleteTurn(i+1, "ok", nil, nil)
	}
	mgr := NewCompactionManager(CompactionConfig{KeepTurns: 10, HistoryPressureTurns: 40})

	decision := mgr.Decide(session.Snapshot())
	if decision.Mode != CompactionSummarize {
		t.Fatalf("Mode = %q, want %q", decision.Mode, CompactionSummarize)
	}
	if !mgr.Apply(session, decision) {
		t.Fatal("expected compaction to apply")
	}
	if got := len(session.Snapshot().RecentInputs); got > 10 {
		t.Fatalf("recent inputs = %d, want <= 10", got)
	}
}

func TestCompactionManagerRepeatedFailuresOpenCircuitBreaker(t *testing.T) {
	mgr := NewCompactionManager(CompactionConfig{MaxFailures: 2})
	mgr.RecordFailure()
	if mgr.CircuitOpen() {
		t.Fatal("circuit opened too early")
	}
	mgr.RecordFailure()
	if !mgr.CircuitOpen() {
		t.Fatal("expected circuit to open")
	}
	decision := mgr.Decide(SessionSnapshot{Turn: 100})
	if decision.Mode != CompactionNone {
		t.Fatalf("Mode = %q, want none while circuit open", decision.Mode)
	}
}

func TestCompactionManagerUserPartialCompactsSelectedRange(t *testing.T) {
	session := NewSession()
	for i := range 12 {
		session.RecordInput("turn")
		session.CompleteTurn(i+1, "ok", nil, nil)
	}
	mgr := NewCompactionManager(CompactionConfig{KeepTurns: 40})
	decision := mgr.UserPartial(5)

	if decision.Mode != CompactionUserPartial {
		t.Fatalf("Mode = %q, want %q", decision.Mode, CompactionUserPartial)
	}
	if !mgr.Apply(session, decision) {
		t.Fatal("expected user partial compaction to apply")
	}
	if got := len(session.Snapshot().RecentInputs); got != 5 {
		t.Fatalf("recent inputs = %d, want 5", got)
	}
}
