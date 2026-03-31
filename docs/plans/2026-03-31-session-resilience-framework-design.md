# Session Resilience Framework Design

**Date:** 2026-03-31  
**Status:** Approved  

## Problem

Forge hits its 50-turn limit due to two compounding failure modes:
1. **Retry loops on transient errors** - API errors (429, 5xx, timeouts) cause the agent to burn turns retrying without proper backoff or circuit breaking
2. **Model thrashing on tools** - The model calls the same tool repeatedly on the same target without making progress, burning through turns

The current retry wrapper (`internal/llm/retry.go`) handles basic exponential backoff but lacks granular error classification, rate limit delay parsing, stream idle timeouts, and circuit breakers. The thrash detection in `internal/react/loop.go` injects overlays but doesn't escalate to hard circuit breaking.

## Solution

A unified **Session Resilience Framework** under `internal/resilience/` with five components, borrowing proven patterns from OpenAI Codex and Anthropic Claude Code (cci).

## Architecture

```
internal/resilience/
  retry/          -- Enhanced retry policy (from codex)
  circuit/        -- Circuit breakers (from cci)
  budget/         -- Token/turn budget tracking (from both)
  errors/         -- Error taxonomy + classification (from codex)
  recovery/       -- Recovery strategies + withheld errors (from cci)
```

## Component 1: Enhanced Retry Policy (`retry/`)

Borrow codex's `RetryPolicy` struct pattern:

- `max_attempts`, `base_delay`, `max_delay`, `jitter_pct`
- Per-error-class retry decisions (not just retryable/non-retryable)
- **Rate limit delay parsing** - extract "try again in Xs" from error body, use that as backoff instead of exponential
- **Stream idle timeout** - configurable (default 30s), kills hung streams before they burn a turn
- **Persistent retry mode** - for unattended sessions, retry 429s indefinitely with 5-min cap and 30s heartbeat

### Key structs

```go
type RetryPolicy struct {
    MaxAttempts     int
    BaseDelay       time.Duration
    MaxDelay        time.Duration
    JitterPct       float64
    RetryableFunc   func(error) bool
    BackoffFunc     func(int, time.Duration) time.Duration
}

type RetryConfig struct {
    RetryPolicy
    StreamIdleTimeout    time.Duration
    RateLimitDelayParse  bool
    PersistentMode       bool
    PersistentMaxDelay   time.Duration
    PersistentHeartbeat  time.Duration
}
```

## Component 2: Circuit Breakers (`circuit/`)

Borrow cci's circuit breaker pattern:

- **Compaction circuit breaker** - 3 consecutive failures → stop compacting for the session (saves 250K calls/day at Anthropic scale)
- **Tool thrash circuit breaker** - extends forge's existing thrash detection: instead of just injecting overlays, track a failure streak. After N streaks, block the tool and force synthesis
- **Error spiral prevention** - if the last message was an API error, skip stop hooks and overlays that would evaluate it (prevents error → hook → retry → error loops)

### Key structs

```go
type CircuitBreaker struct {
    name          string
    maxFailures   int
    failures      int
    lastFailure   time.Time
    state         CircuitState // closed, open, half-open
}

type ThrashTracker struct {
    toolCallStreaks map[string]int    // "tool:target" -> streak count
    circuitBreakers map[string]*CircuitBreaker
    threshold       int
}
```

## Component 3: Token/Turn Budget Tracking (`budget/`)

Borrow from both codex and cci:

- **Diminishing returns detection** - track token delta between turns. If delta < 500 for 2 consecutive checks, inject "deliver now" guidance
- **Foreground vs background classification** - if the user is actively waiting (interactive TUI), retry errors. If it's a background task (pipeline pass), bail on capacity errors immediately
- **Watermark-based error scoping** - each turn gets an error watermark. Only report errors since the last watermark, not the entire session buffer

### Key structs

```go
type BudgetTracker struct {
    turnTokenDeltas       []int
    diminishingThreshold  int
    diminishingChecks     int
    lowDeltaStreak        int
}

type ErrorWatermark struct {
    lastReportedTurn int
    errorsByTurn     map[int][]error
}
```

## Component 4: Error Taxonomy (`errors/`)

Borrow codex's granular error classification:

- 20+ error types with `IsRetryable()`, `IsCapacity()`, `IsAuth()`, `IsContext()` methods
- User-friendly messages per error type
- Recovery path hints (suggest `/compact`, `/model`, etc.)

### Key types

```go
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

type ForgeError struct {
    Class       ErrorClass
    Type        string
    Message     string
    UserMessage string
    Retryable   bool
    Recovery    string
    Raw         error
}
```

## Component 5: Recovery Strategies (`recovery/`)

Borrow cci's withheld error pattern:

- **Withheld errors** - don't surface recoverable errors (context overflow, rate limits) to the model until recovery is exhausted. The model sees nothing and the framework handles it transparently
- **Context overflow auto-recovery** - parse "max_tokens exceeded" errors, auto-adjust and retry before the model ever sees it
- **Single-shot recovery guards** - flags like `hasAttemptedReactiveCompact` to prevent PTL → compact → PTL spirals

### Key structs

```go
type RecoveryStrategy interface {
    CanRecover(ForgeError) bool
    Recover(context.Context, ForgeError) error
}

type RecoveryManager struct {
    strategies     []RecoveryStrategy
    guardFlags     map[string]bool
    withheldErrors []ForgeError
}
```

## Integration Points

| Forge File | Change |
|---|---|
| `internal/llm/retry.go` | Replace with new `retry/` package |
| `internal/react/loop.go` | Add circuit breakers, budget tracking, watermark errors |
| `internal/react/session.go` | Add compaction circuit breaker, withheld error handling |
| `internal/config/config.go` | Add resilience config section |
| `internal/tui/errors.go` | Replace with new `errors/` taxonomy |

## Configuration

```toml
[resilience]
retry_max_attempts = 5
retry_base_delay_ms = 1000
retry_max_delay_ms = 30000
stream_idle_timeout_ms = 30000
compaction_max_failures = 3
token_diminishing_threshold = 500
token_diminishing_checks = 2
tool_thrash_circuit_breaker = 8
persistent_retry = false
persistent_max_delay_ms = 300000
persistent_heartbeat_ms = 30000
```

## Design Principles

1. **Retry smarter** - parse server-provided delays, classify errors granularly, use exponential backoff with jitter
2. **Fail faster** - circuit breakers stop wasteful retries after N failures, background queries bail immediately on capacity errors
3. **Recover transparently** - withheld errors mean the model never sees recoverable failures, preventing error → retry → error spirals
