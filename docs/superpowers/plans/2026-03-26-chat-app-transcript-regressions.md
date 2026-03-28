# Chat App Transcript Regressions Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add automated app-level transcript testing that drives Forge through real multi-turn chat conversations, caps each transcript at 50 turns, and catches runtime regressions that unit-only harness tests miss.

**Architecture:** Reuse the existing `RunChatLive` runtime path and override the live UI only in tests so the real runtime loop, kernel routing, hidden workers, and renderer events still execute. Use a deterministic driver that inspects the runtime prompts it receives, emits realistic tool-call sequences and worker JSON, and fails closed when the runtime takes the wrong path. Keep the test workspaces additive and temporary; do not delete or mutate repo files.

**Tech Stack:** Go, `internal/runtime`, `internal/harness`, existing `agent` tool loop, Go test/build tooling.

---

## File Map

- Create: `internal/runtime/chat_transcript_test.go`
  Purpose: app-level transcript runner, deterministic driver, multi-turn fixtures, and runtime assertions.
- Modify: `internal/runtime/chat.go`
  Purpose: only if transcript tests expose a real runtime bug such as missing synthetic rendering for kernel-local short-circuit responses.
- Modify: `internal/runtime/chat_test.go` or `internal/runtime/chat_pty_test.go`
  Purpose: only if a small helper extraction or PTY smoke expansion is needed.

## Chunk 1: App-Level Transcript Runner

### Task 1: Write failing transcript tests against the real runtime loop

**Files:**
- Create: `internal/runtime/chat_transcript_test.go`

- [ ] **Step 1: Add a scripted `RunChatLive` test runner**

Cover:
- sequential user prompts sent through `inputCh`
- per-turn event collection until `llm.EventDone` or failure
- hard cap of 50 turns per transcript
- capture of visible responses, runtime activity, and optional debug log output

- [ ] **Step 2: Add multi-turn transcript fixtures that reflect known problem areas**

Cover at least:
- neutral directory walkthrough -> evaluative follow-up -> cleanup script follow-up
- evaluative repo review -> vague contextual follow-up -> prompt-boundary question
- typo-heavy repo review prompts from the corpus on the actual app path

- [ ] **Step 3: Run the new focused runtime tests and confirm the first red failure**

Run: `go test ./internal/runtime -run 'TestChatTranscript' -count=1`
Expected: FAIL until the transcript runner and any exposed runtime bug are fixed.

### Task 2: Implement the minimal runtime support needed to make the transcripts pass

**Files:**
- Modify: `internal/runtime/chat.go` only if the transcript tests prove a real app bug
- Create/Modify: `internal/runtime/chat_transcript_test.go`

- [ ] **Step 1: Build a deterministic transcript driver**

Requirements:
- inspect the runtime prompt shape (`HARNESS MODE`, `OBJECTIVE`, worker evidence requirements)
- emit valid tool-call sequences before final answers
- emit structured worker JSON for reader/editor paths
- fail closed on unexpected runtime routes

- [ ] **Step 2: Fix real runtime regressions, not just the test harness**

Examples:
- kernel-local responses that never render into the chat transcript
- worker/local paths that lose follow-up context
- prompt-boundary answers that disappear on the app path

- [ ] **Step 3: Keep all test workspaces temporary and additive**

No repo deletion. No mutation of tracked workspace files. Use temp dirs for fixture repos and local skill files.

### Task 3: Verify broadly and land

**Files:**
- Create/Modify files above

- [ ] **Step 1: Re-run focused transcript tests**

Run: `go test ./internal/runtime -run 'TestChatTranscript' -count=1`
Expected: PASS

- [ ] **Step 2: Run broader verification**

Run:
- `go test ./internal/runtime -count=1`
- `go test ./... -count=1`
- `go build -o ./forge ./cmd/forge`

- [ ] **Step 3: Do one real app smoke after the automated suite**

Run the built app through at least one live transcript and inspect the fresh debug log path it creates.

- [ ] **Step 4: Commit directly to `main`**

Commit message target: `test: add app transcript regressions`
