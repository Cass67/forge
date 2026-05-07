package permissions

import "testing"

func TestDenialTrackingConsecutiveLimit(t *testing.T) {
	tracker := NewDenialTracker(3, 20)
	tracker.RecordDenied()
	tracker.RecordDenied()
	if tracker.ShouldFallback() {
		t.Fatal("fallback too early")
	}
	tracker.RecordDenied()
	if !tracker.ShouldFallback() {
		t.Fatal("expected fallback after 3 consecutive denials")
	}
}

func TestDenialTrackingSuccessResetsConsecutive(t *testing.T) {
	tracker := NewDenialTracker(3, 20)
	tracker.RecordDenied()
	tracker.RecordDenied()
	tracker.RecordAllowed()
	tracker.RecordDenied()
	if tracker.ShouldFallback() {
		t.Fatal("success should reset consecutive denials")
	}
}

func TestDenialTrackingTotalLimitAndReset(t *testing.T) {
	tracker := NewDenialTracker(30, 20)
	for range 20 {
		tracker.RecordDenied()
		tracker.RecordAllowed()
	}
	if !tracker.ShouldFallback() {
		t.Fatal("expected fallback after total denial limit")
	}
	tracker.Reset()
	if tracker.ShouldFallback() {
		t.Fatal("reset should clear fallback")
	}
}
