# Harness Thread Ledger And Turn Resolution Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace short-horizon session heuristics with an explicit thread ledger so the harness can resolve new turns as continue, replay, repair, cancel, supersede, or new-task actions before falling back to lexical routing.

**Architecture:** Introduce a session-owned `ThreadLedger` with typed thread kinds, statuses, and deliverables. Add a turn-intent resolver that runs before normal family classification and that can map follow-up prompts, short acknowledgements, replay requests, and cancellation onto the active thread. Keep `PendingAction` and recent-preview data only as migration bridges.

**Tech Stack:** Go, `internal/harness`, `internal/runtime`, existing session/debug infrastructure, Go test/build tooling, real-log fixtures.

---

## References

- `docs/superpowers/specs/2026-03-27-harness-production-redesign-design.md`
- `docs/reports/forge-harness-architecture-investigation-2026-03-27.md`
- `internal/harness/types.go`
- `internal/harness/session.go`
- `internal/harness/classifier.go`
- `internal/harness/runner.go`

## File Map

- Create: `internal/harness/thread.go`
  Purpose: thread types, statuses, deliverables, and turn-intent resolution helpers.
- Modify: `internal/harness/types.go`
  Purpose: add thread-ledger state to the public harness model.
- Modify: `internal/harness/session.go`
  Purpose: persist and transition thread state.
- Modify: `internal/harness/classifier.go`
  Purpose: resolve active-thread intent before lexical family classification.
- Modify: `internal/harness/runner.go`
  Purpose: run with thread-aware inputs and thread-aware observations.
- Modify: `internal/runtime/chat_debug.go`
  Purpose: trace thread creation, continuation, cancellation, replay, and supersession.
- Modify: `internal/harness/session_test.go`
- Modify: `internal/harness/classifier_test.go`
- Modify: `internal/harness/runner_test.go`
- Create: `internal/harness/testdata/debuglogs/thread-ledger-routing.jsonl`

## Task 1: Add Thread Types And Failing Tests

- [ ] **Step 1: Write failing session and runner tests**

Cover:

- an active preview thread persists beyond one turn
- a short follow-up such as `ok`, `continue`, or `show it again` resolves against the active thread instead of reclassifying as plain answer
- a cancellation such as `stop that` or `cancel the preview` marks the active thread canceled
- a new request that changes scope supersedes the old thread instead of mutating it implicitly

- [ ] **Step 2: Run focused tests to confirm failure**

Run:

- `go test ./internal/harness -run 'Test(Session|Runner|Classifier).*(Thread|Continuation|Cancel|Supersede)' -count=1`

Expected: FAIL because the current session model does not yet have an explicit thread ledger.

## Task 2: Add The Thread Ledger

- [ ] **Step 1: Define thread and intent types**

Add:

- `ThreadKind`
- `ThreadStatus`
- `DeliverableKind`
- `TurnIntent`
- `ThreadState`
- `ThreadLedger`

- [ ] **Step 2: Persist active-thread state in the session**

Implement:

- create thread on first qualifying turn
- mark thread active, canceled, completed, or superseded
- keep compatibility fields updated while the migration is incomplete

- [ ] **Step 3: Resolve turn intent before lexical family classification**

Implement intent categories:

- `new_task`
- `continue_thread`
- `replay_thread`
- `repair_thread`
- `cancel_thread`
- `meta_question`
- `supersede_thread`

## Task 3: Wire The Runner To Use Thread State

- [ ] **Step 1: Enrich runner observations with thread transitions**

Require the runner to record:

- created thread id
- reused thread id
- thread canceled
- thread superseded
- thread completed

- [ ] **Step 2: Keep migration behavior explicit**

Until later slices land:

- bridge existing `PendingAction` into thread creation
- bridge recent preview/artifact state into thread context
- log when compatibility bridges were used so they can be removed later

## Task 4: Verification

- [ ] **Step 1: Re-run focused thread tests**

Run:

- `go test ./internal/harness -run 'Test(Session|Runner|Classifier).*(Thread|Continuation|Cancel|Supersede)' -count=1`

Expected: PASS.

- [ ] **Step 2: Run broader harness/runtime checks**

Run:

- `go test ./internal/harness ./internal/runtime -count=1`

Expected: PASS.

- [ ] **Step 3: Build Forge**

Run:

- `go build -o ./forge ./cmd/forge`

Expected: exit `0`.
