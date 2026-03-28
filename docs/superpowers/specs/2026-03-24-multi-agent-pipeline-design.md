# Multi-Agent Pipeline Rework

**Date:** 2026-03-24
**Status:** Approved
**Branch:** TBD (off main)

## Problem

The multi-agent system has several issues observed during live testing:

1. **Chat pane is dead while sub-agents work.** The user sees only a spinner — no visibility into what the sub-agent is doing without watching the tools pane.
2. **Scout loops without returning results.** Short (< 300 char) but valid responses trigger the "action preamble" nudge, burning up to 4 extra turns before results reach dispatch.
3. **Sub-agent results are truncated.** `compactAssistantHistory` clips messages to 240 chars. `SpawnSubAgent` extracts results from this clipped history, so dispatch loses most of scout's findings.
4. **Dispatch narrates before acting.** Despite prompt restrictions, dispatch sometimes writes prose before its first tool call.
5. **No way to cancel a sub-agent.** Escape kills the entire turn. Users can't cancel a stuck sub-agent and let dispatch try a different approach.
6. **Tools pane overflows.** Long sub-agent sessions produce hundreds of lines with no summarization.
7. **No runtime model assignment.** Per-role model selection is wired in config but there's no TUI for viewing or changing it.

## Design

### 1. Sub-Agent Progress Events in Chat Pane

**New event kind:** `EventProgress` — a lightweight event that carries a one-line summary of sub-agent activity for the chat pane.

**Emitter:** `SubAgentRenderer` emits progress events alongside existing detail events. Progress events are emitted **without** the `SubAgent` field set, so they route to the main `handleLLMEvent` handler (not `handleSubAgentEvent`). The `Agent` field carries the role name for display:

```go
func (r *SubAgentRenderer) ToolCall(name, summary string) {
    // Existing: detail event to tools pane (has SubAgent set)
    r.parent.events <- llm.Event{Kind: llm.EventToolCall, Agent: name, Text: summary, SubAgent: r.role}
    // New: progress event to chat pane (no SubAgent — routes to main handler)
    r.parent.events <- llm.Event{Kind: llm.EventProgress, Agent: r.role, Text: progressLine(r.role, name, summary)}
}

func (r *SubAgentRenderer) ToolResult(name, output, diff string, isError bool) {
    // Existing detail event (unchanged)...
    // New: progress event on errors only (successes are too noisy)
    if isError {
        r.parent.events <- llm.Event{Kind: llm.EventProgress, Agent: r.role, Text: fmt.Sprintf("%s: %s failed", r.role, name)}
    }
}
```

**Consumer:** `handleLLMEvent` routes `EventProgress` to `AddWorkingMessage`. Since `SubAgent` is not set on the event, the `handleSubAgentEvent` early-return at chatmodel.go:750-752 is not triggered:

```go
case llm.EventProgress:
    m.AddWorkingMessage(ev.Text)
```

**Progress line format:** `scout: reading internal/auth/store.go`, `scout: searching for "session"`, `builder: editing internal/agent/agent.go`. Derived from role + tool name + summary arg.

