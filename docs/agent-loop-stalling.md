# Agent Loop Stalling Analysis (Legacy)

> **NOTE:** This document analyzes the legacy agent loop in `internal/agent/agent.go`. The current runtime uses `internal/react/loop.go` with native tool calling via the Responses API / chat completions `tools` parameter, rather than text-based `<tool_call>` parsing. Some observations about preamble detection and anti-narration prompts may still apply, but the specific code paths and loop structure have changed.

## Problem

The chat agent loop (`internal/agent/agent.go`) stops prematurely when the LLM narrates intent instead of emitting `<tool_call>` blocks. The user has to repeatedly type "go", "proceed", etc. to keep the agent working.

## Root Cause

In `agent.go:157-168`, the loop decides to stop or continue based on whether tool calls are present in the response:

1. **Tool calls found** → execute them, loop to next turn (working correctly)
2. **No tool calls + looks like action preamble** → nudge the LLM ("Continue by acting...") and retry, up to **2 times**
3. **No tool calls + doesn't match preamble** → treat as **final answer**, return to user (this is the stall)

### Failure Mode 1: Narrow preamble detection

`looksLikeActionPreamble()` (line 282) only matches responses that **start with** a small set of phrases:

```
"i'm going to", "i noticed we need to", "next i'll",
"i'll ", "let me "
```

Common stalling patterns that **don't match**:
- "First, I need to..."
- "To accomplish this, I'll..."
- "Based on my analysis..."
- "Here's my plan..."
- "Looking at the code..."
- "The next step would be..."

### Failure Mode 2: Low retry limit

Even when preamble *is* detected, the agent only nudges **twice** before giving up. Some models need more prodding to start emitting tool calls.

### Failure Mode 3: No length heuristic

A 50-character response with no tool calls is almost certainly not a real final answer, but the loop treats it the same as a 2000-character thoughtful response.

## Proposed Fixes

### 1. Broaden `looksLikeActionPreamble`

Add more phrases and use `strings.Contains` for some patterns, not just `HasPrefix`:

```go
// Prefix matches
"first,", "to accomplish", "to do this", "based on",
"here's my plan", "here's what", "looking at",
"the next step", "we need to", "we should",
"i can ", "i need to", "i want to",

// Contains matches (anywhere in response)
"i'll start by", "i'll begin by", "let's start",
"steps to take", "my approach"
```

### 2. Increase retry limit

Bump `actionPreambleRetries < 2` to `< 4`. Two nudges is often not enough.

### 3. Add a length/content heuristic

If the response has no tool calls and is short (under ~300 chars), treat it as an implicit preamble regardless of wording:

```go
if len(calls) == 0 {
    isShort := len(strings.TrimSpace(response)) < 300
    isPreamble := looksLikeActionPreamble(response)
    if (isPreamble || isShort) && actionPreambleRetries < 4 && turn+1 < a.maxTurns {
        actionPreambleRetries++
        // nudge...
        continue
    }
    // final answer
}
```

### 4. Strengthen the system prompt

Add anti-narration instructions to the system prompt built in `BuildSystemPrompt()`:

```
Do not narrate what you plan to do. Act immediately by calling tools.
Never say "I'll", "Let me", "I'm going to" — just call the tool.
If you need information, call a tool to get it. If you need to change a file, call the tool.
Only respond with plain text when you have a final answer for the user.
```

### 5. Escalating nudge messages

Instead of repeating the same nudge, escalate:

- Nudge 1: "Continue by acting. Call the next tool now."
- Nudge 2: "You must call a tool or give a final answer. Do not describe what you plan to do."
- Nudge 3: "STOP NARRATING. Call a tool now or say DONE."

## Impact

These changes keep the agent in its autonomous loop longer, reducing the need for the user to manually push it forward. The combination of broader detection + higher retry limit + length heuristic + better prompts should cover most stalling patterns without risk of infinite loops (the `maxTurns` cap still applies).

## Multi-Agent Orchestration (oh-my-openagent Style)

