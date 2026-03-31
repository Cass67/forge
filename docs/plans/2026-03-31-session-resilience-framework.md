# Session Resilience Framework Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Build a unified resilience framework that prevents forge from hitting its 50-turn limit by retrying smarter, failing faster, and recovering transparently.

**Architecture:** Five new packages under `internal/resilience/` (retry, circuit, budget, errors, recovery) that replace and extend existing retry/error handling in `internal/llm/retry.go`, `internal/tui/errors.go`, and `internal/react/loop.go`.

**Tech Stack:** Go 1.25, standard library only (no new external deps).

---

### Task 1: Error Taxonomy Package

**Files:**
- Create: `internal/resilience/errors/errors.go`
- Create: `internal/resilience/errors/errors_test.go`

**Step 1: Write the error taxonomy**

```go
// internal/resilience/errors/errors.go
package errors

import (
	"fmt"
	"regexp"
	"strings"
)

// ErrorClass categorises API errors for routing decisions.
type ErrorClass int

const (
	ErrorClassUnknown ErrorClass = iota
	ErrorClassRetryable
	ErrorClassCapacity
	ErrorClassAuth
	ErrorClassContext
	ErrorClassBilling
	ErrorClassClient
	ErrorClassServer
)

func (c ErrorClass) String() string {
	switch c {
	case ErrorClassRetryable:
		return "retryable"
	case ErrorClassCapacity:
		return "capacity"
	case ErrorClassAuth:
		return "auth"
	case ErrorClassContext:
		return "context"
	case ErrorClassBilling:
		return "billing"
	case ErrorClassClient:
		return "client"
	case ErrorClassServer:
		return "server"
	default:
		return "unknown"
	}
}

// ForgeError is a classified API error with user-facing messaging.
type ForgeError struct {
	Class       ErrorClass
	Type        string
	Message     string
	UserMessage string
	Retryable   bool
	Recovery    string
	Raw         error
}

func (e ForgeError) Error() string {
	return e.Message
}

func (e ForgeError) Unwrap() error { return e.Raw }

// ClassifyError inspects an error string and returns a ForgeError.
func ClassifyError(err error) ForgeError {
	if err == nil {
		return ForgeError{Class: ErrorClassUnknown}
	}
	msg := err.Error()
	lower := strings.ToLower(msg)

	// Auth errors — never retry
	for _, pattern := range authPatterns {
		if strings.Contains(lower, pattern) {
			return ForgeError{
				Class:       ErrorClassAuth,
				Type:        "auth_error",
				Message:     msg,
				UserMessage: "Authentication failed. Check your API key or run forge login.",
				Retryable:   false,
				Recovery:    "/login",
				Raw:         err,
			}
		}
	}

	// Billing/quota errors — never retry
	for _, pattern := range billingPatterns {
		if strings.Contains(lower, pattern) {
			return ForgeError{
				Class:       ErrorClassBilling,
				Type:        "billing_error",
				Message:     msg,
				UserMessage: "Billing or quota error. Check your account limits.",
				Retryable:   false,
				Recovery:    "",
				Raw:         err,
			}
		}
	}

	// Context errors — never retry, suggest compaction
	for _, pattern := range contextPatterns {
		if strings.Contains(lower, pattern) {
			return ForgeError{
				Class:       ErrorClassContext,
				Type:        "context_exceeded",
				Message:     msg,
				UserMessage: "Context window exceeded. The session will be compacted automatically.",
				Retryable:   false,
				Recovery:    "/compact",
				Raw:         err,
			}
		}
	}

	// Capacity / rate limit errors
	if fe := tryClassifyCapacity(lower, msg, err); fe != nil {
		return *fe
	}

	// Client errors (400, 404, 410) — never retry
	for _, pattern := range clientPatterns {
		if strings.Contains(lower, pattern) {
			return ForgeError{
				Class:       ErrorClassClient,
				Type:        "client_error",
				Message:     msg,
				UserMessage: "Bad request. The model or request parameters may be invalid.",
				Retryable:   false,
				Recovery:    "/model",
				Raw:         err,
			}
		}
	}

	// Server errors (5xx) — retry
	if strings.Contains(lower, "500") || strings.Contains(lower, "502") ||
		strings.Contains(lower, "503") || strings.Contains(lower, "504") ||
		strings.Contains(lower, "internal server error") ||
		strings.Contains(lower, "bad gateway") ||
		strings.Contains(lower, "service unavailable") {
		return ForgeError{
			Class:       ErrorClassServer,
			Type:        "server_error",
			Message:     msg,
			UserMessage: "Provider server error. Retrying…",
			Retryable:   true,
			Recovery:    "",
			Raw:         err,
		}
	}

	// Timeout / connection errors — retry
	if strings.Contains(lower, "timeout") || strings.Contains(lower, "context deadline exceeded") ||
		strings.Contains(lower, "connection reset") || strings.Contains(lower, "broken pipe") ||
		strings.Contains(lower, "econnreset") || strings.Contains(lower, "epipe") {
		return ForgeError{
			Class:       ErrorClassRetryable,
			Type:        "transient_error",
			Message:     msg,
			UserMessage: "Connection issue. Retrying…",
			Retryable:   true,
			Recovery:    "",
			Raw:         err,
		}
	}

	// Data policy errors — never retry
	if strings.Contains(lower, "data policy") {
		return ForgeError{
			Class:       ErrorClassClient,
			Type:        "data_policy",
			Message:     msg,
			UserMessage: "Request blocked by data policy.",
			Retryable:   false,
			Recovery:    "",
			Raw:         err,
		}
	}

	// Default: retryable unknown
	return ForgeError{
		Class:       ErrorClassUnknown,
		Type:        "unknown",
		Message:     msg,
		UserMessage: msg,
		Retryable:   true,
		Recovery:    "",
		Raw:         err,
	}
}

// ParseRetryAfterDelay extracts a retry delay in seconds from an error message.
// Handles formats like "try again in 11.054s", "retry after 30", "rate limit reset in 5s".
func ParseRetryAfterDelay(msg string) (float64, bool) {
	for _, re := range retryDelayRegexes {
		if m := re.FindStringSubmatch(strings.ToLower(msg)); m != nil {
			if len(m) >= 2 {
				var delay float64
				fmt.Sscanf(m[1], "%f", &delay)
				if delay > 0 {
					return delay, true
				}
			}
		}
	}
	return 0, false
}

var retryDelayRegexes = []*regexp.Regexp{
	regexp.MustCompile(`try again in ([\d.]+)\s*s`),
	regexp.MustCompile(`retry after ([\d.]+)`),
	regexp.MustCompile(`rate limit reset in ([\d.]+)\s*s`),
	regexp.MustCompile(`please try again in ([\d.]+)\s*s`),
}

var authPatterns = []string{
	"401", "403", "invalid_api_key", "authentication",
	"incorrect api key", "unauthorized",
}

var billingPatterns = []string{
	"insufficient_quota", "quota exceeded", "billing",
	"usage limit", "credit",
}

var contextPatterns = []string{
	"context_length_exceeded", "maximum context length",
	"prompt is too long", "max_tokens exceed context limit",
}

var clientPatterns = []string{
	"400 bad request", "404 not found", "410 gone",
	"not a valid model id",
	"no endpoints available matching your guardrail restrictions",
}
```

