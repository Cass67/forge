package circuit

import (
	"testing"
	"time"
)

func TestBreaker_AllowsWhenClosed(t *testing.T) {
	b := NewBreaker("test", 3, time.Minute)
	if !b.Allow() {
		t.Error("closed breaker should allow")
	}
}

func TestBreaker_TripsAfterMaxFailures(t *testing.T) {
	b := NewBreaker("test", 3, time.Minute)
	b.RecordFailure()
	b.RecordFailure()
	if b.State() != StateClosed {
		t.Error("should still be closed after 2 failures")
	}
	b.RecordFailure()
	if b.State() != StateOpen {
		t.Error("should be open after 3 failures")
	}
	if b.Allow() {
		t.Error("open breaker should not allow")
	}
}

func TestBreaker_ResetsOnSuccess(t *testing.T) {
	b := NewBreaker("test", 3, time.Minute)
	b.RecordFailure()
	b.RecordFailure()
	b.RecordSuccess()
	if b.Failures() != 0 {
		t.Errorf("failures = %d, want 0", b.Failures())
	}
	if b.State() != StateClosed {
		t.Error("state should be closed after success")
	}
}

func TestBreaker_HalfOpenAfterCooldown(t *testing.T) {
	// Use a very short cooldown for testing
	b := NewBreaker("test", 2, 50*time.Millisecond)
	b.RecordFailure()
	b.RecordFailure()
	if b.State() != StateOpen {
		t.Fatal("should be open")
	}
	time.Sleep(60 * time.Millisecond)
	if !b.Allow() {
		t.Error("should allow after cooldown")
	}
	if b.State() != StateHalfOpen {
		t.Errorf("state = %v, want half-open", b.State())
	}
}

func TestBreaker_Reset(t *testing.T) {
	b := NewBreaker("test", 2, time.Minute)
	b.RecordFailure()
	b.RecordFailure()
	b.Reset()
	if b.State() != StateClosed {
		t.Error("should be closed after reset")
	}
	if b.Failures() != 0 {
		t.Error("failures should be 0 after reset")
	}
}

func TestBreaker_HalfOpenFailureReopens(t *testing.T) {
	b := NewBreaker("test", 2, 50*time.Millisecond)
	b.RecordFailure()
	b.RecordFailure()
	if b.State() != StateOpen {
		t.Fatal("should be open")
	}
	time.Sleep(60 * time.Millisecond)
	b.Allow() // transitions to half-open
	if b.State() != StateHalfOpen {
		t.Fatal("should be half-open after Allow()")
	}
	b.RecordFailure() // probe fails → re-open
	if b.State() != StateOpen {
		t.Errorf("state = %v, want open", b.State())
	}
}

func TestBreaker_NewBreakerValidation(t *testing.T) {
	b := NewBreaker("test", 0, 0)
	if b.maxFailures != 1 {
		t.Errorf("maxFailures = %d, want 1", b.maxFailures)
	}
	if b.cooldown != 5*time.Minute {
		t.Errorf("cooldown = %v, want 5m", b.cooldown)
	}
}

func TestBreaker_IgnoresFailuresWhileOpen(t *testing.T) {
	b := NewBreaker("test", 2, time.Hour)
	b.RecordFailure()
	b.RecordFailure()
	if b.State() != StateOpen {
		t.Fatal("should be open")
	}
	// Record more failures — should not extend cooldown
	b.RecordFailure()
	b.RecordFailure()
	b.RecordFailure()
	if b.State() != StateOpen {
		t.Error("should still be open")
	}
	// Cooldown hasn't elapsed, so Allow should still block
	if b.Allow() {
		t.Error("open breaker should not allow")
	}
}
