# Codex Reliability Gap Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Bring Forge closer to Codex’s host-driven reliability by replacing prompt-only behavior with explicit runtime state, first-class exec/process control, and stronger turn routing.

**Architecture:** Codex is more reliable because the host owns turn state, pending input, long-running exec sessions, and visible lifecycle events. Forge now has completion evidence gating, but still lacks several Codex-style host primitives, so the model can still feel stalled or opaque during ordinary coding work. This plan closes the remaining gaps in the runtime and TUI rather than adding more prompt instructions.

**Tech Stack:** Go, Bubble Tea, Forge React runtime, transcript tests, TUI live chat runtime

---

## Source-Backed Gap Summary

Codex evidence gathered from `/tmp/openai-codex`:

- `codex-rs/core/src/tasks/regular.rs`: regular turns loop until pending input is drained, instead of treating steering as an external queue.
- `codex-rs/core/src/tasks/mod.rs`: active turns, interrupt boundaries, and aborted-turn markers are first-class session state.
- `codex-rs/core/src/tools/orchestrator.rs`: tool execution has a single approval + sandbox + retry orchestrator.
- `codex-rs/core/src/tools/handlers/unified_exec.rs` and `codex-rs/core/src/tools/runtimes/unified_exec.rs`: long-running shell commands are first-class PTY sessions, not synchronous shell wrappers.
- `codex-rs/tui/src/bottom_pane/pending_input_preview.rs` and `codex-rs/tui/src/chatwidget/interrupts.rs`: queued steering, interrupts, approvals, and tool lifecycle events are surfaced explicitly in the UI.

Forge source showing the remaining gaps:

- `internal/runtime/chat.go`: queued steering exists as an outer runtime queue, not as first-class turn state owned by the runner/session.
- `internal/react/loop.go`: the runner is still fundamentally a synchronous “prompt -> stream -> maybe tools -> final text” loop with no native concept of interrupt boundary or pending-input continuation.
- `internal/agent/tools/command.go`: `run_command` is synchronous `CombinedOutput()` shell execution, so long-running tasks are still a weak point.
- `internal/tui/chatmodel.go`: the UI can queue steering, but it does not render a Codex-like pending-input surface or explicit active-turn lifecycle beyond flash/status text.
- `internal/agent/tools/registry.go`: tool exposure is broad, but turn-type-specific tool shaping remains weak compared with Codex’s router/orchestrator model.

## Task 1: Make Pending Input A First-Class Runner State

**Files:**
- Modify: `internal/react/session.go`
- Modify: `internal/react/loop.go`
- Modify: `internal/runtime/chat.go`
- Modify: `internal/tui/chatmodel.go`
- Test: `internal/react/loop_test.go`
- Test: `internal/runtime/chat_test.go`
- Test: `internal/tui/chatmodel_test.go`

- [ ] **Step 1: Add explicit pending-input state to the React session**

Track queued steering in `internal/react/session.go` instead of only in the outer chat runtime goroutine. The session should distinguish:
- pending steering for the active turn
- follow-up queued prompts for after the active turn
- whether an interrupt should submit pending steering immediately

- [ ] **Step 2: Teach the runner to continue from pending input without handing control back to outer runtime**

Update `internal/react/loop.go` so a regular turn can:
- finish a model/tool boundary
- detect pending steer input
- append the host-side steering marker/context
- continue the same active work loop

This should reduce the “app stalls until next turn” class of failures.

- [ ] **Step 3: Preserve interrupt boundaries explicitly**

Add a model-visible interrupted-turn marker or runtime note in Forge similar to Codex’s interrupted-turn guidance so the next request is grounded in “the prior turn was interrupted; re-check state before continuing”.

- [ ] **Step 4: Simplify outer runtime queueing**

Reduce `internal/runtime/chat.go` queue management so it delegates active-turn steering to the session/runner rather than owning most of that policy itself.

- [ ] **Step 5: Add regression tests**

Cover:
- queued steering during a running turn resumes work without silent loss
- interrupted turns leave a visible boundary for the next turn
- follow-up steering after a tool boundary is consumed by the same active workflow

## Task 2: Replace Synchronous Shell Execution With First-Class Exec Sessions

**Files:**
- Create: `internal/agent/tools/exec_session.go`
- Modify: `internal/agent/tools/command.go`
- Modify: `internal/agent/tools/registry.go`
- Modify: `internal/agent/event_render.go`
- Modify: `internal/tui/chatmodel.go`
- Test: `internal/agent/tools/command_test.go`
- Test: `internal/tui/chatmodel_test.go`