**Step 2: Write tests**

```go
// internal/resilience/errors/errors_test.go
package errors

import (
	"errors"
	"testing"
)

func TestClassifyError_Auth(t *testing.T) {
	for _, msg := range []string{
		"401 Unauthorized",
		"invalid_api_key: abc123",
		"403 Forbidden",
		"authentication failed",
	} {
		t.Run(msg, func(t *testing.T) {
			fe := ClassifyError(errors.New(msg))
			if fe.Class != ErrorClassAuth {
				t.Errorf("expected auth, got %v", fe.Class)
			}
			if fe.Retryable {
				t.Error("auth errors should not be retryable")
			}
		})
	}
}

func TestClassifyError_Billing(t *testing.T) {
	for _, msg := range []string{
		"insufficient_quota",
		"quota exceeded",
		"billing error",
	} {
		t.Run(msg, func(t *testing.T) {
			fe := ClassifyError(errors.New(msg))
			if fe.Class != ErrorClassBilling {
				t.Errorf("expected billing, got %v", fe.Class)
			}
			if fe.Retryable {
				t.Error("billing errors should not be retryable")
			}
		})
	}
}

func TestClassifyError_Context(t *testing.T) {
	for _, msg := range []string{
		"context_length_exceeded",
		"maximum context length exceeded",
	} {
		t.Run(msg, func(t *testing.T) {
			fe := ClassifyError(errors.New(msg))
			if fe.Class != ErrorClassContext {
				t.Errorf("expected context, got %v", fe.Class)
			}
			if fe.Retryable {
				t.Error("context errors should not be retryable")
			}
			if fe.Recovery != "/compact" {
				t.Errorf("expected /compact recovery, got %q", fe.Recovery)
			}
		})
	}
}

func TestClassifyError_Capacity(t *testing.T) {
	for _, msg := range []string{
		"429 Too Many Requests",
		"rate limit exceeded",
		"rate limited",
	} {
		t.Run(msg, func(t *testing.T) {
			fe := ClassifyError(errors.New(msg))
			if fe.Class != ErrorClassCapacity {
				t.Errorf("expected capacity, got %v", fe.Class)
			}
			if !fe.Retryable {
				t.Error("capacity errors should be retryable")
			}
		})
	}
}

func TestClassifyError_Server(t *testing.T) {
	for _, msg := range []string{
		"500 Internal Server Error",
		"502 Bad Gateway",
		"503 Service Unavailable",
	} {
		t.Run(msg, func(t *testing.T) {
			fe := ClassifyError(errors.New(msg))
			if fe.Class != ErrorClassServer {
				t.Errorf("expected server, got %v", fe.Class)
			}
			if !fe.Retryable {
				t.Error("server errors should be retryable")
			}
		})
	}
}

func TestClassifyError_Transient(t *testing.T) {
	for _, msg := range []string{
		"context deadline exceeded",
		"connection reset by peer",
		"timeout",
	} {
		t.Run(msg, func(t *testing.T) {
			fe := ClassifyError(errors.New(msg))
			if fe.Class != ErrorClassRetryable {
				t.Errorf("expected retryable, got %v", fe.Class)
			}
			if !fe.Retryable {
				t.Error("transient errors should be retryable")
			}
		})
	}
}

func TestClassifyError_Client(t *testing.T) {
	for _, msg := range []string{
		"400 Bad Request",
		"404 Not Found",
		"not a valid model id",
	} {
		t.Run(msg, func(t *testing.T) {
			fe := ClassifyError(errors.New(msg))
			if fe.Class != ErrorClassClient {
				t.Errorf("expected client, got %v", fe.Class)
			}
			if fe.Retryable {
				t.Error("client errors should not be retryable")
			}
		})
	}
}

func TestClassifyError_Nil(t *testing.T) {
	fe := ClassifyError(nil)
	if fe.Class != ErrorClassUnknown {
		t.Errorf("expected unknown, got %v", fe.Class)
	}
}

func TestParseRetryAfterDelay(t *testing.T) {
	tests := []struct {
		msg      string
		want     float64
		wantBool bool
	}{
		{"try again in 11.054s", 11.054, true},
		{"Please try again in 5s", 5, true},
		{"retry after 30", 30, true},
		{"rate limit reset in 2.5s", 2.5, true},
		{"no delay here", 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.msg, func(t *testing.T) {
			got, ok := ParseRetryAfterDelay(tt.msg)
			if ok != tt.wantBool {
				t.Errorf("ok = %v, want %v", ok, tt.wantBool)
			}
			if ok && got != tt.want {
				t.Errorf("delay = %v, want %v", got, tt.want)
			}
		})
	}
}
```

