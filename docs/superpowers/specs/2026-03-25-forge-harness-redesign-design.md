# Forge Harness Redesign Design

## Summary

Replace Forge's current dispatch-centered multi-agent harness with a production-oriented, primary-agent-first kernel.

The new system centers the product around one coherent assistant, `forge`, that acts directly by default, uses skills and repo instructions as first-class steering mechanisms, and invokes bounded internal workers only when delegation materially improves execution. Internal workers are runtime-controlled, schema-bound, non-addressable by the user, and hidden behind a compact Codex-like transcript. The default UI shows a single conversation stream plus compact execution rows. Advanced trace detail appears only when Forge runs with `-d`.

## Problem

The current harness has improved compared with the original free-form loop, but the control plane is still too prompt-shaped.

Observed design failures:

- orchestration authority is split between runtime code and model-authored prose
- dispatch can invent labels such as `SCOPE: repo-review`, and runtime treats those labels as authoritative
- free-form `next_role` and scope inference can escalate simple requests into broader review or recommendation flows
- specialist visibility leaks into the product model, making the app feel like a committee rather than one coherent assistant
- prompt and heuristic layers overlap, making the harness hard to reason about and expensive to stabilize
- UI still exposes too much machinery and too little intentional conversational structure

The result is a harness that behaves like "guarded prompt orchestration" rather than a production-ready agent runtime.

## Goals

- Make `forge` the only user-facing assistant identity.
- Make direct local execution the default behavior.
- Make skills and instructions the primary specialization mechanism.
- Restrict internal worker usage to bounded, high-value cases.
- Move all orchestration authority into deterministic runtime policy.
- Remove prompt-authored scope and handoff metadata as control-plane truth.
- Preserve strong tool autonomy: inspect first, act first, ask only when truly blocked.
- Replace the current default UI with a Codex-like transcript-first interface.
- Keep advanced trace and orchestration visibility available under `-d`.
- Build regression coverage from real debug logs and paraphrase suites.

## Non-Goals

- No compatibility requirement with the current dispatch/scout/architect flow.
- No requirement to preserve current prompt contracts, task-profile sections, or visible agent semantics.
- No attempt to keep the current panel-heavy UI.
- No product model where users can address internal workers by name.
- No architect-like synthesis worker as a default stage after broad inspection.

## Approved Product Direction

### 1. Primary-Agent First

Forge behaves as one strong general-purpose assistant.

- The user talks to `forge`.
- `forge` owns the task from intake to completion.
- `forge` answers directly when possible.
- `forge` uses tools autonomously when inspection or action is needed.
- `forge` only delegates internally when there is a clear execution benefit.

The product must feel like one intelligence, not a visible relay between named specialist speakers.

### 2. Skills First, Workers Second

Skills such as Superpowers are the primary mechanism for reusable behavior, workflow discipline, and specialization.

- Skills define how Forge should approach classes of work.
- Skills can modify behavior before direct action.
- Workers are not the default problem-solving mechanism.
- Internal workers are runtime tools for bounded execution topology, not personalities.

### 3. Runtime-Owned Orchestration

The runtime decides classification, transitions, delegation, and stop conditions in code.

- Prompt-authored labels such as `SCOPE`, `TARGET`, `TOPIC`, `EVIDENCE_MIN_READS`, `next_role`, and `next_task` are not control-plane truth.
- Workers may suggest follow-up work, but runtime policy decides whether that suggestion is accepted.
- No worker may widen task scope, redefine task class, or choose the next worker.

### 4. Hidden Internal Workers

Internal workers remain available, but they are not user-addressable and do not speak inline in the default transcript.

Approved worker classes:

- `reader`
- `editor`
- `verifier`
- `researcher`

They use the same underlying model capability as Forge, but each runs under a tighter execution contract, permission set, output schema, and stop condition.

### 5. Transcript First

The default UI should resemble Codex-style interaction:

- one coherent conversation stream
- compact execution rows while work is happening
- one final `forge` answer per turn
- code blocks, diffs, and verification output styled intentionally
- no visible worker conversations in the main transcript

Advanced trace and orchestration detail only appear when Forge runs with `-d`.

## Architecture

### Core Components

The new kernel should be structured around explicit runtime components instead of role prompts acting as a control plane.

Proposed components:

- `session`: owns chat/session state, follow-up detection, active instructions, and turn lifecycle
- `classifier`: maps a user turn into a typed request family and task shape
- `skill_router`: determines whether skills apply before action
- `planner`: selects the next smallest useful action
- `executor`: performs local tool calls or direct assistant actions
- `worker_manager`: launches and supervises bounded internal workers
- `observer`: parses tool results and worker outputs into typed observations
- `policy`: decides all transitions and stop conditions
- `renderer`: renders transcript-first UI and optional debug trace
- `trace_store`: records decisions, retries, transitions, and verification evidence for `-d`

These components should be testable independently.

### Request Families

The top-level runtime classification should use broad capability families rather than brittle task-specific names.

