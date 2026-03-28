# Forge Harness Production Redesign Design

## Summary

Forge needs one more architectural reset, but not another full rewrite.

The March 25 redesign got the top-level product direction right: one visible `forge` assistant, host-owned orchestration, bounded hidden workers, and transcript-first interaction. The March 27 remediation pass improved the visible path substantially, but it still leaves the harness relying on short-horizon session memory, lexical follow-up routing, and "non-error means complete" decision logic in places where the runtime should instead be enforcing explicit thread state, deliverable contracts, and host-owned lifecycle checks.

That is why the harness keeps slipping back into patch land. The repeated failures are not independent bugs. They are symptoms of one missing layer: a kernel-owned thread and deliverable model for visible collaboration.

This design introduces that layer.

## Problem Statement

The current harness still has four structural weaknesses:

1. Visible collaboration does not have an explicit thread ledger.
   - Preview work, replay requests, visual feedback, pasted artifacts, and cancel/resume turns are inferred from one-turn session snapshots and lexical heuristics.
2. Completion semantics are too weak.
   - `ObservationComplete` currently means "the executor did not fail," not "the requested deliverable was satisfied."
3. Preview success is under-specified.
   - The host can prove that a file exists and a server is reachable, but not that the served artifact is renderable or that the active preview thread is still coherent.
4. Worker governance is incomplete.
   - Hidden workers are already bounded better than the main path, but the runtime still lacks a single thread owner model that cleanly separates visible collaboration from sidecar delegation.

## Why The Current Fixes Still Drift

The recent fixes were correct, but local:

- malformed visible-path tool output now fails closed more often
- preview/artifact state is carried across turns
- strict-local owns more of the visible collaboration path
- long-turn transcript compaction and skill/progress behavior are better controlled

Those were necessary. They were not sufficient.

They did not change the underlying model from:

- "classify each turn mostly from prompt text"
- "carry a little recent runtime state"
- "treat non-error executor output as success"

to:

- "resolve the turn against an explicit active thread"
- "validate whether the requested deliverable was actually satisfied"
- "transition thread state in code"

Until that shift happens, every new log can still expose a different weak seam.

## External Design Inputs

The prior investigation already referenced the right external sources, and the current official guidance still points in the same direction:

- OpenAI's agent guidance favors starting with a single agent plus tools and adding orchestration only when it helps.
- OpenAI's multi-agent guidance separates manager-style "agents as tools" from handoffs; Forge's product shape is manager-owned, not visible handoffs.
- OpenAI's sessions and tracing guidance reinforces that state, tool use, and lifecycle should be host-visible rather than implicit in model prose.
- Anthropic's agent guidance favors simple composable patterns over elaborate agent graphs.
- Anthropic's subagent guidance emphasizes distinct context, permissions, and tools as the reason to delegate at all.
- Anthropic's tooling guidance argues for fewer, clearer, higher-level tools rather than expecting the model to hand-compose brittle low-level workflows.
- MCP makes the host responsible for permissions, roots, authorization, and lifecycle.
- OWASP guidance treats tool outputs, logs, files, and fetched content as untrusted input and recommends least privilege, schema validation, approvals, and monitoring.

The common message is consistent: keep one manager in control, push authority into host code, make tools typed and higher-signal, treat external content as untrusted, and bake observability into the architecture.

## Target Architecture

### 1. Add A Kernel-Owned Thread Ledger

The session must stop acting like "recent hints plus pending action." It needs an explicit ledger of active work.

Add a first-class `ThreadLedger` owned by the harness session:

- `ThreadID`
- `ThreadKind`
- `Goal`
- `Deliverable`
- `Status`
- `Owner`
- `Artifacts`
- `PreviewSession`
- `Evidence`
- `LastValidatedAt`
- `SupersedesThreadID`

Recommended `ThreadKind` values:

- `direct_answer`
- `workspace_inspect`
- `workspace_change`
- `preview_collaboration`
- `verification`
- `external_research`
- `meta_process`

Recommended `ThreadStatus` values:

- `active`
- `awaiting_tool_progress`
- `awaiting_user_feedback`
- `awaiting_verification`
- `blocked`
- `completed`
- `canceled`
- `superseded`

Only one thread may be `active` for the visible conversation at a time.

### 2. Resolve Each Turn Against The Active Thread First

