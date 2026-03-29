# React Loop And Unified Session Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace Forge's thin `react` wrapper with a real ReAct-style turn loop and a unified session history that preserves user input, model output, tool activity, and delegation results in one runtime-owned state model.

**Architecture:** Keep `react` as the flagship runtime, but move turn orchestration out of the host/kernel compatibility layers and into `internal/react`. The first step is a runtime-owned session model; the second is a real loop that streams output, parses tool calls, executes them, appends tool results, and continues until a final answer is produced.

**Tech Stack:** Go, existing `internal/agent` parser/tool helpers, existing `internal/agent/tools` registry, existing `internal/llm` streaming interfaces, existing runtime event rendering.

---

## File Map

- Modify: `internal/react/session.go`
  Purpose: store canonical turn records instead of just recent input strings.
- Modify: `internal/react/loop.go`
  Purpose: grow from a wrapper around `Agent.Run` into a real ReAct runtime loop.
- Modify: `internal/react/prompt.go`
  Purpose: build runtime-owned prompts from session state instead of only trimming user input.
- Modify: `internal/react/compact.go`
  Purpose: compact turn records rather than raw input strings.
- Modify: `internal/react/loop_test.go`
  Purpose: cover turn recording, loop continuation, and no-tool/final-answer behavior.
- Modify: `internal/react/compact_test.go`
  Purpose: cover compaction against canonical turn history.
- Create: `internal/react/session_test.go`
  Purpose: validate snapshot semantics, turn recording, tool-result persistence, and compaction.
- Modify: `internal/runtime/chat.go`
  Purpose: keep runtime wiring thin while `react` owns more of the default execution path.

## Task 1: Finish the unified session model

**Files:**
- Modify: `internal/react/session.go`
- Modify: `internal/react/loop.go`
- Modify: `internal/react/loop_test.go`
- Create: `internal/react/session_test.go`

- [ ] **Step 1: Write failing tests for canonical turn history**

Cover:
- each turn stores normalized input, final response, tool calls, and error state
- snapshots are safe copies
- compaction preserves recent turns while summarizing older ones

- [ ] **Step 2: Run the focused react tests to verify they fail**

Run: `go test ./internal/react -run 'TestRunnerRunRecords|TestSession|TestCompactSessionHistory'`
Expected: FAIL because the session does not yet act as the canonical runtime history.

- [ ] **Step 3: Implement the minimal unified turn record model**

Implement:
- turn records in session state
- completion recording from the runner
- snapshot copies for turn records and tool-call metadata

- [ ] **Step 4: Re-run the focused react tests to verify they pass**

Run: `go test ./internal/react -run 'TestRunnerRunRecords|TestSession|TestCompactSessionHistory'`
Expected: PASS

## Task 2: Replace the wrapper runner with a real ReAct loop

**Files:**
- Modify: `internal/react/loop.go`
- Modify: `internal/react/prompt.go`
- Modify: `internal/react/loop_test.go`

- [ ] **Step 1: Write failing loop tests before implementation**

Cover:
- stream model output
- parse tool calls
- execute tool calls
- append tool results into runtime-owned session state
- continue until a final assistant answer
- retry or fail clearly on malformed non-final plain-text output

- [ ] **Step 2: Run the focused loop tests to verify they fail**

Run: `go test ./internal/react -run 'TestRunnerLoop|TestRunnerRunRejectsInvalidWorkingOutput|TestRunnerAppendsToolResults'`
Expected: FAIL because `Runner.Run` still delegates the full turn to `Agent.Run`.

- [ ] **Step 3: Implement the minimal real loop**

Implement:
- a runtime-owned turn loop in `internal/react/loop.go`
- tool execution against the existing registry under current approval policy
- final-answer detection without kernel worker contracts

- [ ] **Step 4: Re-run the focused loop tests to verify they pass**

Run: `go test ./internal/react -run 'TestRunnerLoop|TestRunnerRunRejectsInvalidWorkingOutput|TestRunnerAppendsToolResults'`
Expected: PASS

## Task 3: Keep runtime wiring and observability stable

**Files:**
- Modify: `internal/runtime/chat.go`
- Modify: `internal/runtime/chat_test.go`

- [ ] **Step 1: Write failing wiring tests**

Cover:
- `react` runtime still emits progress events
- `react` runtime no longer depends on synthetic response patching for continuity
- runtime wiring still registers `spawn_agent` and `wait_agent`

- [ ] **Step 2: Run the focused runtime tests to verify they fail**

Run: `go test ./internal/runtime -run 'TestRunChatTurn|TestRegisterReactDelegationTools'`
Expected: FAIL if runtime assumptions still depend on the thin wrapper behavior.

- [ ] **Step 3: Implement the minimal runtime adjustments**

Implement:
- keep runtime chat wiring thin
- keep trace/debug visibility intact while `react` owns more turn state

- [ ] **Step 4: Re-run the focused runtime tests to verify they pass**

Run: `go test ./internal/runtime -run 'TestRunChatTurn|TestRegisterReactDelegationTools'`
Expected: PASS
