# Forge Borrow Roadmap Design

## Summary

Forge should adopt a focused subset of ideas from `~/git/cci` and `~/git/codex` in four independently shippable phases:

1. add a root `justfile` and normalize local developer workflow
2. strengthen approvals and shell-rule matching
3. grow prompt overlays into a real typed hook runtime
4. harden the memory pipeline and secret redaction

The goal is not to import either system wholesale. The goal is to improve Forge's reliability, usability, and extensibility while preserving its current local-first shape and host-owned execution model.

## Why These Four

The comparison across the three repositories suggests a clear pattern:

- Forge already has the right product center of gravity and a workable runtime.
- `cci` is strongest in collaboration scaffolding, permissions, and lifecycle extensibility.
- Codex is strongest in operational discipline, approvals, and memory architecture.

These four phases capture the most useful ideas that are both:

- high leverage for everyday use
- realistic to integrate without rewriting Forge into a different product

## Non-Goals

- do not turn Forge into a clone of `cci`
- do not import Codex's full workspace, state-db, or packaging model
- do not introduce visible multi-agent complexity as part of this roadmap
- do not add broad plugin-marketplace behavior in the first pass

## Design Principles

1. Keep each phase independently shippable.
2. Favor host-owned runtime state over prompt-only conventions.
3. Improve safety and clarity before adding more autonomy.
4. Reuse Forge's current package boundaries where they are still coherent.
5. Add tests and docs in the same phase as the behavior change.

## Phase 1: Root Justfile

### Problem

Forge's development workflow is documented, but it is not normalized behind one canonical task runner. Common commands are repeated across `README.md`, `BUILD.md`, and `CONTRIBUTING.md`, which increases friction and makes "the right thing to run" less obvious.

### Borrowed Idea

Borrow Codex's repo-root task-runner pattern, but scale it to Forge's simpler Go-only workflow.

### Proposed Changes

- Add a root `justfile`.
- Provide canonical recipes for:
  - building the main binary
  - running the main binary
  - running the full test suite
  - running targeted tests
  - running hook-compatible checks
  - formatting or pre-commit verification when local tooling is available
- Keep the recipes thin wrappers around the existing Go and pre-commit commands.
- Update `README.md`, `BUILD.md`, and `CONTRIBUTING.md` to point contributors to `just` first.

### Acceptance Criteria

- A contributor can discover the common local workflows from `just -l`.
- The documented commands in the repo are consistent with the `justfile`.
- Phase 1 is useful on its own and does not require later phases to pay off.

## Phase 2: Approval And Shell-Rule Reliability

### Problem

Forge already has approvals, sandbox policy handling, and guardian review, but its rule system is still thinner than the command-matching and explanation model in `cci`. The current behavior risks being harder to predict, less expressive for safe allow-rules, and less clear when an action is denied or needs confirmation.

### Borrowed Idea

Borrow the useful structural parts of `cci`'s permission machinery:

- exact, prefix, and wildcard command rules
- clearer command-to-rule matching
- more explicit human-readable approval reasoning

Do this without importing the full `cci` permission stack or classifier system.

### Proposed Changes

- Introduce a small internal matcher for exact, prefix, and wildcard shell rules.
- Rework approval explanations so Forge can say:
  - which rule matched
  - which sandbox policy blocked the action
  - whether the action needs a prompt, is forbidden, or can proceed
- Keep Forge's existing approval policy model, but improve the determinism and clarity of the evaluation path.
- Add regression tests around:
  - wildcard and prefix matching
  - sandbox-policy override behavior
  - user-facing approval summaries

### Acceptance Criteria

- Rule matching is deterministic and test-covered.
- Approval requests explain the reason in concrete terms.
- Safe command families can be expressed without over-broad allow rules.

## Phase 3: Typed Hook Runtime

### Problem

Forge currently has prompt overlay plumbing, but not a real lifecycle hook runtime. That limits extensibility and forces behavior shaping into static prompt text or ad hoc runtime code.

