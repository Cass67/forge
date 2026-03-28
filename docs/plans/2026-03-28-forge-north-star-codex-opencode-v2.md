# Forge North Star: Codex/OpenCode Path (v2)

**Date:** 2026-03-28  
**Branch:** `forge/north-star-codex-opencode`  
**Intent:** Build a Forge runtime that is fluid to use day-to-day while preserving production safety boundaries.  
**Source-grounded:** Verified against `openai/codex` (`codex-rs/core/src/`) and `sst/opencode` (`packages/opencode/src/`) source code.

---

## Product North Star

Forge should feel like one capable coding agent that:

1. Understands the request directly.
2. Chooses tools directly.
3. Delegates only when useful.
4. Streams meaningful progress while working.
5. Finishes with grounded, verifiable outcomes.

Target behavior is Codex/OpenCode-like fluidity: **one ReAct loop, model-led tool selection, host-owned safety boundaries.**

---

## What Codex and OpenCode Actually Do (Source-Verified)

### Codex (`codex-rs/core/src/`)

**Loop:** `submission_loop()` is a `while let Ok(sub) = rx_sub.recv().await` event dispatch. Each `UserInput` op spawns a `RegularTask` which enters `run_turn()` containing an inner `loop {}`:

```
run_turn():
  build prompt (instructions + skills + plugins + connectors + env)
  loop {
    stream model response
    parse tool calls from response items
    execute via ToolOrchestrator (sandbox + approval + hooks)
    feed results back to model
    if !needs_follow_up → break
    if token_limit && needs_follow_up → auto-compact, continue
  }
  emit TurnComplete
```

**Safety stack (NOT tool restriction):**
- `SandboxPolicy` (ReadOnly/WorkspaceWrite/DangerFullAccess) backed by OS-level enforcement (Landlock, Seatbelt, Restricted Tokens)
- `AskForApproval` policy (Never/OnFailure/OnRequest/UnlessTrusted/Granular) with `.rules` files for command-level Allow/Prompt/Forbidden
- `Guardian` auto-reviewer: separate LLM session evaluates risk_score 0-100, approves if < 80, always fails closed
- `ToolOrchestrator`: first attempt under sandbox → on denial, ask approval → retry without sandbox
- Pre/post tool hooks with FailedContinue/FailedAbort outcomes

**Delegation:** Model-driven via `spawn_agent` tool. Sub-agents are full `Codex::spawn()` instances with own session/channels. Parallel via `spawn_agent` + `wait_agent`. Depth limited by `agent_max_depth`. Results are plain text, no structured JSON contract.

**Tool access:** All tools available to all agents. Filtered by feature flags and model capabilities, NOT by agent role. `ToolsConfig::new()` checks features, model_info, session_source, sandbox_policy — no per-role allowlists.

**Error handling:** Retry with exponential backoff on stream disconnect. Transport fallback (WebSocket → HTTPS). Non-retryable errors (ContextWindowExceeded, TurnAborted) break immediately. Tool errors sent back to LLM as `FunctionCallError::RespondToModel`.

### OpenCode (`packages/opencode/src/`)

**Loop:** `while (true)` in `session/prompt.ts`. Stream model response → parse tool calls → execute → feed back → repeat until model emits no tool calls.

**Safety:** Permission system — declarative, composable, overridable per agent and session. `deny by default, ask before running bash`.

**Delegation:** Model-driven via `task` tool. Sub-agents get own session, model, tools. Results are plain text in `<task_result>` wrapper.

---

## What This Means for Forge

### The Core Insight

Both Codex and OpenCode succeed because they follow one principle:

> **The LLM decides what to do. The host decides whether it's allowed.**

Codex enforces "whether it's allowed" via OS sandboxing + approval policies + Guardian. OpenCode via permission system. Both give the LLM full tool access and let it choose.

Forge currently does the inverse: the host decides what to do (classify → plan → dispatch worker), then restricts what the LLM can use (tool allowlists), then validates how the LLM formats its answer (structured JSON contracts). Each host-side interception is a failure point.

### The Specific Problems in Current `internal/harness/`

