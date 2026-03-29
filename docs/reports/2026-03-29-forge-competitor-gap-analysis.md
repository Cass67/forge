# Forge: What Needs to Change to Become a Codex/OpenCode Competitor

**Date:** 2026-03-29  
**Scope:** Repo inspection grounded in current Forge architecture/docs and existing internal design notes.  
**Primary references:** `README.md`, `ARCHITECTURE.md`, `KERNEL_ISSUES.md`, `docs/plans/2026-03-28-forge-north-star-codex-opencode-v2.md`, and related reports already in this repository.

---

## Executive Summary

Forge is already a credible terminal-first local coding agent, but it is **not yet a Codex/OpenCode competitor in interaction quality, execution architecture, or reliability**.

Today, Forge appears to have:

- strong local-repo orientation
- multi-provider/model routing
- useful tool access
- a decent TUI foundation
- thoughtful safety ideas like protected-branch behavior and claim/evidence checks

But the biggest competitive gap is architectural:

> Forge still routes too much work through host-side classification, planning, worker specialization, and validation, while Codex/OpenCode feel fluid because they center everything on a single model-led tool loop with host-enforced safety boundaries.

That creates downstream problems:

- too much orchestration before the model acts
- worker failure modes caused by formatting/contracts instead of task reality
- split state between kernel and agent
- brittle multi-turn behavior
- reduced tool autonomy compared with Codex/OpenCode
- slower, less natural interaction flow

If the goal is to compete seriously, the core change is:

> **Move Forge from host-directed staged orchestration to a unified ReAct-style agent loop where the model chooses actions and the host enforces permissions, approvals, and evidence requirements.**

---

## Bottom Line

Forge needs changes in five areas:

1. **Core runtime architecture**
   - replace classifier/planner/state-machine-heavy flow with a simple ReAct loop
2. **Safety model**
   - shift from tool allowlists to approval/sandbox/policy enforcement
3. **Delegation model**
   - replace host-routed hidden workers with model-invoked sub-agents
4. **Session/state handling**
   - unify state and remove synthetic response hacks
5. **Product polish**
   - improve startup speed, progress streaming, trust, previews, and turn completion quality

Without those changes, Forge may remain useful, but it will still feel more brittle and “framework-y” than Codex/OpenCode.

---

## Where Forge Is Already Strong

These are real assets worth preserving:

### 1. Local-first repo workflow
Forge is clearly designed around operating in the current repository, not around a hosted remote sandbox. That is a good base for a serious coding agent.

### 2. Multi-provider routing
The bootstrap/runtime layer already supports multiple providers and model-routing paths. That is a strong differentiation layer if the execution model becomes simpler and more reliable.

### 3. Existing tool surface
Forge already has file, search, git, command, artifact, preview, and agent-related tools. The raw capability surface is not the main issue.

### 4. Safety ideas that are actually valuable
A few current Forge concepts are stronger than many competitors if implemented cleanly:

- claim/evidence guard
- protected-branch behavior
- traceability and runtime recording
- explicit host-owned approvals

These should survive the redesign.

### 5. TUI and local UX foundation
The chat runtime, Bubble Tea integration, provider/model pickers, and session flow suggest Forge can support a polished local experience.

---

## Main Reasons Forge Is Not Yet a Codex/OpenCode Competitor

## 1. The runtime is too host-directed

Current architecture still depends heavily on:

- classification
- planning
- worker routing
- step selection
- contract validation
- stage transitions

That means Forge often decides *how* work should happen before the model sees enough context.

### Why that hurts competitiveness
Codex/OpenCode feel good because the loop is conceptually simple:

1. user asks for something
2. model decides what tool to use
3. host enforces rules
4. tool results return to model
5. model continues until done

Forge inserts several extra points of failure before or around that loop.

### What needs to change
Forge needs a **single default execution loop** for chat that looks more like:

- build prompt
- stream model output
- parse tool calls
- execute tools under policy
- feed results back
- repeat until the model stops

Not:

- classify request
- choose family
- plan step
- pick worker kind
- restrict worker tools
- validate structured worker contract
- normalize outcome
- synthesize back into visible transcript

