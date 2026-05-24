# Task State Machine Shell Design

## Goal

Recover the runtime invariant Forge lost during the React/native-tool refactors: one runtime-owned coordinator decides task phase, legal tools, budgets, recovery, and completion.

This is not a new fourth state system. It is a shell that makes existing state objects subordinate to one authoritative task coordinator.

## Why This Exists

Recent live runs show the same failure shape returning under different names: wrong intent, wrong tools, stale delegation phase, missing evidence, false completion, over-exploration, continuation drift, provider failures resetting task context, and output/read handle confusion.

Forge previously had state-machine concepts in commits such as:

- `3e31d4a feat: add session runner (state machine, pass loop, event emission)`
- `36cac22 Add dispatch flow state machine`
- `757742a Task 9: add TUI nudge system driven by runtime policy and task state`

The newer React loop kept durable sessions and native tools, but coordination drifted into `internal/react/loop.go` plus several partial state systems: `TurnContract`, `SideEffectIntent`, `TaskState`, delegation state, retry prompts, hook overlays, and budget counters. The result is circular hardening instead of convergence.

## Design Summary

Add a `TaskStateMachine` around the existing ReAct loop. The shell owns the task lifecycle and exposes compatibility views to the current loop until old mechanisms can be collapsed.

Phases:

```text
NoTask
Inspecting
Editing
Verifying
Summarizing
Done
Blocked
Failed
```

Each active task owns:

- `TaskID`
- `UserGoal`
- `Phase`
- `AllowedToolGroups`
- `Evidence`
- `Budgets`
- `FailureState`
- `CompletionCriteria`

The model may choose specific tools only inside the current runtime phase. The model may not redefine the phase by prose, `think`, `tool_help`, or repeated exploration.

## Ownership Rules

The coordinator owns:

- Task creation and task continuation.
- Phase transitions.
- Tool group exposure.
- Read/search/think/tool-help budgets.
- Provider/tool failure recovery policy.
- Completion gating.
- When `continue` resumes versus starts a new task.

Existing objects become views or adapters:

- `TurnContract` becomes the task completion/evidence view.
- `SideEffectIntent` becomes the scoped mutation view for the active task.
- `TaskState` becomes the user-visible/task-planning view.
- Delegation state becomes child work state under the active task.
- Hook overlays become hints generated from task state, not independent policy.

## Data Flow

1. User input enters `Runner.Run`.
2. `TaskStateMachine.ResolveInput` decides one of:
   - start a new task;
   - continue the active task;
   - clear/cancel the active task;
   - answer as plain chat with no task.
3. The coordinator sets the active phase and exposes a tool policy for that phase.
4. The existing ReAct loop runs one model step with that policy.
5. Tool calls are validated and executed by existing code.
6. Tool results feed back into `TaskStateMachine.ApplyToolResult`.
7. Final assistant text is accepted only if `TaskStateMachine.CanComplete` passes.
8. Provider/model failures call `TaskStateMachine.ApplyFailure`, preserving active task context unless the user cancels or clears it.

## Phase Semantics

### NoTask

No active task. Plain chat can answer directly. A new user request can create a task.

### Inspecting

Allowed tools: read/search/list/git-read/delegation-read as needed.

Budgets: bounded reads/searches, same-target loop detection, `think` budget.

Exit conditions:

- enough evidence to edit;
- enough evidence to summarize;
- blocked by missing info;
- user cancels.

### Editing

Allowed tools: read plus write/edit/apply-patch/run-command mutation tools scoped to task paths.

Exit conditions:

- mutation evidence captured;
- blocked by policy/path uncertainty;
- provider/tool failure recoverable under same task.

### Verifying

Allowed tools: validation commands, read diffs/status, targeted reads.

Exit conditions:

- required verification passes;
- verification fails and task returns to Editing;
- blocked after bounded retries.

### Summarizing

Allowed tools: normally none, except read-output/artifact-read if the runtime already has a handle required for the answer.

Exit conditions:

- final answer accepted;
- missing evidence returns to the owning phase.

### Done, Blocked, Failed

Terminal states. `continue` after `Done` starts from the latest user input. `continue` after `Blocked` or `Failed` resumes only if recovery is defined; otherwise Forge asks a concrete question.

## Tool Policy

Tool exposure must be derived from phase, not free-form user text. User text can create or update a task, but after the task exists, phase controls tool groups.

Examples:

- Active `Editing` task plus user says `continue`: expose edit/verify tools, not answer-only chat.
- Active `Verifying` task plus provider failure: retry/fallback without changing phase.
- Active `Inspecting` task with six repeated reads of the same file: block further same-target reads and force summarize or ask.
- Plain chat with no task: expose no tools unless the input explicitly creates an inspect task.

## Budgets

Budgets are hard runtime counters, not prompt nudges.

Minimum budgets:

- total tool steps per task phase;
- repeated same-target reads/searches;
- `think` calls and `think` output size;
- `tool_help` calls;
- provider retry attempts;
- child-agent wait cycles.

On budget exhaustion the coordinator must choose one runtime action:

- transition phase;
- block the tool and force synthesis;
- ask the user a specific question;
- mark task `Blocked` or `Failed`.

## Error Handling

Provider/auth/model failures never erase the active task. They attach to `FailureState` and the coordinator decides retry, fallback, blocked, or failed.

Malformed tool calls remain model-correctable, but repeated malformed calls consume budget. After budget exhaustion, the task transitions to `Failed` or `Blocked` with the exact invalid tool shape.

Child-agent failures attach to the parent task and cannot be hidden by a later empty parent response.

## Migration Strategy

This should land as a shell, not a rewrite.

1. Add `TaskStateMachine` types and pure transition tests.
2. Mirror current `TurnContract`, `SideEffectIntent`, and `TaskState` into the coordinator.
3. Route `continue`, provider failures, final validation, and tool allowlists through the coordinator.
4. Move budget counters from scattered workflow structs into task phase state.
5. Delete redundant heuristics only after equivalent coordinator tests exist.

## Testing Strategy

Start with pure state-machine tests, then integrate with `Runner`.

Required regressions:

- `continue` preserves unfinished edit task across turns.
- provider failure preserves active task and phase.
- repeated same-file reads block at budget and force synthesis.
- `think` budget blocks large/hot-loop reasoning payloads.
- child-agent failure remains visible through parent recovery.
- no tool exposure for plain chat with no active task.
- edit task cannot complete without write evidence.
- verify task cannot complete without verification evidence.

Acceptance criterion: a real run may still fail, but it must fail in a bounded task state (`Blocked`/`Failed`) with a concrete reason, not drift into a new intent, unbounded exploration, or false completion.

## Non-Goals

- Do not rewrite native tool execution.
- Do not replace provider drivers.
- Do not replace the renderer or output store.
- Do not add another independent contract system.
- Do not solve every old heuristic in the first patch.

## Success Definition

Forge stops relying on prompt feedback as the primary coordinator. Task state owns the loop. The existing ReAct machinery becomes an executor inside a bounded runtime state machine.
