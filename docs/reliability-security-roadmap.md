# Forge Reliability And Security Roadmap

Date: 2026-05-08

## Purpose

This document is the working checklist for bringing Forge closer to the reliability and security bar set by mature coding-agent harnesses such as Claude Code, Codex, and OpenCode.

The goal is not feature breadth. The goal is trust:

- long-running sessions do not silently lose state;
- delegated agents are observable, cancellable, and truthful;
- tools remain available when work is pending, without phrase-based guessing;
- approvals and auto-permissions are deterministic first and classifier-assisted only for concrete actions;
- secrets do not leak through reads, writes, commands, classifier prompts, logs, debug views, or summaries;
- compaction and retry behavior recover from common long-session failures instead of looping.

## Inputs Reviewed

- `docs/gaps-and-next-steps.md`
- `docs/claude-code-feature-gaps.md`
- `docs/plans/2026-05-07-best-of-claude-forge.md`
- `docs/plans/2026-05-07-best-of-claude-hardening.md`
- `docs/plans/2026-05-07-best-of-claude-hardening-design.md`
- `docs/reports/2026-05-07-best-of-claude-forge-implementation-findings.md`
- `docs/progress-review-2026-05-08-best-of-claude-hardening.md`
- Recent delegation, skill-routing, secret-redaction, compaction, and approval changes in the working tree.

Reference patterns checked in sibling source trees:

- CCI / Claude Code: explicit async/background agent task state, progress, notifications, and terminal state transitions.
- Codex: explicit active-turn task state, pending mailbox/input state, and `wait_agent` as a synchronization primitive.
- OpenCode: explicit tool/task part states, subtask sessions, resumable task IDs, and stateful `pending` / `running` / `completed` / `error` transitions.

## Non-Goals For This Roadmap

These are useful, but not part of this reliability/security push:

- plugin marketplace;
- public plugin discovery, package ecosystem, or remote cache;
- browser/computer-use automation;
- voice workflows;
- remote experimentation frameworks;
- full Bubble Tea replacement or custom terminal renderer rewrite.

Local plugin safety may appear only where it affects core security boundaries. Marketplace and ecosystem work stays deferred.

## Current Assessment

Forge has real foundations in place:

- scoped permission rules exist under `internal/permissions`;
- high-confidence secret scanning exists under `internal/secscan`;
- read/write/edit/patch/command paths have secret-policy integration;
- action-risk facts, classifier contracts, redacted classifier prompts, and denial tracking exist;
- compaction manager and compaction hook payloads exist;
- delegation can spawn/wait child agents and now has read-only specialist boundaries;
- skill routing has tests for review/status prompts avoiding `brainstorming`;
- post-delegation document writes now keep tools available until a write succeeds;
- broad regex search is no longer hard-blocked in a way that traps the model;
- outstanding child agents are surfaced to the parent prompt instead of letting the model claim none are running.

The remaining gap is reliability maturity. Several capabilities are currently reconstructed from transcript history or prompt overlays rather than being first-class runtime/session state. That is below the bar set by CCI, Codex, and OpenCode.

## Roadmap Status Legend

- `[ ]` not started
- `[~]` partially implemented; needs hardening or proof
- `[x]` implemented and verified
- `[!]` blocking reliability/security gap

## Phase 0: Stabilize The Current Worktree

Goal: turn the current behavior fixes into a clean, reviewed, verifiable baseline before layering on deeper architecture.

- [x] Review the current uncommitted changes as one patch set.
- [x] Remove any phrase-specific routing left over from previous attempts.
- [x] Confirm audit/status prompts route to review/status behavior, not `brainstorming`.
- [x] Confirm broad alternation searches are not hard-blocked, while future loop prevention is handled by stateful thrash controls.
- [x] Confirm post-delegation write state is state-based, not one-off phrase matching.
- [x] Confirm outstanding child-agent prompt state is accurate for multiple agents and mixed completion states.
- [x] Run focused tests: `go test ./internal/react ./internal/skills ./internal/tui -count=1`.
- [x] Run full tests: `go test ./... -timeout 120s`.
- [x] Run build: `just build`.
- [x] Run diff hygiene: `git diff --check`.
- [x] Commit the stabilized baseline.

Acceptance criteria:

- A live delegated audit can complete, write a document, answer status questions truthfully, and stop without hidden subagents continuing unnoticed.
- The parent never says tools or agents are unavailable when runtime state says otherwise.

## Phase 1: First-Class Agent Task State

Goal: stop reconstructing agent truth from transcript history. Agent lifecycle must be first-class runtime/session state.

### 1.1 Add Durable AgentTaskState

