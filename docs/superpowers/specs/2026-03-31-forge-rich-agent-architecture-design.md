# Forge Rich Agent Architecture Design

## Summary

Forge should evolve from a good local coding agent into a full behavior platform.

Today, Forge has a strong runtime kernel, local-first tool model, and a relatively clean visible chat path. What it lacks is the layered behavior stack that makes richer systems feel consistently capable on hard tasks: explicit tool contracts, real plan mode, structured user questioning, persistent memory, robust approval reasoning, skill policy, and lightweight nudges that help the model choose the right workflow at the right time.

The goal is not to turn Forge into a clone of `cci` or Codex. The goal is to combine:

- Forge's local-first runtime and host-owned orchestration
- `cci`'s richer task UX, tool prompting, and coaching layers
- Codex's architectural discipline around prompt composition, approvals, memory, and configuration

The result should be one coherent system: a richer Forge that is easier to trust, easier to extend, and more consistent on real software work.

## Problem Statement

Forge currently has four structural gaps relative to the best ideas in `cci` and Codex:

1. Prompt architecture is too flat.
   - Forge relies on a concise core prompt plus a few runtime notes.
   - It does not yet have a composable prompt platform with clear ownership of static rules, task-mode overlays, tool contracts, memory hints, and approval context.
2. Behavioral workflows are under-specified.
   - Forge has `update_plan`, approvals, and hidden workers, but not a true plan mode, structured clarification path, or a disciplined task-state model comparable to `cci`.
3. Runtime support services are too thin.
   - There is no first-class memory pipeline, no approval guardian/reviewer, no prompt-overlay subsystem, and no layered skill-policy engine.
4. The activation model is implicit.
   - When Forge should plan, ask structured questions, delegate, verify more aggressively, or surface a skill is still driven mostly by generic prompt guidance instead of an explicit runtime policy layer.

## External Design Inputs

### Forge

Forge already has the right product center of gravity:

- one visible assistant
- host-owned tools and approvals
- hidden bounded workers
- local-first repository work
- runtime-owned task and transcript assembly

Those are strong foundations and should remain intact.

### `cci`

`cci` contributes the richest behavioral scaffolding:

- layered system prompt sections
- tool-specific prompt contracts
- explicit plan mode
- structured asking UX
- proactive todo/task management
- stronger subagent briefing rules
- session memory and background memory extraction
- hooks and prompt overlays
- UI nudges and suggestion surfaces

`cci` is the strongest source for "what richer collaboration feels like."

### Codex

Codex contributes the cleanest behavior architecture:

- a more complete base prompt contract for planning, execution, validation, and final responses
- a disciplined AGENTS.md scope model
- permission prompts that prefer scoped sandbox expansion before full escalation
- a guardian approval reviewer that evaluates risky actions against compact transcript evidence
- a two-phase memory pipeline with extraction and consolidation
- layered skill configuration rules rather than only activation heuristics

Codex is the strongest source for "how to structure the richness so it stays coherent."

## Design Principles

1. Keep one visible Forge assistant.
   - Richer behavior must not turn into visible multi-agent chaos.
2. Prefer host-owned state over prompt folklore.
   - If the system needs to know whether a plan is active, a preview is live, or a task is blocked, that state should exist in code.
3. Compose prompts, do not accrete them.
   - Every injected instruction should have an owner, scope, and budget.
4. Add ceremony only when it creates value.
   - The platform should be capable of heavy scaffolding without forcing every turn through it.
5. Treat risky decisions as reviewable artifacts.
   - Approval, memory, and delegation decisions should be explainable from compact runtime evidence.
6. Keep provenance visible.
   - Users and developers should be able to tell whether behavior came from the core prompt, a tool contract, a hook, a skill, or runtime policy.

## Target Architecture

### 1. Prompt Composition Platform

Forge should replace the single monolithic chat instruction model with a prompt composer that assembles:

- core system sections
- task-mode overlays
- tool-contract overlays
- plan/task state
- memory hints
- approval context
- skill state
- hook-provided reminders

Each section should be explicitly owned and independently testable. Prompt assembly should also support budgeting so the runtime can keep the highest-value context when token pressure rises.

### 2. Tool Contracts

Forge should define first-class behavioral contracts for high-leverage tools.

Initial tool-contract coverage:

- planning and task-state tools
- structured user questioning
- subagent and fork delegation
- shell/command execution
- file read/edit/write
- git and merge tools
- web/research tools
- approval and permission request tools

Each contract should define:

- when to use the tool
- when not to use it
- preferred sequencing
- anti-patterns
- examples
- interaction with related tools

This is the biggest gap between Forge and `cci`.

### 3. Explicit Modes

Forge should add explicit runtime modes instead of relying only on inferred user intent:

- `chat`
- `inspect`
- `plan`
- `implement`
- `validate`
- `review`
- `preview`

Modes should not be cosmetic. Each mode should affect:

- prompt overlays
- allowed tool behavior
- completion expectations
- default validation posture
- user-facing suggestions

### 4. Rich Task State

