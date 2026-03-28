# OpenCode, Codex, and Forge: Architectural Comparison

**Date:** 2026-03-28  
**Purpose:** Understand why OpenCode and Codex perform well and Forge struggles, to inform Forge's next redesign.  
**Sources:** All Codex analysis is from verified source code fetched via GitHub API. OpenCode analysis from source code on GitHub. Forge from local codebase.

---

## Summary

Both OpenCode (TypeScript) and Codex (Rust) use a **simple ReAct loop**: call the LLM, execute tool calls, feed results back, repeat until the model stops calling tools. Neither has a classifier, planner, policy engine, or hidden workers with tool allowlists. Safety comes from **sandboxing + approvals**, not from restricting what the LLM can see or do.

Forge (Go) has a **7-stage state machine**, **4 worker types with hardcoded tool allowlists**, **deterministic token-based classification**, **structured JSON output contracts with retry logic**, and **outcome normalization**. This complexity is the primary source of its reliability problems.

**The pattern across all successful coding agents is the same: trust the LLM, sandbox the execution.**

---

## Architecture Comparison

### OpenCode: Flat Agent Loop (TypeScript)

```
User Input
  → System Prompt (environment + skills + instructions)
  → LLM generates response (may include tool calls)
  → Execute tool calls (permission-gated)
  → If tool calls → loop back to LLM
  → If no tool calls → done, return response
```

**Core harness: ~1,850 lines total.**

### Codex: ReAct Loop with Sandboxed Execution (Rust)

```
User Input
  → Build system prompt (instructions + skills + plugins + connectors + environment)
  → Submission loop (event dispatch: while let Ok(sub) = rx_sub.recv())
    → UserInput → spawn_task → run_turn
      → run_turn inner loop:
        → Stream LLM response
        → Parse tool calls from response items
        → Execute via ToolOrchestrator (sandbox + approval + hooks)
        → Feed results back to LLM
        → If model needs follow-up → continue loop
        → If model done → break, emit TurnComplete
      → Auto-compact context if token limit reached mid-turn
  → TurnComplete event
```

**Core harness: ~291KB in codex.rs alone** (but most is configuration, hook routing, and session management — the actual loop is a simple `while let` + inner `loop {}`).

### Forge: Harness Kernel State Machine (Go)

```
User Input
  → Intake (validate, enrich)
  → Classify (deterministic token matching → Family/Intent)
  → Plan (select Step: Local/StrictLocal/Worker/VisibleCollaboration)
  → Act (execute step via appropriate executor)
  → Observe (validate structured output, normalize)
  → Decide (route: Complete/Blocked/Retry/Replan/AwaitingFeedback)
  → Respond (format and return)
  → If Retry/Replan → loop back to appropriate stage
```

**Core harness: ~16,100 lines across ~18 files.**

---

## Detailed Comparison Tables

### Agent Loop

| Aspect | OpenCode | Codex | Forge |
|--------|----------|-------|-------|
| **Loop type** | Simple while loop | Event dispatch + inner ReAct loop | 7-stage state machine |
| **Entry point** | `session/prompt.ts` | `submission_loop()` in `codex.rs` | `runner.Run()` in `harness/runner.go` |
| **Turn model** | One turn = one model call sequence | One turn = spawn_task → run_turn → inner loop | One turn = intake→classify→plan→act→observe→decide→respond |
| **Continuation** | Tool calls → loop | `needs_follow_up` → continue | `OutcomeRetry` / `OutcomeReplan` → re-enter state machine |
| **Termination** | No tool calls → done | `!needs_follow_up` → break | `StateComplete` / `StateBlocked` |
| **Error handling** | Fail fast | Retry with backoff + transport fallback | Nested retry loops (worker 3× + agent 30× + kernel retries) |

### Classification / Intent Routing

| Aspect | OpenCode | Codex | Forge |
|--------|----------|-------|-------|
| **Has classifier** | No | No | Yes (1200 lines, token matching) |
| **How intent is determined** | LLM decides via tool selection | LLM decides via tool selection | Deterministic token matching before LLM call |
| **Classification families** | N/A | N/A | `FamilyAnswer/Inspect/Implement/Verify/Research` |
| **Can misroute** | N/A | N/A | Yes — "make a branch" → `FamilyAnswer` instead of `FamilyImplement` |

