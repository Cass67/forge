# Forge Harness Audit Remediation And Prompt Validation Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Close the remaining post-realignment harness gaps, re-audit the code against the approved architecture, and drive the live app through broad prompt coverage until visible collaboration, inspection, preview, and follow-up turns behave reliably.

**Architecture:** Keep the March 27 control-plane realignment, but harden the remaining seams where prompt-shaped behavior still leaks through: malformed visible-path tool markup, strict-local inspect contracts, preview/artifact state ownership, and debug observability. Then validate the result with targeted regressions, corpus coverage, and live debug-log-driven prompt runs.

**Tech Stack:** Go, `internal/agent`, `internal/agent/tools`, `internal/harness`, `internal/runtime`, Go test/build tooling, live `forge` debug logs, existing investigation/debug reports.

---

## References

- `docs/reports/forge-harness-architecture-investigation-2026-03-27.md`
- `docs/superpowers/plans/2026-03-27-harness-control-plane-realignment.md`
- `docs/reports/forge-harness-debug-2026-03-27.md`
- `docs/reports/forge-harness-stress-20260327T000014.md`

## Chunk 1: Fail Closed On Any Raw Malformed Visible-Path Tool Markup

### Task 1: Catch malformed tool markup even when mixed with prose

**Files:**
- Modify: `internal/agent/agent_test.go`
- Modify: `internal/harness/local_test.go`
- Modify: `internal/harness/strictlocal_test.go`

- [ ] **Step 1: Write failing tests**

Cover:
- a main visible-path action turn that starts with short prose and then emits malformed `<tool_call>` markup retries or fails closed
- local executor validation blocks any raw tool markup residue, not only markup-prefixed responses
- strict local keeps the same fail-closed behavior for prose-prefixed malformed output

- [ ] **Step 2: Run focused tests and confirm failure**

Run:
- `go test ./internal/agent -run 'TestAgent.*Malformed.*Main|TestAgent.*Malformed.*Visible' -count=1`
- `go test ./internal/harness -run 'TestAgentExecutor.*Malformed|TestStrictAgentExecutor.*Malformed' -count=1`

Expected: FAIL before the implementation change.

- [ ] **Step 3: Implement the minimal fix**

Modify:
- `internal/agent/agent.go`
- `internal/harness/local.go`

Implement:
- main-path malformed-tool recovery should trigger on raw tool markup anywhere in the response when no valid calls parsed
- visible-path local validation should reject any raw tool markup residue, including prose-prefixed malformed attempts

- [ ] **Step 4: Re-run focused tests**

Run the two commands above and confirm PASS.

## Chunk 2: Restore Inspect-Specific Contracts On Strict Visible Turns

### Task 2: Keep strict-local inspect turns evidence-first and tool-first

**Files:**
- Modify: `internal/harness/strictlocal_test.go`
- Modify: `internal/harness/local.go`
- Modify: `internal/harness/strictlocal.go`
- Modify: `internal/runtime/chat_debug_test.go`

- [ ] **Step 1: Write failing tests**

Cover:
- visible inspect turns routed to `StepStrictLocal` still use inspect-specific prompt requirements
- strict-local inspect prompts preserve the evidence-first contract and one-tool-call working rule
- debug normalization recognizes strict-local inspect requests, not just hidden workers and read-only inspect turns

- [ ] **Step 2: Run focused tests and confirm failure**

Run:
- `go test ./internal/harness -run 'TestStrictAgentExecutor.*Inspect|TestRunnerVisibleCollaborationUsesStrictLocalStep' -count=1`
- `go test ./internal/runtime -run 'TestEnableChatDebug.*Strict.*Inspect|TestEnableChatDebug.*Inspect' -count=1`

Expected: FAIL before the prompt/normalization changes.

- [ ] **Step 3: Implement the minimal fix**

Modify:
- `internal/harness/strictlocal.go`
- `internal/harness/local.go`
- `internal/runtime/chat_debug.go`

Implement:
- strict-local prompt selection that preserves inspect-specific contracts for visible inspect/progress-update turns
- debug-log strict-turn detection for strict-local visible collaboration requests

- [ ] **Step 4: Re-run focused tests**

Run the two commands above and confirm PASS.

