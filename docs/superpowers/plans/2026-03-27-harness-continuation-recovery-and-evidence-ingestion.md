# Harness Continuation Recovery And Evidence Ingestion Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make follow-up turns, replay requests, pasted logs, pasted HTML, and user-observed failures resolve against the active thread as typed evidence or repair work instead of falling back into fresh-request heuristics.

**Architecture:** Add an evidence-ingestion layer before standard classification. During an active thread, pasted structured content should be classified as evidence objects first. The thread resolver should then decide whether the turn means replay, repair, diagnosis, cancel, or supersede. This is the missing recovery path between "runtime did something" and "user says it is still wrong."

**Tech Stack:** Go, `internal/harness`, `internal/runtime`, debug-log fixtures, Go test/build tooling.

---

## References

- `docs/superpowers/specs/2026-03-27-harness-production-redesign-design.md`
- `internal/harness/classifier.go`
- `internal/harness/session.go`
- `internal/harness/runner.go`
- `docs/reports/forge-harness-debug-2026-03-27.md`

## File Map

- Create: `internal/harness/evidence_ingest.go`
  Purpose: classify pasted content into typed evidence.
- Create: `internal/harness/evidence_ingest_test.go`
- Modify: `internal/harness/classifier.go`
- Modify: `internal/harness/session.go`
- Modify: `internal/harness/runner.go`
- Modify: `internal/runtime/chat_debug.go`
- Modify: `internal/harness/stress_corpus_test.go`
- Create: `internal/harness/testdata/debuglogs/continuation-recovery.jsonl`

## Task 1: Add Failing Recovery Tests

- [ ] **Step 1: Write failing tests**

Cover:

- pasted raw HTML on an active preview thread is treated as `raw_html_artifact` evidence, not a new implementation request
- pasted log output on an active thread triggers diagnose/repair routing
- replay requests such as `show it again` or `where can i see it` stay on the active preview thread
- cancellation such as `stop`, `cancel it`, or `never mind` cleanly closes the thread

- [ ] **Step 2: Run focused tests and confirm failure**

Run:

- `go test ./internal/harness -run 'Test(Classifier|Runner|EvidenceIngest|Stress).*(Replay|Repair|Pasted|Cancel|Continuation)' -count=1`

Expected: FAIL before evidence ingestion and thread-aware recovery exist.

## Task 2: Add Evidence Classification

- [ ] **Step 1: Define evidence kinds**

At minimum:

- `log_excerpt`
- `raw_html_artifact`
- `terminal_output`
- `quoted_model_output`
- `user_observed_error`

- [ ] **Step 2: Ingest evidence before lexical routing**

When an active thread exists:

- detect whether the message is predominantly pasted evidence
- attach it to the thread
- route to replay, diagnose, or repair depending on thread kind and recent validated state

## Task 3: Add Recovery Semantics

- [ ] **Step 1: Implement replay vs repair distinctions**

Examples:

- `show it again` -> replay
- `where can i see them?` -> replay/status
- pasted raw HTML after a claimed preview success -> repair/diagnose
- `stop that` -> cancel

- [ ] **Step 2: Preserve explicit supersession**

If the user clearly asks for a different task, supersede the active thread instead of trying to repair it.

## Task 4: Verification

- [ ] **Step 1: Re-run focused recovery tests**

Run:

- `go test ./internal/harness -run 'Test(Classifier|Runner|EvidenceIngest|Stress).*(Replay|Repair|Pasted|Cancel|Continuation)' -count=1`

Expected: PASS.

- [ ] **Step 2: Re-run broad harness/runtime tests**

Run:

- `go test ./internal/harness ./internal/runtime -count=1`

Expected: PASS.

- [ ] **Step 3: Build Forge**

Run:

- `go build -o ./forge ./cmd/forge`

Expected: exit `0`.