This is the most important change in the repo.

---

## 2. Hidden workers are too rigid and too fragile

The repo’s own `KERNEL_ISSUES.md` already describes several major failures here.

### Problems visible from the notes

#### Worker outputs are over-validated
Workers must emit strict JSON with exact fields. That causes failure even when the work itself was correct.

Result:

- false negatives
- wasted retries
- confusing “worker failed” outcomes
- reduced trust

#### Worker tool access is mismatched
The hardcoded worker allowlists do not match actual task needs.

Examples from the repo notes:

- editor workers lack preview/artifact/git functionality they may need
- researcher workers miss useful repo-history context in some cases
- verifier flow is underpowered and error context is weak

#### Worker model routing is conceptually confused
Kernel workers are mapped through legacy agent-role config in ways that blur two different architectures.

Result:

- confusing config behavior
- hard-to-explain model choices
- harder debugging

### Why this hurts competitiveness
Codex/OpenCode delegation feels lighter because sub-agents are just agents doing bounded work, not a separate contract-heavy worker protocol.

### What needs to change
Replace most host-directed worker dispatch with:

- a `spawn_agent` / `wait_agent` style tool
- plain-text sub-agent outputs
- shared policy/safety boundaries
- no strict JSON return contract for delegation

Keep bounded delegation, but make it model-invoked and tool-mediated.

---

## 3. Forge has split state and transcript continuity problems

The notes show a real architectural issue: state is split between the visible agent and the kernel/session layer, then patched with synthetic response injection.

### Why this matters
This is exactly the kind of thing that makes a coding agent feel flaky in multi-turn work:

- the user asks a follow-up
- the system loses context or behaves inconsistently
- previous work is only partially represented in one of the state stores
- transcript continuity becomes untrustworthy

### What needs to change
Forge needs **one session state model** for the default runtime:

- one conversation history
- one tool-result history
- one trace chain
- sub-agent results represented as actual tool outputs, not fake assistant messages
- one compaction path when context gets large

This is required for reliable multi-turn editing and preview/apply workflows.

---

## 4. Safety is implemented in the wrong layer

Right now, too much safety seems to come from limiting who can do what tool-wise rather than allowing the model to act and constraining execution.

### Why Codex/OpenCode feel better
They generally follow this pattern:

- model can decide actions broadly
- host enforces sandbox/policy/approval boundaries
- dangerous actions are prompted, denied, or re-run after approval

That produces a more capable feeling system without giving up control.

### What needs to change
Forge should move toward a safety stack built around:

- approval policies
- configurable allow/prompt/deny rules
- protected-branch enforcement
- least-privilege execution
- eventually stronger OS-level sandboxing where available
- claim/evidence verification after actions

So the question becomes “is this action allowed?” rather than “does this worker role even have this tool?”

---

## 5. The chat product likely still feels less fluid than competitors

Even without reading every UI file, the architecture and existing reports strongly suggest the user experience gap is mostly about fluidity, not just feature count.

### Competitive expectations today
A serious Codex/OpenCode competitor needs to feel like:

- fast first response
- visible progress while thinking/working
- clear tool activity
- strong multi-turn memory
- easy branch/edit/test loop
- predictable approvals
- graceful recovery when something fails

### What needs to change
Forge should optimize for:

- time-to-first-progress under ~2 seconds
- immediate visible host progress events
- concise summaries of what it is doing
- better interruption/cancel behavior
- fewer dead-end retries
- clearer explanation when blocked by policy, test failure, or tool error

This is not just a UI problem; it depends on simplifying the runtime loop.

---

## 6. The repo still carries multiple architectural eras at once

From the docs alone, Forge currently contains:

- chat runtime
- harness kernel
- hidden workers
- legacy multi-agent compatibility paths
- writer/auditor/summarizer pipeline
- old and new UI/runtime layers

### Why this matters
That increases:

- maintenance cost
- cognitive load
- accidental coupling
- config confusion
- difficulty shipping one excellent default path

### What needs to change
Forge needs a clearer product line:

