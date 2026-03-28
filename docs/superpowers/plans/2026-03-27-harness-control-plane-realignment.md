# Forge Harness Control-Plane Realignment Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the remaining prompt-shaped visible execution path with a fail-closed, host-owned control plane so preview, research, implementation, and handoff turns stop depending on fragile free-form tool markup and shell orchestration.

**Architecture:** Keep one visible `forge` assistant and the existing kernel classifier/session/policy shell, but split execution into a conversational path and a strict action path. The strict path will own visible collaboration, preview/server lifecycle, and malformed-tool recovery using typed host tools and structured observations. Hidden workers remain bounded helpers for isolated research, edits, and verification only.

**Tech Stack:** Go, existing `internal/harness`, `internal/agent`, `internal/runtime`, `internal/agent/tools`, Go test/build tooling, real-log regression fixtures.

---

## References

- Investigation report: `docs/reports/forge-harness-architecture-investigation-2026-03-27.md`
- Existing redesign design: `docs/superpowers/specs/2026-03-25-forge-harness-redesign-design.md`
- Existing redesign implementation plan: `docs/superpowers/plans/2026-03-25-forge-harness-redesign-implementation.md`

## Chunk 1: Make The Current Main Path Fail Closed

### Task 1: Add regression coverage for malformed visible-path tool output

**Files:**
- Modify: `internal/agent/agent_test.go`
- Modify: `internal/harness/local_test.go`
- Modify: `internal/runtime/chat_debug_test.go`
- Create: `internal/harness/testdata/debuglogs/visible-collaboration-malformed-tool.jsonl`

- [ ] **Step 1: Write failing tests for the current silent-complete behavior**

Cover:
- a visible local turn that emits raw `<tool_call>` markup with missing `"name"` is retried or blocked instead of being accepted as a final answer
- `AgentExecutor.Execute(...)` does not treat raw malformed tool markup as a successful completed observation
- runtime emits a user-visible blocked/error response rather than ending the turn silently

- [ ] **Step 2: Run the focused tests and verify they fail**

Run:
- `go test ./internal/agent -run 'TestAgent.*Malformed.*Visible' -count=1`
- `go test ./internal/harness -run 'TestLocal.*Malformed.*Visible' -count=1`
- `go test ./internal/runtime -run 'TestChatDebug.*Malformed.*Visible' -count=1`

Expected: FAIL because the main visible path still treats malformed tool markup as a completed answer.

- [ ] **Step 3: Implement the minimal fail-closed behavior**

Modify:
- `internal/agent/agent.go`
  Add malformed-tool retry/blocked behavior for the main local path, not only `scout` and subagents.
- `internal/harness/local.go`
  Reject raw tool markup or empty actionable output as `ObservationBlocked`.
- `internal/runtime/chat.go`
  Ensure blocked local turns still produce a user-visible blocked response.

- [ ] **Step 4: Run the focused tests and verify they pass**

Run:
- `go test ./internal/agent -run 'TestAgent.*Malformed.*Visible' -count=1`
- `go test ./internal/harness -run 'TestLocal.*Malformed.*Visible' -count=1`
- `go test ./internal/runtime -run 'TestChatDebug.*Malformed.*Visible' -count=1`

Expected: PASS.

## Chunk 2: Move Preview And Artifact Lifecycle Into Host-Owned Tools

### Task 2: Add typed preview/artifact tools and session state

**Files:**
- Create: `internal/agent/tools/artifact.go`
- Create: `internal/agent/tools/artifact_test.go`
- Create: `internal/agent/tools/preview.go`
- Create: `internal/agent/tools/preview_test.go`
- Modify: `internal/agent/tools/registry.go`
- Modify: `internal/harness/types.go`
- Modify: `internal/harness/session.go`
- Modify: `internal/harness/session_test.go`
- Modify: `internal/harness/runner.go`
- Modify: `internal/runtime/chat.go`

- [ ] **Step 1: Write failing tests for typed preview workflows**

Cover:
- the host can create a tracked artifact and return a structured handle
- the host can start or reuse a preview server without model-authored shell process management
- preview state persists across turns in session state
- follow-up turns can reuse the stored preview handle instead of rediscovering the server

- [ ] **Step 2: Run the focused tests and verify they fail**

Run:
- `go test ./internal/agent/tools -run 'Test(Artifact|Preview)' -count=1`
- `go test ./internal/harness -run 'TestSession.*Preview|TestRunner.*Preview' -count=1`
- `go test ./internal/runtime -run 'TestRunChat.*Preview' -count=1`

Expected: FAIL because the preview/artifact tools and session state do not exist yet.

- [ ] **Step 3: Implement the typed host-owned preview stack**

Implement:
- `artifact_write` / `artifact_read` style tool(s) with structured return values
- `preview_server_ensure` / `preview_server_status` style tool(s) with structured return values
- tracked preview state in harness session state
- runtime wiring so preview tools are available on the strict visible path

- [ ] **Step 4: Run the focused tests and verify they pass**

