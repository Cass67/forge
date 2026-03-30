# Git Conflict Workflow Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a first-class git conflict workflow primitive so Forge handles merge/rebase conflict sessions with native tools instead of improvising entirely through generic shell commands.

**Architecture:** Keep the default chat loop generic and native-tool-first, but add a dedicated `git_merge_status` tool that inspects repository conflict state and suggests the next step. Pair that with runtime notes and commit guardrails in `internal/react` so the model is steered toward the bounded workflow instead of repeating bad `git commit` attempts.

**Tech Stack:** Go, existing `internal/agent/tools` registry, existing native-tool react runtime, existing runtime chat wiring.

---

## File Map

- Create: `internal/agent/tools/git_merge.go`
  Purpose: inspect merge/rebase/cherry-pick state, parse porcelain status, and summarize next actions.
- Modify: `internal/agent/tools/git_test.go`
  Purpose: cover clean repos and conflicted merge repos.
- Modify: `internal/runtime/chat.go`
  Purpose: register the new tool on the default chat path.
- Modify: `internal/runtime/chat_test.go`
  Purpose: verify tool registration.
- Modify: `internal/react/loop.go`
  Purpose: steer active merge sessions toward `git_merge_status` in runtime notes and commit blockers.
- Modify: `internal/react/loop_test.go`
  Purpose: keep merge-blocker tests aligned with the new workflow wording.

## Task 1: Add a native merge-status tool

**Files:**
- Create: `internal/agent/tools/git_merge.go`
- Modify: `internal/agent/tools/git_test.go`

- [x] **Step 1: Write failing tests**
- [x] **Step 2: Run targeted tests to verify failure**
- [x] **Step 3: Implement `git_merge_status` with operation detection and porcelain parsing**
- [x] **Step 4: Re-run targeted tests to verify pass**

## Task 2: Register the workflow tool on the live path

**Files:**
- Modify: `internal/runtime/chat.go`
- Modify: `internal/runtime/chat_test.go`

- [x] **Step 1: Write a failing registration test**
- [x] **Step 2: Run targeted test to verify failure**
- [x] **Step 3: Register `git_merge_status` in the default tool registry**
- [x] **Step 4: Re-run targeted test to verify pass**

## Task 3: Steer merge sessions into the workflow

**Files:**
- Modify: `internal/react/loop.go`
- Modify: `internal/react/loop_test.go`

- [x] **Step 1: Update merge-blocker/runtime-note wording to explicitly point at `git_merge_status`**
- [x] **Step 2: Re-run focused react tests to verify behavior remains green**

## Verification

- `go test ./internal/agent/tools -run 'TestGitMergeStatus(CleanRepo|ReportsConflicts)'`
- `go test ./internal/runtime -run 'TestRegisterToolsAddsGitMergeStatus'`
- `go test ./internal/react -run 'TestRunner(BlocksCommitWhileMergeConflictsRemain|RequiresMutationBeforeRetryingFailedCommit)'`
- `go test ./internal/agent/tools ./internal/react ./internal/runtime`
- `go test ./...`

## Remaining Follow-Up

- If we want even stronger merge ergonomics, add a narrower `git_stage_files` or `git_merge_continue` tool instead of relying on `run_command` for those steps.
- If we want more visible model guidance, add a short native-system-prompt hint about using `git_merge_status` during merge/rebase conflict work.
