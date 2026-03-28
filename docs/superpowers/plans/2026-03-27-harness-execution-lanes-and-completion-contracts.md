# Harness Execution Lanes And Completion Contracts Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace binary success semantics with explicit execution lanes and deliverable validation so visible collaboration cannot silently "complete" when the requested artifact, preview, verification, or repair was never actually satisfied.

**Architecture:** Keep `local`, `strict_local`, and `worker` execution paths, but formalize them as `conversational`, `strict_action`, and `worker_sidecar` lanes. Add typed action outcomes and deliverable checks so the kernel can distinguish retry, replan, awaiting feedback, satisfied deliverable, and blocked states. Keep skill injection and visible progress host-managed so the strict-action lane reports real state transitions instead of provider-imagined narration.

**Tech Stack:** Go, `internal/harness`, `internal/agent`, `internal/runtime`, Go test/build tooling.

---

## References

- `docs/superpowers/specs/2026-03-27-harness-production-redesign-design.md`
- `internal/harness/planner.go`
- `internal/harness/policy.go`
- `internal/harness/local.go`
- `internal/harness/strictlocal.go`
- `internal/harness/runner.go`

## File Map

- Modify: `internal/harness/types.go`
  Purpose: add lane, outcome, and deliverable-status types.
- Modify: `internal/harness/planner.go`
  Purpose: choose lane from thread state and turn intent.
- Modify: `internal/harness/policy.go`
  Purpose: decide retry/replan/feedback/complete/blocked from typed outcomes.
- Modify: `internal/harness/local.go`
  Purpose: make conversational local execution report outcome details instead of only complete/blocked.
- Modify: `internal/harness/strictlocal.go`
  Purpose: make strict-action execution return typed action outcomes.
- Modify: `internal/harness/runner.go`
  Purpose: stop treating "no error" as "done".
- Modify: `internal/agent/agent.go`
  Purpose: surface malformed or mixed action output in a way the lane contract can reason about.
- Modify: `internal/agent/progress.go`
  Purpose: align progress emission with typed lane state instead of prompt narration.
- Modify: `internal/agent/system.go`
  Purpose: keep skill routing and strict-action system behavior host-owned.
- Modify: `internal/agent/event_render.go`
  Purpose: render progress updates as an accumulating visible activity list instead of a spinner-only wait state.
- Modify: `internal/tui/chatmodel.go`
  Purpose: preserve and display the running progress list clearly in the live transcript.
- Modify: tests under `internal/harness` and `internal/runtime`

## Task 1: Add Failing Completion-Contract Tests

- [ ] **Step 1: Write failing tests**

Cover:

- a strict visible turn that writes an artifact but never satisfies its declared deliverable does not complete
- a visible turn that emits malformed tool residue reports retry or blocked, never satisfied
- a direct-answer turn may still complete without tools
- a turn awaiting explicit user feedback is not recorded as completed
- a long-running strict-action turn emits host-owned progress before the final answer
- progress entries accumulate as distinct milestones instead of replacing each other with an indistinct spinner state

- [ ] **Step 2: Run focused tests and confirm failure**

Run:

- `go test ./internal/harness -run 'Test(Plan|Policy|Runner|StrictLocal|Local).*(Lane|Deliverable|Retry|AwaitingFeedback)' -count=1`
- `go test ./internal/runtime -run 'TestRunChat.*(Progress|StrictLocal)' -count=1`
- `go test ./internal/agent -run 'Test.*Progress.*' -count=1`

Expected: FAIL before the new lane and outcome types exist.

## Task 2: Add Lane And Outcome Types

- [ ] **Step 1: Define explicit lane and outcome types**

Add:

- `ExecutionLane`
- `OutcomeKind`
- `DeliverableStatus`
- structured outcome payload on `Observation`
- explicit progress milestone types for visible turns
- progress payload text built from host state and validated tool outcomes

- [ ] **Step 2: Route turns into the correct lane**

Rules:

- pure direct answers stay conversational
- any visible collaboration that depends on tools, artifacts, previews, edits, or repair uses strict action
- hidden workers remain sidecars only

## Task 3: Replace Binary Completion Decisions

- [ ] **Step 1: Make policy inspect outcome kind plus deliverable status**

Require decisions such as:

- retry
- replan
- awaiting user feedback
- complete
- blocked
- emit progress milestone

- [ ] **Step 2: Keep final user-facing response generation manager-owned**

Ensure:

- strict-action executor observations are validated before being surfaced as final answers
- completion is only emitted once the deliverable is satisfied
- progress lines come from host state changes, not model-written prose
- the user sees a growing activity list while the turn is live, not only a single transient status row

## Task 4: Verification

- [ ] **Step 1: Re-run focused lane/contract tests**

Run:

- `go test ./internal/harness -run 'Test(Plan|Policy|Runner|StrictLocal|Local).*(Lane|Deliverable|Retry|AwaitingFeedback)' -count=1`
- `go test ./internal/runtime -run 'TestRunChat.*(Progress|StrictLocal)' -count=1`
- `go test ./internal/agent -run 'Test.*Progress.*' -count=1`

Expected: PASS.

- [ ] **Step 2: Run affected packages**

Run:

- `go test ./internal/agent ./internal/harness ./internal/runtime -count=1`

Expected: PASS.

- [ ] **Step 3: Build Forge**

Run:

- `go build -o ./forge ./cmd/forge`

Expected: exit `0`.
