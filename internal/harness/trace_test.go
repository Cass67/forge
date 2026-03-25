package harness

import (
	"strings"
	"testing"
)

func TestRecorderBuildsDebugSummary(t *testing.T) {
	recorder := NewRecorder()
	recorder.Record(TraceRecord{
		State:  StateClassify,
		Family: FamilyInspect,
		Step:   StepLocal,
		Reason: "inspection language",
	})

	records := recorder.Records()
	if len(records) != 1 {
		t.Fatalf("records = %#v", records)
	}
	got := records[0]
	if got.Timestamp.IsZero() {
		t.Fatal("expected timestamp to be populated")
	}
	for _, want := range []string{"state=classify", "family=inspect", "step=local", "reason=inspection language"} {
		if !strings.Contains(got.DebugSummary, want) {
			t.Fatalf("debug summary %q missing %q", got.DebugSummary, want)
		}
	}
}