Recommended families:

- `answer`
- `inspect`
- `implement`
- `debug`
- `verify`
- `research`
- `transform`
- `mixed`

The key property is that these are orchestration categories, not user-facing labels. Narrower task shapes may exist underneath, but they should be runtime-owned subtypes derived from these families, not prompt-authored strings.

### Runtime State Machine

The runtime should follow a small deterministic state machine:

- `Intake`
- `Classify`
- `PlanStep`
- `Act`
- `Observe`
- `Decide`
- `Respond`
- `Complete`
- `Blocked`

Flow:

1. `Intake`
   Normalize the user turn, load carry-forward state, and detect whether the turn is a new task, a follow-up, or a continuation.

2. `Classify`
   Determine request family and execution posture in code.

3. `PlanStep`
   Choose the next smallest useful action. A step may be:
   - local response
   - local tool call
   - skill activation
   - worker launch
   - user clarification if truly blocked

4. `Act`
   Execute the chosen step.

5. `Observe`
   Parse outputs into typed observations such as:
   - evidence gathered
   - file edited
   - tests passed
   - tests failed
   - ambiguity remains
   - blocked by missing information
   - worker launched
   - worker completed

6. `Decide`
   Determine the next runtime transition from policy, not prose.

7. `Respond`
   Emit either a compact progress update or the final user-facing `forge` answer.

This state machine is the orchestration authority. Workers are only one action available within `PlanStep`.

## Local-First Execution Policy

Forge stays local unless delegation buys something concrete.

Stay local when:

- the task is small or sequential
- the next step depends on immediate context
- the work is mostly one reasoning chain
- one or a few tool calls are enough
- delegation would mainly add latency or transcript noise

Spawn a worker only when one of these is true:

- parallel independent work can proceed safely
- verification should be independent of the implementation path
- long-running work should happen in the background
- a bounded implementation or inspection slice can be isolated safely
- external documentation or web research should run separately

Hard rules:

- a worker cannot spawn another worker
- a worker cannot change the task family
- a worker cannot widen scope
- a worker returns structured output only
- worker suggestions are advisory only

Most turns should not spawn a worker.

## Skills Integration

Skills are first-class runtime inputs.

The runtime should:

- discover active instructions and skills before planning action
- allow skills to affect approach, guardrails, and workflow discipline
- preserve the "use skill if applicable" rule without turning skills into personalities
- keep skill effects separate from worker effects

Operationally:

- skills answer "how should Forge approach this task?"
- workers answer "should part of this execution happen in isolation or in parallel?"

This separation keeps the product simple and the runtime intelligible.

## Typed Contracts

### Forge Contract

Forge is the only participant allowed to produce user-facing prose.

Inputs:

- user turn
- session state
- active instructions
- active skills
- prior observations

Allowed outputs:

- `respond`
- `tool_call`
- `worker_task`
- `clarify`
- `complete`
- `blocked`

### Reader Contract

Purpose:

- bounded inspection and evidence gathering

Inputs:

- exact question
- allowed tools
- allowed paths or domains
- stop condition
- evidence budget

Outputs:

- `status`
- `evidence[]`
- `coverage`
- `gaps[]`
- `suggested_next`

Restrictions:

- no recommendations
- no planning
- no user-facing prose

### Editor Contract

Purpose:

- implement a bounded change

Inputs:

- exact objective
- allowed write scope
- relevant context
- required verification

Outputs:

- `status`
- `changes[]`
- `verification_attempts[]`
- `remaining_issues[]`
- `suggested_next`

Restrictions:

- no widening scope
- no opportunistic refactors outside the assigned slice

### Verifier Contract

Purpose:

- independent validation of a claim, change, or fix

Inputs:

- claim to verify
- files or commands to check
- pass/fail criteria

Outputs:

- `status`
- `checks[]`
- `failures[]`
- `confidence`

Restrictions:

- no implementation changes

### Researcher Contract

Purpose:

- docs, web, and reference lookup under runtime policy

Inputs:

- research question
- source policy
- allowed domains if any
- citation requirement

Outputs:

- `status`
- `findings[]`
- `sources[]`
- `confidence`

Restrictions:

- no local code changes
- no planning or orchestration

### Contract Enforcement

Runtime should validate all worker outputs in code.

- unknown fields are rejected
- missing required fields are rejected
- invalid enum values are rejected
- invalid worker results fail closed
- invalid worker results trigger retry, fallback, or local recovery based on policy

## Runtime Policy Table

The runtime decides next steps from request family and observation state.

Recommended high-level policy:

- `answer`
  - local answer by default
  - optional local tool use
  - worker only if external research is required

- `inspect`
  - local tools first
  - `reader` only for large or clearly bounded background inspection

- `implement`
  - local edit path by default
  - `editor` only for a sharply isolated slice or useful background-safe implementation task
  - follow with local verification or `verifier` depending on independence needs

- `debug`
  - local investigation first
  - optional `reader` for parallel evidence collection
  - `editor` only after root cause is established
  - `verifier` for independent regression confirmation when appropriate

