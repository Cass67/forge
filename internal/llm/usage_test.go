package llm_test

import (
	"testing"

	"forge/internal/llm"
)

func TestUsageTrackerEmpty(t *testing.T) {
	tracker := llm.NewUsageTracker()
	total := tracker.Total()
	if total.InputTokens != 0 || total.OutputTokens != 0 {
		t.Fatalf("expected zero totals, got %+v", total)
	}
	if len(tracker.Entries()) != 0 {
		t.Fatalf("expected no entries, got %d", len(tracker.Entries()))
	}
}

func TestUsageTrackerRecord(t *testing.T) {
	tracker := llm.NewUsageTracker()
	tracker.Record(llm.UsageEntry{
		Agent: "writer",
		Model: "test-model",
		Pass:  1,
		Round: 1,
		Usage: llm.Usage{InputTokens: 100, OutputTokens: 50},
	})
	tracker.Record(llm.UsageEntry{
		Agent: "auditor",
		Model: "test-model",
		Pass:  1,
		Round: 1,
		Usage: llm.Usage{InputTokens: 200, OutputTokens: 75},
	})

	entries := tracker.Entries()
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}
	if entries[0].Agent != "writer" {
		t.Errorf("expected first entry agent=writer, got %q", entries[0].Agent)
	}

	total := tracker.Total()
	if total.InputTokens != 300 {
		t.Errorf("expected 300 input tokens, got %d", total.InputTokens)
	}
	if total.OutputTokens != 125 {
		t.Errorf("expected 125 output tokens, got %d", total.OutputTokens)
	}
}

func TestUsageTrackerEntriesIsCopy(t *testing.T) {
	tracker := llm.NewUsageTracker()
	tracker.Record(llm.UsageEntry{
		Agent: "writer",
		Usage: llm.Usage{InputTokens: 10, OutputTokens: 5},
	})

	entries := tracker.Entries()
	entries[0].Usage.InputTokens = 9999

	original := tracker.Entries()
	if original[0].Usage.InputTokens != 10 {
		t.Error("Entries() should return a copy, but original was modified")
	}
}

func TestEstimateTokens(t *testing.T) {
	tests := []struct {
		input    string
		expected int
	}{
		{"", 0},
		{"a", 1},
		{"abcd", 1},
		{"abcde", 2},
		{"hello world", 3},
	}
	for _, tc := range tests {
		got := llm.EstimateTokens(tc.input)
		if got != tc.expected {
			t.Errorf("EstimateTokens(%q) = %d, want %d", tc.input, got, tc.expected)
		}
	}
}