## Chunk 3: Move Preview And Artifact State Into Harness Session

### Task 3: Persist preview/artifact handles and requested server state across turns

**Files:**
- Modify: `internal/agent/tools/artifact.go`
- Modify: `internal/agent/tools/artifact_test.go`
- Modify: `internal/agent/tools/preview.go`
- Modify: `internal/agent/tools/preview_test.go`
- Modify: `internal/harness/types.go`
- Modify: `internal/harness/session.go`
- Modify: `internal/harness/session_test.go`
- Modify: `internal/runtime/chat.go`

- [ ] **Step 1: Write failing tests**

Cover:
- `preview_server_ensure` accepts a JSON-decoded numeric port request
- tracked artifact metadata can be read back by handle
- session state retains last preview/artifact metadata after a successful preview-oriented turn
- follow-up turns can reuse stored preview/artifact state rather than rediscovering it from scratch

- [ ] **Step 2: Run focused tests and confirm failure**

Run:
- `go test ./internal/agent/tools -run 'Test(Artifact|Preview)' -count=1`
- `go test ./internal/harness -run 'TestSession.*(Preview|Artifact)' -count=1`
- `go test ./internal/runtime -run 'TestBuildHarnessRunnerConfigIncludesStrictLocalExecutor|TestRunChat.*Preview' -count=1`

Expected: FAIL on the new session-state and port-handling coverage before implementation.

- [ ] **Step 3: Implement the minimal fix**

Modify:
- `internal/agent/tools/artifact.go`
- `internal/agent/tools/preview.go`
- `internal/harness/types.go`
- `internal/harness/session.go`
- `internal/runtime/chat.go`

Implement:
- `artifact_read`
- float64-to-int coercion for requested preview port
- preview/artifact session state and runtime wiring
- preview runtime shutdown on chat teardown

- [ ] **Step 4: Re-run focused tests**

Run the three commands above and confirm PASS.

## Chunk 4: Re-Audit And Expand Regression Coverage

### Task 4: Verify the code now matches the investigation report and realignment intent

**Files:**
- Modify: `docs/reports/forge-harness-debug-2026-03-27.md`
- Create: `docs/reports/forge-harness-audit-remediation-2026-03-27.md`
- Modify: `internal/harness/stress_corpus_test.go`
- Modify: `internal/runtime/chat_debug_test.go`

- [ ] **Step 1: Extend regression/corpus coverage**

Add prompt coverage for:
- prose-prefixed malformed tool attempts
- visible inspect + progress-update phrasing
- preview follow-ups and short continuations
- prompt variants that should still route to the same strict-local behavior

- [ ] **Step 2: Run focused regressions**

Run:
- `go test ./internal/harness -run 'TestLargePromptStressCorpusRoutesConsistently|TestRegression.*' -count=1`
- `go test ./internal/runtime -run 'TestEnableChatDebug.*' -count=1`

- [ ] **Step 3: Write the remediation audit note**

Document:
- what was still wrong
- how the implementation changed
- what remains intentionally lexical versus host-owned

## Chunk 5: Broad Verification And Live Prompt Validation

### Task 5: Verify packages, build, then run live prompt sweeps against debug logs

**Files:**
- Modify only the files above plus new validation docs/log summaries if needed

- [ ] **Step 1: Run package verification**

Run:
- `go test ./internal/agent ./internal/agent/tools ./internal/harness ./internal/runtime -count=1`

- [ ] **Step 2: Run full-repo verification**

Run:
- `go test ./... -count=1`
- `go build -o ./forge ./cmd/forge`

- [ ] **Step 3: Run live prompt validation**

Drive the app with:
- a wide prompt corpus targeting at least 100 prompts spanning answer, inspect, debug, implement, verify, preview, and continuation turns
- multi-turn sequences targeting at least 50 turns total, including short acknowledgements and follow-up requests

Capture:
- debug logs
- routing/step selection
- blocked/retry traces
- response quality notes

- [ ] **Step 4: Iterate until the live prompt sweep stops surfacing harness-design regressions**

For each failure:
- extract the log evidence
- identify root cause
- add or extend a regression test
- implement the minimal fix
- rerun verification and the affected live prompts
