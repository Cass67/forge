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

Forge now has real foundations in place:

- scoped permission rules exist under `internal/permissions`;
- high-confidence secret scanning exists under `internal/secscan`;
- read/write/edit/patch/command paths have secret-policy integration;
- action-risk facts, classifier contracts, redacted classifier prompts, and denial tracking exist;
- compaction manager and compaction hook payloads exist;
- delegation can spawn/wait/status/kill child agents and now has read-only specialist boundaries;
- skill routing has tests for review/status prompts avoiding `brainstorming`;
- post-delegation document writes now keep tools available until a write succeeds;
- broad regex search is no longer hard-blocked in a way that traps the model;
- outstanding child agents are surfaced to the parent prompt instead of letting the model claim none are running;
- debug, event, prompt, and TUI agent-state payloads are redacted before user-visible or persisted surfaces.

The remaining gap is acceptance coverage. The core state machines now have unit coverage, but several live harness checks still need repeatable provider-backed evidence.

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

- [x] Create a first-class `AgentTaskState` model owned by runtime/session, not only `AgentPool` internals.
- [x] Track `id`, `role`, `description`, `prompt`, `status`, `created_at`, `started_at`, `completed_at`, `last_activity_at`, `result`, `error`, and `parent_turn`.
- [x] Track status values: `pending`, `running`, `completed`, `failed`, `killed`, `timeout`, `not_found`.
- [x] Keep progress records: last tool name, recent tool activities, token/turn counts if available.
- [x] Store enough state for UI and model prompts to agree.
- [x] Add tests for every state transition.

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

- [x] Replace `outstandingSpawnedAgents(snapshot)` as the primary source with runtime `AgentTaskState` queries.
- [x] Keep transcript reconstruction only as a fallback for restored old sessions.
- [x] Add tests proving malformed or missing tool results cannot hide a running agent.
- [x] Add tests for multiple agents where one completes and another continues.
- [x] Add tests for repeated waits against the same agent ID.
- [x] Add tests for failed, killed, timeout, and not-found agents.

Acceptance criteria:

- UI, prompt overlays, and `wait_agent` all read the same state source.
- A stale or malformed `wait_agent` transcript result cannot make Forge lie about active agents.

### 1.3 Add Agent Status, Kill, And Resume Controls

- [x] Add a runtime status query for all agents in the current session.
- [x] Add slash command or tool support for `agent status`.
- [x] Add cancellation support for running child agents.
- [x] Add `kill_agent` or equivalent command/tool with approval rules if needed.
- [x] Add resume semantics or explicit “cannot resume” behavior for completed/failed tasks.
- [x] Add tests for cancellation cleanup and prompt truthfulness after kill.

Acceptance criteria:

- A user can ask what agents are doing and get a factual answer from state.
- A user can stop runaway child work without killing the whole Forge session.

### 1.4 Make Agent Progress Durable And Visible

- [x] Capture recent child tool activity in state.
- [x] Render child activity from state in the side panel.
- [x] Include concise progress in prompt context only when relevant.
- [x] Avoid dumping large child transcripts into parent context by default.
- [x] Add tests for progress update ordering and terminal states.

Acceptance criteria:

- The side panel is not just a stream of events; it reflects authoritative state.
- Parent responses do not rely on guessing from prior text.

## Phase 2: Delegation Reliability And Parent Follow-Through

Goal: delegated workflows should consistently finish the user’s requested outcome, especially audit/review/write-doc tasks.

### 2.1 Define Delegation Contracts

- [x] Define parent-owned vs child-owned actions.
- [x] Mark read-only agents as inspect/report only.
- [x] Ensure child prompts instruct read-only agents to return findings and proposed output, not write files or run commands.
- [x] Ensure parent state records pending post-delegation actions: write doc, run verification, commit, ask user, no action.
- [x] Add tests for each pending action kind.

Acceptance criteria:

- Read-only agents cannot accidentally own writes/commits.
- Parent tools are restored based on pending action state, not wording variants.

