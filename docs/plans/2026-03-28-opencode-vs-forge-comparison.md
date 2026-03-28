# OpenCode vs Forge: Architectural Comparison

**Date:** 2026-03-28
**Purpose:** Understand why OpenCode performs well and Forge struggles, to inform Forge's next redesign.

---

## Summary

OpenCode is a radically simpler architecture. It has no harness kernel, no classifier/planner/policy pipeline, no hidden workers, no state machine, and no structured worker output validation. It gives the LLM all tools, lets it decide what to do, and uses a permission system for safety. Forge has a 7-stage state machine, 4 worker types with strict tool allowlists, deterministic classification, structured output contracts, and outcome normalization. Forge's complexity is the primary source of its reliability problems.

---

## Architecture Comparison

### OpenCode: Flat Agent Loop

```
User Input
  → System Prompt (environment + skills + instructions)
  → LLM generates response (may include tool calls)
  → Execute tool calls (permission-gated)
  → If tool calls → loop back to LLM
  → If no tool calls → done, return response
```

**Key files:**
- `packages/opencode/src/session/prompt.ts` - Main loop (~700 lines)
- `packages/opencode/src/agent/agent.ts` - Agent definitions (~300 lines)
- `packages/opencode/src/tool/tool.ts` - Tool interface (~100 lines)
- `packages/opencode/src/tool/registry.ts` - Tool registry (~200 lines)

**Total core harness: ~1300 lines**

### Forge: Harness Kernel State Machine

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

**Key files:**
- `internal/harness/runner.go` - Orchestrator (~600 lines)
- `internal/harness/classifier.go` - Token-based classification (~1200 lines)
- `internal/harness/planner.go` - Step selection (~100 lines)
- `internal/harness/policy.go` - Decision logic (~100 lines)
- `internal/harness/workers.go` - Worker execution (~250 lines)
- `internal/harness/contracts.go` - Structured output validation (~400 lines)
- `internal/harness/local.go` - Local executor (~450 lines)
- `internal/harness/strictlocal.go` - Strict local executor (~250 lines)
- `internal/harness/session.go` - Session state (~150 lines)
- `internal/harness/types.go` - Type definitions (~300 lines)
- `internal/harness/outcome.go` - Outcome normalization (~250 lines)
- `internal/harness/thread.go` - Thread ledger (~600 lines)
- `internal/harness/scope.go` - Scope analysis (~250 lines)
- `internal/harness/trace.go` - Tracing (~100 lines)
- `internal/agent/agent.go` - Agent loop (~1500 lines)
- `internal/agent/system.go` - System prompt builder (~2500 lines)
- `internal/agent/roles.go` - Role definitions (~300 lines)
- `internal/agent/subagent.go` - Sub-agent spawning (~400 lines)

**Total core harness: ~9500 lines**

**Forge's harness is ~7x more code for fundamentally less reliable behavior.**

---

## Key Differences

### 1. No Classifier

**OpenCode:** No classification step. The LLM decides what to do based on the user's message and available tools.

**Forge:** `classifier.go` (1200 lines) performs deterministic token matching:
- Splits user input into tokens
- Matches against hardcoded token sets: `answerTokens`, `inspectTokens`, `implementTokens`, `debugTokens`, `researchTokens`
- Maps to families: `FamilyAnswer`, `FamilyInspect`, `FamilyImplement`, `FamilyVerify`, `FamilyResearch`
- Determines `WantsAction`, `CanStayLocal`, `PrefersVisibleExecution`
- Routes through thread ledger for active thread detection

**Why this hurts Forge:**
- Token matching is brittle — "make a branch" can misroute to `FamilyAnswer` instead of `FamilyImplement`
- Adds latency before any LLM call
- Maintenance burden: new patterns require code changes, not prompt updates
- The LLM already knows how to classify intent — Forge does it poorly beforehand

### 2. No Planner / Policy Engine

**OpenCode:** No planning step. The LLM has all tools available and decides what to use.