Run:
- `go test ./internal/agent/tools -run 'Test(Artifact|Preview)' -count=1`
- `go test ./internal/harness -run 'TestSession.*Preview|TestRunner.*Preview' -count=1`
- `go test ./internal/runtime -run 'TestRunChat.*Preview' -count=1`

Expected: PASS.

## Chunk 3: Add A Strict Visible-Action Executor

### Task 3: Split conversational local turns from strict action turns

**Files:**
- Create: `internal/harness/strictlocal.go`
- Create: `internal/harness/strictlocal_test.go`
- Modify: `internal/harness/types.go`
- Modify: `internal/harness/local.go`
- Modify: `internal/harness/planner.go`
- Modify: `internal/harness/policy.go`
- Modify: `internal/harness/runner.go`
- Modify: `internal/harness/runner_test.go`
- Modify: `internal/runtime/chat.go`

- [ ] **Step 1: Write failing tests for strict visible execution**

Cover:
- visible collaboration routes into the strict action executor instead of the generic free-form local loop
- strict action turns accept only one of: valid tool call, valid final response, blocked result
- malformed tool output is retried or blocked, never treated as completed success
- pure answer/meta turns still use the conversational path

- [ ] **Step 2: Run the focused tests and verify they fail**

Run:
- `go test ./internal/harness -run 'Test(StrictLocal|Runner.*Visible|Policy.*Visible)' -count=1`
- `go test ./internal/runtime -run 'TestRunChat.*StrictLocal' -count=1`

Expected: FAIL because strict visible-action execution does not exist yet.

- [ ] **Step 3: Implement the strict local executor**

Implement:
- a typed action result model in `internal/harness/types.go`
- strict local execution in `internal/harness/strictlocal.go`
- planner/policy routing that sends tool-heavy visible turns into the strict executor
- conversational local execution reserved for pure answer/meta turns

- [ ] **Step 4: Run the focused tests and verify they pass**

Run:
- `go test ./internal/harness -run 'Test(StrictLocal|Runner.*Visible|Policy.*Visible)' -count=1`
- `go test ./internal/runtime -run 'TestRunChat.*StrictLocal' -count=1`

Expected: PASS.

## Chunk 4: Tighten Guardrails, Handoffs, And Evals

### Task 4: Add guardrails and real-log regressions around the new boundary

**Files:**
- Modify: `internal/harness/contracts.go`
- Modify: `internal/harness/contracts_test.go`
- Modify: `internal/harness/regression_test.go`
- Modify: `internal/harness/stress_corpus_test.go`
- Modify: `internal/runtime/chat_debug.go`
- Modify: `internal/runtime/chat_debug_test.go`
- Modify: `internal/agent/tools/safety.go`
- Modify: `internal/agent/tools/registry.go`

- [ ] **Step 1: Write failing tests for guardrails and regressions**

Cover:
- preview/server tools cannot escape the workspace or reuse stale process state incorrectly
- indirect prompt/tool-output contamination does not widen scope or bypass policy
- real-log regressions include malformed main-path tool turns, preview follow-ups, and visible collaboration long runs
- debug tracing captures retry, blocked, recovery, and preview-state transitions

- [ ] **Step 2: Run the focused tests and verify they fail**

Run:
- `go test ./internal/harness -run 'Test(Regression|Stress|Contracts).*Preview|Test(Regression|Stress).*Malformed' -count=1`
- `go test ./internal/runtime -run 'TestChatDebug.*(Preview|Malformed)' -count=1`
- `go test ./internal/agent/tools -run 'Test.*Safety.*Preview' -count=1`

Expected: FAIL because the new guardrails and trace states are not fully wired yet.

- [ ] **Step 3: Implement the guardrails and regression fixtures**

Implement:
- stricter tool safety checks for preview/artifact lifecycle
- richer blocked/retry trace emission
- regression fixtures derived from the March 27 visible-collaboration failures

- [ ] **Step 4: Run the focused tests and verify they pass**

Run:
- `go test ./internal/harness -run 'Test(Regression|Stress|Contracts).*Preview|Test(Regression|Stress).*Malformed' -count=1`
- `go test ./internal/runtime -run 'TestChatDebug.*(Preview|Malformed)' -count=1`
- `go test ./internal/agent/tools -run 'Test.*Safety.*Preview' -count=1`

Expected: PASS.

## Chunk 5: Full Verification

### Task 5: Verify the realigned control plane end to end

**Files:**
- Modify: the files above only

- [ ] **Step 1: Run the targeted package suites**

Run:
- `go test ./internal/agent ./internal/agent/tools ./internal/harness ./internal/runtime -count=1`

Expected: PASS.

- [ ] **Step 2: Run the full repository test suite**

Run:
- `go test ./... -count=1`

Expected: PASS, or any unrelated pre-existing failure is documented before handoff.

- [ ] **Step 3: Build Forge**

Run:
- `go build -o ./forge ./cmd/forge`

Expected: exit `0`.

- [ ] **Step 4: Replay the captured visible-collaboration scenarios**

Run:
- `go test ./internal/harness -run 'TestRegressionFixturesRouteWithoutEscalation|TestLargePromptStressCorpusRoutesConsistently' -count=1`

Expected: PASS, including the March 27 visible-collaboration fixtures.
