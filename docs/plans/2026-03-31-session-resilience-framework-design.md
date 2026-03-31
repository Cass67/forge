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

---

## Implementation Status

**Date completed:** 2026-03-31  
**Status:** ✅ Complete — 12 commits, all 30 test packages passing, 0 lint issues

### What was actually built vs design

| Design Component | Implementation | Deviation from Design |
|---|---|---|
| `retry/` package | Enhanced `internal/llm/retry.go` in-place | Kept as enhanced retry.go instead of new package — less churn, same functionality |
| `circuit/` package | `internal/resilience/circuit/breaker.go` | None — matches design exactly |
| `budget/` package | `internal/resilience/budget/tracker.go` | Simplified: removed unbounded `deltas` slice (latent memory leak found in review) |
| `errors/` package | `internal/resilience/errors/errors.go` | Tightened overly broad patterns (`"401"` → `"401 unauthorized"`, `"credit"` → `"insufficient credit"`) |
| `recovery/` package | `internal/resilience/recovery/manager.go` | Extracted `GuardCompaction` constant; added slice copy in `TakeWithheldErrors` |
| TUI errors | `internal/tui/errors.go` rewritten | Added `containsLower`/`indexLower` helpers to avoid `strings` import |
| Loop integration | `internal/react/loop.go` | Added compaction failure tracking + circuit breaker tripping |
| Config section | `internal/config/config.go` | Added `Resilience` struct with 5 fields and defaults |
| Bootstrap wiring | `internal/runtime/chat.go` + `session.go` | Wired `NewRetryDriverWithIdleTimeout` + `CompactionMaxFailures` + `Interactive` |

### Code review fixes applied during implementation

Every task went through spec review + code quality review. Fixes applied:

- **Task 1 (errors):** Tightened `"401"`/`"403"` patterns to avoid false positives; removed redundant regex; added data policy + default fallback tests
- **Task 2 (circuit):** Added `RecordFailure` guard for open state (prevents cooldown extension); made `State()` report effective state matching `Allow()`; added half-open→re-open test
- **Task 3 (budget):** Removed unbounded `deltas` slice (memory leak); switched to `RWMutex` for read-heavy methods; added `ResetErrors` + default value tests
- **Task 4 (recovery):** Extracted `GuardCompaction` constant; added slice copy in `TakeWithheldErrors` (prevents aliasing footgun); added guard-isolation test
- **Task 5 (retry):** All existing 27 tests pass — backward compatibility preserved

### Final commit log

```
69fc00f feat: wire resilience config into bootstrap and chat setup
2838f42 feat: add resilience config section with sensible defaults
3dc2a30 feat: integrate compaction failure tracking into react loop
51267e7 refactor: update TUI error handling to use resilience taxonomy
88c9247 refactor: enhance retry driver with error taxonomy and retry-after parsing
531654a fix: harden recovery manager per code review — extract constant, copy slice, add test
6a9eeca fix: harden budget tracker per code review — remove unbounded deltas, RWMutex, add tests
e9ee455 feat: add budget tracker package for resilience framework
5a793b0 fix: harden circuit breaker per code review — guard RecordFailure, fix State(), add tests
64ce9dd feat: add circuit breaker package for resilience framework
0a426c1 fix: tighten error patterns and add missing tests per code review
690605f feat: add error taxonomy package for resilience framework
```

### Test results

```
30 packages tested, all PASS
0 lint issues (golangci-lint)
0 build errors
```

### Configuration (actual defaults)

```toml
[resilience]
compaction_max_failures = 3       # trips after 3 consecutive compaction failures
token_diminishing_threshold = 500 # flags when token delta < 500 for 2 turns
token_diminishing_checks = 2      # consecutive low-delta turns before flagging
tool_thrash_circuit_breaker = 8   # (reserved for future tool thrash escalation)
stream_idle_timeout_ms = 30000    # kills hung streams after 30s
```