### Tool Access

| Aspect | OpenCode | Codex | Forge |
|--------|----------|-------|-------|
| **Tool selection** | All tools available | All tools available (filtered by feature flags, not agent role) | Hardcoded allowlists per worker type |
| **Tool filtering** | Permission system (configurable) | Feature flags + model capabilities + session source | Hardcoded switch statement per worker |
| **Can worker edit files** | N/A (no workers) | Yes (all agents have apply_patch + shell) | WorkerEditor: yes, WorkerReader: no |
| **Can worker run commands** | N/A | Yes (all agents have shell/exec_command) | WorkerEditor: yes, WorkerVerifier: yes, WorkerReader: no |
| **Can worker git commit** | N/A | Yes (via shell) | No worker has `git_commit` |
| **Can worker access web** | N/A | Yes (web_search tool) | Only WorkerResearcher has `web_search`/`web_fetch` |
| **Can worker use previews** | N/A | N/A | No worker has preview tools |
| **User-configurable tools** | Yes (permission config) | Yes (feature flags + .rules files + exec policy) | No (hardcoded in Go) |

### Safety / Execution Model

| Aspect | OpenCode | Codex | Forge |
|--------|----------|-------|-------|
| **Safety approach** | Permission system | Sandboxing + approvals + Guardian | Tool restriction + structured output validation |
| **Filesystem sandbox** | None | Landlock (Linux), Seatbelt (macOS), Restricted tokens (Windows) | None |
| **Network sandbox** | None | Yes (domain allow/deny, network proxy) | None |
| **Approval policy** | Configurable per tool/action | `Never/OnFailure/OnRequest/UnlessTrusted/Granular` | None |
| **Auto-reviewer** | None | Guardian (LLM-based, risk-score < 80 → approve) | None |
| **Execution policy** | None | `.rules` files (Allow/Prompt/Forbidden per command prefix) | None |
| **Hook system** | None | Pre/post tool hooks, session start hooks, user prompt hooks | None |
| **Sandbox retry** | N/A | First attempt under sandbox → on denial, ask approval → retry without sandbox | N/A |

### Sub-Agent / Delegation Model

| Aspect | OpenCode | Codex | Forge |
|--------|----------|-------|-------|
| **Sub-agent dispatch** | LLM-driven via `task` tool | LLM-driven via `spawn_agent` tool | Host-driven via harness classification |
| **Sub-agent creation** | New session with own tools | `Codex::spawn()` — full new session with own state | Worker dispatch with restricted tools |
| **Sub-agent tools** | Own permission scope | Full tools (gated by features/flags, not restricted by parent) | Subset of parent tools (hardcoded allowlist) |
| **Sub-agent result format** | Plain text (no validation) | Plain text (no structured JSON contract) | Strict JSON (validated, retried 3× on failure) |
| **Sub-agent isolation** | Separate session | Separate `Codex` instance with own channels | Worker runs in same process, shares agent state |
| **Parallel sub-agents** | Yes (via `task` tool) | Yes (`spawn_agent` + `wait_agent`, CSV batch spawn) | No (workers sequential) |
| **Depth limiting** | N/A | `agent_max_depth` enforced, features disabled at limit | N/A |
| **Sub-agent naming** | `@general`, `@explore` | Auto-assigned from pool (`agent_names.txt`), hierarchical paths | `WorkerReader/Editor/Verifier/Researcher` |

### Context Management

| Aspect | OpenCode | Codex | Forge |
|--------|----------|-------|-------|
| **History management** | Simple message array | `SessionState` with mutex-protected history | Dual state (agent history + kernel session state) |
| **Compaction** | Basic truncation | Auto-compact (inline local + remote server-side), pre-sampling + mid-turn | History budget truncation |
| **Compaction trigger** | Token limit | `auto_compact_token_limit` + model switch + mid-turn overflow | Max tokens per turn |
| **Cross-turn context** | History preserved | History preserved + `CompactedItem` replaces old messages | Agent ↔ kernel state desync (separate state systems) |

### Error Handling

