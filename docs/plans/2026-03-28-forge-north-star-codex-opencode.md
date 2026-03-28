# Forge North Star: Codex/OpenCode Path

**Date:** 2026-03-28  
**Branch:** `forge/north-star-codex-opencode`  
**Intent:** Build a Forge runtime that is fluid to use day-to-day while preserving production safety boundaries.

---

## Product North Star

Forge should feel like one capable coding agent that:

1. Understands the request directly.
2. Chooses tools directly.
3. Delegates only when useful.
4. Streams meaningful progress while working.
5. Finishes with grounded, verifiable outcomes.

Target behavior is Codex/OpenCode-like fluidity, not classifier/planner-driven choreography.

---

## Source-Grounded Signals (Read Directly)

The direction above is grounded in direct source reading, not second-hand summaries:

- `openai/codex`:
  - `codex-rs/core/src/codex.rs`
    - `submission_loop(...)`: event-dispatch loop over incoming ops.
    - `run_turn(...)`: turn orchestration with an internal follow-up loop.
    - `run_sampling_request(...)` + `try_run_sampling_request(...)`: model stream, execute tools, set `needs_follow_up`, repeat until done.
  - `codex-rs/core/src/codex_delegate.rs`
    - `run_codex_thread_interactive(...)` and `run_codex_thread_one_shot(...)`: sub-agent sessions are first-class and return through event streams.

- `sst/opencode`:
  - `packages/opencode/src/session/prompt.ts`
    - `while (true)` turn loop with step progression and finish checks.
    - Tool resolution each step, model call, process tool calls, continue loop until finished.
    - Built-in handling for compaction/subtask continuation inside the same loop.
  - `packages/opencode/src/tool/task.ts`
    - LLM-dispatched sub-agent tool (`task`) with permission checks and plain-text `<task_result>` return shape.

The common feel from these implementations: one primary loop, model-led tool/delegation choices, host-owned execution boundaries.

---

## Architectural Direction

### 1) LLM-Driven ReAct Core Loop

Use one primary turn loop:

1. Send prompt + context + tools.
2. Model emits tool calls or final answer.
3. Host executes tool calls.
4. Feed tool results back.
5. Repeat until model completes.

No pre-LLM deterministic intent classifier as control-plane authority.

### 2) Thin Host Control Plane (Keep This)

Host should enforce boundaries, not micro-orchestrate reasoning:

- sandbox and approval policy for risky operations
- protected-branch policy
- claim/evidence guard (no ungrounded branch/commit/preview claims)
- traceability and replay logs
- cancellation/timeouts/circuit breaking

### 3) LLM-Driven Delegation

Delegation should be a tool (`spawn_agent`/`task`) selected by the model, not dispatched by host token routing.

- sub-agent returns plain text/evidence
- parent agent interprets results
- optional parallel delegation for independent subtasks

### 4) Transcript-First UX

Default UX remains one assistant stream with compact, frequent progress updates and no silent stalls.

---

## What We Are Moving Away From

The following are not north-star mechanisms:

- deterministic token classifier as primary router
- fixed worker type dispatch as default execution shape
- strict worker JSON schema as a hard dependency for progress
- prompt-shaped orchestration that must be patched per phrase

These patterns can remain temporarily for compatibility during migration, but they are not the target end state.

---

## Migration Plan (Phased)

## Phase A: Dual Runtime Flag

- Introduce a new experimental runtime mode (for example: `FORGE_CHAT_RUNTIME=react`).
- Keep current kernel path intact while new loop is built.
- Route only selected conversations through ReAct mode.

Exit criteria:
- ReAct mode can complete common local coding tasks with tools and final grounded responses.

## Phase B: Safety Layer Parity

- Port non-negotiable protections to the ReAct path:
  - protected-branch behavior
  - claim/evidence guard
  - explicit approvals/sandbox for risky actions
  - bounded retry/circuit-break behavior

Exit criteria:
- Safety controls are equivalent or stronger than current kernel behavior.

## Phase C: Delegation Pivot

- Implement model-driven delegation tool.
- Allow plain-text sub-agent outcomes.
- Add optional parallel sub-agent support with bounded depth.

Exit criteria:
- No host worker-class routing required for core delegation flows.

## Phase D: Default Switch

- Make ReAct mode default once telemetry/regression gates are stable.
- Keep kernel mode as fallback temporarily.
- Remove obsolete classifier/worker-contract coupling after soak period.

Exit criteria:
- Default Forge behavior is fluid, stable, and no longer phrase-patch dependent.

---

## Non-Negotiable Requirements

These stay regardless of loop style:

- no unverified side-effect claims
- no direct edits on protected branches without explicit safe context transition
- no hidden escalations beyond declared permissions
- full traceability for why a turn succeeded, retried, or failed

---

## Success Metrics

We are done when:

1. Repeated real-user prompts no longer require per-phrase router patches.
2. Median time-to-first-meaningful-progress update is low and consistent.
3. Multi-turn preview/apply flows complete without classifier drift.
4. Failure paths are explicit and recoverable instead of silent loops.
5. User trust improves: fewer “it fell over again” incidents in debug logs.

---

## Immediate Next Step

Create and execute a concrete implementation plan for **Phase A + Phase B** on this branch, with replay fixtures and manual break-it runs as release gates.