### Keep as primary
- `forge` = the best interactive coding agent path

### Keep as secondary/legacy
- `forge make` = batch/improvement pipeline, if still valuable

### Demote or remove from critical path
- compatibility-only role systems
- alternate orchestration paths that are no longer strategic
- duplicated state handling

A competitor wins with one excellent default experience, not several overlapping architectures.

---

## Recommended Strategic Direction

The repo already contains the right high-level direction in `docs/plans/2026-03-28-forge-north-star-codex-opencode-v2.md`.

That direction is correct.

### The right target
Forge should become:

- one visible primary coding agent
- one unified ReAct loop
- tool-first execution
- model-led delegation when useful
- host-enforced approval/sandbox/evidence boundaries
- unified state across turns
- explicit traces and trustworthy summaries

### In plain terms
Forge should feel less like:

- a workflow engine wrapped around an LLM

and more like:

- an LLM-native coding runtime with strong local safety controls

---

## Concrete Changes Needed

## A. Replace the default kernel path with a ReAct runtime

### Needed change
Introduce a new default chat runtime centered on:

- stream model output
- detect tool calls
- execute tools
- append tool results
- continue until final answer

### Why
This removes the biggest source of routing brittleness.

### Keep
- existing tools
- trace capture
- claim/evidence checks
- branch protections

### Stop doing in the default path
- pre-LLM classification
- step-family planning
- worker contract validation
- synthetic response insertion

---

## B. Convert hidden workers into real sub-agents

### Needed change
Use a model-callable delegation tool such as:

- `spawn_agent(task_description, role)`
- `wait_agent(id)`

### Why
This preserves delegation benefits while removing most worker-specific complexity.

### Design requirements
- plain text result contract
- bounded depth
- optional parallelism
- same approval scope unless explicitly narrowed
- results returned as tool outputs

---

## C. Build a real approval/policy system

### Needed change
Create configurable policy objects and rules for:

- read-only vs workspace-write vs dangerous access
- command allow/prompt/deny rules
- approval escalation on sandbox failure
- protected branch checks

### Why
This matches how strong coding agents balance capability and safety.

### Important nuance
Do this before broadening autonomy too much.

---

## D. Unify session state and compaction

### Needed change
Use one history for:

- user messages
- assistant/tool outputs
- delegation results
- summaries/compaction

### Why
This is necessary for stable multi-turn coding.

### Also needed
- context compaction when budgets are exceeded
- recovery from disconnects/retries without losing turn coherence

---

## E. Improve observability and failure quality

### Needed change
Make failures explicit and actionable.

Examples:

- “command denied by approval policy”
- “tool returned non-zero exit code”
- “context window exceeded; compacted history and retried”
- “sub-agent failed after 2 retries due to network timeout”

### Why
Users trust coding agents that fail transparently.

---

## F. Raise the bar on product fluidity

### Needed change
Polish the interactive loop around the runtime changes:

- instant host progress updates
- clean streaming and tool-status rendering
- easy preview/apply/undo mental model
- reliable cancel/interruption
- clearer success summaries tied to actual evidence

### Why
Competing with Codex/OpenCode is at least as much about feel as about architecture.

---

## Priority Order

If this repo wants the shortest path to competitiveness, the order should be:

1. **New ReAct chat runtime behind a flag**
2. **Unified state model**
3. **Approval/policy system**
4. **Delegation tools replacing worker contracts**
5. **Default switch to new runtime**
6. **Cleanup of legacy kernel-only complexity**
7. **UI/product polish on top**

That order reduces risk while moving quickly toward the real target.

---

## What Not to Do

A few paths would probably waste time:

### 1. Do not keep patching the classifier forever
If the architecture keeps relying on phrase routing and host-side intent detection, it will keep accumulating edge cases.

### 2. Do not double down on stricter worker contracts
That likely increases reliability on paper while decreasing practical task success.

### 3. Do not solve this only in the UI
A nicer TUI cannot compensate for a brittle orchestration core.

### 4. Do not broaden tool autonomy without policy controls
The right change is freedom through governed execution, not unrestricted tool access.

