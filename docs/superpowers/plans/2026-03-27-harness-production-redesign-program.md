# Forge Harness Production Redesign Program

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the remaining patch-prone harness seams with a thread-ledger-driven, deliverable-validated control plane that can sustain visible collaboration, previews, follow-ups, and bounded worker use without per-phrase repairs.

**Architecture:** Keep one visible `forge` assistant and the current kernel shell, but add a proper thread ledger, explicit execution lanes, typed deliverable contracts, a host-owned preview lifecycle, evidence-first follow-up ingestion, bounded worker governance, and release-grade tracing/evals.

**Tech Stack:** Go, existing `internal/harness`, `internal/agent`, `internal/agent/tools`, `internal/runtime`, Go tests/build tooling, debug-log fixtures, stress corpora, manual transcript validation.

---

## References

- `docs/superpowers/specs/2026-03-27-harness-production-redesign-design.md`
- `docs/reports/forge-harness-architecture-investigation-2026-03-27.md`
- `docs/reports/forge-harness-audit-remediation-2026-03-27.md`
- `docs/superpowers/plans/2026-03-27-harness-control-plane-realignment.md`

## Slice Order

### Slice 1: Thread Ledger And Turn Resolution

Plan: `docs/superpowers/plans/2026-03-27-harness-thread-ledger-and-turn-resolution.md`

Why first:

- every recurring failure is harder than it should be because the runtime does not have an explicit active-thread model
- follow-up, replay, repair, cancel, and pasted-evidence handling all depend on this foundation

Output:

- explicit thread state in session
- turn-intent resolution ahead of lexical family classification
- compatibility bridge from the current `PendingAction` and recent-preview hints into thread-ledger semantics

### Slice 2: Execution Lanes And Completion Contracts

Plan: `docs/superpowers/plans/2026-03-27-harness-execution-lanes-and-completion-contracts.md`

Why second:

- once the thread owner exists, the runtime must stop treating "no executor error" as success
- visible collaboration needs a stronger action lane than plain local execution

Output:

- typed turn outcomes
- deliverable validation
- host-owned retry/replan/blocked transitions

### Slice 3: Preview And Artifact Lifecycle

Plan: `docs/superpowers/plans/2026-03-27-harness-preview-and-artifact-lifecycle.md`

Why third:

- preview work is the highest-volume failure surface
- it needs a real host-owned lifecycle before follow-up recovery and evaluation can be trusted

Output:

- preview session model
- render validation
- preview reuse, supersession, and teardown

### Slice 4: Continuation Recovery And Evidence Ingestion

Plan: `docs/superpowers/plans/2026-03-27-harness-continuation-recovery-and-evidence-ingestion.md`

Why fourth:

- once preview and thread state are explicit, the runtime can properly ingest raw pasted HTML, logs, replay requests, and repair feedback

Output:

- evidence classification
- replay vs repair routing
- cancel/resume semantics
- thread-consistent handling of pasted runtime evidence

### Slice 5: Worker Governance And Trust Boundaries

Plan: `docs/superpowers/plans/2026-03-27-harness-worker-governance-and-trust-boundaries.md`

Why fifth:

- worker safety and delegation quality depend on the thread and lane model already existing
- security boundaries should be enforced with the new control plane, not patched onto the old one

Output:

- stricter worker admission rules
- validated sidecar result merge
- explicit untrusted-input handling
- approval and permission hardening

### Slice 6: Observability, Evals, And Production Gates

Plan: `docs/superpowers/plans/2026-03-27-harness-observability-evals-and-production-gates.md`

Why last:

- release gates are only meaningful after the architectural seams above exist
- this slice turns the redesign into a measurable product boundary rather than a best-effort fix

Output:

- trace coverage for new state transitions
- replay corpus and wide prompt coverage
- CI/release gates for harness regressions

## Dependency Rules

- Slice 1 must land before any other slice starts.
- Slice 2 depends on Slice 1.
- Slice 3 depends on Slices 1 and 2.
- Slice 4 depends on Slices 1, 2, and 3.
- Slice 5 depends on Slices 1 and 2, and should be completed before Slice 6 is declared done.
- Slice 6 runs throughout for fixture growth, but its release gates only become authoritative after Slices 1 through 5 land.

## Cross-Slice Requirements

- Every slice must add or update replayable regression coverage.
- Every slice must update the debug trace when it introduces a new state transition.
- Every slice must prefer host-owned policy and typed validation over prompt wording.
- No slice may widen worker autonomy or move more control-plane truth into model prose.
- Every slice that introduces visible user-facing work must define the progress events that work emits.

## Release Criteria

- [ ] Slice 1 implemented and verified
- [ ] Slice 2 implemented and verified
- [ ] Slice 3 implemented and verified
- [ ] Slice 4 implemented and verified
- [ ] Slice 5 implemented and verified
- [ ] Slice 6 implemented and verified
- [ ] Real logs replay cleanly across the major failure classes seen in March 2026
- [ ] Wide prompt and long-turn coverage stop surfacing architecture-level regressions
