# Forge Phase 3 Typed Hooks Design

## Summary

Phase 3 should turn Forge's current hook-overlay plumbing into a small typed hook runtime that is owned by the runtime itself, not by user configuration or an external plugin surface.

This phase is about structure and determinism, not extensibility for its own sake. Forge already stores and renders `HookOverlay` values, but the runtime has no first-class model for hook points, execution order, hook results, or failure handling. The result is that hook-like behavior is scattered across `internal/react` and `internal/runtime` as direct overlay mutations.

The recommended phase 3 shape is:

- introduce typed hook events and a bounded execution engine under `internal/hooks`
- keep registration internal/runtime-owned for now
- let hook handlers return one or more bounded results:
  - prompt overlay injection
  - informational runtime note
  - hard block
- adapt the existing runtime guidance and guardian/suggestion overlays to flow through the hook engine instead of direct session overlay mutation

## Why This Scope

The roadmap called for a typed hook runtime, but not for a general plugin platform. Adding user-configured registration in the same phase would expand the problem into configuration design, security boundaries, lifecycle ownership, and compatibility guarantees. That is avoidable right now.

An internal-only first pass gives Forge the important part:

- stable lifecycle points
- deterministic handler ordering
- bounded result types
- contained failures
- a single integration surface for future extension

That is enough to make the runtime more reliable and easier to grow without prematurely opening an external hook API.

## Existing State

Today:

- `internal/hooks` only defines `Overlay` and conversion into prompt overlays
- `internal/react/session.go` stores raw hook overlays on the session
- `internal/react/prompt.go` injects stored overlays into prompt composition
- `internal/react/loop.go` and `internal/runtime/chat.go` directly set and clear overlays for runtime conditions

This works for prompt nudges, but it is not yet a hook runtime because:

- there is no typed event model
- there is no registration or dispatcher
- there is no result contract beyond overlay text
- there is no failure containment policy
- there is no general way to represent a block or informational note without directly mutating unrelated session state

## Design Goals

1. Preserve existing user-visible behavior where possible.
2. Make hook execution deterministic and easy to reason about.
3. Keep hook results narrow and runtime-safe.
4. Avoid widening the security surface in this phase.
5. Create a reusable runtime-owned foundation for later extension.

## Non-Goals

- no user-configured hook registration in phase 3
- no remote hooks or plugin marketplace behavior
- no arbitrary shell execution from hook handlers
- no persistence format for hooks yet
- no attempt to replace the approval model with hooks

## Proposed Runtime Model

### Hook Points

Phase 3 should introduce a small, explicit set of hook points that map cleanly to current runtime flow:

- `session_start`
- `session_end`
- `permission_request`
- `before_tool`
- `after_tool`
- `pre_compact`
- `post_compact`
- `turn_complete`
- `prompt_context`

Not every point has to be wired to a full set of handlers immediately, but the type system should define them now so later work extends the same model instead of inventing more ad hoc paths.

`prompt_context` is especially important for the current overlay behavior: it gives the runtime a typed place to derive prompt guidance without directly mutating session overlay state from unrelated code.

### Hook Event

Each hook invocation should receive a typed event containing:

- hook point
- current session snapshot
- relevant action/tool metadata when applicable
- compact point-specific payload

This should stay value-oriented and read-only from the hook handler's perspective. Handlers should not mutate session state directly.

### Hook Result

Handlers should return a bounded list of results. The engine should support:

- `OverlayResult`
  - converted into prompt overlays
- `NoteResult`
  - converted into a runtime note or surfaced for UI/runtime use
- `BlockResult`
  - indicates the current action/step should not proceed

Each result should carry provenance and priority where relevant so existing prompt composition stays stable.

### Registration

Registration should be explicit and internal:

- runtime code constructs a registry
- runtime-owned handlers are registered in a deterministic order
- handlers are invoked in registration order for each hook point

This keeps ownership clear and avoids introducing config or user API questions in the same phase.