---

## Recommended Positioning After the Rewrite

If Forge makes these changes successfully, its differentiation could be:

- local-first coding agent
- stronger trust and traceability than many competitors
- provider-flexible runtime instead of single-vendor lock-in
- branch-aware and evidence-aware execution
- fluid Codex/OpenCode-style interaction without giving up host control

That is a credible market position.

---

## Action Plan

## Phase 0: Product Decision and Success Criteria

### Goal
Commit the repo to one primary interactive architecture.

### Actions
- Declare the default north star: **one visible agent, one ReAct loop, host-enforced safety**.
- Treat current kernel/classifier/worker orchestration as transitional, not the long-term center.
- Define measurable success metrics for the replacement runtime.

### Exit criteria
- Written architecture decision merged into docs.
- Team agrees `forge` is the flagship path.
- Success metrics tracked, including:
  - time to first progress
  - task completion rate
  - tool-call success rate
  - multi-turn continuity success
  - retry rate per task

---

## Phase 1: Introduce a New ReAct Runtime Behind a Flag

### Goal
Prove Forge can handle common coding tasks without classifier/planner/worker orchestration.

### Actions
- Add a new runtime package, e.g. `internal/react/`.
- Implement the core loop:
  - build prompt
  - stream model output
  - parse tool calls
  - execute tools
  - append tool results
  - continue until no more tool calls
- Route chat via `FORGE_CHAT_RUNTIME=react` or equivalent runtime toggle.
- Reuse existing tool implementations where possible.
- Preserve current trace/event rendering so behavior is observable.

### Deliverables
- `internal/react/loop.go`
- `internal/react/session.go`
- `internal/react/prompt.go`
- runtime routing in `internal/runtime/chat.go`

### Exit criteria
- React mode can:
  - read files
  - search repo
  - edit a file
  - run a command
  - explain results to the user
- Streaming works end-to-end.
- No classifier or worker contract is required for these tasks.

---

## Phase 2: Unify Session State

### Goal
Remove agent/kernel split-brain behavior.

### Actions
- Define a single canonical conversation/session structure for the new runtime.
- Ensure user messages, assistant messages, tool calls, tool results, and delegation results all live in one history model.
- Remove synthetic assistant-response injection from the new path.
- Add explicit turn records so retries and compaction are inspectable.

### Deliverables
- unified session/history model
- removal of synthetic response behavior in the new runtime
- tests covering multi-turn continuity

### Exit criteria
- Follow-up prompts retain context reliably.
- Tool-result context is preserved across turns.
- No fake assistant messages are needed to keep transcript continuity.

---

## Phase 3: Replace Tool Allowlists with Approval/Policy Controls

### Goal
Make the model broadly capable while preserving user control.

### Actions
- Design approval primitives:
  - `Allow`
  - `Prompt`
  - `Deny`
- Add sandbox/access modes:
  - read-only
  - workspace-write
  - dangerous/full-access
- Support command and tool rules from config.
- Preserve protected-branch behavior as policy enforcement, not worker-role behavior.
- Route risky tool execution through approval checks.

### Deliverables
- `internal/react/approval.go`
- `internal/react/sandbox.go`
- config support for approval rules
- enforcement hooks around tool execution

### Exit criteria
- Dangerous actions are consistently prompted or denied.
- Safe actions can proceed without friction.
- Current safety guarantees are preserved or improved.

---

## Phase 4: Turn Hidden Workers into Model-Invoked Sub-Agents

### Goal
Keep delegation, but simplify it radically.

### Actions
- Implement `spawn_agent(task_description, role)` tool.
- Implement `wait_agent(id)` for async/parallel work.
- Let sub-agents run with the same core loop and policy system.
- Return sub-agent results as plain text or structured tool output, not strict worker JSON contracts.
- Enforce bounded depth and optional concurrency limits.

### Deliverables
- `internal/react/tools/spawn_agent.go`
- `internal/react/tools/wait_agent.go`
- agent registry/pool

