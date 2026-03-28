# Harness Worker Governance And Trust Boundaries Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Keep hidden workers useful but tightly bounded, and move tool, content, and delegation trust boundaries fully into host policy rather than prompt wording.

**Architecture:** Workers remain sidecars for parallel or isolated work only. The manager owns the active thread, validates worker output, merges allowed state changes, and treats worker results, logs, files, and fetched content as untrusted until proven otherwise. Tool approvals and permission boundaries stay host-owned.

**Tech Stack:** Go, `internal/harness`, `internal/agent/tools`, `internal/runtime`, Go test/build tooling, regression fixtures.

---

## References

- `docs/superpowers/specs/2026-03-27-harness-production-redesign-design.md`
- `internal/harness/policy.go`
- `internal/harness/workers.go`
- `internal/harness/contracts.go`
- `internal/agent/tools/safety.go`

## File Map

- Modify: `internal/harness/policy.go`
  Purpose: admit workers from thread/lane context instead of prompt hints alone.
- Modify: `internal/harness/workers.go`
  Purpose: keep workers sidecar-only and forbid thread ownership transfer.
- Modify: `internal/harness/contracts.go`
  Purpose: validate worker outputs as advisory structured observations.
- Create: `internal/harness/trust.go`
  Purpose: shared trust-boundary classification for content sources.
- Modify: `internal/agent/tools/safety.go`
  Purpose: align tool safety with the new trust model.
- Modify: relevant tests under `internal/harness` and `internal/agent/tools`

## Task 1: Add Failing Governance Tests

- [ ] **Step 1: Write failing tests**

Cover:

- an active preview collaboration thread does not hand off visible ownership to a worker
- worker results cannot directly mark a thread complete without manager validation
- worker output containing untrusted prompt-like content is treated as data, not instructions
- unsafe tool categories remain approval-gated even if a worker requests them

- [ ] **Step 2: Run focused tests and confirm failure**

Run:

- `go test ./internal/harness -run 'Test(Policy|Worker|Contracts).*(Sidecar|Ownership|Untrusted|Approval)' -count=1`
- `go test ./internal/agent/tools -run 'Test.*Safety.*' -count=1`

Expected: FAIL before the new governance rules exist.

## Task 2: Tighten Worker Admission

- [ ] **Step 1: Make admission depend on thread and lane**

Require:

- visible preview collaboration stays manager-owned
- workers are admitted only for independent verification, isolated edit slices, or parallel research
- no worker may widen scope or change thread kind

- [ ] **Step 2: Validate worker merge semantics**

Worker outputs may:

- add evidence
- propose changes
- report verification

Worker outputs may not:

- mutate preview session state directly
- mark the visible thread complete on their own
- emit final user-facing prose

## Task 3: Add Trust-Boundary Handling

- [ ] **Step 1: Define trusted vs untrusted sources**

Treat as untrusted:

- files
- logs
- fetched web content
- generated artifacts
- pasted terminal output
- worker output before validation

- [ ] **Step 2: Align tool safety and approvals**

Require explicit host checks for:

- shell execution with side effects
- preview/server lifecycle operations
- broad writes
- networked operations outside approved paths

## Task 4: Verification

- [ ] **Step 1: Re-run focused governance tests**

Run the two commands above and confirm PASS.

- [ ] **Step 2: Run affected packages**

Run:

- `go test ./internal/harness ./internal/agent/tools -count=1`

Expected: PASS.

- [ ] **Step 3: Build Forge**

Run:

- `go build -o ./forge ./cmd/forge`

Expected: exit `0`.