### 2.2 Replace Natural-Language Tool Restoration With Action State

- [x] Keep the current pending-write fix as a short-term bridge.
- [x] Replace write-intent inference with explicit action markers produced by delegation orchestration.
- [x] Extract target path and action type from user request or child report into structured state.
- [x] Add tests for “memo”, “note”, “report”, “findings”, and pathless document requests without adding phrase-specific runtime branches.
- [x] Add tests showing generic follow-ups still expose the right tools while pending action exists.

Acceptance criteria:

- “where is the doc?”, “what happened?”, and “continue” all behave correctly because pending action state exists.

### 2.3 End-To-End Live Delegation Tests

- [x] Add a repeatable live harness test for spawn/wait/write-doc.
- [x] Add a live harness test for multiple agents where one is still running during a status question.
- [x] Add a live harness test for cancelled child agents.
- [x] Add a live harness test for read-only child audit plus parent write.
- [x] Store debug-log assertions without leaking secrets.

Acceptance criteria:

- Long delegated workflows are verified against real `bin/forge`, not only unit tests.

## Phase 3: Permission And Approval Security

Goal: deterministic policy first, classifier second, and no user-intent classifier loops.

### 3.1 Scoped Permission Rules Closeout

- [x] Create a closeout table for permission scopes: managed, user, project, local, session, CLI.
- [x] Verify precedence and conflict behavior: deny > ask > allow at the right scope.
- [x] Verify command/path/tool matching with realistic cases.
- [x] Document examples in README or dedicated config docs.
- [x] Add tests for invalid config and unsupported tool names.

Acceptance criteria:

- Users can understand and predict why an action was allowed, denied, or asked.

### 3.2 Classifier Cancellation And Timeouts

- [x] Replace classifier calls that use `context.Background()` with caller-scoped context.
- [x] Add short classifier timeout in runtime configuration.
- [x] Add cancellation tests for user abort.
- [x] Add timeout fallback tests for interactive and headless modes.
- [x] Ensure fallback is `ask` or `deny`, never `allow`.

Acceptance criteria:

- A stuck classifier cannot keep a Forge turn alive after cancellation.

### 3.3 Redacted Classifier Observability

- [x] Keep classifier observer events redacted.
- [x] Expand redaction tests beyond GitHub PAT-like tokens.
- [x] Cover bearer tokens, OpenAI-like keys, Anthropic-like keys, AWS-like keys, generic `TOKEN=...`, and private-key blocks.
- [x] Verify redaction in action summary, detail, path, reason, fallback, approval updates, debug logs, and classifier prompt.
- [x] Document the redaction contract.

Acceptance criteria:

- Debugging classifier decisions never requires printing secret values.

### 3.4 Command Risk Analyzer Hardening

- [x] Keep current risk facts for common commands.
- [x] Add conservative shell construct detection: pipes to shell, command substitution, redirects to sensitive paths, env dumps, credential path reads.
- [x] Add tests for `curl | sh`, `bash -c`, `rm -rf`, `.env` reads, `printenv`, and safe commands like `go test` / `git status`.
- [x] Feed risk facts to approvals and classifier prompt.
- [x] Do not build a full shell parser until the conservative analyzer proves insufficient.

Acceptance criteria:

- Common dangerous shell forms are blocked/asked before classifier involvement.

## Phase 4: Secret Handling Across Tool Boundaries

Goal: secrets are inaccessible by default and redacted everywhere they can surface.

### 4.1 Secret Scanner Rule Matrix

- [x] Expand `internal/secscan` with high-confidence rules only.
- [x] Add private-key block detection.
- [x] Add bearer token and generic assignment detection with low false-positive thresholds.
- [x] Keep scanner public formatting match-free; never expose matched values.
- [x] Add table-driven scanner tests using dummy values only.

Acceptance criteria:

- Scanner finds common credential classes without noisy broad false positives.

### 4.2 Tool Boundary Enforcement