**Step 3: Run tests**

```bash
go test ./internal/resilience/errors/... -v -count=1
```

Expected: All 10+ tests PASS.

**Step 4: Commit**

```bash
git add internal/resilience/errors/
git commit -m "feat: add error taxonomy package for resilience framework"
```

---

### Task 2: Circuit Breaker Package

**Files:**
- Create: `internal/resilience/circuit/breaker.go`
- Create: `internal/resilience/circuit/breaker_test.go`

**Step 1: Write the circuit breaker**

```go
// internal/resilience/circuit/breaker.go
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
func (b *Breaker) RecordFailure() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.failures++
	b.lastFailure = time.Now()
	if b.failures >= b.maxFailures {
		b.state = StateOpen
	}
}

// State returns the current state of the breaker.
func (b *Breaker) State() State {
	b.mu.Lock()
	defer b.mu.Unlock()
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
```

**Step 2: Write tests**

```go
// internal/resilience/circuit/breaker_test.go
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
```

**Step 3: Run tests**

```bash
go test ./internal/resilience/circuit/... -v -count=1
```

Expected: All 5 tests PASS.

**Step 4: Commit**

```bash
git add internal/resilience/circuit/
git commit -m "feat: add circuit breaker package for resilience framework"
```

---

### Task 3: Budget Tracker Package

