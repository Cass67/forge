// Package budget monitors token deltas across turns to detect diminishing returns
// and tracks session error budgets for foreground vs background classification.
package budget

import "sync"

// Tracker monitors token deltas across turns to detect diminishing returns.
type Tracker struct {
	mu             sync.RWMutex
	threshold      int
	requiredChecks int
	lowDeltaStreak int
}

// NewTracker creates a budget tracker.
// threshold: token delta below which progress is considered diminishing.
// requiredChecks: consecutive low-delta turns before flagging.
func NewTracker(threshold, requiredChecks int) *Tracker {
	if threshold <= 0 {
		threshold = 500
	}
	if requiredChecks <= 0 {
		requiredChecks = 2
	}
	return &Tracker{
		threshold:      threshold,
		requiredChecks: requiredChecks,
	}
}

// RecordTurn records the token delta for a turn.
// Returns true if diminishing returns are detected.
func (t *Tracker) RecordTurn(delta int) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if delta < t.threshold {
		t.lowDeltaStreak++
	} else {
		t.lowDeltaStreak = 0
	}
	return t.lowDeltaStreak >= t.requiredChecks
}

// DiminishingReturns returns true if the tracker has detected diminishing returns.
func (t *Tracker) DiminishingReturns() bool {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.lowDeltaStreak >= t.requiredChecks
}

// Reset resets the tracker state.
func (t *Tracker) Reset() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.lowDeltaStreak = 0
}

// LowDeltaStreak returns the current streak of low-delta turns.
func (t *Tracker) LowDeltaStreak() int {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.lowDeltaStreak
}

// SessionBudget tracks interactive mode state (foreground vs background).
type SessionBudget struct {
	mu          sync.RWMutex
	interactive bool
	errorBudget int
	maxErrors   int
}

// NewSessionBudget creates a session budget tracker.
func NewSessionBudget(interactive bool, maxErrors int) *SessionBudget {
	if maxErrors <= 0 {
		maxErrors = 5
	}
	return &SessionBudget{
		interactive: interactive,
		maxErrors:   maxErrors,
	}
}

// IsInteractive returns true if the session is in interactive (foreground) mode.
func (s *SessionBudget) IsInteractive() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.interactive
}

// RecordError records an error and returns true if the budget is exhausted.
func (s *SessionBudget) RecordError() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.errorBudget++
	return s.errorBudget >= s.maxErrors
}

// ResetErrors resets the error budget.
func (s *SessionBudget) ResetErrors() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.errorBudget = 0
}

// SetInteractive updates the interactive flag.
func (s *SessionBudget) SetInteractive(v bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.interactive = v
}
