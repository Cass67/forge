# Reactive Compaction Live Coverage Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Add deterministic local-provider acceptance coverage proving Forge reacts to a context-window error by compacting, retrying, and continuing.

**Architecture:** Extend the existing `cmd/forge/live_acceptance_test.go` local OpenAI-compatible harness. The mock provider will return a classified context-window error for a marker prompt, then assert the retry request contains compaction context before returning a final success response.

**Tech Stack:** Go tests, `httptest`, Forge console mode, existing react runtime compaction machinery.

---

### Task 1: Add Reactive Compaction Acceptance Test

**Files:**
- Modify: `cmd/forge/live_acceptance_test.go`

**Step 1: Write the failing test**

Add `TestLiveAcceptanceReactiveCompactionRecoversWithLocalProvider` that:
- starts `newLiveAcceptanceMock`;
- runs `bin/forge` in console mode with enough `LIVE_REACTIVE_COMPACTION_PRIMER` turns to make reactive compaction meaningful, then `LIVE_REACTIVE_COMPACTION_CHECK`;
- expects console output to include `react runtime: compacting after context window error` and `REACTIVE_COMPACTION_CONTINUED`;
- calls a mock assertion that verifies the provider saw a retry with compaction context.

**Step 2: Run test to verify it fails**

Run: `go test ./cmd/forge -run 'TestLiveAcceptanceReactiveCompactionRecoversWithLocalProvider' -count=1`

Expected: FAIL because the mock does not yet return a context error/recovery flow.

### Task 2: Implement Minimal Mock Provider Flow

**Files:**
- Modify: `cmd/forge/live_acceptance_test.go`

**Step 1: Add mock state**

Add booleans for context-error request and compaction retry observation.

**Step 2: Add provider branches**

For the first `LIVE_REACTIVE_COMPACTION_CHECK` request, return HTTP 400 with text containing `maximum context length`.

For the retry request, assert the request body includes the compaction system context text (`Earlier conversation summary` or `context window exceeded`) and return `REACTIVE_COMPACTION_CONTINUED`.

Return the first error in OpenAI-style structured JSON so the OpenAI client preserves the provider message for Forge error classification.

**Step 3: Run test to verify it passes**

Run: `go test ./cmd/forge -run 'TestLiveAcceptanceReactiveCompactionRecoversWithLocalProvider' -count=1`

Expected: PASS.

### Task 3: Update Roadmap Documentation

**Files:**
- Modify: `docs/reliability-security-roadmap.md`

**Step 1: Update live matrix wording**

Change the compaction item from manual-only coverage to manual plus reactive context-error recovery coverage.

**Step 2: Preserve external-provider future note**

Keep external provider smoke checks documented as inconclusive/future, not a release gate.

### Task 4: Verify

Run:
- `go test ./cmd/forge -run 'TestLiveAcceptance' -count=1`
- `go test ./... -timeout 120s`
- `just build`
- `git diff --check`
- `gitleaks git --redact`

Expected: all commands exit 0.