- [x] Verify read redaction before line-number formatting.
- [x] Verify write/edit/apply_patch blocking before approval prompts.
- [x] Verify command output redaction before truncation and return to model.
- [x] Verify search/glob boundaries respect ignore files and secret file rules.
- [x] Verify git diff/commit helper paths do not print secrets.
- [x] Add tests for each tool boundary.

Acceptance criteria:

- No tool result or approval prompt leaks a dummy secret in tests.

### 4.3 Secret Policy Documentation

- [x] Document `[security.secrets]` modes: `allow`, `redact`, `ask`, `block`.
- [x] Document defaults.
- [x] Document how to audit a false positive safely.
- [x] Document that real secrets must not appear in examples, fixtures, logs, or commit messages.

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

- [x] Implement token diminishing detection or remove/document config as future.
- [x] Implement tool thrash circuit breaker using stateful loop metrics.
- [x] Replace hard blocking of broad searches with progressive warnings/recovery state.
- [x] Add recovery prompt overlays that are finite and testable.
- [x] Add tests for repeated same-file search/read loops, repeated no-op edits, and repeated failed delegation waits.

Acceptance criteria:

- Forge can detect and interrupt unproductive loops without trapping the model in “blocked tool” recovery loops.

### 5.3 Compaction State Machine Closeout

- [x] Verify compaction modes: none, micro, summarize, reactive, user partial.
- [x] Implement real microcompaction for large tool results or mark it explicitly unimplemented.
- [x] Verify prompt-too-long reactive compaction and one retry.
- [x] Verify compaction failure circuit opens and success resets failures.
- [x] Document `CompactionHookPayload` contract and stability expectations.
- [x] Add golden-ish pre/post hook payload tests.

Acceptance criteria:

- Context pressure has a deterministic recovery path and observable state.

## Phase 6: TUI Truth And Observability

Goal: the UI and model-visible state must agree.

### 6.1 Agent Panel From State

- [x] Render side-panel child agents from `AgentTaskState`.
- [x] Show status, last activity, elapsed time, and terminal result.
- [x] Show killed/failed/timeout distinctly.
- [x] Avoid stale panels after terminal states.
- [x] Add TUI tests for panel state transitions.

Acceptance criteria:

- If the UI shows an agent running, parent prompt state also knows it is running.

### 6.2 Debug Log Trustworthiness

- [x] Include agent lifecycle events in chat debug logs.
- [x] Include tool exposure decisions and reason codes.
- [x] Redact secrets in debug payloads.
- [x] Add tests for redacted debug events.

Acceptance criteria:

- A failed live run can be diagnosed from debug logs without exposing secrets.

### 6.3 Long Transcript Performance

- [x] Cache rendered transcript blocks.
- [x] Add viewport rendering guardrails for very large transcripts.
- [x] Keep trace/export complete even when viewport rendering is virtualized.
- [x] Add performance counters in debug view only.

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

- [x] Live delegated read-only audit returns report content and parent writes requested report file.
- [x] Live status question reports outstanding child agent truthfully.
- [x] Live child cancellation stops child activity and updates model-visible state plus debug lifecycle logs.
- [x] Live stalled stream test fails with idle timeout.
- [x] Live secret-output command returns redacted content.
- [x] Live write containing dummy secret is blocked without writing file.
- [x] Live manual compaction test emits compact boundary and continues.
- [x] Live reactive context-error compaction test compacts, retries, and continues.

Note: provider-backed smoke checks may still be inconclusive during external stalls; the local-provider harness below is the deterministic acceptance source for the live matrix.

2026-05-09 live follow-up:

- `FORGE_CHAT_CONSOLE=1 ./bin/forge -yolo -d --model copilot/gpt-5` against a temp fixture produced `all 3 attempts failed: stream idle timeout after 30s` before any child agent was spawned. Debug evidence: `/tmp/forge-live-status-forge-live-status-1j1o6k.jsonl`.
- A minimal `copilot/gpt-4.1-mini` smoke run did not reach an LLM request before the 120s wrapper timeout. Debug evidence: `/tmp/forge-live-smoke-20260509002327.jsonl`.
- Because the provider path stalled before child-agent actions, outstanding-agent status and cancellation were verified with the local-provider harness instead.