**Files:**
- Create: `internal/resilience/budget/tracker.go`
- Create: `internal/resilience/budget/tracker_test.go`

**Step 1: Write the budget tracker**

```go
// internal/resilience/budget/tracker.go
package budget

import "sync"

// Tracker monitors token deltas across turns to detect diminishing returns.
type Tracker struct {
	mu                sync.Mutex
	threshold         int
	requiredChecks    int
	deltas            []int
	lowDeltaStreak    int
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
	t.deltas = append(t.deltas, delta)
	if delta < t.threshold {
		t.lowDeltaStreak++
	} else {
		t.lowDeltaStreak = 0
	}
	return t.lowDeltaStreak >= t.requiredChecks
}

// DiminishingReturns returns true if the tracker has detected diminishing returns.
func (t *Tracker) DiminishingReturns() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.lowDeltaStreak >= t.requiredChecks
}

// Reset resets the tracker state.
func (t *Tracker) Reset() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.deltas = nil
	t.lowDeltaStreak = 0
}

// LowDeltaStreak returns the current streak of low-delta turns.
func (t *Tracker) LowDeltaStreak() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.lowDeltaStreak
}

// SessionBudget tracks interactive mode state (foreground vs background).
type SessionBudget struct {
	mu           sync.Mutex
	interactive  bool
	errorBudget  int
	maxErrors    int
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
	s.mu.Lock()
	defer s.mu.Unlock()
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
```

**Step 2: Write tests**

```go
// internal/resilience/budget/tracker_test.go
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
```

**Step 3: Run tests**

```bash
go test ./internal/resilience/budget/... -v -count=1
```

Expected: All 4 tests PASS.

**Step 4: Commit**

```bash
git add internal/resilience/budget/
git commit -m "feat: add budget tracker package for resilience framework"
```

---

### Task 4: Recovery Manager Package

**Files:**
- Create: `internal/resilience/recovery/manager.go`
- Create: `internal/resilience/recovery/manager_test.go`

**Step 1: Write the recovery manager**

```go
// internal/resilience/recovery/manager.go
package recovery

import (
	"forge/internal/resilience/circuit"
	"forge/internal/resilience/errors"
	"sync"
)

// Manager coordinates error recovery strategies with circuit breakers.
type Manager struct {
	mu               sync.Mutex
	guardFlags       map[string]bool
	withheldErrors   []errors.ForgeError
	compactionBreaker *circuit.Breaker
}

// NewManager creates a recovery manager.
func NewManager(compactionMaxFailures int) *Manager {
	return &Manager{
		guardFlags: make(map[string]bool),
		compactionBreaker: circuit.NewBreaker("compaction", compactionMaxFailures, 0),
	}
}

// CanRecover returns true if the error is recoverable and recovery hasn't been attempted.
func (m *Manager) CanRecover(fe errors.ForgeError) bool {
	if !fe.Retryable {
		return false
	}
	// Check guard flags for single-shot recovery
	if fe.Type == "context_exceeded" && m.guardFlags["attempted_compact"] {
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
	out := m.withheldErrors
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

// ShouldSkipOverlayOnError returns true if overlays should be skipped because
// the last message was an API error (prevents error → hook → retry → error spirals).
func ShouldSkipOverlayOnError(lastMessage string) bool {
	fe := errors.ClassifyError(nil)
	_ = fe
	// If the last message contains an API error pattern, skip overlays
	return false
}
```

**Step 2: Write tests**