| Aspect | OpenCode | Codex | Forge |
|--------|----------|-------|-------|
| **Retry logic** | None (fail fast) | Retry with exponential backoff on stream disconnect, transport fallback (WS→HTTPS) | Nested retries: worker 3×, agent 30×, kernel Decide retries |
| **Circuit breaker** | None | None (but non-retryable errors break immediately) | None (can create 90+ attempt loops) |
| **Retryable errors** | N/A | Stream disconnections | JSON validation failures, tool errors |
| **Non-retryable errors** | All | `ContextWindowExceeded`, `UsageLimitReached`, `TurnAborted` | None (everything retries) |
| **Error sent to LLM** | Yes (tool error → model sees it) | Yes (`FunctionCallError::RespondToModel`) | Sometimes (validation failures hidden from model) |
| **Fail-closed** | N/A | Guardian: any parse error → high-risk denial | Workers: after 3 retries → blocked, agent retries |

---

## What Codex Has That Neither OpenCode Nor Forge Has

| Feature | Codex | OpenCode | Forge |
|---------|-------|----------|-------|
| **Filesystem sandboxing** | Landlock/Seatbelt/Restricted Tokens | None | None |
| **Network sandboxing** | Domain allow/deny + proxy | None | None |
| **Guardian auto-reviewer** | LLM-based risk assessment (risk_score < 80 → approve) | None | None |
| **Execution policy rules** | `.rules` files with Allow/Prompt/Forbidden | None | None |
| **Pre/post tool hooks** | Hook system (FailedContinue/FailedAbort) | None | None |
| **Parallel sub-agents** | `spawn_agent` + `wait_agent` + CSV batch | `task` tool | No (sequential workers) |
| **Agent depth limiting** | `agent_max_depth` with feature disabling | None | None |
| **Agent registry** | `AgentRegistry` with nicknames, metadata, tracking | None | None |
| **Remote compaction** | Server-side context compression | None | None |
| **Dynamic tools (MCP)** | Full MCP support with namespaced tools | None | None |
| **Code mode / JS REPL** | Sandboxed code execution environments | None | None |
| **Image generation** | Conditional tool | None | None |
| **File watching** | Background file watcher events | None | None |
| **Rollout recording** | Full session recording for replay/debugging | None | None |
| **Session persistence** | DB-backed session state | None | None |
| **Transport fallback** | WebSocket → HTTPS on failure | None | None |
| **Tool search** | Dynamic tool discovery at runtime | None | None |
| **Permission requests** | LLM can request additional filesystem/network permissions | None | None |

## What Forge Has That Neither OpenCode Nor Codex Has

| Feature | Forge | OpenCode | Codex |
|---------|-------|----------|-------|
| **Deterministic classifier** | Token-matching classifier (1200 lines) | None | None |
| **Harness state machine** | 7-stage pipeline | None | None |
| **Hidden workers** | 4 worker types with tool allowlists | None | None |
| **Structured output contracts** | Strict JSON validation with retry | None | None |
| **Thread phase model** | ideate/apply phases | None | None |
| **Outcome normalization** | Observation → Decision routing | None | None |
| **Claim-evidence guard** | Validates side-effect claims against tool evidence | None | None |
| **Worker tool allowlists** | Hardcoded per worker type | None | None |

---

## Why Codex and OpenCode Work Better Than Forge

### Principle 1: Trust the Model for Intent

Both Codex and OpenCode let the LLM decide what tools to call. The model is better at understanding "make a branch" means "run git checkout -b" than a token matcher that looks for the word "branch" in the input.

Forge's classifier intercepts before the LLM call and can misroute. When it does, the wrong worker type gets dispatched, the wrong tools are available, and the task fails.

### Principle 2: Sandbox Execution, Not Tool Access

Codex's safety model is "give the agent full access, but run dangerous operations in a sandbox and ask for approval." This means:
- The agent can always complete its task
- Safety is enforced at execution time, not tool-registration time
- The agent gets useful error messages from the sandbox ("permission denied") that help it adapt

Forge's safety model is "restrict which tools the agent can see." This means:
- The agent can't complete tasks that require tools outside its allowlist
- The agent can't even try the right approach
- No useful error feedback ("tool not found" vs "permission denied")