2026-05-09 local-provider live harness:

- `go test ./cmd/forge -run TestLiveAcceptance -count=1` builds a fresh `bin/forge`, runs console mode against a local OpenAI-compatible provider, and verifies spawn/wait/write-doc delegation, a read-only audit child without write/run tools, multi-agent status with one child still running, cancellation, secret command output redaction, blocked dummy-secret writes, debug-log secret boundaries, and manual compaction continuation.
- This harness is deterministic and exercises the real CLI/runtime/tool loop without external provider stalls. The external Copilot-backed checks above remain useful provider smoke coverage, but no longer block the local acceptance matrix.
- External provider smoke checks remain future non-blocking work: use them only to catch provider wire/API drift, not as a required release gate while provider stalls are outside Forge control.

### 7.4 Code Review Follow-Up

- [x] Redact debug `task_state` metadata before writing `llm.request` records.
- [x] Redact `AgentTaskState` event payloads before live renderer/TUI surfaces.
- [x] Redact `AgentTaskState` recent activity/result/error text before prompt or pane rendering.
- [x] Redact `wait_agent`, `agent_status`, and `kill_agent` tool-result payloads before transcript/TUI exposure.
- [x] Redact stored native tool-call arguments before replaying history to the model.
- [x] Redact stored assistant tool-call preambles and reasoning before replaying history to the model.
- [x] Redact console/TUI tool-call summaries before rendering command arguments.
- [x] Redact assistant tool-call preambles before console/TUI rendering.
- [x] Match direct children for scoped path rules such as `docs/**/*.md`.
- [x] Keep `wait_agent` timeout as unresolved state: timeout remains non-terminal in prompt/tool logic, and existing tests prove a timed-out child can later complete or fail.

### 7.3 Required Repository Checks

- [x] `go test ./... -timeout 120s`
- [x] `just build`
- [x] `git diff --check`
- [x] No untracked generated artifacts unless intentionally documented.
- [x] No secrets in docs, tests, fixtures, debug logs, or commit messages.

2026-05-09 repository follow-up:

- `git status --short --branch` in `.worktrees/live-acceptance-20260509` showed only intended source/doc changes during this follow-up, including the new acceptance test source; no generated artifacts were present.
- `gitleaks git --redact` scanned 494 commits and reported no leaks found.

## Immediate Next Work Items

These should be done before new feature breadth:

1. [x] Stabilize and commit the current delegation/skill-routing/search changes.
2. [x] Introduce first-class `AgentTaskState` and migrate UI/prompt/wait logic to it.
3. [x] Add status/kill controls for child agents.
4. [x] Thread approval classifier cancellation context and timeout behavior.
5. [x] Enforce stream idle timeout for streaming and tool-streaming paths.
6. [x] Replace config-only token/tool-thrash fields with implemented state machines or remove/document them as future.
7. [x] Expand secret scanner and redaction matrix.
8. [x] Add live harness checks for delegated write, outstanding agent status, cancellation, and secret handling.

## Definition Of Done

Forge reaches this roadmap’s reliability/security bar when all of the following are true:

- [x] every running child agent has authoritative state;
- [x] parent responses cannot contradict child-agent state;
- [x] users can status/kill/resume or explicitly recover child work;
- [x] pending delegated writes and verification actions are state-based;
- [x] broad audit/review prompts do not enter brainstorming or phrase-specific loops;
- [x] approval and classifier decisions are cancellable, auditable, and redacted;
- [x] secret handling is enforced at every tool boundary;
- [x] provider stalls and context pressure have finite recovery paths;
- [x] loop/thrash prevention is stateful and non-trapping;
- [x] all acceptance checks in Phase 7 pass on a fresh build.