- `verify`
  - local or `verifier`, depending on independence and cost

- `research`
  - local if trivial and already in context
  - `researcher` when online or reference retrieval is genuinely needed

Critical rule:

Overview and inspection requests end with `forge` unless the user explicitly asked for evaluation, recommendations, risks, or next actions. There is no default "inspection then synthesis worker" pattern.

## UI and Transcript Design

### Default UI

The default interface should be a transcript-first chat UI.

Requirements:

- one coherent `forge` conversation stream
- compact execution rows while work is in progress
- syntax-highlighted code blocks
- styled diff/code boxes
- clear add/remove styling
- clear status indicators for running, success, failure, and info
- no side tools panel by default
- no inline worker conversation bubbles

Examples of compact execution rows:

- `inspecting repository structure`
- `reading 3 files`
- `editing 2 files`
- `running targeted tests`
- `verifying fix independently`
- `checking documentation`

Worker identities may be known internally, but the main transcript should not expose them as independent speakers.

### Debug Mode

Advanced trace only appears when Forge runs with `-d`.

The debug view may include:

- worker type
- typed task envelope summary
- retries
- timing
- verification commands
- observation summaries
- state transitions

Debug trace is for inspection and trust, not the primary interaction model.

### Phase 6 UI Replacement

The current UI should not be carried forward.

Approved direction:

- remove the current default panel-heavy layout
- use a Codex-like transcript and code presentation model
- intentionally style code, diffs, verification output, and status indicators
- research high-quality TUI chat/code themes and adopt one coherent visual direction
- use success/failure/info states clearly, including red/green semantics where appropriate

This phase is a real redesign, not cosmetic cleanup.

## Migration Strategy

### Phase 1: Introduce the New Kernel Alongside the Old One

Build a new runtime path with:

- primary-agent-first control loop
- typed classification
- typed worker contracts
- policy-driven transitions
- transcript-first rendering hooks

Do not reuse the current dispatch/scout/architect loop as the core.

### Phase 2: Move the Common Path to the New Kernel

Migrate the default happy path first:

- direct answers
- local inspection
- local implementation
- local verification

This should cover the majority of normal usage before workers are reintroduced.

### Phase 3: Reintroduce Bounded Workers

Add:

- `reader`
- `editor`
- `verifier`
- `researcher`

Each behind runtime policy only, with typed contracts and deterministic validation.

### Phase 4: Remove Old Orchestration Authority

Delete or quarantine:

- dispatch-centered control flow
- prompt-authored task-profile sections as runtime truth
- free-form `next_role` chaining
- architect-as-default synthesis stage

Any compatibility adapter that remains must not be orchestration-authoritative.

### Phase 5: Lock It Down with Evals and Real-Log Regression Suites

Turn real failures into regression assets:

- over-escalation from simple prompts
- missing evidence loops
- malformed worker output
- mixed prose/tool output
- empty worker output
- long-running background verification
- follow-up interpretation routing

Add paraphrase suites so semantically equivalent prompts behave the same way.

### Phase 6: Replace the UI

Replace the current default interface with the approved transcript-first design and retain advanced trace only under `-d`.

This phase should ship only after the new kernel is already driving the conversation flow, so UI is layered on the correct runtime semantics.

## Testing and Production Readiness

The redesign is not complete until the following bar is met.

### Control-Plane Correctness

- runtime is the sole orchestration authority
- prompt-authored orchestration metadata is ignored or treated as non-authoritative
- worker outputs are schema-validated
- invalid worker results fail closed

### Real-World Regression Coverage

- every resolved failure from debug logs becomes a test or eval
- paraphrase suites exist for semantically equivalent prompts
- negative tests exist for unwanted escalation and unwanted synthesis

### Verification Discipline

- implementation paths end with explicit verification
- independent verification is available
- no success claims are surfaced without evidence

### Observability

- every important transition is visible in `-d`
- trace shows why a worker was or was not used
- classification, retries, and verification outcomes are inspectable

### Responsiveness

- local-first paths remain fast
- workers improve throughput instead of adding constant ceremony
- background work does not block transcript responsiveness

## Risks

- the clean-break effort touches the product's core loop and will require broad test updates
- classification policy that is too coarse will still produce avoidable escalations
- retaining any old orchestration authority for convenience could undermine the redesign
- UI replacement can become a distraction if done before control-plane stability

## Success Criteria

The redesign is successful when all of the following are true:

- users experience Forge as one coherent assistant
- simple asks stay simple
- broad asks no longer automatically escalate into review/recommendation flows
- skills affect behavior without turning into visible pseudo-agents
- workers are used sparingly and only when they clearly help
- the default transcript feels deliberate and production-ready
- debug mode can explain runtime decisions after the fact
- real-log regressions stay fixed under paraphrased prompts

## Immediate Next Step

Write the implementation plan for the clean-break kernel, then execute it as a staged replacement rather than patching the existing dispatch-centered architecture further.
