# Agent View Copy-Safe Panel Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Prevent terminal copy selection from including agent side-panel text by replacing the side-by-side panel with a dedicated full-width `/agentview`.

**Architecture:** Keep collecting agent work in the existing `toolsSections` and `agentTasks` buffers, but stop rendering it beside the transcript. Add a full-width agent view mode opened by `/agentview`; `Tab` cycles active agents inside that mode, and `Esc` closes it. Remove the old `/panel` command surface so `/agentview` is the only visible agent-work panel control.

**Tech Stack:** Go, Bubble Tea, Lip Gloss, existing TUI tests in `internal/tui/chatmodel_test.go`.

---

### Task 1: Prove Normal Transcript Is Copy-Safe

**Files:**
- Modify: `internal/tui/chatmodel_test.go`

**Step 1:** Add a failing test showing agent work is not rendered in the normal transcript view, even when agent work exists.

**Step 2:** Run `go test ./internal/tui -run 'TestChatModelNormalViewDoesNotRenderAgentWorkBesideTranscript' -count=1` and verify failure.

### Task 2: Add `/agentview` Full-Width View

**Files:**
- Modify: `internal/tui/chatmodel.go`
- Modify: `internal/tui/chatmodel_test.go`

**Step 1:** Add failing tests for `/agentview` opening a full-width agent work view and `Esc` closing it.

**Step 2:** Implement `agentViewVisible` state, command handling, and render path.

**Step 3:** Run focused TUI tests and verify pass.

### Task 3: Add Agent Cycling

**Files:**
- Modify: `internal/tui/chatmodel.go`
- Modify: `internal/tui/chatmodel_test.go`

**Step 1:** Add a failing test showing `Tab` cycles selected active agents while `/agentview` is open.

**Step 2:** Implement selected-agent index helpers and filtering for the full-width view.

**Step 3:** Run focused TUI tests and verify pass.

### Task 4: Verify

Run:
- `go test ./internal/tui -run 'AgentView|Panel|Mouse|Tab' -count=1`
- `go test ./internal/tui -count=1`
- `go test ./... -timeout 120s`
- `just build`
- `git diff --check`