### Principle 3: Plain Text Results

Codex sub-agents return their final response as plain text. The parent LLM reads it and decides what to do. No JSON schema validation, no retry loops, no blocked observations.

Forge workers must return strict JSON. Validation failures cascade into 3 retries per worker, then the agent retries the worker, creating 90+ attempt loops for formatting issues.

### Principle 4: LLM-Driven Delegation

In Codex, the LLM itself decides when to delegate via `spawn_agent`. It writes the task description, selects the role, and interprets the result. The LLM is the orchestrator.

In Forge, the harness decides when to delegate based on token matching. The harness selects the worker type, constructs the task, and validates the result. The host is the orchestrator, and it's worse at it than the LLM.

### Principle 5: Parallelism Where Possible

Codex supports parallel sub-agents (`spawn_agent` doesn't block) and CSV batch spawning. Multiple agents can work simultaneously.

Forge workers are sequential. The harness dispatches one worker at a time, waits for results, then decides what to do next.

### Principle 6: Error Recovery with Context

Codex sends tool errors back to the LLM as context (`FunctionCallError::RespondToModel`). The model sees what went wrong and adapts. Sandbox denials include helpful error messages.

Forge hides validation failures from the model. The worker gets a vague "Your previous output was invalid" retry prompt. The model can't adapt because it doesn't know what went wrong.

---

## Codex's Guardian: A Key Innovation

The Guardian is Codex's most interesting architectural feature for Forge to study. It's an **automated approval reviewer** — a separate LLM session that evaluates tool calls:

1. Agent wants to execute a command
2. If approval policy is `OnRequest`, the Guardian reviews instead of prompting the user
3. Guardian builds a compact transcript (recent messages + planned action)
4. Guardian LLM (prefers `gpt-5.4`) produces a risk assessment: `{risk_level, risk_score, rationale, evidence}`
5. If `risk_score < 80` → auto-approve. Otherwise → deny with "do not attempt workarounds" message
6. Guardian **always fails closed**: timeout, parse error, or any failure → high-risk denial

The Guardian is locked down:
- `approval_policy = Never` (can't execute commands itself)
- `sandbox_policy = ReadOnly` (can't write files)
- No sub-agent features (can't spawn agents)
- 90-second timeout
- Cached trunk session for prompt-cache efficiency

**This is the right way to add safety without restricting capability.** Forge could implement something similar instead of restricting worker tool access.

---

## Lines of Code Comparison

| Component | OpenCode | Codex | Forge |
|-----------|----------|-------|-------|
| Core orchestrator | ~700 | 291KB (`codex.rs`)* | ~600 (`runner.go`) |
| Agent definitions | ~300 | 8KB (`codex_thread.rs`) | ~800 (`agent.go` + `roles.go`) |
| System prompts | ~50 | Dynamic (built from config) | ~2,500 (`system.go`) |
| Tool registry | ~200 | ~200KB (`client.rs` + `tools/`)* | ~500 |
| Classification | 0 | 0 | ~1,200 (`classifier.go`) |
| Worker execution | 0 | 0 | ~250 (`workers.go`) |
| Worker validation | 0 | 0 | ~400 (`contracts.go`) |
| State machine | 0 | 0 | ~600 (policy + types) |
| Thread management | 0 | 0 | ~600 (`thread.go`) |
| Sub-agent system | ~300 | 29KB (`codex_delegate.rs`) | ~700 (`subagent.go` + `delegate.go`) |
| Sandbox/approval | 0 | ~100KB (`exec.rs` + `exec_policy.rs` + `sandboxing/`)* | 0 |
| Guardian reviewer | 0 | ~20KB (`guardian/`) | 0 |
| Skills system | 0 | ~30KB (`skills/`) | 0 |
| MCP tools | 0 | ~40KB (`mcp/`) | 0 |
| **Total** | **~1,850** | **~800KB*** | **~16,100** |

*Codex's `codex.rs` is a monolithic file containing session management, prompt building, hook routing, turn lifecycle, compaction, streaming, tool orchestration, and sub-agent management. It's large but structurally simple — an event dispatch loop with an inner ReAct loop.

---

## Lessons for Forge's Redesign

### Must Adopt (High Confidence)

1. **Remove the classifier** — Let the LLM classify intent via tool selection. Token matching is brittle and adds latency.

2. **Remove worker tool allowlists** — Give all agents access to all tools. Use a permission/approval system for safety instead.

3. **Remove structured worker output contracts** — Sub-agents should return plain text. The parent LLM reads and interprets.

4. **Replace state machine with ReAct loop** — Simple `while (tool_calls) { execute; feed_back; }` loop. No 7-stage pipeline.

5. **Add sandboxing** — Use OS-level sandboxing (landlock/seatbelt) to restrict file/network access during execution, not tool access during registration.

### Should Consider (Medium Confidence)

6. **Add approval policies** — Configurable `Never/OnFailure/OnRequest` for command execution. Like Codex's `AskForApproval`.

7. **Add execution policy rules** — `.rules` files for command-level Allow/Prompt/Forbidden. User-configurable without code changes.

8. **Add LLM-driven delegation** — `spawn_agent` tool where the LLM decides when and how to delegate.

9. **Add parallel sub-agents** — Don't block on worker completion. Let multiple agents run simultaneously.

10. **Add a Guardian reviewer** — Auto-approve low-risk commands, deny high-risk ones. Fails closed.

### Nice to Have (Lower Priority)

11. **Add pre/post tool hooks** — Extensible hook system for custom safety/validation logic.

12. **Add MCP tool support** — Dynamic tools from external servers.

13. **Add skills system** — Load instruction documents from `.md` files instead of hardcoded prompts.

14. **Add context compaction** — Auto-compact when token limit reached mid-turn.

15. **Add session persistence** — DB-backed session state for resume.

16. **Add transport fallback** — WebSocket → HTTPS fallback on connection failures.

---

## References

### Codex (OpenAI)
- `codex-rs/core/src/codex.rs` — Main orchestrator (291KB), contains `Codex`, `Session`, `TurnContext`, `submission_loop()`, `run_turn()`, `run_sampling_request()`
- `codex-rs/core/src/codex_thread.rs` — Thread/stream wrapper (8KB)
- `codex-rs/core/src/codex_delegate.rs` — Sub-agent spawning (29KB), `run_codex_thread_interactive()`, `run_codex_thread_one_shot()`
- `codex-rs/core/src/client.rs` — LLM client (70KB), streaming, tool call parsing
- `codex-rs/core/src/tools/` — Tool router, registry, orchestrator, parallel execution
- `codex-rs/core/src/tools/spec.rs` — Tool definitions and `ToolsConfig`
- `codex-rs/core/src/exec.rs` — Command execution (40KB)
- `codex-rs/core/src/exec_policy.rs` — Execution policy rules (31KB)
- `codex-rs/core/src/sandboxing/` — OS-level sandbox implementations
- `codex-rs/core/src/guardian/` — Auto-reviewer (prompt, review session, approval request)
- `codex-rs/core/src/agent/` — Agent control, registry, roles
- `codex-rs/core/src/skills/` — Skill loading and injection
- `codex-rs/core/src/mcp/` — MCP tool support
- `codex-rs/core/src/compact.rs` — Context compaction (16KB)

### OpenCode (Anomaly)
- `packages/opencode/src/agent/agent.ts` — Agent definitions (~300 lines)
- `packages/opencode/src/tool/tool.ts` — Tool interface (~100 lines)
- `packages/opencode/src/tool/registry.ts` — Tool registry
- `packages/opencode/src/tool/task.ts` — Sub-agent task tool
- `packages/opencode/src/session/prompt.ts` — Main session loop

### Forge (Local)
- `internal/harness/runner.go` — Harness kernel orchestrator
- `internal/harness/classifier.go` — Token-based classification
- `internal/harness/workers.go` — Worker execution
- `internal/harness/contracts.go` — Structured output validation
- `internal/harness/policy.go` — Decision logic
- `internal/agent/agent.go` — Agent loop
- `internal/agent/system.go` — System prompt builder
- `ARCHITECTURE_ISSUES.md` — Dual-architecture analysis
- `KERNEL_ISSUES.md` — Kernel-specific issues