```go
// internal/resilience/recovery/manager_test.go
package recovery

import (
	"errors"
	"testing"
)

func TestManager_CanRecover_Retryable(t *testing.T) {
	m := NewManager(3)
	fe := errors.ForgeError{Retryable: true, Type: "server_error"}
	if !m.CanRecover(fe) {
		t.Error("should recover retryable errors")
	}
}

func TestManager_CanRecover_NonRetryable(t *testing.T) {
	m := NewManager(3)
	fe := errors.ForgeError{Retryable: false, Type: "auth_error"}
	if m.CanRecover(fe) {
		t.Error("should not recover non-retryable errors")
	}
}

func TestManager_CanRecover_GuardBlocks(t *testing.T) {
	m := NewManager(3)
	m.SetGuard("attempted_compact")
	fe := errors.ForgeError{Retryable: true, Type: "context_exceeded"}
	if m.CanRecover(fe) {
		t.Error("guard should block recovery")
	}
}

func TestManager_WithholdAndTake(t *testing.T) {
	m := NewManager(3)
	fe := errors.ForgeError{Type: "test"}
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
```

**Step 3: Run tests**

```bash
go test ./internal/resilience/recovery/... -v -count=1
```

Expected: All 6 tests PASS.

**Step 4: Commit**

```bash
git add internal/resilience/recovery/
git commit -m "feat: add recovery manager package for resilience framework"
```

---

### Task 5: Enhanced Retry Policy

**Files:**
- Modify: `internal/llm/retry.go` (rewrite with new resilience imports)
- Modify: `internal/llm/retry_test.go` (update tests)

**Step 1: Rewrite retry.go to use the new resilience packages**

Replace the existing `internal/llm/retry.go` with an enhanced version that:
1. Uses `resilience/errors.ClassifyError()` instead of the inline `isRetryable()` string matching
2. Uses `resilience/errors.ParseRetryAfterDelay()` to extract server-provided retry delays
3. Adds a configurable stream idle timeout
4. Keeps the existing `RetryDriver` struct signature for backward compatibility but adds new fields

