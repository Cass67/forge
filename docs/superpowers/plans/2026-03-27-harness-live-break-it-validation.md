# Harness Live Break-It Validation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Prove the redesigned Forge harness survives broad real-world and adversarial chat flows by combining transcript stress coverage, live interactive probing, debug-log inspection, and root-cause-first remediation.

**Architecture:** Use the current kernel harness as the system under test. Start with existing stress and transcript suites to confirm the merged baseline, then exercise the real interactive chat surface under debug logging with diverse multi-turn prompts that target routing, continuation, progress visibility, strict-local preview flows, inspect follow-ups, and prompt-boundary handling. Any live failure must become a focused failing regression before code changes, then rerun targeted and full verification plus another live pass.

**Tech Stack:** Go, Bubble Tea TUI, Forge harness kernel, transcript/runtime tests, PTY/live chat surface, JSONL debug logs.

---

## Chunk 1: Baseline And Test Surface Confirmation

### Task 1: Confirm the current merged baseline and test surfaces

**Files:**
- Modify: `docs/superpowers/plans/2026-03-27-harness-live-break-it-validation.md`
- Read: `docs/reports/forge-harness-audit-remediation-2026-03-27.md`
- Read: `docs/reports/forge-harness-debug-2026-03-27.md`
- Read: `internal/runtime/chat.go`
- Read: `internal/runtime/chat_transcript_test.go`
- Read: `internal/runtime/chat_pty_test.go`

- [ ] **Step 1: Reconfirm the live entrypoints and existing adversarial coverage**

Run: `rg -n "runChat\\(|EnableChatDebug|TestChatTranscript|TestChatPTY|StressCorpus" cmd/forge/main.go internal/runtime internal/harness`
Expected: clear references to the real chat entry, debug log hook, transcript tests, PTY tests, and stress corpus.

- [ ] **Step 2: Run the existing routing and transcript regression baseline**

Run: `go test ./internal/harness ./internal/runtime -count=1`
Expected: PASS with no harness/runtime failures.

- [ ] **Step 3: Run the full repository baseline**

Run: `go test ./... -count=1`
Expected: PASS.

- [ ] **Step 4: Rebuild the shipped binary**

Run: `go build -o ./forge ./cmd/forge`
Expected: exit 0 and a fresh `./forge` binary.

## Chunk 2: Live Interactive Break-It Campaign

### Task 2: Exercise the real app under debug logging

**Files:**
- Read: `cmd/forge/main.go`
- Read: `internal/runtime/chat_debug.go`
- Artifact: `/tmp/forge-live-break-it-debug.jsonl`
- Artifact: `/tmp/forge-live-break-it-notes.md`

- [ ] **Step 1: Start the real Forge binary in debug mode**

Run: `./forge -d --debug-file /tmp/forge-live-break-it-debug.jsonl`
Expected: interactive chat launches with the debug surface and writes a fresh JSONL log.

- [ ] **Step 2: Drive diverse multi-turn prompt families**

Exercise at minimum:
- inspect/review prompts
- terse follow-up acknowledgements
- visible collaboration prompts
- preview/mockup/web-page prompts
- referential follow-ups (`show it again`, `fix that`, `is it still up?`)
- prompt-boundary attempts
- plan/process questions
- explicit new-intent interruptions during a pending continuation

Expected: correct routing, visible progress updates during work, useful final answers, and no silent hangs or raw tool markup.

- [ ] **Step 3: Inspect the debug trace after each suspicious turn**

Run: `tail -n 200 /tmp/forge-live-break-it-debug.jsonl`
Expected: trace entries show coherent family/lane/outcome routing and no repeated malformed strict-local churn or fallback confusion.

## Chunk 3: Failure Conversion And Remediation

### Task 3: Turn any live failure into a reproducible regression before fixing

**Files:**
- Modify: relevant focused tests in `internal/harness/*_test.go` or `internal/runtime/*_test.go`
- Modify: smallest matching production file once root cause is proven

- [ ] **Step 1: Reproduce the exact failing flow in a focused test**

Run: focused `go test` command for the new regression.
Expected: FAIL before the fix for the same reason seen in live use.

- [ ] **Step 2: Implement the smallest root-cause fix**

Expected: one coherent change that addresses the proved failure mode without widening behavior unnecessarily.

- [ ] **Step 3: Re-run focused and broad verification**

Run:
- `go test ./internal/harness ./internal/runtime -count=1`
- `go test ./... -count=1`
- `go build -o ./forge ./cmd/forge`
Expected: PASS.

- [ ] **Step 4: Re-run the live break-it scenario**

Expected: the original live failure no longer reproduces, and nearby follow-ups still behave correctly.

## Chunk 4: Exit Criteria

### Task 4: Close only when the evidence is strong

**Files:**
- Modify: `docs/reports/forge-harness-debug-2026-03-27.md` or a new dated report if new failures/fixes are found

- [ ] **Step 1: Record what was tested and what failed or held**

Expected: concrete prompt families, log references, and verification commands are documented.

- [ ] **Step 2: Stop only when either**

Expected:
- no live/manual failures are found across a broad prompt set and the baseline suite is green, or
- a specific remaining risk is isolated with documented reproduction and next action.
