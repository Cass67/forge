package tui

import "testing"

func TestLiveProgressReplacesLatestMessageWithinActiveTrack(t *testing.T) {
	slot := LiveProgressState{}
	slot = slot.Apply(ProgressUpdate{TurnID: 3, ReplaceKey: "active", Message: "reviewing the repo"})
	slot = slot.Apply(ProgressUpdate{TurnID: 3, ReplaceKey: "active", Message: "checking tests"})
	if slot.LatestMessage() != "checking tests" {
		t.Fatalf("slot = %#v", slot)
	}
	if got := slot.RenderMessage(); got != "checking tests" {
		t.Fatalf("slot = %#v", slot)
	}
}

func TestLiveProgressFinalizeReturnsSystemNote(t *testing.T) {
	slot := LiveProgressState{
		TurnID:     7,
		ReplaceKey: "active",
		Entries:    []string{"reviewing the repo", "checking tests"},
	}

	record, ok := slot.Finalize()
	if !ok {
		t.Fatal("expected finalized record")
	}
	if record.Kind != RecordSystem {
		t.Fatalf("record kind = %v", record.Kind)
	}
	if record.TurnID != 7 {
		t.Fatalf("turn id = %d", record.TurnID)
	}
	if len(record.Segments) != 1 || record.Segments[0].Kind != SegmentText || record.Segments[0].Text != "reviewing the repo\nchecking tests" {
		t.Fatalf("record = %#v", record)
	}
	if !record.Final {
		t.Fatalf("record = %#v", record)
	}
}

func TestLiveProgressResetClearsState(t *testing.T) {
	slot := LiveProgressState{
		TurnID:     7,
		ReplaceKey: "active",
		Entries:    []string{"checking tests"},
	}
	if got := slot.Reset(); !got.IsZero() {
		t.Fatalf("slot = %#v", got)
	}
}