- [ ] **Step 1: Introduce an exec-session manager**

Create a small process/session manager for long-running shell commands. It should allocate session IDs, keep stdout/stderr buffers, support polling, and support stdin writes where relevant.

- [ ] **Step 2: Split short command execution from long-running execution**

Keep short read/check commands on the current path, but route long-running or PTY-style commands through the new manager. This should become Forge’s equivalent to Codex unified exec.

- [ ] **Step 3: Expose lifecycle events to the UI**

Emit begin/end/status events so the TUI can show:
- command started
- waiting for background terminal
- command finished
- command still running with session ID

- [ ] **Step 4: Remove remaining preview-server dependence on generic shell**

Keep preview on dedicated tools, but also make the runtime resilient if a model still tries to launch a long-running local process.

- [ ] **Step 5: Add regression tests**

Cover:
- long-running command returns a session handle instead of hanging the turn
- preview-style server launch never blocks the chat loop
- command lifecycle is visible in the transcript/UI

## Task 3: Add Early Turn Routing, Not Just Final-Answer Gating

**Files:**
- Modify: `internal/runtime/chat.go`
- Modify: `internal/runtime/completion_enforcement.go`
- Modify: `internal/react/prompt.go`
- Test: `internal/runtime/chat_test.go`
- Test: `internal/runtime/chat_transcript_test.go`

- [ ] **Step 1: Add host-side turn classification**

Classify turns into categories such as:
- repo inspection
- implementation
- validation
- preview/design
- planning/analysis

- [ ] **Step 2: Inject host-owned first-action guidance**

For repo-grounded turns, require the first step to be a tool action or a narrow allowed exception. This should happen before the model freewrites, not only after a bad final answer is produced.

- [ ] **Step 3: Tighten tool availability by task class where needed**

For example:
- preview/design turns should bias toward preview/artifact tools
- repo inspection turns should bias toward repo-read tools before synthesis
- implementation turns should preserve write + validation expectations

- [ ] **Step 4: Add transcript regressions**

Cover:
- “inspect the repo” cannot complete with planning prose first
- preview turns choose preview tools rather than generic shell
- plan/analysis follow-ups remain grounded without forcing redundant reads every turn

## Task 4: Surface Runtime State The Way Codex Does

**Files:**
- Modify: `internal/tui/chatmodel.go`
- Modify: `internal/tui/chatmsg.go`
- Create: `internal/tui/pendinginput.go`
- Test: `internal/tui/chatmodel_test.go`

- [ ] **Step 1: Add a pending-input preview surface**

Render queued steering and queued follow-up messages above the composer, instead of only flashing `queued steering`.

- [ ] **Step 2: Distinguish active runtime states**

Add explicit visible states such as:
- working
- waiting on background command
- waiting on approval
- queued steering pending
- interrupted, awaiting direction

- [ ] **Step 3: Keep interrupt affordances stable**

Ensure `Esc` always means interrupt active work when no popup/approval is owning focus, and the footer shows the current behavior clearly.

## Task 5: Lock It Down With Codex-Style Behavior Evals

**Files:**
- Modify: `internal/runtime/chat_transcript_test.go`
- Modify: `internal/runtime/chat_test.go`
- Modify: `internal/tui/chatmodel_test.go`
- Modify: `internal/agent/tools/command_test.go`

- [ ] **Step 1: Add transcript cases for the known failure classes**

Cases:
- narrated intent without tools
- fake blockage
- fake edits
- fake verification
- long-running shell session
- preview lifecycle visibility
- queued steering during active work

- [ ] **Step 2: Add multi-turn interruption/resume tests**

Exercise interrupted turns, steering after interruption, and follow-up answers that must re-check state.

- [ ] **Step 3: Make package-level test commands part of the acceptance criteria**

Run:
- `go test ./internal/react -count=1`
- `go test ./internal/runtime -count=1`
- `go test ./internal/tui -count=1`
- `go test ./internal/agent/tools -count=1`

## Recommended Execution Order

1. Task 3 first: reduce wasted turns by routing the first action correctly.
2. Task 1 second: move queued steering into first-class runner/session state.
3. Task 2 third: stop long-running shell work from freezing or confusing the turn loop.
4. Task 4 fourth: surface runtime state clearly in the TUI.
5. Task 5 throughout: codify each failure as a regression before moving on.
