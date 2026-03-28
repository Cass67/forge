# Harness Observability, Evals, And Production Gates Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make the redesign measurable and releaseable by adding trace coverage for the new control plane, replayable failure fixtures, broad prompt corpora, long-turn validation, and explicit production gates.

**Architecture:** Observability is part of the harness contract. Every thread transition, lane decision, deliverable check, preview validation result, worker admission, and blocked/retry path should be visible in traces and reproducible in tests. Release should be blocked by harness regressions, not by user frustration after the fact.

**Tech Stack:** Go, `internal/harness`, `internal/runtime`, regression fixtures, debug-log tooling, manual runbooks, Go test/build tooling.

---

## References

- `docs/superpowers/specs/2026-03-27-harness-production-redesign-design.md`
- `docs/reports/forge-harness-debug-2026-03-27.md`
- `docs/reports/forge-harness-audit-remediation-2026-03-27.md`
- `internal/runtime/chat_debug.go`
- `internal/harness/stress_corpus_test.go`

## File Map

- Modify: `internal/runtime/chat_debug.go`
  Purpose: emit trace detail for threads, lanes, deliverable checks, preview probes, and worker admission.
- Modify: `internal/runtime/chat_debug_test.go`
- Modify: `internal/harness/regression_test.go`
- Modify: `internal/harness/stress_corpus_test.go`
- Modify: `internal/tui/chatmodel_test.go`
  Purpose: verify that progress entries remain visible as an accumulating list during active turns.
- Create: `internal/harness/testdata/debuglogs/production-redesign/`
  Purpose: real-log fixtures grouped by failure class.
- Create: `docs/reports/forge-harness-production-validation-runbook.md`
  Purpose: manual validation steps and evidence format.

## Task 1: Add Trace Coverage

- [ ] **Step 1: Write failing trace tests**

Cover:

- thread created/continued/canceled/superseded/completed
- lane selected
- deliverable satisfied vs unsatisfied
- preview probe result
- worker admitted or denied with reason
- progress milestones emitted with useful user-facing text during long-running visible turns

- [ ] **Step 2: Run focused tests and confirm failure**

Run:

- `go test ./internal/runtime -run 'TestChatDebug.*(Thread|Lane|Deliverable|Preview|Worker)' -count=1`

Expected: FAIL before the new trace fields are emitted.

## Task 2: Expand Replayable Regression Coverage

- [ ] **Step 1: Build fixture classes from real failures**

At minimum:

- malformed visible-path action output
- preview thread replay
- preview thread repair after user-pasted evidence
- cancel/resume
- worker admission boundaries
- long-running turns with multiple visible progress milestones

- [ ] **Step 2: Keep the corpus wide**

Target:

- at least 100 prompt variants
- at least 50 multi-turn transitions
- multiple providers where feasible

## Task 3: Define Production Gates

- [ ] **Step 1: Add required verification commands**

Run:

- `go test ./internal/agent ./internal/agent/tools ./internal/harness ./internal/runtime -count=1`
- `go test ./... -count=1`
- `go build -o ./forge ./cmd/forge`

- [ ] **Step 2: Add manual validation runbook**

Include:

- preview creation
- preview replay
- preview repair after broken artifact evidence
- inspect continuation
- worker sidecar verification
- cancellation and supersession
- progress-list behavior during long-running turns, including at least one turn with multiple intermediate updates before completion

- [ ] **Step 3: Block release on harness regressions**

Do not call the redesign done until:

- replay fixtures pass
- corpus tests pass
- long-turn preview sessions pass
- manual runbook passes without new architecture-level drift
- visible progress is clearly better than a spinner in manual transcript validation

## Task 4: Verification

- [ ] **Step 1: Re-run focused debug/regression tests**

Run:

- `go test ./internal/runtime -run 'TestChatDebug.*(Thread|Lane|Deliverable|Preview|Worker)' -count=1`
- `go test ./internal/harness -run 'Test(Regression|Stress).*' -count=1`

Expected: PASS.

- [ ] **Step 2: Run the full verification set**

Run the three repository-wide commands above and confirm PASS.
