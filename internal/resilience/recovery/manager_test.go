package recovery

import (
	"testing"

	forgeerrors "forge/internal/resilience/errors"
)

func TestManager_CanRecover_Retryable(t *testing.T) {
	m := NewManager(3)
	fe := forgeerrors.ForgeError{Retryable: true, Type: "server_error"}
	if !m.CanRecover(fe) {
		t.Error("should recover retryable errors")
	}
}

func TestManager_CanRecover_NonRetryable(t *testing.T) {
	m := NewManager(3)
	fe := forgeerrors.ForgeError{Retryable: false, Type: "auth_error"}
	if m.CanRecover(fe) {
		t.Error("should not recover non-retryable errors")
	}
}

func TestManager_CanRecover_GuardBlocks(t *testing.T) {
	m := NewManager(3)
	m.SetGuard("attempted_compact")
	fe := forgeerrors.ForgeError{Retryable: true, Type: "context_exceeded"}
	if m.CanRecover(fe) {
		t.Error("guard should block recovery")
	}
}

func TestManager_WithholdAndTake(t *testing.T) {
	m := NewManager(3)
	fe := forgeerrors.ForgeError{Type: "test"}
	m.WithholdError(fe)
	out := m.TakeWithheldErrors()
	if len(out) != 1 {
		t.Fatalf("expected 1 withheld error, got %d", len(out))
	}
	if out[0].Type != "test" {
		t.Errorf("expected type test, got %s", out[0].Type)
	}
	// Should be empty after take
	out2 := m.TakeWithheldErrors()
	if len(out2) != 0 {
		t.Error("withheld errors should be cleared after take")
	}
}

func TestManager_Guards(t *testing.T) {
	m := NewManager(3)
	if m.HasGuard("test") {
		t.Error("guard should not be set initially")
	}
	m.SetGuard("test")
	if !m.HasGuard("test") {
		t.Error("guard should be set")
	}
	m.ClearGuard("test")
	if m.HasGuard("test") {
		t.Error("guard should be cleared")
	}
}

func TestManager_CompactionBreaker(t *testing.T) {
	m := NewManager(3)
	cb := m.CompactionBreaker()
	if cb == nil {
		t.Fatal("compaction breaker should not be nil")
	}
	cb.RecordFailure()
	cb.RecordFailure()
	cb.RecordFailure()
	if cb.State().String() != "open" {
		t.Error("breaker should be open after 3 failures")
	}
}

func TestManager_CanRecover_GuardDoesNotBlockOtherTypes(t *testing.T) {
	m := NewManager(3)
	m.SetGuard(GuardCompaction)
	fe := forgeerrors.ForgeError{Retryable: true, Type: "server_error"}
	if !m.CanRecover(fe) {
		t.Error("guard should only block context_exceeded, not server_error")
	}
}
