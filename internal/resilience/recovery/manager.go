// Package recovery coordinates error recovery strategies with circuit breakers
// and single-shot recovery guards to prevent retry spirals.
package recovery

import (
	"sync"

	"forge/internal/resilience/circuit"
	"forge/internal/resilience/errors"
)

// GuardCompaction is the guard flag name set after attempting reactive compaction.
const GuardCompaction = "attempted_compact"

// Manager coordinates error recovery strategies with circuit breakers.
type Manager struct {
	mu                sync.Mutex
	guardFlags        map[string]bool
	withheldErrors    []errors.ForgeError
	compactionBreaker *circuit.Breaker
}

// NewManager creates a recovery manager.
// compactionMaxFailures sets the failure threshold for the compaction circuit breaker.
func NewManager(compactionMaxFailures int) *Manager {
	return &Manager{
		guardFlags:        make(map[string]bool),
		compactionBreaker: circuit.NewBreaker("compaction", compactionMaxFailures, 0),
	}
}

// CanRecover returns true if the error is recoverable and recovery hasn't been attempted.
func (m *Manager) CanRecover(fe errors.ForgeError) bool {
	if !fe.Retryable {
		return false
	}
	// Check guard flags for single-shot recovery
	m.mu.Lock()
	defer m.mu.Unlock()
	if fe.Type == "context_exceeded" && m.guardFlags[GuardCompaction] {
		return false
	}
	return true
}

// WithholdError records a recoverable error without surfacing it to the model.
func (m *Manager) WithholdError(fe errors.ForgeError) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.withheldErrors = append(m.withheldErrors, fe)
}

// TakeWithheldErrors returns and clears withheld errors.
func (m *Manager) TakeWithheldErrors() []errors.ForgeError {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]errors.ForgeError, len(m.withheldErrors))
	copy(out, m.withheldErrors)
	m.withheldErrors = nil
	return out
}

// SetGuard sets a single-shot recovery guard flag.
func (m *Manager) SetGuard(name string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.guardFlags[name] = true
}

// ClearGuard removes a guard flag.
func (m *Manager) ClearGuard(name string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.guardFlags, name)
}

// HasGuard returns true if the guard flag is set.
func (m *Manager) HasGuard(name string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.guardFlags[name]
}

// CompactionBreaker returns the compaction circuit breaker.
func (m *Manager) CompactionBreaker() *circuit.Breaker {
	return m.compactionBreaker
}
