// Package circuit implements a thread-safe circuit breaker that trips after
// N consecutive failures and recovers via a cooldown-based half-open probe.
package circuit

import (
	"sync"
	"time"
)

// State represents the state of a circuit breaker.
type State int

const (
	StateClosed State = iota
	StateOpen
	StateHalfOpen
)

func (s State) String() string {
	switch s {
	case StateClosed:
		return "closed"
	case StateOpen:
		return "open"
	case StateHalfOpen:
		return "half-open"
	default:
		return "unknown"
	}
}

// Breaker is a circuit breaker that trips after N consecutive failures.
type Breaker struct {
	mu          sync.Mutex
	name        string
	maxFailures int
	failures    int
	state       State
	lastFailure time.Time
	cooldown    time.Duration
}

// NewBreaker creates a circuit breaker that trips after maxFailures consecutive failures.
func NewBreaker(name string, maxFailures int, cooldown time.Duration) *Breaker {
	if maxFailures < 1 {
		maxFailures = 1
	}
	if cooldown <= 0 {
		cooldown = 5 * time.Minute
	}
	return &Breaker{
		name:        name,
		maxFailures: maxFailures,
		state:       StateClosed,
		cooldown:    cooldown,
	}
}

// Allow returns true if the circuit allows a request through.
func (b *Breaker) Allow() bool {
	b.mu.Lock()
	defer b.mu.Unlock()

	switch b.state {
	case StateClosed:
		return true
	case StateOpen:
		if time.Since(b.lastFailure) > b.cooldown {
			b.state = StateHalfOpen
			return true
		}
		return false
	case StateHalfOpen:
		return true
	}
	return false
}

// RecordSuccess records a successful operation, resetting the breaker.
func (b *Breaker) RecordSuccess() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.failures = 0
	b.state = StateClosed
}

// RecordFailure records a failed operation. Trips the circuit if threshold exceeded.
// Failures are ignored while the breaker is already open (prevents cooldown extension).
func (b *Breaker) RecordFailure() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.state == StateOpen {
		return
	}
	b.failures++
	b.lastFailure = time.Now()
	if b.failures >= b.maxFailures {
		b.state = StateOpen
	}
}

// State returns the effective current state of the breaker.
// If the breaker is open but the cooldown has elapsed, it reports half-open
// (matching what Allow() would do).
func (b *Breaker) State() State {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.state == StateOpen && time.Since(b.lastFailure) > b.cooldown {
		return StateHalfOpen
	}
	return b.state
}

// Failures returns the current consecutive failure count.
func (b *Breaker) Failures() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.failures
}

// Reset resets the breaker to initial state.
func (b *Breaker) Reset() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.failures = 0
	b.state = StateClosed
	b.lastFailure = time.Time{}
}