| Current Mechanism | Problem | Codex/OpenCode Equivalent |
|---|---|---|
| `Classify()` token matching (classifier.go, 48KB) | Misroutes intent ("make a branch" → FamilyAnswer). Adds latency before LLM call. Requires code changes for new patterns. | No classifier. LLM decides via tool selection. |
| `Plan()` step selection (StepLocal/StrictLocal/Worker) | Dispatches workers before LLM sees task. Wrong step = wrong tools = task fails. | No planner. LLM calls tools directly. |
| `workerToolAllowlist()` hardcoded per worker | Workers can't complete objectives (no git_commit for editor, no web_search for verifier). | All tools available to all agents. Safety via sandbox/approval. |
| Structured JSON output contract (`contracts.go`) | Workers fail for formatting issues. 3 retries with vague "invalid output" nudge. Nested retry loops (worker 3× + agent 30×). | Sub-agents return plain text. No JSON validation. |
| 7-stage state machine (Intake→Classify→Plan→Act→Observe→Decide→Respond) | Each stage is a failure point. Decide can retry/replan creating loops. No circuit breaker. | Simple `loop { stream → execute → continue_or_break }`. No state machine. |
| Dual state (agent history + kernel session) | Context loss between agent and kernel. Follow-up turns lose context. | Single session state. History preserved across turns. |
| `EmitSyntheticResponse()` | Injects fake assistant message into agent history. Agent thinks it said things it didn't. | No synthetic injection. Sub-agent results are real tool outputs. |

---

## Architectural Direction

### 1) ReAct Core Loop

Replace the 7-stage state machine with one loop:

```
func (r *Runner) Run(ctx context.Context, input string) (TurnResult, error) {
    prompt := r.buildPrompt(input)
    for {
        response, toolCalls := r.model.Stream(ctx, prompt)
        r.emitProgress(response)
        
        if len(toolCalls) == 0 {
            return r.finalize(response), nil
        }
        
        for _, call := range toolCalls {
            result, err := r.executeTool(ctx, call)
            if err != nil {
                result = formatToolError(call, err)
            }
            prompt = append(prompt, toolResult(call, result))
        }
        
        if r.tokenBudgetExceeded() {
            prompt = r.compact(prompt)
        }
    }
}
```

No pre-LLM classification. No step planning. No structured output validation. The model decides what tools to call. The host executes them safely.

### 2) Approval Policy System (Not Tool Allowlists)

Replace `workerToolAllowlist()` with a configurable approval system:

```go
type ApprovalPolicy int

const (
    ApprovalNever       ApprovalPolicy = iota  // Auto-approve everything
    ApprovalOnFailure                           // Ask only when sandbox denies
    ApprovalOnRequest                           // Ask for every risky action
    ApprovalUnlessTrusted                       // Ask unless command is known-safe
)

type ApprovalRule struct {
    CommandPrefix []string       // ["git", "push"]
    Decision      RuleDecision   // Allow / Prompt / Forbidden
}

type ApprovalConfig struct {
    DefaultPolicy ApprovalPolicy
    Rules         []ApprovalRule
    SandboxPolicy SandboxPolicy  // ReadOnly / WorkspaceWrite / DangerFullAccess
}
```

Loaded from config (not hardcoded). User can add rules without code changes. The host enforces boundaries, not tool access.

### 3) Model-Driven Delegation

Replace host-dispatched workers with a `spawn_agent` tool:

```go
// Tool definition available to the LLM
var SpawnAgentTool = tool.Define("spawn_agent", {
    Description: "Spawn a sub-agent for a well-scoped task",
    Parameters: {
        task_description: string,
        role: "default" | "explorer" | "worker",
    },
    Execute: func(args, ctx) {
        subAgent := r.session.SpawnAgent(args.task_description, args.role)
        result := subAgent.Run(ctx)  // plain text result
        return result, nil           // no JSON validation
    },
})
```