`update_plan` should become part of a broader task-state subsystem.

Recommended task state:

- objective
- operation/mode
- plan steps
- active step
- blocked reason
- required verification
- target branch or artifact when relevant
- approval requirements

Recommended step states:

- `pending`
- `in_progress`
- `blocked`
- `completed`

Only one step should be active at a time. The runtime should surface task state to both the model and UI.

### 5. Plan Mode

Forge should gain a real plan mode, distinct from lightweight planning.

Plan mode should:

- bias toward repo exploration before implementation
- allow structured clarification questions
- produce an explicit implementation plan for user approval
- pass plan-approved context into the implementation phase

Lightweight `update_plan` remains useful for ordinary multi-step work. Plan mode should be reserved for ambiguous, high-impact, or architectural tasks.

### 6. Structured User Questioning

Forge should add a structured ask-user path rather than forcing every clarification into plain prose.

This should support:

- multiple choice questions
- recommended options
- optional previews for UI/architecture comparisons
- plan-mode clarification without accidentally asking for invisible-plan approval

This is a direct lift from `cci`'s stronger question UX.

### 7. Delegation Model: Visible Coordinator, Typed Workers, Context-Sharing Forks

Forge already has hidden workers. It should formalize three delegation shapes:

- visible coordinator
- fresh specialist worker
- context-sharing fork

The coordinator needs explicit rules for:

- when to delegate
- when not to duplicate delegated work
- how to brief a worker
- how to safely summarize worker results
- how to avoid hallucinating in-flight outcomes

This is where Codex and `cci` converge: delegation works best when the visible agent owns synthesis and workers own bounded execution.

### 8. Approval System With Guardian Review

Forge should keep its existing approval rules and add a higher-trust review layer for risky or ambiguous actions.

The guardian reviewer should:

- receive a compact transcript
- receive the exact planned action
- treat transcript/tool output as evidence, not instructions
- optionally perform read-only verification checks
- return structured risk assessment

This is one of the most valuable Codex ideas because it moves risky approvals from shallow heuristics toward evidence-backed review.

### 9. Memory Pipeline

Forge should adopt a Codex-style two-phase memory system.

Phase 1: extraction

- extract structured memories from eligible completed sessions or turns
- redact secrets
- write normalized records

Phase 2: consolidation

- consolidate retained records into local memory artifacts
- keep bounded summaries
- update a model-readable memory summary

Memory should be:

- local-first
- bounded
- redactable
- disableable
- clearly separated from transient chat context

This is better than both "no memory" and "append everything forever."

### 10. Hooks And Prompt Overlays

Forge should support hooks and overlays as first-class runtime inputs.

Hooks should be able to:

- annotate startup context
- influence permissions
- emit reminders or warnings
- add prompt overlays
- observe tool outcomes

But hook provenance must remain visible and debuggable. Hidden prompt mutations are unacceptable.

### 11. Skills 2.0

Forge's current skill system is useful but shallow. It should be upgraded into:

- a real skill catalog
- required skills
- suggested skills
- auto-activated skills
- config-based skill enable/disable rules by name and path
- observable skill activation state

This combines `cci`'s richer usage model with Codex's config-rule discipline.

### 12. Nudges And UX Surfaces

Forge should add lightweight user-facing nudges:

- suggested skill
- suggested plan mode
- suggested verification
- suggested review
- structured choice overlays

These should be driven by runtime policy and task state, not by random model commentary.

## Activation Strategy

Forge should become "max capability, selectively activated."

That means:

- a stronger always-on baseline
- richer overlays activated automatically when task complexity, ambiguity, or risk warrants them
- explicit user-triggered entry points for plan mode and similar workflows

This avoids the two bad outcomes:

- a lean Forge that remains underpowered on difficult tasks
- a bloated Forge that feels heavy on trivial requests

## Recommended Implementation Order

1. Prompt composition platform
2. Tool contracts
3. Plan mode and structured questioning
4. Rich task state
5. Approval guardian
6. Memory pipeline
7. Skills 2.0 and policy engine
8. Hooks and overlays
9. UX nudges

This order reflects dependency reality: prompt and state architecture must exist before richer services can be added cleanly.

## Risks

1. Prompt sprawl
   - Mitigation: composable sections, budgets, explicit ownership.
2. Conflicting behavior layers
   - Mitigation: activation policy engine and strict precedence rules.
3. Opaque runtime behavior
   - Mitigation: provenance tracking and debug-visible overlay sources.
4. Memory leakage or poor retention
   - Mitigation: secret redaction, bounded summaries, disable/clear controls.
5. Approval overfitting
   - Mitigation: guardian only for risky/ambiguous actions, not every trivial command.

## Success Criteria

Forge should feel:

- faster and more direct on simple tasks than `cci`
- more reliable and guided on complex tasks than Forge today
- easier to trust on risky actions because approvals are evidence-backed
- easier to extend because prompt/state/skill systems are modular

The target is not "more features." The target is a Forge that behaves like a top-tier coding agent system on both simple and difficult work without feeling internally confused.