**Helper function** `progressLine(role, toolName, summary string) string` maps tool calls to human-readable one-liners. Tool names must match actual registry `.Name` values (verify against each tool's constructor):
- `read_file` + path → `"<role>: reading <basename>"`
- `search`/`grep` + pattern → `"<role>: searching for <pattern>"`
- `glob` + pattern → `"<role>: finding <pattern>"`
- `edit_file` + path → `"<role>: editing <basename>"`
- `run_command` + cmd → `"<role>: running <cmd truncated to 40>"`
- `write_file` + path → `"<role>: writing <basename>"`
- `delegate` + role → `"dispatching to <role>"`
- fallback → `"<role>: <tool_name>"`

### 2. Scout Feedback Bug Fixes

**Sub-agent nudge bypass:** Add `isSubAgent bool` field to `Agent` struct in agent.go. Set to `true` in `SpawnSubAgent` when constructing the sub-agent. The nudge logic in `Run` skips the preamble/short-response check when `isSubAgent` is true:

```go
if !a.isSubAgent && (isPreamble || isShort) && actionPreambleRetries < 4 && turn+1 < a.maxTurns {
    // nudge...
}
```

Sub-agents are expected to produce concise structured output. Their prompts already instruct them on output format.

**Note on dispatch nudging:** Dispatch is the *primary* agent (not a sub-agent), so it still gets nudged. This is correct — dispatch should be nudged when it narrates without acting. The prose filter (Section 3) handles the display side; the nudge handles the behavioral side. These interact safely: if dispatch's visible text is stripped by the prose filter, the *raw response* still contains tool call tags so `len(calls) > 0` and the nudge path isn't reached. If dispatch narrates without a tool call, the raw response has no calls, the nudge fires, and the prose filter suppresses the narration display.

**Full response preservation:** Add `lastFullResponse string` field to `Agent` struct in agent.go. Set it in **both** code paths — the "final answer" path (no tool calls) and the tool-call path (capturing `visibleText` before compaction):

```go
// Final answer path (no tool calls):
if len(calls) == 0 {
    a.lastFullResponse = response  // preserve full text
    // ... existing nudge / history logic ...
}

// Tool call path (after executing tools, before compaction):
a.lastFullResponse = visibleText  // preserve visible text from tool-call turns too
if assistantSummary := compactAssistantHistory(visibleText); assistantSummary != "" {
    // ... existing compaction logic ...
}
```

This ensures that if a sub-agent exits via max turns (still making tool calls), the last visible text is preserved in full. The max-turns fallback in `SpawnSubAgent` then reads from `lastFullResponse` instead of compacted history:

```go
result := sub.lastFullResponse
if result == "" {
    result = "(sub-agent produced no output)"
}
```

The history scan fallback is removed — `lastFullResponse` is always set (to either the final response or the last visible text before the turn limit).

### 3. Dispatch Output Filtering

**Strip leading prose from dispatch:** Add `role string` field to `Agent` struct in agent.go. Set to `"dispatch"` in `configureMultiAgent` (via a new `SetRole` method), empty string for normal mode and sub-agents.

In the streaming filter within `Run`, add a `seenToolCall bool` to the per-turn streaming state. When `a.role == "dispatch"`, only emit `AgentToken` when `seenToolCall` is true:

```go
// Per-turn state (inside the for-turn loop, before streaming):
seenToolCall := false

// In the line-by-line filter:
if !inCodeFence {
    if _, ok := isToolCallOpen(trimmed); ok {
        seenToolCall = true
        inToolCall = true
        continue
    }
    // ...
}
if !inToolCall {
    if a.role != "dispatch" || seenToolCall {
        a.renderer.AgentToken(line)
    }
}
```

**Semantics of `seenToolCall`:** Set to true when the streaming filter encounters a tool call open tag *in the current turn's streamed response*. Once set, it stays true for the rest of that turn. This means:
- Turn where dispatch delegates: prose before `<tool_call>` is suppressed, nothing after (tool call is the whole response). User sees nothing from dispatch this turn.
- Turn where dispatch presents results (after delegation returned): no tool calls in this turn's response, `seenToolCall` stays false, so dispatch's prose is suppressed. This is **intentional** — dispatch's result presentation still appears via `compactAssistantHistory` being stored in history, and the sub-agent's progress events already showed the user what happened.

Wait — that would suppress dispatch's final result presentation too. Let me revise:

**Revised approach:** Only suppress prose on turns where dispatch *also* emits a tool call. If the response has no tool calls (dispatch is presenting results), let all tokens through. The check moves to *after* parsing:

```go
// After ParseToolCalls:
if a.role == "dispatch" && len(calls) > 0 {
    // This turn had tool calls — don't show the prose that came before them.
    // visibleText is the non-tool-call portion; suppress it.
    // (The tool execution results will be shown via progress events.)
} else {
    // No tool calls this turn — dispatch is presenting results. Show everything.
    // Already rendered via streaming above.
}
```

In practice: the streaming filter already suppresses tool call XML. For dispatch turns with tool calls, the visible prose before the tool call is just narration ("Let me delegate to scout") — we suppress it by not storing it in history and not rendering it. The simplest implementation: when `a.role == "dispatch"` and calls were found, pass empty string to `compactAssistantHistory` instead of `visibleText`.

### 4. Sub-Agent Cancellation

**New fields on `Agent` struct** (in agent.go):

```go
type Agent struct {
    // ... existing fields ...
    mu             sync.Mutex
    activeSubCancel context.CancelFunc
    role           string  // "dispatch" when in multi-agent mode, "" otherwise
}
```

**Child context per sub-agent:** `SpawnSubAgent` creates a derived context:

```go
subCtx, subCancel := context.WithCancel(ctx)
defer subCancel()

a.mu.Lock()
a.activeSubCancel = subCancel
a.mu.Unlock()
defer func() {
    a.mu.Lock()
    a.activeSubCancel = nil
    a.mu.Unlock()
}()

// Pass subCtx (not ctx) to sub.Run:
err := sub.Run(subCtx, task)
```

**CancelSubAgent method** on Agent (thread-safe):

```go
func (a *Agent) CancelSubAgent() {
    a.mu.Lock()
    defer a.mu.Unlock()
    if a.activeSubCancel != nil {
        a.activeSubCancel()
    }
}
```

**Escape key routing in TUI** (chatmodel.go). When `activeSubAgent != ""`:

- First Escape → send `"__cancel_subagent__"` to inputCh
- Track `lastEscapeTime`. Second Escape within 2s → send `"__cancel_turn__"` (existing behavior)

**Runtime handling** (chat.go). Add explicit case in the input switch, **not** the default path:

```go
case "__cancel_subagent__":
    a.CancelSubAgent()  // thread-safe method, no external locking needed
    evRenderer.Info("sub-agent cancelled")
```

The sub-agent's `Run` loop will get context cancellation on the next `Stream` call. `SpawnSubAgent` detects the cancellation and returns a structured message to dispatch:

```go
if err != nil && subCtx.Err() != nil {
    subRenderer.Info(fmt.Sprintf("[%s] cancelled", role))
    return fmt.Sprintf("CANCELLED: %s was cancelled by user. Present what you have or re-delegate.", role), nil
}
```

**TUI feedback:** Chat pane shows `"sub-agent cancelled"` via the Info event. Tools pane shows `└─ scout cancelled ────` via the SubAgentRenderer's Info call.

### 5. Tools Pane Collapse

**Per-session buffers:** Replace the flat `toolsBuf string` with a structured list:

```go
type toolsSection struct {
    role      string // "" for main agent tools
    buf       string // full detail
    summary   string // collapsed summary (set on completion)
    collapsed bool   // true after sub-agent completes
    turnCount int
    toolCount int
}
```

Add to ChatModel:

```go
toolsSections []toolsSection
```

**On sub-agent start:** Push a new section with the role name.
**During sub-agent:** Append to current section's `buf`, increment counters.
**On sub-agent complete:** Generate summary line, set `collapsed = true`.
**Main agent tools:** Append to a section with `role == ""`, never collapsed.

**Rendering:** Iterate sections. Collapsed sections render as their summary (2-3 lines). Active section renders full `buf`. Main agent sections always render full. The rendered output is concatenated for the tools pane viewport.

**Expand/collapse:** `Tab` key when tools pane is focused toggles collapse on the section under the scroll cursor. `/expand` without arguments retains its existing behavior (show last expandable tool result). No new `/expand <role>` variant — the Tab toggle is simpler.

### 6. Agent Configuration TUI

**/agents command:** Toggle multi-agent mode on/off. When toggling on, calls `configureMultiAgent`. When toggling off, restores original system prompt and full tool registry (need to store pre-agent state). Shows current state as a flash message: `"agents: enabled"` / `"agents: disabled"`.

**/agents models overlay:** Opens a modal similar to the existing model picker:

```
 Agent Models
 ─────────────────────────────────────
 dispatch    claude-sonnet-4-6  (default)
 scout       claude-haiku-4-5
 builder     claude-sonnet-4-6  (default)
 doctor      claude-sonnet-4-6  (default)
 architect   claude-haiku-4-5
 ─────────────────────────────────────
 ↑↓ select  Enter: pick model  s: save  Esc: close
```

- Up/down to select role
- Enter opens the existing model picker (reuses `modelsList` from available models in the session — same source as the main model picker)
- Selected model applies immediately for the session
- `s` saves current assignments to `[chat.agents.models]` in the TOML config
- `Escape` closes the overlay
- `(default)` label shown when a role has no explicit override (falls back to chat model)

**Save to TOML:** Uses a targeted config update approach. Read existing config file, parse it, update only the `[chat.agents.models]` section, write back. This avoids the `config.Save` full-encode problem that would overwrite a minimal user config with all defaults. Implementation: add `SaveAgentModels(models AgentModels)` method to config that does a surgical TOML update.

### 7. Deferred

- **Parallel delegation:** Deferred to future iteration. Sequential flow needs to be solid first.
- **Agent memory:** Scratchpad already exists. Fix #2 (result truncation) makes it useful. No new mechanism needed.

## Files Changed

| File | Change |
|------|--------|
| `internal/llm/types.go` | Add `EventProgress` kind |
| `internal/agent/agent.go` | Add `isSubAgent`, `lastFullResponse`, `role`, `mu`, `activeSubCancel` fields; `SetRole`, `CancelSubAgent` methods; skip nudge for sub-agents; dispatch prose filter; set `lastFullResponse` on both code paths |
| `internal/agent/event_render.go` | `SubAgentRenderer` emits progress events (without SubAgent field); `progressLine` helper |
| `internal/agent/subagent.go` | Child context with cancel; set/clear `activeSubCancel` under mutex; read `lastFullResponse`; set `isSubAgent` on sub-agent; detect cancellation |
| `internal/tui/chatmodel.go` | Route `EventProgress` to chat; escape routing for sub-agent cancel (double-tap logic); tools pane sections replacing flat `toolsBuf`; Tab to toggle collapse; `/agents` command; `/agents models` overlay |
| `internal/tui/chatmsg.go` | No change (MsgWorking already exists) |
| `internal/runtime/chat.go` | Handle `__cancel_subagent__` case in input switch; call `a.CancelSubAgent()` |
| `internal/config/config.go` | Add `SaveAgentModels` for surgical TOML update |
| `internal/agent/roles.go` | No change |

## Testing

- Unit tests for `progressLine` mapping (verify tool names match registry)
- Unit test: sub-agent skips nudge on short response
- Unit test: `lastFullResponse` preserved on both final-answer and max-turns paths
- Unit test: dispatch prose suppressed on tool-call turns, shown on result-presentation turns
- Unit test: `CancelSubAgent` is safe to call when no sub-agent active
- Integration test: sub-agent cancellation returns structured message to dispatch
- Manual TUI testing: progress in chat pane, tools pane collapse/expand, model picker overlay, save to TOML
