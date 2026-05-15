# Tool Turn Reliability Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make malformed or incomplete tool calls recoverable model feedback instead of terminal Forge errors.

**Architecture:** Copy the Codex/OpenCode pattern: advertise tool schemas from a single contract, validate arguments before execution, and convert validation failures into tool results tied to the original call ID. Keep fatal errors for infrastructure failures, not model-correctable argument mistakes.

**Tech Stack:** Go, existing `agenttools.Tool` registry, OpenAI-compatible native tool schemas, ReAct loop tests.

---

### Task 1: Fix Tool Schema Required Fields

**Files:**
- Modify: `internal/llm/drivers/openai.go`
- Test: `internal/llm/drivers/openai_internal_test.go`

- [ ] **Step 1: Write the failing test**

Add a test that builds a tool with required and optional params, calls the OpenAI schema helper, and asserts only required params appear in `required`.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/llm/drivers -run TestToolDefSchemaHonorsRequiredParameters`

Expected: FAIL because optional params are currently marked required.

- [ ] **Step 3: Implement minimal schema fix**

Change `toolDefSchema` to append `p.Name` to `required` only when `p.Required` is true.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/llm/drivers -run TestToolDefSchemaHonorsRequiredParameters`

Expected: PASS.

### Task 2: Add Central Tool Argument Validation

**Files:**
- Modify: `internal/react/loop.go`
- Test: `internal/react/loop_test.go`

- [ ] **Step 1: Write the failing tests**

Add tests proving missing `read_file.path` and missing `update_plan.steps` become tool results and the loop continues, not terminal runner errors.

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/react -run 'TestRunnerToolValidationFailureContinuesLoop|TestRunnerUpdatePlanMissingStepsJSONContinuesLoop'`

Expected: FAIL because validation currently reaches tool execution and aborts the turn.

- [ ] **Step 3: Implement validation helper**

Add `validateToolArgs(tool agenttools.Tool, args map[string]any) string` near `executeNativeToolCalls`. Validate required presence, required string non-empty, and scalar types for `string`, `int`, and `bool`.

- [ ] **Step 4: Convert validation failure to recoverable tool result**

In `executeNativeToolCalls`, after JSON parse and before hooks/execution, call validation. On validation failure, render `ToolCall`, render `ToolResult(..., true)`, append the native tool result, update workflow guards, and continue without returning an error.

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/react -run 'TestRunnerToolValidationFailureContinuesLoop|TestRunnerUpdatePlanMissingStepsJSONContinuesLoop'`

Expected: PASS.

### Task 3: Verify Existing Early Output And Malformed JSON Behavior

**Files:**
- Modify only if tests reveal regression: `internal/react/loop.go`
- Test: `internal/react/loop_test.go`, `internal/llm/drivers/openai_internal_test.go`

- [ ] **Step 1: Run targeted regression suite**

Run: `go test ./internal/react -run 'TestRunnerRendersToolCallAndStatsBeforeToolExecution|TestRunnerNativePathHandlesMalformedArgsJSON|TestRunnerNativeToolCallingPath'`

Expected: PASS.

- [ ] **Step 2: Run driver malformed-args regression**

Run: `go test ./internal/llm/drivers -run TestRepairToolCallArgsJSON`

Expected: PASS.

### Task 4: Full Relevant Verification

**Files:**
- No code changes.

- [ ] **Step 1: Run fresh package tests**

Run: `go test -count=1 ./internal/react ./internal/tui ./internal/llm/drivers`

Expected: all packages PASS.

- [ ] **Step 2: Inspect final diff**

Run: `git diff -- internal/react/loop.go internal/react/loop_test.go internal/llm/drivers/openai.go internal/llm/drivers/openai_internal_test.go`

Expected: diff contains central validation and schema fixes only; no unrelated refactors.