**Forge:** `planner.go` + `policy.go` select an execution step:
- `StepLocal` — agent runs with full tools
- `StepStrictLocal` — agent runs with constrained tools (preview mode)
- `StepWorker` — hidden worker runs with subset of tools
- `StepVisibleCollaboration` — visible collaborative agent
- `AdmitWorker()` decides if a worker should be dispatched based on session state
- Workers have hardcoded tool allowlists in `workers.go`

**Why this hurts Forge:**
- Tool allowlists prevent workers from completing their objectives (e.g., WorkerEditor can't `git_commit`)
- The planning decision is made before the LLM has even seen the task
- Workers are dispatched for tasks the primary agent could handle directly
- Adds a full round-trip (plan → dispatch worker → validate → return) for simple tasks

### 3. No Structured Worker Output Contract

**OpenCode:** Sub-agents return their final text response. The parent agent reads it as context.

**Forge:** Workers must return strict JSON:
```json
{
  "status": "complete|blocked",
  "message": "user-visible summary",
  "artifact_kind": "evidence|implementation|diagnosis|plan",
  "artifact": "detailed payload",
  "next_role": "",
  "next_task": ""
}
```

Validated by `contracts.go` using `decodeStrictJSON()`. Malformed JSON → retry up to 3 times → blocked.

**Why this hurts Forge:**
- LLMs frequently produce slightly malformed JSON (extra whitespace, markdown in artifact, missing optional fields)
- Each validation failure costs a full API round-trip
- After 3 failures, the worker is permanently blocked
- The primary agent then retries the same worker, creating nested retry loops
- OpenCode avoids this entirely by not requiring structured output from sub-agents

### 4. No State Machine

**OpenCode:** Simple loop. If the model calls a tool → execute → continue. If the model stops → done.

**Forge:** 7-stage state machine with transitions:
```
Intake → Classify → Plan → Act → Observe → Decide → Respond
                                        ↑        │
                                        └─ Retry ┘
                                        ↑        │
                                        └─ Replan─┘
```

Each stage has its own validation, error handling, and retry logic. The Decide stage can route to:
- `StateComplete` — done
- `StateBlocked` — permanent failure
- `StateRetry` — try the same step again
- `StateReplan` — go back to planning
- `StateAwaitingFeedback` — stop and wait for user

**Why this hurts Forge:**
- Retry loops can compound: worker fails → kernel retries → worker fails again → agent retries → 90+ API calls
- No circuit breaker to stop failing patterns
- Each state transition is a potential failure point
- Debugging requires tracing through 7 stages instead of 1 loop

### 5. No Hidden Workers

**OpenCode:** Three agent modes:
- `build` — primary agent, full access
- `plan` — primary agent, read-only + plan file writes
- `general` — sub-agent, dispatched via `task` tool for parallel research
- `explore` — sub-agent, read-only codebase exploration

Sub-agents are dispatched by the LLM itself via the `task` tool, not by a host-side classifier.

**Forge:** Four worker types:
- `WorkerReader` — research, read-only tools
- `WorkerEditor` — implementation, edit tools
- `WorkerVerifier` — verification, read + run commands
- `WorkerResearcher` — external research, web tools

Workers are dispatched by the harness kernel based on classification, not by the LLM.

**Why this hurts Forge:**
- The LLM is better at deciding when delegation is useful than a token matcher
- Workers run with restricted tools and can't complete their objectives
- Worker results require strict validation that frequently fails
- Workers add latency (dispatch → validate → return) for tasks the primary agent could handle

### 6. Permission System vs Tool Allowlists

**OpenCode:** Permission system applies to all agents uniformly:
```typescript
const defaults = Permission.fromConfig({
  "*": "allow",
  doom_loop: "ask",
  external_directory: { "*": "ask" },
  question: "deny",
  read: { "*": "allow", "*.env": "ask" },
})

// Plan mode restricts via permission overrides:
plan: {
  permission: Permission.merge(defaults, Permission.fromConfig({
    edit: { "*": "deny" },
  })),
}
```

Permission rules are declarative, composable, and overridable per agent and per session.

**Forge:** Hardcoded tool allowlists per worker:
```go
func workerToolAllowlist(kind WorkerKind) []string {
    switch kind {
    case WorkerReader:
        return []string{"read_file", "glob", "search", ...}
    case WorkerEditor:
        return []string{"read_file", "write_file", "edit_file", ...}
    // ...
    }
}
```

**Why OpenCode's approach is better:**
- Single mechanism for all agents (no special worker path)
- Users can override permissions in config
- Composable: merge defaults + agent-specific + session-specific
- No code changes needed to adjust tool access

### 7. Sub-Agent Model

**OpenCode:** Sub-agents are dispatched by the LLM via the `task` tool:
```typescript
// The LLM calls:
task({ subagent_type: "explore", description: "find auth files", prompt: "..." })

// TaskTool creates a new session, runs the sub-agent, returns text result
```

Sub-agents get their own session, model, and tools. Results come back as plain text in a `<task_result>` wrapper. The parent agent decides what to do with the result.

**Forge:** Sub-agents are dispatched by the harness kernel:
```go
// The harness decides to dispatch a worker:
step := Plan(class, session)
if step.Kind == StepWorker {
    obs := manager.Execute(ctx, WorkerTask{Kind: WorkerEditor, ...})
    // Must return strict JSON validated by contracts.go
}
```

**Why OpenCode's approach is better:**
- The LLM decides when delegation is useful (not a token matcher)
- Sub-agent results are plain text (no strict JSON validation)
- Sub-agents get full tool access within their permission scope
- No intermediate validation layer that can fail
- Simpler error path: sub-agent fails → text error message → parent handles it

---

## What OpenCode Gets Right

### 1. Trust the Model

OpenCode's entire architecture trusts the LLM to:
- Classify user intent
- Choose appropriate tools
- Decide when to delegate
- Format its own output

Forge distrusts the model at every stage:
- Classifies intent with token matching (doesn't trust LLM)
- Restricts tools via allowlists (doesn't trust LLM)
- Validates output with strict JSON schema (doesn't trust LLM)
- Retries on validation failure (doesn't trust LLM)

**Result:** OpenCode's trust is well-placed. Modern LLMs are very good at tool selection and intent classification when given clear system prompts. Forge's deterministic layers add latency and failure points without improving reliability.

### 2. Minimal Indirection

OpenCode: User → Agent → Tools → Response

Forge: User → Intake → Classify → Plan → Act → Observe → Decide → Response

Each layer of indirection in Forge is a potential failure point. OpenCode removes all of them.

### 3. Plain Text Results

OpenCode sub-agents return plain text. No JSON schema, no validation, no retry logic. The parent LLM reads the text and decides what to do.

Forge workers must return strict JSON. Validation failures cascade into retry loops that waste API calls and time.

### 4. Declarative Configuration

OpenCode agents are defined declaratively:
```typescript
agents: {
  build: { name: "build", permission: ..., mode: "primary" },
  plan: { name: "plan", permission: ..., mode: "primary" },
  general: { name: "general", permission: ..., mode: "subagent" },
}
```

Users can add custom agents, change permissions, and override models — all from config. No code changes needed.

Forge agents are defined in Go code. Adding a new agent type requires modifying multiple files.

### 5. One Tool Interface

OpenCode has one `Tool.define()` interface. Every tool implements the same contract:
```typescript
Tool.define("id", {
  description: "...",
  parameters: z.object({...}),
  execute: async (args, ctx) => ({ output, metadata, title }),
})
```

Forge has different tool interfaces for:
- Primary agent tools (full registry)
- Worker tools (allowlist-filtered)
- Sub-agent tools (role-specific allowlists)
- Visible collaboration tools (subset)

### 6. LLM-Driven Delegation

OpenCode sub-agents are dispatched by the LLM via the `task` tool. The LLM decides:
- When to delegate
- Which agent to use
- What prompt to send
- How to interpret results

Forge dispatches workers based on token matching in the classifier. The host decides:
- When to delegate (based on classification)
- Which worker to use (based on step planning)
- What task to give (based on planner output)
- Whether results are valid (based on JSON schema)

**The LLM is better at all four decisions.**

---

## What Forge Could Learn

### Immediate Wins (Phase 1)

1. **Remove structured worker output validation** — let workers return plain text
2. **Remove deterministic classification** — let the LLM decide what to do
3. **Expand worker tool access** — give workers the same tools as the primary agent
4. **Add circuit breakers** — stop retrying after 3 consecutive failures
5. **Reduce prompt complexity** — OpenCode's explore prompt is 15 lines, Forge's worker prompts are 200+

### Medium-Term (Phase 2)

6. **Permission system** — replace tool allowlists with declarative permission rules
7. **LLM-driven delegation** — let the LLM decide when to use sub-agents via a `task` tool
8. **Declarative agent config** — define agents in config, not code
9. **Remove state machine** — replace with simple loop
10. **Unified tool interface** — one tool contract for all execution modes

### Long-Term (Phase 3)

11. **Client/server architecture** — decouple TUI from agent runtime
12. **Plugin system** — allow external tools and agents
13. **Session persistence** — store and resume agent sessions
14. **Streaming-first** — stream everything, buffer nothing

---

## Lines of Code Comparison

| Component | OpenCode | Forge | Ratio |
|-----------|----------|-------|-------|
| Core harness | ~1,300 | ~9,500 | 1:7 |
| Agent definitions | ~300 | ~800 | 1:3 |
| System prompts | ~50 | ~2,500 | 1:50 |
| Tool registry | ~200 | ~500 | 1:3 |
| Classification | 0 | ~1,200 | 0:∞ |
| Worker validation | 0 | ~400 | 0:∞ |
| State machine | 0 | ~600 | 0:∞ |
| Thread management | 0 | ~600 | 0:∞ |
| **Total** | **~1,850** | **~16,100** | **1:9** |

Forge's harness is **9x more code** for less reliable behavior.

---

## Why Simplicity Wins

OpenCode's architecture follows a principle that Forge should adopt:

> **Let the LLM do what LLMs are good at. Let the host do what hosts are good at.**

**LLMs are good at:**
- Understanding user intent
- Choosing appropriate tools
- Formatting responses
- Deciding when to delegate
- Interpreting ambiguous requests

**Hosts are good at:**
- Executing tool calls safely
- Enforcing permissions
- Managing state persistence
- Handling cancellation
- Tracing and observability

Forge has the host doing LLM things (classifying intent, planning steps, validating output format) and the LLM doing host things (formatting JSON, following strict output schemas). This inversion is the root cause of Forge's reliability problems.

---

## Recommendations

### For the v2 plan specifically:

The v2 plan (`docs/plans/2026-03-28-kernel-architecture-fixes-v2.md`) is correct in keeping strict JSON contracts and improving retry diagnostics. It's a good incremental fix.

### For the next redesign:

The lessons from OpenCode suggest a more fundamental simplification:

1. **Remove the harness kernel entirely** — replace with a simple agent loop
2. **Remove deterministic classification** — let the LLM classify intent
3. **Remove worker types** — replace with permission-scoped sub-agents
4. **Remove structured output validation** — sub-agents return plain text
5. **Remove the state machine** — replace with a loop
6. **Add a permission system** — declarative, composable, overridable
7. **Add LLM-driven delegation** — `task` tool like OpenCode's

This would reduce Forge's harness from ~16,000 lines to ~2,000 lines while improving reliability.

The v2 plan's claim-evidence guard and protected-branch policy are good host-side safety mechanisms that should be preserved in any redesign. They're examples of the host doing what hosts are good at.

---

## References

- OpenCode agent definitions: `packages/opencode/src/agent/agent.ts`
- OpenCode tool interface: `packages/opencode/src/tool/tool.ts`
- OpenCode tool registry: `packages/opencode/src/tool/registry.ts`
- OpenCode task tool: `packages/opencode/src/tool/task.ts`
- OpenCode session loop: `packages/opencode/src/session/prompt.ts`
- Forge harness runner: `internal/harness/runner.go`
- Forge classifier: `internal/harness/classifier.go`
- Forge worker contracts: `internal/harness/contracts.go`
- Forge agent loop: `internal/agent/agent.go`
- Forge system prompts: `internal/agent/system.go`