Before classifying the new user message by lexical intent, the runtime should answer:

1. Is there an active thread?
2. Is this input:
   - a new task
   - a follow-up instruction
   - visual feedback on the active preview
   - a replay request
   - a repair/diagnose request
   - pasted evidence
   - a cancellation
   - a meta/process question
3. Does this input stay on the active thread, mutate it, repair it, or supersede it?

That resolution step must happen before normal family classification.

The current `PendingAction` and `HasRecentPreview()` heuristics can remain as compatibility helpers during migration, but the end state should be thread-ledger-first routing.

### 3. Split Execution Into Explicit Lanes

Forge should keep three execution lanes:

1. `conversational`
   - direct answer or short meta/process turns
   - prose is allowed
   - tools are optional, not required
2. `strict_action`
   - any visible turn whose deliverable depends on tool use, artifacts, previews, edits, verification, or repair
   - host validates each step
   - success requires deliverable satisfaction, not merely syntactic completion
3. `worker_sidecar`
   - bounded hidden delegation for parallel or isolated work
   - never owns the visible transcript
   - returns structured observations only

The lane is a property of the active thread plus current turn intent, not a one-off prompt guess.

### 4. Replace Binary Completion With Deliverable Contracts

`ObservationComplete` is too coarse. The kernel should distinguish:

- `needs_tool_step`
- `needs_retry`
- `needs_replan`
- `awaiting_user_feedback`
- `deliverable_satisfied`
- `blocked`

Each thread must carry a typed deliverable contract.

Recommended deliverable classes:

- `answer_only`
- `evidence_backed_explanation`
- `workspace_change_with_verification`
- `preview_available_and_renderable`
- `research_summary_with_sources`

Examples:

- A preview thread is not complete because `mockups.html` exists.
- It is complete only when the host can prove:
  - the artifact exists
  - the artifact type is what the thread expects
  - any requested preview URL is live
  - the served content is renderable
  - the result returned to the user matches the active preview state

### 5. Make Preview Lifecycle A Real Subsystem

Preview state should no longer be "a recent artifact and a recent URL."

Add a host-owned preview session model:

- `PreviewSessionID`
- `ArtifactHandle`
- `Root`
- `Route`
- `ServerToken`
- `Port`
- `URL`
- `ReachabilityStatus`
- `RenderStatus`
- `LastProbeAt`
- `LastProbeSummary`

Recommended preview lifecycle:

1. artifact created or updated
2. artifact validated locally
3. preview session ensured or reused
4. served content probed
5. renderability classified
6. thread state updated

The host should provide a render probe instead of relying on the model to infer success from raw file text.

### 6. Treat Pasted Logs, HTML, And Tool Output As Evidence Objects

When a user pastes a raw HTML blob, stack trace, or tool transcript during an active thread, that content should not be treated as fresh instructions by default.

It should be ingested as typed evidence:

- `log_excerpt`
- `raw_html_artifact`
- `terminal_output`
- `user_observed_error`
- `quoted_model_output`

The runtime should then ask:

- does this evidence contradict the thread's last validated state?
- does it imply replay, repair, or diagnosis?
- does it invalidate the current deliverable?

This is the missing bridge between active thread state and user-supplied runtime evidence.

### 7. Keep Workers As Sidecars, Not Conversation Owners

Hidden workers still have a place, but only as bounded helpers.

They may be used for:

- independent verification
- isolated edit slices
- parallel research

They must not:

- own the active visible thread
- change thread kind
- widen scope
- mutate preview state directly
- produce final user-facing prose

All worker outputs must be validated and merged by the manager before they affect the thread ledger.

### 8. Keep Skill Routing And Progress Host-Owned

The harness already learned this lesson once. It needs to stay explicit in the redesign.

Skills are part of the control plane, not a fictional self-service capability inside provider prompts.

Required behavior:

- the manager decides which required or auto skills apply from thread kind, turn intent, and request text
- executors receive injected skill context from the host
- the model is never told to go load skill files on its own
- long-running visible threads emit short host-rendered progress updates at start, after validated tool milestones, and on retry/replan/blocked transitions
- worker activity is summarized into the visible progress stream instead of leaking raw worker transcripts into the main chat

This keeps two recurring failure modes out of the redesign:

- silent long-running visible turns
- prompt-shaped "I am using X skill" behavior that is not actually backed by host state