Sub-agents are full agent instances (like Codex's `Codex::spawn()`). They have access to all tools within their approval scope. Results are plain text. The parent LLM interprets results and decides what to do next.

### 4) Error Recovery

Replace nested retry loops with Codex's approach:
- **Retryable errors** (stream disconnect): retry with exponential backoff
- **Non-retryable errors** (context exceeded, cancelled): break immediately
- **Tool errors**: send back to LLM as context (`FunctionCallError::RespondToModel`)
- **No structured output validation retries**: sub-agents return plain text
- **Circuit breaker**: after 5 consecutive same-type failures, stop and report

### 5) Single Session State

Replace dual state (agent history + kernel session) with one unified session:
- One conversation history
- One trace log
- Sub-agent results injected as tool outputs (not synthetic assistant messages)
- Context compaction when token budget exceeded mid-turn

---

## What We Keep From Current Forge

These are valuable and should be preserved:

1. **Claim-evidence guard** — validates side-effect claims match tool evidence. Codex doesn't have this. Good innovation.
2. **Thread phase model (ideate/apply)** — useful for preview/edit workflows. Codex doesn't have this explicitly but achieves it via approval policies.
3. **Protected-branch policy** — prevents direct edits on main/master. Codex achieves this via sandbox policies.
4. **Trace/recording system** — full traceability for debugging. Codex has rollout recording; Forge's is good.
5. **Skill injection** — loading instruction documents into prompts. Codex has this too.

---

## What We Remove

| Component | Reason | Replacement |
|---|---|---|
| `classifier.go` (48KB) | Token matching is brittle, adds latency | LLM decides via tool selection |
| `Plan()` step selection | Dispatches before LLM sees task | LLM calls tools directly |
| `workerToolAllowlist()` | Prevents task completion | Approval policy system |
| Structured JSON contracts | Causes nested retry loops | Plain text sub-agent results |
| `EmitSyntheticResponse()` | Breaks conversation continuity | Tool outputs as real messages |
| State machine (7 stages) | Each stage is a failure point | Simple ReAct loop |
| `outcome.go` normalization | Masks real errors | Direct error reporting |
| Dual session state | Context loss | Unified session state |

---

## Migration Plan (Phased)

## Implementation Status (2026-03-28)

- Phase 0: complete
  - baseline report added at `docs/reports/2026-03-28-react-runtime-baseline.md`
  - transcript/stress fixtures remain runnable in CI (`100+` prompt corpus, `50`-turn flow)
- Phase A: complete
  - `FORGE_CHAT_RUNTIME=react` runtime path added (`internal/react/*`, `internal/runtime/chat.go`)
- Phase B: complete (staged sandboxing)
  - config-backed approval/rules/sandbox gate added (`internal/react/approval*.go`, `internal/react/sandbox.go`, config `[approval]`)
  - protected-branch auto-transition enforced for mutating actions in react mode
- Phase C: complete
  - model-driven delegation tools added: `spawn_agent`, `wait_agent`
  - async agent pool wired to existing sub-agent runtime (`internal/react/agent_pool.go`, `internal/react/tools/*`, runtime wiring)
- Phase D: complete (session compaction scaffold)
  - session compaction and progress heartbeat integrated in react runner (`internal/react/compact.go`, `loop.go`, `session.go`)
- Phase E: complete for runtime default switch
  - default chat runtime switched to react; `FORGE_CHAT_RUNTIME=kernel` remains fallback
  - destructive legacy cleanup is intentionally deferred for soak and rollback safety

### Phase 0: Baseline And Golden Fixtures

Capture current behavior before migration so we can prove improvements:

- collect baseline latency/failure metrics from real debug logs
- pin golden transcript + debug-log fixtures for preview/apply and branch flows
- record current failure classes (misroute, retry loops, silent waits, claim mismatches)

**Exit criteria:**
- baseline report checked in
- golden fixtures runnable in CI/local
- migration phases can be compared against an explicit baseline

### Phase A: Dual Runtime Flag

Introduce `FORGE_CHAT_RUNTIME=react` environment variable. When set, route through new ReAct loop instead of current harness kernel.

**New files:**
- `internal/react/loop.go` — ReAct loop (`for { stream → execute → continue_or_break }`)
- `internal/react/session.go` — Unified session state
- `internal/react/prompt.go` — Prompt builder (instructions + skills + environment)

**Modified files:**
- `internal/runtime/chat.go` — Check env var, route to react or kernel

**Keep intact:**
- `internal/harness/*` — untouched, still works for default mode
- `internal/agent/agent.go` — agent.Run() reused by react loop

**Exit criteria:**
- ReAct mode can complete common coding tasks (file read, edit, command execution)
- Streaming works end-to-end
- Tool calls execute and feed back correctly

### Phase B: Approval Policy System

Build approval/sandbox system alongside ReAct loop.

**New files:**
- `internal/react/approval.go` — ApprovalPolicy + ApprovalRule + ApprovalConfig
- `internal/react/approval_config.go` — Load from config file
- `internal/react/sandbox.go` — SandboxPolicy enforcement (OS-level where possible)
- `internal/react/known_safe.go` — Known-safe command list

**Modified files:**
- `internal/react/loop.go` — Gate tool execution through approval system
- `config.toml` — Add `[approval]` section

**Exit criteria:**
- Commands can be auto-approved, prompted, or forbidden by config
- Protected-branch behavior preserved
- Claim-evidence guard still works
- Safety is equivalent or stronger than current kernel
- Initial sandboxing is staged: least-privilege execution + approval/rules first, OS sandbox parity tracked as follow-on hardening work

### Phase C: Delegation Tool

Add model-driven `spawn_agent` tool.

**New files:**
- `internal/react/tools/spawn_agent.go` — spawn_agent tool definition
- `internal/react/tools/wait_agent.go` — wait_agent tool for async delegation
- `internal/react/agent_pool.go` — Agent registry and lifecycle

**Exit criteria:**
- LLM can delegate sub-tasks via spawn_agent
- Sub-agents return plain text (no JSON contract)
- Parallel delegation works for independent subtasks
- Depth limiting enforced
- No host-side worker-class routing required

### Phase D: Context Compaction

Add auto-compaction when token budget exceeded mid-turn.

**New files:**
- `internal/react/compact.go` — Inline compaction (summarize history, replace with CompactedItem)

**Exit criteria:**
- Long sessions don't fail on context overflow
- Compaction preserves key context for continuation

### Phase E: Default Switch + Cleanup

- Make ReAct mode default
- Keep kernel mode as fallback behind `FORGE_CHAT_RUNTIME=kernel`
- Remove deprecated code after 2-week soak period, only after required safeguards are migrated into React path and verified:
  - `internal/harness/classifier.go`
  - `internal/harness/workers.go` (worker allowlists)
  - `internal/harness/contracts.go` (structured output)
  - selected state-machine-only paths in `internal/harness/policy.go`
  - selected legacy-normalization-only paths in `internal/harness/outcome.go`

**Exit criteria:**
- Default Forge behavior is fluid, stable
- No per-phrase router patches needed
- Regression tests pass for both modes

---

## Non-Negotiable Requirements

These stay regardless of loop style:

- No unverified side-effect claims (claim-evidence guard)
- No direct edits on protected branches without explicit safe context transition
- No hidden escalations beyond declared permissions
- Full traceability for why a turn succeeded, retried, or failed
- Sub-agents cannot escape approval scope
- All tool execution is sandbox-bounded where OS support exists

---

## Success Metrics

We are done when:

1. **No phrase patches:** Repeated real-user prompts no longer require per-phrase router patches.
2. **Fast first progress:** Median time-to-first-host-progress update is < 2 seconds (host heartbeat/progress events, not model-token timing).
3. **Multi-turn works:** Preview/apply flows complete without state drift across turns.
4. **Errors are visible:** Failure paths produce explicit, actionable messages — no silent loops.
5. **Tool success rate > 95%:** Tasks don't fail for formatting, allowlist, or classification reasons.
6. **Retry budget < 3:** Average retries per task under 3, no 90-attempt death spirals.
7. **User trust:** Fewer "it fell over again" incidents. Debug logs show clear causality.

---

## Immediate Next Step

Create and execute a concrete implementation plan for **Phase A** on this branch:

1. Create `internal/react/loop.go` with the ReAct loop
2. Add `FORGE_CHAT_RUNTIME=react` routing in `chat.go`
3. Wire up existing agent + tools to new loop
4. Manual smoke test: "read this file", "edit this function", "run this command"
5. Verify streaming and tool execution work end-to-end

---

## Appendix: Codex Architecture Reference (Source-Verified)

### Key Structs

| Struct | Purpose |
|---|---|
| `Codex` | Top-level: submit channel + event channel |
| `Session` | Core mutable state: history, active turn, services |
| `TurnContext` | Per-turn config snapshot (model, policies, tools, instructions) |
| `ToolRouter` | Routes tool calls from response items to handlers |
| `ToolOrchestrator` | Approval + sandbox + retry logic for tool execution |
| `ToolCallRuntime` | Cancellation-aware tool dispatch with parallel support |
| `AgentControl` | Multi-agent spawn/message/shutdown |
| `AgentRegistry` | Thread-limited agent tracking with nicknames |
| `GuardianReviewSession` | Cached LLM session for auto-approval |

### Key Functions

| Function | Purpose |
|---|---|
| `submission_loop()` | Event dispatch: `while let Ok(sub) = rx_sub.recv()` |
| `run_turn()` | Inner ReAct loop: stream → execute tools → continue or break |
| `run_sampling_request()` | Stream model response, parse tool calls |
| `try_run_sampling_request()` | Single streaming attempt with retry logic |
| `handle_output_item_done()` | Routes completed response items to tool handlers |
| `review_approval_request()` | Guardian LLM-based risk assessment |
| `run_codex_thread_interactive()` | Spawn sub-agent with event forwarding |
| `run_codex_thread_one_shot()` | Spawn sub-agent for single task |

### Tool List (Source-Verified)

**Execution:** `shell`, `shell_command`, `exec_command` (UnifiedExec), `write_stdin`, `local_shell`  
**Editing:** `apply_patch` (Freeform + Function variants)  
**Delegation:** `spawn_agent`, `send_input`, `wait_agent`, `list_agents`, `close_agent`, `resume_agent`, `spawn_agents_on_csv`  
**Code execution:** `code_mode`, `wait`  
**JS:** `js_repl`, `js_repl_reset`  
**Discovery:** `tool_search`, `tool_suggest`  
**Web:** `web_search`, `view_image`, `image_generation`  
**Interaction:** `request_permissions`, `request_user_input`  
**Dynamic:** MCP tools (namespaced), app connector tools, dynamic tool specs