### Borrowed Idea

Borrow `cci`'s notion of typed lifecycle hooks, but start with a bounded Forge version that fits the current runtime.

### Proposed Changes

- Expand `internal/hooks` from overlay conversion into a typed runtime surface.
- Define an initial set of hook points around the current chat/runtime flow, such as:
  - session start
  - session end
  - before tool use
  - after tool use
  - turn complete
- Define a bounded hook result model:
  - prompt overlay injection
  - informational runtime note
  - hard block for clearly invalid next steps
- Keep execution deterministic, ordered, and bounded by timeout.
- Wire hook output into prompt composition and relevant UI/runtime surfaces.

### Constraints

- This phase is not a general remote plugin platform.
- Hook execution must not weaken Forge's approval or secret-handling guarantees.
- Failures should degrade safely rather than destabilize the main session.

### Acceptance Criteria

- Forge can register and execute typed runtime hooks at a small number of stable lifecycle points.
- Hook results can influence prompt overlays without corrupting prompt assembly.
- Hook failures are contained and tested.

## Phase 4: Memory Pipeline Hardening

### Problem

Forge has already started a memory subsystem, but it is still shallow:

- extraction is minimal
- consolidation is simple append-and-dedupe behavior
- redaction only handles a tiny pattern set

That is enough for experimentation, but not enough for a memory feature that should be trusted.

### Borrowed Idea

Borrow Codex's discipline around bounded extraction and consolidation, plus `cci`'s stronger local secret-scanning mindset.

### Proposed Changes

- Keep the current lightweight package boundary under `internal/memory`, but make the roles clearer:
  - extraction decides what session facts are memory-worthy
  - redaction removes sensitive material conservatively
  - consolidation retains a bounded, deduplicated working set
  - prompt injection consumes only a compact summary
- Expand redaction coverage beyond the current small regex set.
- Ensure blocked or error-heavy turns do not poison stored summaries without review.
- Add tests proving that obvious secret-like material is redacted before memory retention.

### Acceptance Criteria

- Memory records stay bounded and deterministic.
- Secret-like values are redacted before being stored or surfaced.
- Prompt-visible memory remains short and useful rather than verbose transcript spillover.

## Delivery Strategy

The phases should land in order:

1. `justfile`
2. approvals and shell rules
3. typed hooks
4. memory hardening

Each phase should include:

- code changes
- targeted tests
- doc updates

This sequencing matters:

- phase 1 improves contributor velocity immediately
- phase 2 improves trust in execution before adding more runtime power
- phase 3 adds the extension surface on top of a safer approval path
- phase 4 hardens retained context after the runtime extension points are clearer

## Testing Strategy

Each phase should be verified independently.

Phase 1:

- recipe smoke checks
- doc consistency review

Phase 2:

- matcher unit tests
- approval flow regression tests
- sandbox interaction tests

Phase 3:

- hook ordering tests
- timeout/failure containment tests
- prompt composition tests

Phase 4:

- extraction tests
- consolidation tests
- redaction regression tests
- prompt memory summary tests

## Risks

### Over-importing from `cci`

`cci` contains richer machinery than Forge currently needs. The risk is importing complexity instead of importing the useful idea. The mitigation is to borrow structure, not volume.

### Weak hook isolation

Hooks are valuable only if they cannot destabilize the main agent loop. The mitigation is to keep hook capabilities narrow, typed, and timeout-bounded.

### False confidence in memory safety

A memory feature with weak redaction is worse than no memory feature. The mitigation is to make phase 4 conservative, bounded, and test-heavy.

## Recommendation

Proceed with this roadmap as a targeted architecture-upgrade sequence, not as a minimum-diff port and not as a wholesale subsystem transplant.

This is the most efficient path that still meaningfully improves Forge's day-to-day developer experience, execution reliability, and long-term extensibility.