- [ ] Create a first-class `AgentTaskState` model owned by runtime/session, not only `AgentPool` internals.
- [ ] Track `id`, `role`, `description`, `prompt`, `status`, `created_at`, `started_at`, `completed_at`, `last_activity_at`, `result`, `error`, and `parent_turn`.
- [ ] Track status values: `pending`, `running`, `completed`, `failed`, `killed`, `timeout`, `not_found`.
- [ ] Keep progress records: last tool name, recent tool activities, token/turn counts if available.
- [ ] Store enough state for UI and model prompts to agree.
- [ ] Add tests for every state transition.

Code areas:

- `internal/react/agent_pool.go`
- `internal/runtime/chat.go`
- `internal/tui/*`
- `internal/react/loop.go`

Reference behavior:

- CCI `LocalAgentTaskState`
- OpenCode `ToolStateRunning` and subtask session state
- Codex `ActiveTurn.tasks` and pending input/mailbox state

### 1.2 Replace Transcript Reconstruction With State Queries

- [ ] Replace `outstandingSpawnedAgents(snapshot)` as the primary source with runtime `AgentTaskState` queries.
- [ ] Keep transcript reconstruction only as a fallback for restored old sessions.
- [ ] Add tests proving malformed or missing tool results cannot hide a running agent.
- [ ] Add tests for multiple agents where one completes and another continues.
- [ ] Add tests for repeated waits against the same agent ID.
- [ ] Add tests for failed, killed, timeout, and not-found agents.

Acceptance criteria:

- UI, prompt overlays, and `wait_agent` all read the same state source.
- A stale or malformed `wait_agent` transcript result cannot make Forge lie about active agents.

### 1.3 Add Agent Status, Kill, And Resume Controls

- [ ] Add a runtime status query for all agents in the current session.
- [ ] Add slash command or tool support for `agent status`.
- [ ] Add cancellation support for running child agents.
- [ ] Add `kill_agent` or equivalent command/tool with approval rules if needed.
- [ ] Add resume semantics or explicit “cannot resume” behavior for completed/failed tasks.
- [ ] Add tests for cancellation cleanup and prompt truthfulness after kill.

Acceptance criteria:

- A user can ask what agents are doing and get a factual answer from state.
- A user can stop runaway child work without killing the whole Forge session.

### 1.4 Make Agent Progress Durable And Visible

- [ ] Capture recent child tool activity in state.
- [ ] Render child activity from state in the side panel.
- [ ] Include concise progress in prompt context only when relevant.
- [ ] Avoid dumping large child transcripts into parent context by default.
- [ ] Add tests for progress update ordering and terminal states.

Acceptance criteria:

- The side panel is not just a stream of events; it reflects authoritative state.
- Parent responses do not rely on guessing from prior text.

## Phase 2: Delegation Reliability And Parent Follow-Through

Goal: delegated workflows should consistently finish the user’s requested outcome, especially audit/review/write-doc tasks.

### 2.1 Define Delegation Contracts

- [ ] Define parent-owned vs child-owned actions.
- [ ] Mark read-only agents as inspect/report only.
- [ ] Ensure child prompts instruct read-only agents to return findings and proposed output, not write files or run commands.
- [ ] Ensure parent state records pending post-delegation actions: write doc, run verification, commit, ask user, no action.
- [ ] Add tests for each pending action kind.

Acceptance criteria:

- Read-only agents cannot accidentally own writes/commits.
- Parent tools are restored based on pending action state, not wording variants.

### 2.2 Replace Natural-Language Tool Restoration With Action State

- [ ] Keep the current pending-write fix as a short-term bridge.
- [ ] Replace write-intent inference with explicit action markers produced by delegation orchestration.
- [ ] Extract target path and action type from user request or child report into structured state.
- [ ] Add tests for “memo”, “note”, “report”, “findings”, and pathless document requests without adding phrase-specific runtime branches.
- [ ] Add tests showing generic follow-ups still expose the right tools while pending action exists.

Acceptance criteria:

- “where is the doc?”, “what happened?”, and “continue” all behave correctly because pending action state exists.

### 2.3 End-To-End Live Delegation Tests

- [ ] Add a repeatable live harness test for spawn/wait/write-doc.
- [ ] Add a live harness test for multiple agents where one is still running during a status question.
- [ ] Add a live harness test for cancelled child agents.
- [ ] Add a live harness test for read-only child audit plus parent write.
- [ ] Store debug-log assertions without leaking secrets.

Acceptance criteria:

- Long delegated workflows are verified against real `bin/forge`, not only unit tests.

## Phase 3: Permission And Approval Security

Goal: deterministic policy first, classifier second, and no user-intent classifier loops.

