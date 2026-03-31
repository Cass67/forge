package budget

import "testing"

func TestTracker_DetectsDiminishingReturns(t *testing.T) {
	tr := NewTracker(500, 2)
	if tr.RecordTurn(600) {
		t.Error("should not flag on high delta")
	}
	if tr.RecordTurn(100) {
		t.Error("should not flag on first low delta")
	}
	if !tr.RecordTurn(50) {
		t.Error("should flag on second consecutive low delta")
	}
}

func TestTracker_ResetsStreakOnHighDelta(t *testing.T) {
	tr := NewTracker(500, 2)
	tr.RecordTurn(100)
	tr.RecordTurn(600)
	if tr.DiminishingReturns() {
		t.Error("streak should be reset by high delta")
	}
}

func TestSessionBudget_ErrorBudget(t *testing.T) {
	sb := NewSessionBudget(true, 3)
	if sb.RecordError() {
		t.Error("should not exhaust on first error")
	}
	sb.RecordError()
	if !sb.RecordError() {
		t.Error("should exhaust on third error")
	}
}

func TestSessionBudget_Interactive(t *testing.T) {
	sb := NewSessionBudget(false, 5)
	if sb.IsInteractive() {
		t.Error("should not be interactive")
	}
	sb.SetInteractive(true)
	if !sb.IsInteractive() {
		t.Error("should be interactive after set")
	}
}

func TestSessionBudget_ResetErrors(t *testing.T) {
	sb := NewSessionBudget(true, 3)
	sb.RecordError()
	sb.RecordError()
	sb.RecordError() // exhausted
	sb.ResetErrors()
	// After reset, need 3 more errors to exhaust
	if sb.RecordError() {
		t.Error("should not exhaust immediately after reset")
	}
	sb.RecordError()
	if !sb.RecordError() {
		t.Error("should exhaust on third error after reset")
	}
}

func TestNewTracker_Defaults(t *testing.T) {
	tr := NewTracker(0, 0)
	if tr.threshold != 500 {
		t.Errorf("threshold = %d, want 500", tr.threshold)
	}
	if tr.requiredChecks != 2 {
		t.Errorf("requiredChecks = %d, want 2", tr.requiredChecks)
	}
}

func TestNewSessionBudget_Defaults(t *testing.T) {
	sb := NewSessionBudget(true, 0)
	if sb.maxErrors != 5 {
		t.Errorf("maxErrors = %d, want 5", sb.maxErrors)
	}
}