### Execution Rules

The hook engine should:

- run handlers in deterministic order
- recover safely from handler panics
- return structured execution output rather than mutating the session directly
- stop at the first hard block for a given point
- collect overlays and notes from non-blocking handlers

If a handler fails, the engine should degrade safely:

- do not crash the main loop
- surface a contained note/result if useful
- do not silently corrupt prompt state

## Integration Plan

### 1. Extend `internal/hooks`

`internal/hooks` should grow from overlay utilities into the typed runtime surface:

- hook point enums/types
- event types
- result types
- handler interface/function type
- registry and dispatcher
- conversion helpers from hook results into prompt overlays

This keeps hook semantics in one place instead of splitting type definitions between runtime packages.

### 2. Adapt Session Snapshot Consumption

The session can still store prompt-facing overlays for now, but those overlays should become the output of hook execution rather than the input interface that runtime code edits directly.

Short term:

- keep `HookOverlays` on `SessionSnapshot`
- populate them from hook execution output
- avoid broad session API churn in the same phase

This lets phase 3 land without rewriting all prompt composition behavior at once.

### 3. Replace Direct Overlay Mutation With Hook Execution

The main candidates are the current runtime-generated overlays:

- review guidance
- blocked-plan guidance
- synthesis guidance
- validation failure guidance
- search thrash guidance
- git workflow guidance
- repeat-loop guidance
- suggested skill overlay
- guardian warning overlay

These should move behind runtime-owned hook handlers registered at the relevant hook points instead of calling `SetHookOverlay` or `ClearHookOverlay` from many places.

### 4. Wire Prompt Composition To Hook Output

`internal/react/prompt.go` should continue consuming hook-produced overlays, but the source of truth becomes typed hook execution rather than manual overlay mutation.

The prompt builder should not need to know which specific runtime subsystem produced an overlay; it should only consume normalized results.

## Recommended File Shape

- Create `internal/hooks/runtime.go`
  Responsibility: registry, handler execution, deterministic dispatch.
- Create `internal/hooks/types.go`
  Responsibility: hook points, events, results, execution output.
- Create `internal/hooks/runtime_test.go`
  Responsibility: dispatcher ordering, block behavior, failure containment.
- Modify `internal/hooks/overlays.go`
  Responsibility: convert overlay results into promptcomposer overlays.
- Modify `internal/react/session.go`
  Responsibility: store normalized hook output with minimal API churn.
- Modify `internal/react/prompt.go`
  Responsibility: consume hook-produced overlays/notes cleanly.
- Modify `internal/react/loop.go`
  Responsibility: execute runtime-owned hooks instead of directly mutating session overlays.
- Modify `internal/runtime/chat.go`
  Responsibility: route skill suggestions and guardian feedback through typed hooks.

Tests should be added in the affected packages rather than only at integration level.

## Failure Handling

Hook failures should be contained. The engine should prefer:

- isolated recovery
- surfaced evidence in tests
- non-corrupt prompt output

If a hook panics or returns malformed output, Forge should continue operating unless the hook explicitly produced a valid block result before failing.

## Testing Strategy

Phase 3 should add:

- unit tests for hook registration and ordering
- unit tests for block short-circuiting
- unit tests for panic/error containment
- prompt tests proving hook overlay output still reaches the system prompt correctly
- runtime tests proving current guidance overlays are reproduced through hooks

## Acceptance Criteria

- Forge has a typed hook runtime under `internal/hooks`.
- Hook handlers are registered and executed internally in deterministic order.
- Hook output can inject overlays, emit notes, and block execution.
- Existing runtime guidance behavior is routed through the hook engine instead of ad hoc overlay mutation.
- Hook failures are contained and covered by tests.

## Recommendation

Implement phase 3 as an internal runtime foundation only. That gives Forge the reliability and architectural clarity it needs now, while keeping user-configured registration as a later decision layered on top of a safer core.