### 9. Make Progress A First-Class UX Contract

Forge should not feel like a spinner with a surprise essay at the end.

Visible progress needs its own product contract:

- progress updates are part of the turn lifecycle, not optional renderer polish
- updates should be short, concrete, and cumulative
- updates should explain what Forge is doing now and why that step matters
- updates should arrive at meaningful milestones, not only at turn start and final completion
- the host decides when to emit them from validated state changes

Recommended progress event classes:

- `starting`
- `inspecting`
- `editing`
- `running_verification`
- `creating_artifact`
- `starting_preview`
- `preview_ready`
- `reusing_preview`
- `worker_started`
- `worker_finished`
- `retrying_after_invalid_output`
- `blocked`
- `finalizing`

Recommended UX shape:

- one compact running list in the transcript while the turn is active
- each line reads like a short operational update, not chain-of-thought
- new entries append as work progresses
- the final answer comes after the progress list, not instead of it

Good examples:

- `Inspecting the workspace to find the current preview files`
- `Creating a new mockup artifact at themes_preview.html`
- `Starting a local preview for the updated artifact`
- `Preview is live at http://127.0.0.1:4173/themes_preview.html`
- `Retrying because the previous tool output was malformed`

Bad examples:

- generic spinner with no intermediate state
- one vague line that never changes
- hidden reasoning monologue
- synthetic claims not backed by host-validated milestones

This is especially important for visible collaboration, preview work, and long-running verification turns.

### 10. Add Trust Boundaries Explicitly

The harness must treat these as untrusted:

- file contents
- logs
- fetched web pages
- generated artifacts
- pasted tool output
- worker output until validated

The host must own:

- tool permission boundaries
- preview/server lifecycle
- structured output validation
- approval gates for high-impact actions
- trace records for decisions and failures

## Required Invariants

The redesign is only correct if these remain true:

1. Every visible turn resolves to exactly one thread action: create, continue, replay, repair, supersede, cancel, or answer meta.
2. Only `forge` speaks to the user in the default transcript.
3. Hidden workers are sidecars only and cannot take ownership of the visible thread.
4. A strict-action turn cannot succeed unless its deliverable contract is satisfied.
5. Preview success requires render validation, not only file existence or open port.
6. Pasted logs, HTML, and tool output are evidence first, instructions second.
7. External content is untrusted and cannot directly alter runtime policy.
8. Completion, retry, blocked, and replan decisions are host-owned.
9. Every stateful lifecycle surface has explicit teardown or supersession semantics.
10. Every major failure class has a replayable regression fixture before the redesign is considered done.
11. Long-running visible work emits host-owned progress events instead of relying on model narration.
12. Visible progress updates are frequent enough to show momentum and concrete enough to explain the current milestone.

## Recommended Program Decomposition

Implement this as six slices:

1. thread ledger and turn resolution
2. execution lanes and completion contracts
3. preview/artifact lifecycle and render validation
4. continuation recovery and evidence ingestion
5. worker governance and trust boundaries
6. observability, evals, and production gates

Those slices are intentionally narrower than the current patch stream. Each one changes a distinct control-plane seam and can be verified independently.

## What Not To Do

Do not continue investing in:

- more keyword patches for preview follow-ups
- more prompt bullets telling the model how to behave
- more server shell recipes in prompts
- more parser repair without stronger deliverable validation
- more hidden-worker routing tweaks without a thread owner model

Those can reduce incidents temporarily. They do not close the architectural gap.

## Done Means

The redesign is complete only when:

- visible collaboration is thread-ledger-driven instead of recent-turn-driven
- completion depends on deliverable validation
- preview threads survive feedback, replay, cancel/resume, and pasted evidence coherently
- hidden workers stay bounded and sidecar-only
- logs and external content are treated as untrusted evidence
- the harness passes real-log replay, broad paraphrase coverage, and long multi-turn preview sessions without new patch-specific routing work

## References

External sources are intentionally not duplicated inline here. Use the primary-source reference list already captured in `docs/reports/forge-harness-architecture-investigation-2026-03-27.md`, especially:

- OpenAI practical agent guidance, multi-agent orchestration, sessions, guardrails, and tracing
- Anthropic guidance on effective agents, subagents, multi-agent systems, and tool design
- MCP architecture, tools, roots, and authorization
- OWASP AI agent security and prompt-injection prevention guidance