The [oh-my-openagent](https://github.com/code-yeongyu/oh-my-openagent) project solves stalling at the architecture level with hierarchical delegation, model routing, verification gates, and explicit autonomy prompts. This section evaluates how difficult each pattern would be to add to forge.

### What Forge Already Has

Forge is closer to multi-agent than it looks:

| Capability | Status | Where |
|---|---|---|
| Multi-provider support (Anthropic, OpenAI, Groq, etc.) | Done | `llm/drivers/`, `llm/registry.go` |
| Multiple models in one session (writer/auditor/summarizer) | Done (improve mode) | `session/runner.go`, `session/round.go` |
| Model switching at runtime | Done (chat `/model`) | `runtime/chat.go:SwitchModel()` |
| Concurrent goroutine patterns | Done (provider probing) | `bootstrap/provider_probe.go` |
| Tool registry with auto-approve | Done | `agent/tools/registry.go` |
| Event-driven TUI (channel-based) | Done | `tui/chatlive_bubbletea.go` |
| Skills system (injectable context) | Done | `skills/skills.go` |
| Stateful chat sessions | Done | `chatstate/` |

### What's Missing

| Capability | Effort | Notes |
|---|---|---|
| Agent as a first-class abstraction | Medium | Currently `Agent` is 1:1 with a chat session |
| Concurrent agent execution | Medium | Go makes this easy; the patterns exist in `provider_probe.go` |
| Inter-agent communication | Medium | Need a message bus or shared channel |
| Tool routing per agent | Low | Filter `Registry` per agent role |
| Agent-specific state/history | Low | Just multiple `Agent` instances |
| Task delegation framework | High | The orchestrator logic itself |
| Result merging/voting | High | Only needed for advanced patterns |

---

### Pattern 1: Hierarchical Delegation

**What it is:** An orchestrator agent receives the user's request, classifies it (research, implement, debug, plan), and dispatches to a specialist sub-agent with a tailored system prompt and tool subset.

**Difficulty: Medium**

Forge's `Agent` struct already supports everything a sub-agent needs — its own driver, history, tools, and system prompt. The main work is:

1. **Define agent roles** — each role gets a system prompt and tool filter:

```go
type AgentRole struct {
    Name        string
    SystemExtra string           // appended to base system prompt
    AllowTools  []string         // nil = all tools
    Model       string           // preferred model (falls back to default)
    MaxTurns    int              // per-delegation limit
}

var roles = map[string]AgentRole{
    "researcher": {
        Name:        "researcher",
        SystemExtra: "You are a code researcher. Read files, search, grep. Do not modify anything.",
        AllowTools:  []string{"read_file", "glob", "search", "list_dir", "git_log", "git_diff", "web_search", "web_fetch"},
        MaxTurns:    10,
    },
    "implementer": {
        Name:        "implementer",
        SystemExtra: "You are a code implementer. Write and edit files to accomplish the goal.",
        AllowTools:  []string{"read_file", "write_file", "edit_file", "glob", "search", "run_command", "git_status"},
        MaxTurns:    20,
    },
    "debugger": {
        Name:        "debugger",
        SystemExtra: "You are a debugger. Investigate failures, read logs, run tests, identify root causes.",
        AllowTools:  []string{"read_file", "glob", "search", "run_command", "git_diff", "git_log"},
        MaxTurns:    15,
    },
}
```

2. **Add a `delegate` tool** to the orchestrator — this is the key piece:

```go
// The orchestrator agent gets a "delegate" tool that spawns sub-agents
{
    Name:        "delegate",
    Description: "Delegate a task to a specialist agent",
    Parameters: map[string]ToolParam{
        "role":    {Type: "string", Description: "Agent role: researcher, implementer, debugger"},
        "task":    {Type: "string", Description: "What the sub-agent should accomplish"},
    },
    Execute: func(ctx context.Context, args map[string]any) (string, error) {
        role := roles[args["role"].(string)]
        sub := NewSubAgent(role, parentDriver, parentTools.Filter(role.AllowTools))
        return sub.Run(ctx, args["task"].(string))
    },
}
```

3. **Wire sub-agent output back to orchestrator history** — the result string feeds back into the orchestrator's conversation.

**What already works:** `Agent` is stateless enough to instantiate multiple times. The `tools.Registry` is a simple map that can be filtered. The driver interface is shared.

**The hard part:** The orchestrator's system prompt needs to be good enough to classify intent and delegate correctly. This is a prompt engineering problem more than a code problem. oh-my-openagent's Sisyphus prompt is ~2000 lines for a reason.

---

### Pattern 2: Category-Based Model Routing

**What it is:** Instead of hardcoding "use GPT-4o for everything," map task categories to optimal models. Research tasks use a fast/cheap model, deep implementation uses the best available, quick grep uses the fastest.

**Difficulty: Low**

Forge already has multi-model in improve mode (writer, auditor, summarizer can be different models). Extending this to chat mode is straightforward:

```go
// config.toml
[chat.models]
orchestrator = "claude-sonnet-4-20250514"    # fast, good at routing
researcher   = "claude-sonnet-4-20250514"    # fast reads
implementer  = "claude-sonnet-4-20250514"    # best code generation
debugger     = "o4-mini"                     # good at reasoning through failures
```

```go
// In runtime/chat.go, resolve a driver per role:
func (s *ChatSetup) DriverForRole(role string) llm.Driver {
    modelName := s.Config.Chat.RoleModels[role]
    if modelName == "" {
        modelName = s.Config.Chat.Model // fallback to default
    }
    driver, err := s.Registry.Lookup(modelName)
    if err != nil {
        return s.Driver // fallback to primary
    }
    return driver
}
```

**The hard part:** Nothing, really. This is config + a lookup function. The `llm.Registry` already maps names to drivers.

---

### Pattern 3: Verification Gates

**What it is:** Sub-agents can't just say "done" — they must prove it. The implementer must show a passing build. The debugger must show the test passes. The orchestrator checks evidence before accepting.

**Difficulty: Low-Medium**

Forge's improve mode already does this — the auditor reviews the writer's output and can reject it (no "APPROVED" signal → another round). Adapting this for chat:

```go
type VerificationGate struct {
    Command     string   // e.g., "go build ./..."
    SuccessExit int      // expected exit code
    MustContain []string // output must contain these
    MustNotContain []string // output must NOT contain these
}

var roleGates = map[string][]VerificationGate{
    "implementer": {
        {Command: "go build ./...", SuccessExit: 0},
    },
    "debugger": {
        {Command: "go test ./...", SuccessExit: 0},
    },
}
```

After a sub-agent returns, the orchestrator runs its verification gates. If they fail, it either re-delegates or reports the failure.

**What already works:** `run_command` tool exists, the improve mode's convergence detection (`auditorApproved()`) is the same pattern. The `Round` struct already snapshots code before/after for diffing.

**The hard part:** Knowing *which* verification to run. For Go projects it's obvious (`go build`, `go test`). For arbitrary projects you'd need project detection — which `system.go:detectProjectType()` already does.

---

### Pattern 4: Explicit "Don't Stop" Prompts

**What it is:** Every agent's system prompt includes strong instructions to act autonomously, never narrate, and keep going until the task is complete.

**Difficulty: Trivial**

This is just adding text to `BuildSystemPrompt()` in `internal/agent/system.go`:

```go
const autonomyDirective = `
## Execution Rules
- Act immediately. Call tools, do not describe what you plan to do.
- Never respond with "I'll", "Let me", "I'm going to" without a tool call.
- If you need information, call a tool. If you need to change code, call a tool.
- Only respond with plain text when you have a complete final answer.
- If you are unsure, investigate first (read files, search) rather than asking.
- Keep working until the task is fully complete or you hit a genuine blocker.
`
```

This is the highest-ROI change — it addresses the stalling problem directly and requires zero architectural work.

---

### Implementation Roadmap

Ordered by effort-to-impact ratio:

| Phase | What | Effort | Impact |
|---|---|---|---|
| **0** | Anti-narration system prompt + better preamble detection | 1-2 hours | Fixes the immediate stalling problem |
| **1** | Category-based model routing in config | Half a day | Lets you use cheap models for cheap tasks |
| **2** | Agent roles with filtered tool registries | 1-2 days | Specialist agents with constrained capabilities |
| **3** | `delegate` tool + orchestrator prompt | 2-3 days | Full hierarchical delegation |
| **4** | Verification gates | 1 day | Agents must prove completion |
| **5** | Concurrent sub-agent execution | 1-2 days | Parallel research/implementation |

Phase 0 is independent of everything else. Phases 1-4 build on each other but each is usable standalone. Phase 5 is a nice-to-have.

**Total for the full stack: ~1-2 weeks of focused work.**

The biggest variable isn't the code — it's the orchestrator prompt. oh-my-openagent's Sisyphus prompt is thousands of lines of carefully tuned routing logic. Getting that right is iterative and takes real-world testing.

---

## oh-my-openagent Prompt Architecture (Deep Dive)

The repo has **11 named agents**, each with a Greek mythology identity. Analysing their prompts reveals patterns forge can adopt without copying their full complexity.

### Agent Roster

| Agent | Role | Read-Only? | Key Trait |
|---|---|---|---|
| **Sisyphus** | Main orchestrator | No | Classifies intent, delegates everything non-trivial |
| **Atlas** | Plan executor | No (but never writes code directly) | Runs plans from `.sisyphus/plans/`, delegates all implementation |
| **Prometheus** | Strategic planner | Yes | Interview-driven planning, refuses to implement even if asked |
| **Hephaestus** | Autonomous deep worker | No | "KEEP GOING. SOLVE PROBLEMS." — the anti-stalling agent |
| **Sisyphus Junior** | Lightweight executor | No | Minimal prompt, spawned for simple category tasks |
| **Oracle** | Architecture/debugging consultant | Yes | Extended thinking (32K budget), strict brevity in output |
| **Explore** | Codebase search | Yes | Must launch 3+ tools simultaneously, parallel grep |
| **Librarian** | External docs/reference | Yes | GitHub CLI, web search, constructs permalinks |
| **Metis** | Pre-planning consultant | Yes | Analyses request BEFORE Prometheus plans |
| **Momus** | Plan reviewer/critic | Yes | Reviews plans, approval bias ("when in doubt, APPROVE") |
| **Multimodal Looker** | Media interpreter | Yes | PDFs, images, diagrams |

### Key Prompt Patterns Worth Stealing

#### 1. Intent Gate (Sisyphus Phase 0)

Every incoming message is classified before any work begins:

```
trivial     → answer directly, no delegation
explicit    → user knows what they want, delegate to implementer
exploratory → research first (Explore + Librarian in parallel), then plan
open-ended  → interview mode (Prometheus), then plan, then implement
ambiguous   → ask ONE clarifying question, then classify
```

**Why this matters for forge:** The current agent just starts working immediately. An intent gate prevents wasting expensive model turns on tasks that need a cheap grep, and prevents shallow answers on tasks that need deep research.

**Forge implementation:** This doesn't need a separate agent. Add a classification step to the orchestrator's system prompt:

```
Before acting on any request, classify it:
- TRIVIAL: Answer from knowledge, no tools needed
- SEARCH: Need to read/find code (use grep, glob, read_file)
- IMPLEMENT: Need to write/edit code
- DEBUG: Something is broken, need investigation
- PLAN: Multi-step work, need to break down first

State your classification, then act accordingly.
```

#### 2. Structured Delegation Prompts (Sisyphus Phase 2B)

When Sisyphus delegates, it uses a mandatory 6-section format:

```
TASK:             What to do (one sentence)
EXPECTED OUTCOME: What "done" looks like
REQUIRED TOOLS:   Which tools the sub-agent should use
MUST DO:          Non-negotiable requirements
MUST NOT DO:      Explicit constraints
CONTEXT:          Relevant findings from exploration phase
```

**Why this matters:** Vague delegation ("go fix the bug") leads to sub-agents floundering. Structured prompts give them everything they need to work autonomously.

**Forge implementation:** The `delegate` tool's `task` parameter becomes a structured format. The orchestrator's prompt teaches it to always use this format.

#### 3. The Anti-Stalling Prompt (Hephaestus)

Hephaestus is the agent that directly solves forge's stalling problem. Its core behavioral rules:

```
KEEP GOING. SOLVE PROBLEMS. ASK ONLY WHEN TRULY IMPOSSIBLE.

Forbidden:
- Asking "Should I proceed?"
- Stopping after partial implementation
- Narrating plans without acting

Exploration hierarchy before asking the user:
1. Direct tools (read, search, glob)
2. Explore agent (parallel grep)
3. Librarian (external docs)
4. Context inference (git log, README)
5. LAST RESORT: ask user

Execution loop: EXPLORE → PLAN → DECIDE → EXECUTE → VERIFY
Progress updates are mandatory (brief, after each step).
```

**Why this matters:** This is exactly the prompt text forge needs. The exploration hierarchy is particularly valuable — it gives the agent a checklist of self-help options before it's allowed to stop and ask.

**Forge implementation:** Adapt this directly into `BuildSystemPrompt()`. The exploration hierarchy maps 1:1 to forge's existing tools.

#### 4. Parallel-First Exploration (Explore Agent)

The Explore agent must always fire 3+ tools simultaneously:

```
Required output format:
<analysis> intent analysis </analysis>
<results> files found, answer, next_steps </results>

Must launch 3+ tools simultaneously.
All paths must be absolute.
Fire multiple searches in parallel for broad queries.
```

**Why this matters:** Sequential searching is slow and wasteful. If you're looking for "how auth works," you should grep for `auth`, glob for `*auth*`, and read the README all at once.

**Forge implementation:** This is a prompt-level change for the researcher role. Forge's tool execution is currently sequential (line 173 in agent.go), but concurrent tool execution within a turn is a separate enhancement.

#### 5. Verification-Then-Approve Loop (Momus + Atlas)

Atlas's completion flow:

```
1. Sub-agent says "done"
2. Run automated checks (build, test, lint)
3. Manual code review: read EVERY changed file
4. Hands-on QA: actually run the feature
5. Final Verification Wave: 4 parallel reviewers
   - Plan Compliance reviewer
   - Code Quality reviewer
   - Manual QA reviewer
   - Scope Fidelity reviewer
   ALL must APPROVE
```

This is overkill for forge's use case but the core idea — **don't trust "done", verify it** — maps to forge's existing auditor pattern. A simplified version:

```
1. Sub-agent returns result
2. Orchestrator runs `go build ./...` (or equivalent)
3. If build fails → re-delegate with error context
4. Orchestrator runs `go test ./...`
5. If tests fail → re-delegate with failure output
6. Read the diff, verify it makes sense
7. Accept
```

#### 6. Notepad as Shared Memory (Atlas)

Atlas uses `.sisyphus/notepads/` files as cumulative intelligence shared across stateless sub-agents:

```
Write findings to notepad files.
Sub-agents are stateless — they don't remember previous delegations.
The notepad is how you pass context between delegations.
```

**Why this matters for forge:** When the orchestrator delegates to a researcher, then to an implementer, the implementer has no context from the research. A shared scratchpad solves this without needing inter-agent message passing.

**Forge implementation:** A `.forge/scratchpad/` directory or a `think` tool that writes to a shared file. Sub-agents get the scratchpad content in their system prompt or initial context.

#### 7. Session Continuity (Sisyphus session_id)

Each delegation includes a `session_id` so follow-up work on the same task preserves context:

```
First delegation: session_id = "fix-auth-bug"
Follow-up:       session_id = "fix-auth-bug" (resumes, saves ~70% tokens)
New task:         session_id = "add-logging" (fresh context)
```

**Forge implementation:** Sub-agents could have persistent history keyed by session ID, stored in `.forge/sessions/`. When re-delegating to the same role for the same task, restore the previous history.

### What Forge Can Skip

Not everything in oh-my-openagent is necessary:

- **Metis** (pre-planning consultant): Overkill. The orchestrator can do its own pre-analysis.
- **Momus** (plan critic with retry loop): Only needed if plans are unreliable. Start without it.
- **Multimodal Looker**: Niche. Add later if needed.
- **Atlas vs Sisyphus split**: They have two orchestrators for different contexts. Forge can start with one.
- **The full 4-reviewer verification wave**: Way too expensive. A single build+test gate is enough.
- **Dynamic prompt builder**: Their prompts are assembled from ~10 composable sections. Forge can use static prompts initially and add composition later.

### Revised Forge Agent Roster (Minimal Viable)

Based on the oh-my-openagent patterns, forge needs **5 agents** to start:

| Agent | Maps to | Forge Role |
|---|---|---|
| Sisyphus | Orchestrator | Classifies intent, delegates, verifies |
| Hephaestus | Implementer | Writes/edits code autonomously, never stops |
| Explore | Researcher | Parallel search, read-only, fast model |
| Oracle | Debugger | Read-only analysis, extended reasoning |
| Prometheus | Planner | Interview mode for complex tasks, produces plan files |

The orchestrator prompt is the linchpin. A reasonable starting point (~200 lines, not 2000):

```
You are the forge orchestrator. You classify tasks and delegate to specialists.

## Intent Classification
For every message, classify:
- TRIVIAL: You can answer directly with no tools
- SEARCH: Delegate to researcher (read-only exploration)
- IMPLEMENT: Delegate to implementer (code changes)
- DEBUG: Delegate to debugger (investigation + reasoning)
- PLAN: Delegate to planner (multi-step breakdown)

## Delegation Format
Always delegate with:
- TASK: one-sentence description
- OUTCOME: what "done" looks like
- CONTEXT: relevant findings, file paths, error messages
- CONSTRAINTS: what NOT to do

## Verification
After any implementation delegation:
1. Run build command for detected project type
2. If it fails, re-delegate with error output
3. Read the changed files to verify correctness

## Rules
- Default to delegation. Only handle trivial tasks yourself.
- Never narrate. Act or delegate.
- After researcher returns findings, delegate to implementer with those findings as context.
```
