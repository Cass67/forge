# React Default-Path Cleanup Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Remove legacy role-based subagent execution from Forge's default `react` runtime path, keep lightweight delegation, and slim prompt/runtime overhead without deleting the kernel fallback.

**Architecture:** Add a `react`-native spawned-agent execution path owned by the default runtime, rewire `spawn_agent`/`wait_agent` to use it, and keep delegation results plain text with minimal lifecycle metadata. Reduce prompt bloat by stopping avoidable project scanning and broad always-on skill/tool prompt injection in the main visible path.

**Tech Stack:** Go, existing `internal/agent.Agent`, `internal/react` runtime code, existing approval gate and tool registry, Go test tooling.

---

## File Map

- Modify: `internal/runtime/chat.go`
  Purpose: replace `react` delegation wiring so it no longer calls `Agent.SpawnSubAgent`.
- Modify: `internal/runtime/chat_test.go`
  Purpose: lock runtime wiring and default-path expectations with tests.
- Modify: `internal/react/agent_pool.go`
  Purpose: remove legacy role remapping and support react-native spawn metadata.
- Modify: `internal/react/agent_pool_test.go`
  Purpose: cover role handling and async lifecycle behavior.
- Modify: `internal/react/tools/spawn_agent.go`
  Purpose: keep async lifecycle output minimal and compatible with react-native delegation.
- Modify: `internal/react/tools/wait_agent.go`
  Purpose: surface plain-text completion results without worker-contract expectations.
- Modify: `internal/react/tools/spawn_wait_test.go`
  Purpose: cover lifecycle envelope and plain-text completion behavior.
- Modify: `internal/agent/system.go`
  Purpose: slim visible prompt construction and remove avoidable project scanning from the default path.
- Modify: `internal/agent/agent.go`
  Purpose: stop always injecting the full skill catalog into the main visible system prompt.
- Modify: `internal/agent/system_test.go` or nearest existing agent/system tests
  Purpose: verify prompt construction behavior and prevent regressions.

## Task 1: Lock the desired react delegation behavior with failing tests

**Files:**
- Modify: `internal/runtime/chat_test.go`
- Modify: `internal/react/agent_pool_test.go`
- Modify: `internal/react/tools/spawn_wait_test.go`

- [ ] **Step 1: Write failing tests for the new default-path delegation rules**

Cover:
- `registerReactDelegationTools` uses a react-native spawn path instead of `Agent.SpawnSubAgent`
- `MapSpawnRole` no longer remaps `default` to `builder` or `explorer` to `scout`
- `wait_agent` returns the delegated plain-text result in its completion payload

- [ ] **Step 2: Run the targeted tests to verify they fail**

Run: `go test ./internal/runtime ./internal/react ./internal/react/tools -run 'Test(RegisterReactDelegationTools|MapSpawnRole|SpawnAgent|WaitAgent)'`
Expected: FAIL because current code still routes through legacy subagent wiring and remaps roles.

- [ ] **Step 3: Implement the minimal react-native delegation changes**

Implement:
- remove role remapping in `internal/react/agent_pool.go`
- create a react-native spawned-agent executor in the runtime path
- rewire `registerReactDelegationTools` to use the new executor
- keep async lifecycle metadata, but carry completion text directly

- [ ] **Step 4: Re-run the targeted tests to verify they pass**

Run: `go test ./internal/runtime ./internal/react ./internal/react/tools -run 'Test(RegisterReactDelegationTools|MapSpawnRole|SpawnAgent|WaitAgent)'`
Expected: PASS

## Task 2: Reduce prompt bloat in the visible default path

**Files:**
- Modify: `internal/agent/system.go`
- Modify: `internal/agent/agent.go`
- Modify: prompt-related tests in `internal/agent`

- [ ] **Step 1: Write failing tests for prompt assembly changes**

Cover:
- visible main-agent prompt does not require `detectProject()` repo walking on each build
- visible main-agent prompt does not automatically include the full skill activation catalog
- prompt still includes the essential working-directory and tool guidance context

- [ ] **Step 2: Run the focused prompt tests to verify they fail**

Run: `go test ./internal/agent -run 'Test(BuildSystemPrompt|NewAgent)'`
Expected: FAIL because current prompt assembly still includes project scanning and full skill catalog injection.

- [ ] **Step 3: Implement the minimal prompt-slimming pass**

Implement:
- split visible prompt building from worker/strict-local prompt building where needed
- remove or gate `detectProject()` from the default visible prompt
- stop passing `skills.Describe(loadedSkills)` into the default visible prompt

- [ ] **Step 4: Re-run the focused prompt tests to verify they pass**

Run: `go test ./internal/agent -run 'Test(BuildSystemPrompt|NewAgent)'`
Expected: PASS

## Task 3: Run regression coverage for touched paths

**Files:**
- Modify only if a test fix is required

- [ ] **Step 1: Run targeted package tests for the changed areas**

Run: `go test ./internal/agent ./internal/react ./internal/react/tools ./internal/runtime`
Expected: PASS

- [ ] **Step 2: Run a broader verification pass if targeted tests are clean**

Run: `go test ./...`
Expected: PASS, or a bounded list of unrelated failures already present in the worktree.

- [ ] **Step 3: Record any residual follow-up work**

Capture:
- remaining gaps to the full north-star design
- whether `react` still needs a true loop/session rewrite later
- whether kernel fallback cleanup can now be scheduled safely