### 3.1 Scoped Permission Rules Closeout

- [ ] Create a closeout table for permission scopes: managed, user, project, local, session, CLI.
- [ ] Verify precedence and conflict behavior: deny > ask > allow at the right scope.
- [ ] Verify command/path/tool matching with realistic cases.
- [ ] Document examples in README or dedicated config docs.
- [ ] Add tests for invalid config and unsupported tool names.

Acceptance criteria:

- Users can understand and predict why an action was allowed, denied, or asked.

### 3.2 Classifier Cancellation And Timeouts

- [ ] Replace classifier calls that use `context.Background()` with caller-scoped context.
- [ ] Add short classifier timeout in runtime configuration.
- [ ] Add cancellation tests for user abort.
- [ ] Add timeout fallback tests for interactive and headless modes.
- [ ] Ensure fallback is `ask` or `deny`, never `allow`.

Acceptance criteria:

- A stuck classifier cannot keep a Forge turn alive after cancellation.

### 3.3 Redacted Classifier Observability

- [ ] Keep classifier observer events redacted.
- [ ] Expand redaction tests beyond GitHub PAT-like tokens.
- [ ] Cover bearer tokens, OpenAI-like keys, Anthropic-like keys, AWS-like keys, generic `TOKEN=...`, and private-key blocks.
- [ ] Verify redaction in action summary, detail, path, reason, fallback, approval updates, debug logs, and classifier prompt.
- [ ] Document the redaction contract.

Acceptance criteria:

- Debugging classifier decisions never requires printing secret values.

### 3.4 Command Risk Analyzer Hardening

- [ ] Keep current risk facts for common commands.
- [ ] Add conservative shell construct detection: pipes to shell, command substitution, redirects to sensitive paths, env dumps, credential path reads.
- [ ] Add tests for `curl | sh`, `bash -c`, `rm -rf`, `.env` reads, `printenv`, and safe commands like `go test` / `git status`.
- [ ] Feed risk facts to approvals and classifier prompt.
- [ ] Do not build a full shell parser until the conservative analyzer proves insufficient.

Acceptance criteria:

- Common dangerous shell forms are blocked/asked before classifier involvement.

## Phase 4: Secret Handling Across Tool Boundaries

Goal: secrets are inaccessible by default and redacted everywhere they can surface.

### 4.1 Secret Scanner Rule Matrix

- [ ] Expand `internal/secscan` with high-confidence rules only.
- [ ] Add private-key block detection.
- [ ] Add bearer token and generic assignment detection with low false-positive thresholds.
- [ ] Keep scanner public formatting match-free; never expose matched values.
- [ ] Add table-driven scanner tests using dummy values only.

Acceptance criteria:

- Scanner finds common credential classes without noisy broad false positives.

### 4.2 Tool Boundary Enforcement

- [ ] Verify read redaction before line-number formatting.
- [ ] Verify write/edit/apply_patch blocking before approval prompts.
- [ ] Verify command output redaction before truncation and return to model.
- [ ] Verify search/glob boundaries respect ignore files and secret file rules.
- [ ] Verify git diff/commit helper paths do not print secrets.
- [ ] Add tests for each tool boundary.

Acceptance criteria:

- No tool result or approval prompt leaks a dummy secret in tests.

### 4.3 Secret Policy Documentation

- [ ] Document `[security.secrets]` modes: `allow`, `redact`, `ask`, `block`.
- [ ] Document defaults.
- [ ] Document how to audit a false positive safely.
- [ ] Document that real secrets must not appear in examples, fixtures, logs, or commit messages.

Acceptance criteria:

- Users know what Forge blocks/redacts and why.

## Phase 5: Long-Session Resilience

Goal: long sessions should recover from provider stalls, context pressure, compaction failures, and tool loops.

### 5.1 Enforce Stream Idle Timeout

- [x] Confirm `stream_idle_timeout_ms` is actually enforced, not merely configured.
- [x] Add tests for stalled `Stream` and `StreamWithToolsOptions`.
- [x] Reset idle timer on token/tool-call progress.
- [x] Return a classified retryable idle-timeout error before any partial output is emitted.
- [x] Do not retry after meaningful partial output unless the driver can safely resume.

Acceptance criteria:

- A stalled provider stream fails predictably and does not hang the UI indefinitely.

### 5.2 Token Diminishing And Tool Thrash Controls

- [ ] Implement token diminishing detection or remove/document config as future.
- [ ] Implement tool thrash circuit breaker using stateful loop metrics.
- [ ] Replace hard blocking of broad searches with progressive warnings/recovery state.
- [ ] Add recovery prompt overlays that are finite and testable.
- [ ] Add tests for repeated same-file search/read loops, repeated no-op edits, and repeated failed delegation waits.