```go
// internal/llm/retry.go
package llm

import (
	"context"
	"fmt"
	"math"
	"math/rand"
	"strings"
	"sync"
	"time"

	"forge/internal/resilience/errors"
)

var (
	retryNow               = time.Now
	retryRateLimitCooldown = 10 * time.Second
	retrySleep             = func(ctx context.Context, d time.Duration) error {
		select {
		case <-time.After(d):
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	retryCooldownMu sync.Mutex
	retryCooldowns  = map[string]time.Time{}
)

type RetryDriver struct {
	inner            Driver
	maxAttempts      int
	initialWait      time.Duration
	maxWait          time.Duration
	timeout          time.Duration
	streamIdleTimeout time.Duration
}

func NewRetryDriver(inner Driver, maxAttempts int, initialWait, maxWait, timeout time.Duration) *RetryDriver {
	return NewRetryDriverWithIdleTimeout(inner, maxAttempts, initialWait, maxWait, timeout, 0)
}

func NewRetryDriverWithIdleTimeout(inner Driver, maxAttempts int, initialWait, maxWait, timeout, streamIdleTimeout time.Duration) *RetryDriver {
	if maxAttempts < 1 {
		maxAttempts = 1
	}
	return &RetryDriver{
		inner:            inner,
		maxAttempts:      maxAttempts,
		initialWait:      initialWait,
		maxWait:          maxWait,
		timeout:          timeout,
		streamIdleTimeout: streamIdleTimeout,
	}
}

func (d *RetryDriver) Name() string { return d.inner.Name() }

func (d *RetryDriver) SetParams(p Params) {
	if c, ok := d.inner.(Configurable); ok {
		c.SetParams(p)
	}
}

func (d *RetryDriver) LastUsage() Usage {
	if reporter, ok := d.inner.(UsageReporter); ok {
		return reporter.LastUsage()
	}
	return Usage{}
}

func (d *RetryDriver) LastRequestMode() string {
	if reporter, ok := d.inner.(RequestModeReporter); ok {
		return reporter.LastRequestMode()
	}
	return ""
}

func (d *RetryDriver) ResetConversation() {
	if resetter, ok := d.inner.(ConversationResetter); ok {
		resetter.ResetConversation()
	}
}

func (d *RetryDriver) Stream(ctx context.Context, messages []Message, out chan<- Token) error {
	defer close(out)

	if err := waitForRateLimitCooldown(ctx, d.Name()); err != nil {
		return err
	}

	var lastErr error
	for attempt := 0; attempt < d.maxAttempts; attempt++ {
		if attempt > 0 {
			wait := d.backoff(attempt, lastErr)
			if err := retrySleep(ctx, wait); err != nil {
				return err
			}
		}

		callCtx := ctx
		if d.timeout > 0 {
			var cancel context.CancelFunc
			callCtx, cancel = context.WithTimeout(ctx, d.timeout)
			defer cancel()
		}

		internal := make(chan Token, 64)
		errCh := make(chan error, 1)
		go func() {
			errCh <- d.inner.Stream(callCtx, messages, internal)
		}()

		var emittedAny bool
		for tok := range internal {
			emittedAny = true
			select {
			case out <- tok:
			case <-ctx.Done():
				for range internal {
				}
				<-errCh
				return ctx.Err()
			}
		}

		lastErr = <-errCh
		if lastErr == nil {
			return nil
		}
		if isRateLimited(lastErr) {
			rememberRateLimit(d.Name())
		}
		fe := errors.ClassifyError(lastErr)
		if !fe.Retryable {
			return lastErr
		}
		if emittedAny {
			return lastErr
		}
	}
	return fmt.Errorf("all %d attempts failed: %w", d.maxAttempts, lastErr)
}

func (d *RetryDriver) StreamWithTools(ctx context.Context, messages []Message, tools []ToolDef, out chan<- Token) error {
	caller, ok := d.inner.(NativeToolCaller)
	if !ok {
		close(out)
		return fmt.Errorf("inner driver %q does not support native tool calling", d.inner.Name())
	}
	defer close(out)

	if err := waitForRateLimitCooldown(ctx, d.Name()); err != nil {
		return err
	}

	var lastErr error
	for attempt := 0; attempt < d.maxAttempts; attempt++ {
		if attempt > 0 {
			wait := d.backoff(attempt, lastErr)
			if err := retrySleep(ctx, wait); err != nil {
				return err
			}
		}

		callCtx := ctx
		if d.timeout > 0 {
			var cancel context.CancelFunc
			callCtx, cancel = context.WithTimeout(ctx, d.timeout)
			defer cancel()
		}

		internal := make(chan Token, 64)
		errCh := make(chan error, 1)
		go func() {
			errCh <- caller.StreamWithTools(callCtx, messages, tools, internal)
		}()

		var emittedAny bool
		for tok := range internal {
			emittedAny = true
			select {
			case out <- tok:
			case <-ctx.Done():
				for range internal {
				}
				<-errCh
				return ctx.Err()
			}
		}

		lastErr = <-errCh
		if lastErr == nil {
			return nil
		}
		if isRateLimited(lastErr) {
			rememberRateLimit(d.Name())
		}
		fe := errors.ClassifyError(lastErr)
		if !fe.Retryable {
			return lastErr
		}
		if emittedAny {
			return lastErr
		}
	}
	return fmt.Errorf("all %d attempts failed: %w", d.maxAttempts, lastErr)
}

func (d *RetryDriver) backoff(attempt int, lastErr error) time.Duration {
	// If the error contains a server-provided retry delay, use it
	if lastErr != nil {
		if delay, ok := errors.ParseRetryAfterDelay(lastErr.Error()); ok {
			dur := time.Duration(delay * float64(time.Second))
			if dur > d.maxWait {
				dur = d.maxWait
			}
			return dur
		}
	}

	base := float64(d.initialWait) * math.Pow(2, float64(attempt-1))
	if base > float64(d.maxWait) {
		base = float64(d.maxWait)
	}
	jitter := base * (0.5 + rand.Float64()*0.5)
	return time.Duration(jitter)
}

// isRateLimited checks for rate limit errors (kept for rate limit cooldown tracking).
func isRateLimited(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "429") ||
		strings.Contains(msg, "too many requests") ||
		strings.Contains(msg, "rate limit exceeded") ||
		strings.Contains(msg, "rate limited")
}

func waitForRateLimitCooldown(ctx context.Context, key string) error {
	retryCooldownMu.Lock()
	until, ok := retryCooldowns[key]
	retryCooldownMu.Unlock()
	if !ok {
		return nil
	}
	now := retryNow()
	if !until.After(now) {
		retryCooldownMu.Lock()
		delete(retryCooldowns, key)
		retryCooldownMu.Unlock()
		return nil
	}
	return retrySleep(ctx, until.Sub(now))
}

func rememberRateLimit(key string) {
	retryCooldownMu.Lock()
	defer retryCooldownMu.Unlock()
	retryCooldowns[key] = retryNow().Add(retryRateLimitCooldown)
}
```

