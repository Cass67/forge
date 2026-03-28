package harness

import (
	"strings"
	"testing"
)

func TestTraceRecorderBuildsDebugSummary(t *testing.T) {
	recorder := NewRecorder()
	recorder.Record(TraceRecord{
		State:                 StateClassify,
		Family:                FamilyInspect,
		Step:                  StepLocal,
		ThreadPhase:           ThreadPhaseIdeate,
		ClaimGuardStatus:      "evidence_present",
		WorkspacePolicyAction: "Switched to branch forge/inspect-1",
		ToolCallCount:         2,
		Reason:                "inspection language",
	})

	records := recorder.Records()
	if len(records) != 1 {
		t.Fatalf("records = %#v", records)
	}
	got := records[0]
	if got.Timestamp.IsZero() {
		t.Fatal("expected timestamp to be populated")
	}
	for _, want := range []string{
		"state=classify",
		"family=inspect",
		"step=local",
		"thread_phase=ideate",
		"claim_guard=evidence_present",
		"workspace_policy=Switched to branch forge/inspect-1",
		"tool_calls=2",
		"reason=inspection language",
	} {
		if !strings.Contains(got.DebugSummary, want) {
			t.Fatalf("debug summary %q missing %q", got.DebugSummary, want)
		}
	}
}