### Exit criteria
- LLM can delegate bounded tasks.
- Parallel delegation works for independent subtasks.
- Worker JSON validation path is no longer required in the new runtime.

---

## Phase 5: Add Compaction and Robust Error Recovery

### Goal
Make long sessions resilient instead of brittle.

### Actions
- Add history compaction/summarization when nearing context limits.
- Distinguish retryable vs non-retryable errors.
- Retry transport/stream failures with backoff.
- Stop retrying on deterministic failures.
- Add a circuit breaker for repeated identical tool/runtime failures.

### Deliverables
- compaction logic
- retry policy logic
- user-visible failure reporting improvements

### Exit criteria
- Long sessions continue without losing essential context.
- Repeated failures do not spiral indefinitely.
- Errors are user-comprehensible and traceable.

---

## Phase 6: Make the New Runtime the Default

### Goal
Ship the new architecture as the flagship Forge experience.

### Actions
- Make the ReAct runtime the default for `forge`.
- Keep the old kernel path behind an explicit fallback switch for a transition period.
- Run side-by-side regression testing during the soak period.
- Update docs and onboarding around the new architecture.

### Deliverables
- default runtime switch
- migration docs
- regression matrix comparing old vs new runtime behavior

### Exit criteria
- New runtime is default.
- Regression rate is acceptable.
- Common coding tasks succeed more reliably than under the old path.

---

## Phase 7: Remove or Isolate Legacy Complexity

### Goal
Reduce maintenance drag and config confusion.

### Actions
- Remove or quarantine classifier-only logic from the primary path.
- Remove worker-contract-specific code not needed after delegation tools land.
- Untangle legacy agent-role config from modern runtime config.
- Document `forge make` as a separate pipeline product if it remains.

### Deliverables
- architecture cleanup PRs
- simplified config surface
- reduced coupling between legacy and flagship runtimes

### Exit criteria
- One obvious primary architecture remains.
- Config maps cleanly to actual runtime behavior.
- New contributors can understand the main path quickly.

---

## Phase 8: Compete on Product Quality, Not Just Architecture

### Goal
Turn the architecture win into a superior user experience.

### Actions
- Improve progress rendering and activity summaries.
- Tighten approval UX.
- Improve previews and artifact flows.
- Add clearer “done” summaries linked to evidence.
- Add task-focused evals using real repo workflows.
- Benchmark Forge against Codex/OpenCode on realistic local coding tasks.

### Suggested benchmark tasks
- explain an unfamiliar codepath
- fix a failing unit test
- implement a small feature across multiple files
- run and interpret tests
- perform a preview/apply change safely on a protected branch
- spawn sub-agents for parallel investigation

### Exit criteria
- Forge feels fluid in daily use.
- Benchmarks show comparable task success and lower friction.
- The repo can honestly position Forge as a serious competitor.

---

## Near-Term 30-Day Plan

If the goal is immediate forward motion, the next month should focus on this sequence:

### Week 1
- Finalize architecture decision doc.
- Create `internal/react/` skeleton.
- Wire runtime flag in chat.
- Build minimal stream → tool → continue loop.

### Week 2
- Add unified session history.
- Smoke test read/edit/search/command tasks.
- Add basic trace hooks and failure surfacing.

### Week 3
- Implement approval rules and risky-action prompting.
- Preserve protected-branch behavior.
- Add initial compaction scaffolding.

### Week 4
- Implement `spawn_agent`/`wait_agent`.
- Run side-by-side evals on representative coding tasks.
- Decide default-switch timing based on reliability data.

---

## Final Assessment

Forge does **not** need to become a copy of Codex or OpenCode.

But it **does** need to adopt the architectural principle that makes them work:

> **one model-led tool loop, with safety enforced by the host rather than by brittle orchestration layers**

If Forge keeps its best ideas:

- local-first workflow
- provider flexibility
- claim/evidence validation
- protected-branch safety
- traceability

while replacing its most brittle parts:

- classifier-heavy routing
- worker allowlists
- strict worker JSON contracts
- split state
- synthetic transcript patching

then it has a credible path to becoming a real competitor instead of just a promising internal framework.