Acceptance criteria:

- Forge can detect and interrupt unproductive loops without trapping the model in “blocked tool” recovery loops.

### 5.3 Compaction State Machine Closeout

- [ ] Verify compaction modes: none, micro, summarize, reactive, user partial.
- [ ] Implement real microcompaction for large tool results or mark it explicitly unimplemented.
- [ ] Verify prompt-too-long reactive compaction and one retry.
- [ ] Verify compaction failure circuit opens and success resets failures.
- [ ] Document `CompactionHookPayload` contract and stability expectations.
- [ ] Add golden-ish pre/post hook payload tests.

Acceptance criteria:

- Context pressure has a deterministic recovery path and observable state.

## Phase 6: TUI Truth And Observability

Goal: the UI and model-visible state must agree.

### 6.1 Agent Panel From State

- [ ] Render side-panel child agents from `AgentTaskState`.
- [ ] Show status, last activity, elapsed time, and terminal result.
- [ ] Show killed/failed/timeout distinctly.
- [ ] Avoid stale panels after terminal states.
- [ ] Add TUI tests for panel state transitions.

Acceptance criteria:

- If the UI shows an agent running, parent prompt state also knows it is running.

### 6.2 Debug Log Trustworthiness

- [ ] Include agent lifecycle events in chat debug logs.
- [ ] Include tool exposure decisions and reason codes.
- [ ] Redact secrets in debug payloads.
- [ ] Add tests for redacted debug events.

Acceptance criteria:

- A failed live run can be diagnosed from debug logs without exposing secrets.

### 6.3 Long Transcript Performance

- [ ] Cache rendered transcript blocks.
- [ ] Add viewport rendering guardrails for very large transcripts.
- [ ] Keep trace/export complete even when viewport rendering is virtualized.
- [ ] Add performance counters in debug view only.

Acceptance criteria:

- Long sessions remain usable while preserving complete transcript data.

## Phase 7: Verification And Acceptance Matrix

Goal: every reliability/security claim has a test or live reproduction.

### 7.1 Required Unit Suites

- [x] `go test ./internal/permissions -count=1`
- [x] `go test ./internal/secscan -count=1`
- [x] `go test ./internal/agent/tools -count=1`
- [x] `go test ./internal/react -count=1`
- [x] `go test ./internal/runtime -count=1`
- [x] `go test ./internal/tui -count=1`
- [x] `go test ./internal/config -count=1`

### 7.2 Required Integration/Live Checks

- [x] Live delegated audit writes requested report file.
- [ ] Live status question reports outstanding child agent truthfully.
- [ ] Live child cancellation stops child activity and updates UI/model state.
- [ ] Live stalled stream test fails with idle timeout.
- [ ] Live secret-output command returns redacted content.
- [ ] Live write containing dummy secret is blocked without writing file.
- [ ] Live long-session compaction test emits compact boundary and continues.

### 7.3 Required Repository Checks

- [x] `go test ./... -timeout 120s`
- [x] `just build`
- [x] `git diff --check`
- [ ] No untracked generated artifacts unless intentionally documented.
- [ ] No secrets in docs, tests, fixtures, debug logs, or commit messages.

## Immediate Next Work Items

These should be done before new feature breadth:

1. [x] Stabilize and commit the current delegation/skill-routing/search changes.
2. [ ] Introduce first-class `AgentTaskState` and migrate UI/prompt/wait logic to it.
3. [ ] Add status/kill controls for child agents.
4. [ ] Thread approval classifier cancellation context and timeout behavior.
5. [x] Enforce stream idle timeout for streaming and tool-streaming paths.
6. [ ] Replace config-only token/tool-thrash fields with implemented state machines or remove/document them as future.
7. [ ] Expand secret scanner and redaction matrix.
8. [ ] Add live harness checks for delegated write, outstanding agent status, cancellation, and secret handling.

## Definition Of Done

Forge reaches this roadmap’s reliability/security bar when all of the following are true:

- [ ] every running child agent has authoritative state;
- [ ] parent responses cannot contradict child-agent state;
- [ ] users can status/kill/resume or explicitly recover child work;
- [ ] pending delegated writes and verification actions are state-based;
- [ ] broad audit/review prompts do not enter brainstorming or phrase-specific loops;
- [ ] approval and classifier decisions are cancellable, auditable, and redacted;
- [ ] secret handling is enforced at every tool boundary;
- [ ] provider stalls and context pressure have finite recovery paths;
- [ ] loop/thrash prevention is stateful and non-trapping;
- [ ] all acceptance checks in Phase 7 pass on a fresh build.
