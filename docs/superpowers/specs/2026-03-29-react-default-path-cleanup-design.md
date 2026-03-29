# React Default-Path Cleanup Design

## Summary

Remove the legacy role-based subagent machinery from Forge's default `react` runtime path so the app behaves more like Codex and OpenCode: one primary agent, model-driven tool use, lightweight delegation, and less prompt/runtime bloat.

The default path should stop remapping delegation requests into `builder` and `scout`, stop forcing spawned agents through legacy role prompts and structured output retries, and stop rebuilding avoidable prompt metadata on every turn. `kernel` remains as a fallback during migration, but `react` must no longer depend on the old subagent harness to complete a normal delegation flow.

## Problem

The March 28 north-star refactor changed the default runtime to `react`, but the current `react` path still carries key pieces of the old subagent architecture:

- `spawn_agent` in `react` mode still routes into `Agent.SpawnSubAgent`
- host code still remaps generic roles onto legacy worker roles
- the legacy subagent path still applies tool allowlists, role prompts, and structured output recovery loops
- prompt construction still injects broad tool and skill prose and performs avoidable project scanning work
- the current `react` runner is only a thin wrapper around `Agent.Run`, not an independently owned runtime path

This leaves Forge feeling tighter and heavier than Codex/OpenCode even though the entrypoint says `react`.

## Goals

- Make default `react` delegation independent from legacy role-based subagent execution.
- Keep `spawn_agent` and `wait_agent` available in `react` mode.
- Remove host-side role remapping from the default `react` path.
- Return plain text subagent results to the parent flow instead of legacy structured worker envelopes.
- Reduce prompt overhead caused by always-on skill catalogs, verbose tool prose, and per-turn project scanning.
- Preserve existing approval-policy and branch-safety behavior.
- Keep `kernel` fallback available during migration.

## Non-Goals

- No full rewrite of `Agent.Run` in this change.
- No deletion of the `kernel` runtime in this change.
- No broad redesign of skills themselves.
- No attempt to remove all legacy harness code immediately; only remove it from the default `react` path.

## Approved Direction

### 1. React-Native Delegation

`spawn_agent` in default `react` mode should create a normal spawned agent session instead of calling the legacy role/subagent runtime.

That spawned agent:

- uses the same base model family as the parent unless explicitly overridden
- gets the standard tool registry subject to approval policy, not a legacy per-role allowlist
- receives a lightweight spawned-agent system suffix instead of a legacy role prompt
- returns plain text back to the parent

The parent remains responsible for interpreting the result.

### 2. Role Strings Become Advisory Only

Role values passed to `spawn_agent` may remain part of the tool contract for compatibility, but they should not drive host-side remapping into `builder`, `scout`, `doctor`, or `architect`.

Allowed roles for the default path:

- `default`
- `worker`
- `explorer`

These should only influence a small prompt hint at most. They must not change tool access, output schema, or runtime execution model.

### 3. Plain-Text Delegation Results

The default `react` path should stop expecting structured worker contracts for spawned agents.

- `spawn_agent` may still return lifecycle metadata such as id and running state
- `wait_agent` should return the plain-text result plus minimal status metadata
- no structured-output retry loop
- no JSON validation against legacy worker schemas

This keeps delegation closer to Codex/OpenCode behavior while preserving async lifecycle control.

### 4. Prompt Slimming

The default path should reduce avoidable prompt weight.

Changes:

- stop serializing more prompt metadata than the model needs every turn
- remove or cache expensive per-turn project detection
- keep hidden-tool disclosure behavior, but avoid broad always-on prose where possible
- stop injecting the full visible skill activation catalog when it is not needed for the current turn

The first pass should focus on safe reductions with low behavioral risk.

### 5. Migration Boundary

The cleanup should draw a clear line:

- `react` default path uses react-native delegation and slimmer prompt assembly
- `kernel` fallback may continue using legacy harness machinery until a later cleanup

This avoids a risky big-bang rewrite while fixing the path users hit by default.

## Architecture

### Runtime Wiring

`internal/runtime/chat.go`

- stop wiring `react` delegation tools to `Agent.SpawnSubAgent`
- wire them to a new `react`-native spawned-agent launcher
- keep `kernel` wiring intact

### React Agent Pool

`internal/react/agent_pool.go`

- remove role remapping
- carry role as caller-provided metadata only
- add any depth/accounting hooks needed for safe spawned-agent lifecycle management in `react`

### Spawned Agent Execution

Add a small `react`-owned spawned-agent executor that:

- builds a child agent from the parent runtime context
- shares the same approval gate and base registry
- applies only a lightweight spawned-agent instruction suffix
- runs the delegated task and stores the plain-text result

This executor must not depend on `internal/agent/roles.go` or `internal/harness/contracts.go`.

### Prompt Assembly

`internal/agent/system.go` and related helpers

- separate stable prompt context from dynamic per-turn context
- trim or defer project metadata that requires walking the repo
- keep skill injection available, but stop advertising the entire loaded skill catalog by default in the main visible prompt

## Testing Strategy

Use TDD for all behavior changes.

Required coverage:

- `react` delegation no longer calls `SpawnSubAgent`
- no role remapping from `default` or `explorer` to legacy role names
- spawned agents in `react` mode return plain-text results without structured-output retries
- prompt building no longer performs unnecessary project scanning in the default path
- existing runtime registration and fallback behavior stays intact

## Risks

- Spawned agents may rely on some legacy guardrails implicitly today.
- Prompt slimming can shift model behavior if reduced too aggressively.
- The current `react` runner is still thin, so this cleanup improves the default path without fully realizing the original north-star runtime.

## Mitigations

- Keep changes scoped to the default `react` path.
- Preserve `kernel` fallback.
- Add focused tests around delegation wiring and prompt construction before changing implementation.
- Make prompt reductions incremental and measurable rather than wholesale.