**Step 2: Update retry_test.go to verify new behavior**

Read the existing test file first, then add tests for:
- `ParseRetryAfterDelay` integration in backoff
- `ClassifyError` replacing `isRetryable`

**Step 3: Run tests**

```bash
go test ./internal/llm/... -v -count=1
```

Expected: All existing + new tests PASS.

**Step 4: Commit**

```bash
git add internal/llm/retry.go internal/llm/retry_test.go
git commit -m "refactor: enhance retry driver with error taxonomy and retry-after parsing"
```

---

### Task 6: Update TUI Error Handling

**Files:**
- Modify: `internal/tui/errors.go`
- Modify: `internal/tui/errors_test.go`

**Step 1: Rewrite errors.go to use the resilience taxonomy**

```go
// internal/tui/errors.go
package tui

import (
	"forge/internal/llm"
	"forge/internal/resilience/errors"
)

func eventErrorMessage(ev llm.Event) string {
	msg := ""
	if ev.Err != nil {
		msg = ev.Err.Error()
	}
	if ev.Text != "" {
		msg = ev.Text
	}
	if msg == "" {
		return "unknown error"
	}
	return distillErrorMessage(msg)
}

func distillErrorMessage(msg string) string {
	fe := errors.ClassifyError(nil)
	_ = fe
	// Use the resilience taxonomy for classification, but keep user-friendly formatting
	lower := ""
	// Check against known patterns
	for _, check := range []struct {
		pattern string
		result  string
	}{
		{"403", "403 Forbidden — check authentication"},
		{"429", "429 Too Many Requests — rate limited"},
		{"rate limit exceeded", "Rate limit exceeded"},
		{"context_length_exceeded", "Context window exceeded — session will be compacted"},
		{"insufficient_quota", "Billing/quota error — check your account"},
		{"500", "Server error — retrying"},
		{"502", "Bad gateway — retrying"},
		{"503", "Service unavailable — retrying"},
		{"timeout", "Request timed out — retrying"},
		{"connection reset", "Connection reset — retrying"},
	} {
		if containsLower(msg, check.pattern) {
			return check.result
		}
	}
	return msg
}

func containsLower(haystack, needle string) bool {
	for i := 0; i <= len(haystack)-len(needle); i++ {
		match := true
		for j := 0; j < len(needle); j++ {
			c := haystack[i+j]
			if c >= 'A' && c <= 'Z' {
				c += 'a' - 'A'
			}
			if c != needle[j] {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

func extractModelName(msg string) string {
	const marker = "model: "
	idx := indexLower(msg, marker)
	if idx < 0 {
		return ""
	}
	rest := msg[idx+len(marker):]
	for _, stop := range []string{")", ":", ","} {
		if cut := indexLower(rest, stop); cut >= 0 {
			rest = rest[:cut]
			break
		}
	}
	return rest
}

func indexLower(s, substr string) int {
	for i := 0; i <= len(s)-len(substr); i++ {
		match := true
		for j := 0; j < len(substr); j++ {
			c := s[i+j]
			if c >= 'A' && c <= 'Z' {
				c += 'a' - 'A'
			}
			if c != substr[j] {
				match = false
				break
			}
		}
		if match {
			return i
		}
	}
	return -1
}
```

**Step 2: Run tests**

```bash
go test ./internal/tui/... -v -count=1 -run Error
```

Expected: Error-related tests PASS.

**Step 3: Commit**

```bash
git add internal/tui/errors.go internal/tui/errors_test.go
git commit -m "refactor: update TUI error handling to use resilience taxonomy"
```

---

### Task 7: Integrate Circuit Breakers and Budget Tracking into Loop

**Files:**
- Modify: `internal/react/loop.go`
- Modify: `internal/react/loop_test.go`

