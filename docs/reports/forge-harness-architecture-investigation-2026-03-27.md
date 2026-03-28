# Forge Harness Architecture Investigation - 2026-03-27

## Executive Conclusion

The current harness failures are architectural, not lexical.

The March 25 redesign diagnosed the right problem: Forge needed runtime-owned orchestration, bounded workers, and one coherent user-facing assistant. The implementation only completed that discipline on the hidden-worker path. The main visible local path still relies on the generic `internal/agent.Agent` loop, so malformed tool markup, shell-heavy preview orchestration, and provider-specific tool-calling weaknesses still leak directly into user-facing behavior.

The result is a split system:

- hidden workers are contract-driven, validated, retried, and fail closed
- visible local execution is still prompt-driven and effectively fail open

That is why we keep landing on phrase patches and prompt tweaks. We fixed routing and wording around the real issue, but not the control plane that decides whether a turn actually made progress.

## What The Approved Design Said

The approved redesign already set the right target:

- `docs/superpowers/specs/2026-03-25-forge-harness-redesign-design.md:11-22`
  The control plane was still too prompt-shaped.
- `docs/superpowers/specs/2026-03-25-forge-harness-redesign-design.md:68-75`
  Classification, transitions, delegation, and stop conditions should live in runtime code.
- `docs/superpowers/specs/2026-03-25-forge-harness-redesign-design.md:87-99`
  Hidden workers should run under tighter contracts and stay out of the visible transcript.
- `docs/superpowers/specs/2026-03-25-forge-harness-redesign-design.md:141-189`
  The kernel should own a small deterministic state machine.
- `docs/superpowers/specs/2026-03-25-forge-harness-redesign-design.md:364-372`
  Invalid worker outputs should fail closed and trigger retry or fallback.

The implementation plan also made one compromise that matters now:

- `docs/superpowers/plans/2026-03-25-forge-harness-redesign-implementation.md:5-8`
  It moved orchestration into `internal/harness`, but explicitly reused `internal/agent.Agent` as the execution engine for both local turns and worker turns.

That reuse is where the redesign drifted off course.

## What The Code Actually Does

### 1. Visible collaboration is still a prompt wrapper around the generic agent loop

`internal/harness/local.go:36-92` runs local work by calling `e.Agent.Run(...)` and then treating `e.Agent.LastResponse()` as a completed observation.

`internal/harness/local.go:62-74` shows that `PrefersVisibleExecution` only changes the prompt and tool set selection.

`internal/harness/local.go:197-219` proves visible collaboration is enforced by instructions in text, not by a stricter runtime contract.

### 2. Visible collaboration is explicitly excluded from the strict worker path

`internal/harness/policy.go:5-8` returns `WorkerNone` whenever `PrefersVisibleExecution` is true.

That means the strict path already built for workers is never used for the exact class of tasks that is failing most often: preview, artifact, mockup, browser, and server-heavy requests.

### 3. The worker path is the only place with strong invariants

`internal/harness/workers.go:52-117` creates hidden workers with filtered tools, isolated state, bounded retries, and structured validation.

`internal/harness/workers.go:175-194` explicitly forces workers back into a strict single-tool-turn or single-JSON-result state machine after bad output.

This is the behavior we wanted from the redesign, but it only exists for workers.

### 4. The main visible path still fail-opens on malformed tool markup

`internal/agent/agent.go:340-359` retries malformed tool markup only for `scout` and other subagents.

`internal/agent/agent.go:377-456` shows the problem on the main path: if `len(calls) == 0`, the response is treated as final unless one of a few narrow special cases fires.

So when the main visible assistant emits raw `<tool_call>` markup that does not parse into a valid call, the runtime does not recover. It accepts the turn as done.

### 5. The kernel then blesses the bad observation as complete

`internal/harness/runner.go:61-71` executes the step, enriches the observation, calls `Decide`, and emits a final Forge response when the observation is not blocked.

`internal/harness/policy.go:27-37` makes the decision logic binary: blocked if the observation carries an error, complete otherwise.

If local execution returns malformed tool markup as plain response text, that still counts as `ObservationComplete`.

### 6. Runtime bridging makes the silent failure worse

`internal/runtime/chat.go:604-617` only emits a synthetic final response when the kernel produced one and the main agent did not already store a response, or when the step was a worker step.

If the main local path stores malformed tool markup as `LastResponse()`, runtime assumes the assistant already responded, so it does not synthesize a corrected user-facing answer.

## Evidence From The Latest Failure

Latest log checked:

- `/tmp/forge-debug-grok-theme.jsonl`

Observed sequence:

1. The user asked for theme mockups plus a running local web server.
2. The harness routed the turn into visible collaboration.
3. The model first made reasonable discovery calls and found `themes_preview.html`.
4. A `run_command` verification came back with `SERVER_LIVE:no`.
5. The next model response emitted raw `<tool_call>` markup containing only `args.command` and no `name`.
6. No tool was executed after that malformed response.
7. The harness trace still recorded `observation complete` and `turn complete`.
8. The user saw no useful final response.

This is the exact failure signature of a prompt-shaped main path running on a fail-open loop:

- the model had the right intention
- the tool-call serialization was wrong
- the host did not convert that into a retry, recovery, or safe failure

## Why The Last Redesign Missed This

The redesign missed the true issue because the design and the implementation diverged at the execution boundary:

1. The design correctly rejected prompt-authored orchestration.
2. The implementation preserved the generic `agent.Agent` loop for local turns.
3. The new kernel took over routing, session carry-forward, worker contracts, and transcript shaping, so it looked like the redesign had landed.
4. Real failures then concentrated on classifier and follow-up edge cases, which drew attention toward phrase coverage and pending-action logic.
5. The visible collaboration path remained prompt-enforced rather than contract-enforced, so every provider-specific tool-calling weakness still leaked through the same old loop.

In short:

- the outer kernel changed
- the inner visible execution contract did not

That is why this felt like the third design and still behaved like the first one under pressure.

## External Design Guidance

The external references are aligned on a few principles that matter directly here.

### 1. Start simple and add orchestration only when it demonstrably helps

OpenAI recommends an incremental approach, noting that a single agent can handle many tasks by adding tools and that code orchestration is more deterministic in speed, cost, and performance. Anthropic makes the same point more bluntly: the most successful agent systems use simple, composable patterns rather than complex frameworks, and developers should increase complexity only when needed.

Implication for Forge:

- default to one visible Forge assistant
- keep most turns local
- use extra agents only where the work is genuinely parallel, isolated, or independently verifiable

### 2. If you do use multiple agents, keep one manager in control of the user-facing answer

OpenAI distinguishes two useful patterns:

- manager-style "agents as tools" when one agent should own the final answer and shared guardrails
- handoffs when the specialist should become user-facing for the next segment

Forge's product direction is already the first pattern, not the second. That means internal workers should remain bounded helpers. They should not take over the visible conversation.

### 3. Separate contexts, tools, and permissions are the real value of subagents

Anthropic's subagent guidance emphasizes separate context windows, specific tool access, and independent permissions. It also says to stay in the main conversation when phases share heavy context, latency matters, or the change is quick and targeted.

Implication for Forge:

- workers should exist for bounded parallel research, isolated edit slices, or independent verification
- visible collaboration requests that depend on shared local context and immediate iteration should not be punted into model-authored shell orchestration

### 4. Tool design matters more than adding more tools

Anthropic's tooling guidance is directly relevant:

- more tools do not automatically improve outcomes
- clear names, clear schemas, and high-signal outputs reduce agent mistakes
- tool descriptions and specs need explicit, unambiguous contracts
- high-level tools should encode common workflows instead of forcing the model to compose low-level API or shell steps every time

Implication for Forge:

- `run_command` is too low-level to be the main preview/server/browser orchestration interface
- preview and artifact workflows need host-owned, typed tools

### 5. Security boundaries belong to the host, not to prompts

MCP's architecture is explicit:

- the host controls connection permissions, lifecycle, authorization decisions, and context aggregation
- roots define filesystem boundaries
- tool schemas are typed
- tool annotations are hints, not security controls
- OAuth 2.1 and PKCE are required for authorization flows

Implication for Forge:

- prompt text should never be the primary control mechanism for sensitive operations
- server lifecycle, preview ports, artifact exposure, and filesystem scope need host-owned state and enforcement

### 6. Multi-agent systems need explicit guardrails, observability, and effort budgets

Anthropic's multi-agent research write-up shows both the benefit and the risk:

- multi-agent improved breadth-first research because separate context windows and parallel work increased capacity
- coordination complexity grew rapidly
- explicit task descriptions, effort scaling rules, guardrails, and observability were necessary to keep the system from spiraling

Implication for Forge:

- multi-agent should be reserved for the cases that justify the coordination cost
- the host must own budgets, limits, retries, and stop conditions

### 7. Production harnesses need validation, HITL boundaries, and injection defenses

OWASP's agent guidance recommends:

- least privilege on tools
- explicit approvals for high-impact actions
- isolated memory/context
- structured outputs with schema validation
- monitoring and anomaly detection

OWASP's prompt-injection guidance is especially relevant for coding agents:

- remote/indirect prompt injection comes from external content the model reads
- agent-specific attacks include forged tool outputs, tool manipulation, and poisoned working memory

Implication for Forge:

- file contents, web content, logs, and generated artifacts must be treated as untrusted inputs
- state passed between agents must be validated and structured
- the system needs kill-switches, approvals, and safer handling for arbitrary shell execution

### 8. Tracing and evals are part of the architecture, not optional debug polish

OpenAI traces runs, agent executions, model generations, function tools, guardrails, and handoffs by default. Anthropic explicitly attributes progress on their multi-agent system to fast iteration loops with observability and test cases.

Implication for Forge:

- real-log regressions are not a cleanup task after the redesign
- they are the proof that the redesign actually removed the failure mode

## Recommended Target Architecture

### A. Keep one visible Forge, but split execution contracts

Forge should keep a single visible assistant identity.

But the runtime needs two different execution contracts:

1. Conversational contract
   Used for pure answers, policy/meta replies, and short user-facing prose.

2. Strict action contract
   Used for any turn that requires tools, artifacts, previews, inspection, or edits on the visible path.

Strict action turns should allow only:

- exactly one valid tool call
- or one explicit final response
- or one blocked result

Malformed tool markup, mixed prose-plus-invalid-action output, or empty action attempts must never be interpreted as success.

### B. Make preview and artifact workflows host-owned

Forge needs typed host tools for common collaboration flows instead of raw shell recipes.

Recommended tools:

- `artifact_write`
  Create a named artifact and return path, mime type, and stable handle.
- `artifact_read`
  Read back a tracked artifact by handle.
- `preview_server_ensure`
  Start or reuse a local preview server for a specific artifact directory and return structured status.
- `preview_server_status`
  Return whether the tracked preview is live, which port it owns, and the last verified path.
- `preview_open` or `browser_snapshot`
  Open or verify the preview target without forcing the model to hand-write curl or process-control shell.

The host should own:

- process lifecycle
- port selection
- PID tracking
- verification
- cleanup
- one-turn and cross-turn session state

### C. Persist runtime state for artifacts, previews, and verification

The session already carries evidence and pending actions.

It should also carry:

- active preview handle
- preview root directory
- tracked server PID or logical server token
- last verified URL/path
- last verification timestamp/result
- artifact handles created this turn

That removes the need for the model to rediscover or re-orchestrate environment state every turn.

### D. Reuse workers only where they are actually superior

Keep workers for:

- bounded parallel research
- sharply isolated edit slices
- independent verification

Do not use workers as the default answer path.

Do not use workers for ordinary sequential local collaboration unless the task can be cleanly isolated.

### E. Add host-side guardrails and lifecycle hooks

Forge needs explicit control points around tool use and subagent execution:

- pre-tool validation and deny/allow/transform
- tool-class-specific validators
- post-tool state validation
- stop/subagent-stop auditing
- consequence-based approval gates

The existing path bounding is useful, but it is not enough for preview servers, shell orchestration, or multi-agent coordination.

## Phased Migration Plan

### Phase 0: Stop the bleed

Goal:
Make the current local path fail closed on malformed action output.

Changes:

- `internal/agent/agent.go`
  Treat malformed tool markup on the main local path as retryable failure, not final answer.
- `internal/harness/local.go`
  Reject raw tool markup as a successful completed local observation.
- `internal/harness/policy.go`
  Add a distinct blocked reason for malformed-action local turns.
- `internal/runtime/chat.go`
  Ensure the user receives a real blocked/error response rather than silence.

Success criteria:

- malformed main-path tool markup never yields `observation complete`
- the same Grok theme log now ends in retry, recovery, or explicit blocked response

### Phase 1: Add host-owned preview and artifact tools

Goal:
Remove shell-authored preview lifecycle from the common path.

Changes:

- create `internal/agent/tools/preview_*.go` or equivalent runtime-owned preview tool package
- add structured tool types and tests
- extend session/runtime state to persist preview handles
- keep `run_command` available for genuine shell work, but not as the default preview/server interface

Success criteria:

- mockup/theme/preview requests no longer require `pkill`, `python -m http.server`, or curl hand-authored by the model
- preview follow-up turns reuse stored state

### Phase 2: Introduce a strict visible-action executor

Goal:
Stop using the generic free-form `Agent.Run(...)` loop for visible collaboration and tool-heavy local turns.

Changes:

- add a new strict local executor path under `internal/harness`
- reuse parser/tool infrastructure, but enforce one-step typed outputs
- use the generic conversational loop only for pure answer/meta turns

Success criteria:

- visible collaboration has the same fail-closed guarantees that workers already have
- provider-specific tool-call formatting failures no longer silently complete the turn

### Phase 3: Align worker and local contracts

Goal:
Use one contract model across strict local execution and hidden workers.

Changes:

- unify step/result typing where possible
- ensure both paths support retries, validation, structured artifacts, and blocked results
- keep workers stricter where isolation is required, but not fundamentally different in safety semantics

Success criteria:

- local strict turns and worker turns behave consistently under malformed output, retries, and tool validation

### Phase 4: Add security and lifecycle guardrails

Goal:
Make dangerous or stateful operations host-controlled.

Changes:

- pre-tool policy for shell, filesystem writes, and networked preview operations
- better isolation for agent-to-agent/result passing
- explicit risk-level approvals
- injection-aware handling for external content and tool outputs

Success criteria:

- dangerous actions are approval-gated
- indirect prompt injection from files/logs/web content is handled as untrusted input
- multi-agent state passing is structured and validated

### Phase 5: Lock the redesign down with evals

Goal:
Prove the redesign fixes the real failure mode.

Required eval classes:

- malformed main-path tool markup
- preview/server lifecycle recovery
- visible-collaboration long runs
- provider-specific tool-calling variance
- follow-up reuse of preview/server state
- prompt injection in logs, file contents, and fetched content
- routing paraphrase regressions

## What Not To Do Next

Do not spend another round on:

- widening phrase matchers for preview/server asks
- adding more visible-collaboration prompt bullets
- teaching the model ever more specific shell recipes for servers
- patching parser heuristics without changing main-path completion semantics

Those can reduce symptoms, but they do not change the failure boundary.

## Confidence Assessment

High confidence:

- the current failure is architectural
- the main visible path still lacks the invariants already present in the worker path
- host-owned preview/server lifecycle is the right direction

Moderate confidence:

- the strict visible-action executor can reuse parts of `agent.Agent` rather than requiring a full replacement
- preview tooling should live under the existing agent tool registry rather than as an entirely separate runtime subsystem

Main remaining risk:

- the migration will touch the boundary between runtime, harness, tool registry, and TUI event reporting, so the first pass must be backed by real-log regressions instead of only unit tests

## External References

- OpenAI, "A practical guide to building agents"
  https://openai.com/business/guides-and-resources/a-practical-guide-to-building-ai-agents/
- OpenAI Agents SDK, "Agent orchestration"
  https://openai.github.io/openai-agents-python/multi_agent/
- OpenAI Agents SDK, "Guardrails"
  https://openai.github.io/openai-agents-python/guardrails/
- OpenAI Agents SDK, "Tracing"
  https://openai.github.io/openai-agents-python/tracing/
- Anthropic, "Building effective agents"
  https://www.anthropic.com/engineering/building-effective-agents
- Anthropic, "How we built our multi-agent research system"
  https://www.anthropic.com/engineering/multi-agent-research-system
- Anthropic Claude Code docs, "Create custom subagents"
  https://code.claude.com/docs/en/sub-agents
- Anthropic Claude Code docs, "Hooks reference"
  https://code.claude.com/docs/en/hooks
- Anthropic Claude Code docs, "Extend Claude with skills"
  https://code.claude.com/docs/en/slash-commands
- Anthropic, "Writing effective tools for AI agents"
  https://www.anthropic.com/engineering/writing-tools-for-agents
- Model Context Protocol specification, "Architecture"
  https://modelcontextprotocol.io/specification/2025-03-26/architecture
- Model Context Protocol specification, "Tools"
  https://modelcontextprotocol.io/specification/2025-03-26/server/tools
- Model Context Protocol specification, "Roots"
  https://modelcontextprotocol.io/specification/2025-03-26/client/roots
- Model Context Protocol specification, "Authorization"
  https://modelcontextprotocol.io/specification/2025-03-26/basic/authorization
- OWASP Cheat Sheet, "AI Agent Security"
  https://cheatsheetseries.owasp.org/cheatsheets/AI_Agent_Security_Cheat_Sheet.html
- OWASP Cheat Sheet, "LLM Prompt Injection Prevention"
  https://cheatsheetseries.owasp.org/cheatsheets/LLM_Prompt_Injection_Prevention_Cheat_Sheet.html
- NIST AI 100-1, "Artificial Intelligence Risk Management Framework (AI RMF 1.0)"
  https://www.nist.gov/publications/artificial-intelligence-risk-management-framework-ai-rmf-10
- NIST AI 600-1, "Artificial Intelligence Risk Management Framework: Generative Artificial Intelligence Profile"
  https://www.nist.gov/publications/artificial-intelligence-risk-management-framework-generative-artificial-intelligence
- Yao et al., "ReAct: Synergizing Reasoning and Acting in Language Models"
  https://arxiv.org/abs/2210.03629
- Schick et al., "Toolformer: Language Models Can Teach Themselves to Use Tools"
  https://arxiv.org/abs/2302.04761
