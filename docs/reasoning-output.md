# Reasoning Output

## Current State

The chat transcript does not currently show internal planning or reasoning by default.

What is visible today:

- Final assistant text in the agent pane
- Inline `Working` updates for explicit runtime/progress events
- Raw tool activity in the tools pane

What is not visible today:

- Per-turn plan or todo lists
- Reasoning summaries
- Provider reasoning/thinking events
- Any hidden chain-of-thought style output

This means the left pane can still feel blank between the user prompt and the final answer unless the model explicitly narrates its own plan in normal output text.

## Desired Outcome

Make the agent pane feel operationally alive without exposing raw hidden reasoning.

The user-visible goal is:

- show what the system is doing
- show what it plans to do next
- avoid duplicating the tools pane
- avoid pretending local estimates or synthesized summaries are provider-authored reasoning

## Non-Goals

- Do not expose raw chain-of-thought
- Do not mirror the full tools pane into the transcript
- Do not invent fake reasoning text and present it as model output

## Output Types

There are three useful transcript surfaces:

### 1. Working

Short operational progress updates.

Examples:

- Inspecting repository structure
- Reading key files
- Comparing config paths
- Preparing patch

These are best driven from explicit runtime signals, not inferred from hidden reasoning.

### 2. Plan

Lightweight visible task list for the current turn.

Examples:

- inspect repo layout
- check config and entry points
- summarize findings

This should be shown only when the agent explicitly emits it or when the runtime can derive it from explicit workflow state. It should not be fabricated from hidden reasoning.

### 3. Reasoning Summary

A short summarized explanation of the current decision process.

Examples:

- comparing prompt overhead against prior turns
- checking whether tools or system prompt dominate token cost
- deciding whether to compact history or reduce prompt surface

This should only be shown when a provider exposes a summary-safe reasoning channel, or when the application intentionally generates a separate explicit summary message. It should not be treated as equivalent to chain-of-thought.

## Recommended Direction

### Near Term

Improve visible work state, not reasoning internals.

Specifically:

- keep inline `Working` messages in the transcript
- add explicit `Plan` blocks when the agent/runtime has real plan data
- keep raw tool calls and tool results in the tools pane only

This gives the user useful visibility without crossing into hidden reasoning disclosure.

### Later

Add provider-aware reasoning summaries if the drivers expose them safely.

This would require:

- a distinct reasoning-summary event type
- driver support for summary-safe reasoning events
- transcript rendering that clearly labels these as summaries

## UI Shape

Inline transcript blocks are preferred over a separate status strip.

Suggested rendering:

```text
Working
Inspecting repository structure

Plan
- inspect repo layout
- read config and entry points
- summarize findings

Agent • 22:10:14
Here is what I found...
```

Visual rules:

- `Working` should be dim and compact
- `Plan` should be distinct from both `Working` and normal agent output
- `Agent` remains the final answer stream
- tool details stay on the right

## Implementation Notes

Potential implementation path:

1. Keep existing inline `Working` transcript messages for runtime info.
2. Add a new transcript message kind for `Plan`.
3. Teach the agent/runtime loop to emit explicit plan messages when available.
4. Add a new event/message type for `Reasoning summary` only if supported safely by providers.

## Open Questions

- Should `Plan` be auto-generated from explicit workflow state, or only shown when the model emits a plan?
- Should `Working` messages be persisted for the whole session or collapsed per turn?
- Which providers, if any, expose a reasoning-summary channel worth surfacing?