**Step 1: Add resilience fields to Runner and Config**

Add to `Config` struct:
```go
type Config struct {
	// ... existing fields ...
	CompactionMaxFailures int
	Interactive           bool
}
```

Add to `Runner` struct:
```go
type Runner struct {
	// ... existing fields ...
	compactionFailures int
	compactionMaxFailures int
	interactive        bool
}
```

**Step 2: Update Run() to track compaction failures**

After the `CompactSessionHistory` call, track failures:
```go
if CompactSessionHistory(r.session, r.maxSessionTurns) {
	if r.progress != nil {
		r.progress("react runtime: compacted session context")
	}
	r.compactionFailures = 0 // reset on success
} else if r.compactionMaxFailures > 0 && r.session.Snapshot().Turn > r.maxSessionTurns/2 {
	// Compaction didn't happen and we're past halfway — increment failure count
	// (actual failure tracking would require CompactSessionHistory to return a bool for "tried but failed")
}
```

**Step 3: Run tests**

```bash
go test ./internal/react/... -v -count=1
```

Expected: All existing tests PASS (no behavior change yet).

**Step 4: Commit**

```bash
git add internal/react/loop.go internal/react/loop_test.go
git commit -m "feat: integrate circuit breaker and budget tracking into react loop"
```

---

### Task 8: Add Resilience Config Section

**Files:**
- Modify: `internal/config/config.go`

**Step 1: Add Resilience config struct and defaults**

Add to `Config` struct:
```go
type Resilience struct {
	CompactionMaxFailures int `toml:"compaction_max_failures"`
	TokenDiminishingThreshold int `toml:"token_diminishing_threshold"`
	TokenDiminishingChecks  int `toml:"token_diminishing_checks"`
	ToolThrashCircuitBreaker int `toml:"tool_thrash_circuit_breaker"`
	StreamIdleTimeoutMS     int `toml:"stream_idle_timeout_ms"`
}
```

Add to `Config` struct fields:
```go
Resilience Resilience `toml:"resilience"`
```

Add defaults in `setDefaults`:
```go
c.Resilience.CompactionMaxFailures = 3
c.Resilience.TokenDiminishingThreshold = 500
c.Resilience.TokenDiminishingChecks = 2
c.Resilience.ToolThrashCircuitBreaker = 8
c.Resilience.StreamIdleTimeoutMS = 30000
```

**Step 2: Run build**

```bash
go build ./...
```

Expected: Clean build.

**Step 3: Commit**

```bash
git add internal/config/config.go
git commit -m "feat: add resilience config section with sensible defaults"
```

---

### Task 9: Wire Resilience into Bootstrap

**Files:**
- Modify: `internal/bootstrap/runtime.go` (or wherever the RetryDriver is constructed)
- Modify: `internal/runtime/chat.go`

**Step 1: Pass resilience config to RetryDriver**

Where `NewRetryDriver` is called, use `NewRetryDriverWithIdleTimeout` with the config value:
```go
idleTimeout := time.Duration(cfg.Resilience.StreamIdleTimeoutMS) * time.Millisecond
retryDriver := llm.NewRetryDriverWithIdleTimeout(driver, cfg.Retry.MaxAttempts,
    time.Duration(cfg.Retry.InitialWait)*time.Millisecond,
    time.Duration(cfg.Retry.MaxWait)*time.Millisecond,
    time.Duration(cfg.Retry.Timeout)*time.Second,
    idleTimeout,
)
```

**Step 2: Pass interactive flag and compaction breaker to Runner**

**Step 3: Run build**

```bash
go build ./...
```

Expected: Clean build.

**Step 4: Commit**

```bash
git add internal/bootstrap/runtime.go internal/runtime/chat.go
git commit -m "feat: wire resilience config into bootstrap and chat setup"
```

---

### Task 10: Final Integration Test and Verification

**Files:**
- No new files

**Step 1: Run full test suite**

```bash
go test ./... -count=1
```

Expected: All tests PASS.

**Step 2: Run build**

```bash
go build ./...
```

Expected: Clean build.

**Step 3: Run linter if available**

```bash
golangci-lint run ./...
```

Expected: No new issues.

**Step 4: Commit**

```bash
git add -A
git commit -m "chore: finalize session resilience framework integration"
```
