# React Completion Enforcement Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Prevent Forge from completing repo-grounded turns unless the host sees concrete tool evidence that matches the work claimed in the final answer.

**Architecture:** Add a host-owned completion compliance layer on top of the existing React runtime completion check. The runner will buffer candidate final answers, validate them against per-turn tool evidence, auto-reprompt once on non-compliance, and fail visibly on repeated non-compliance instead of accepting plausible narration.

**Tech Stack:** Go, React runtime session/loop, Forge runtime completion checks, table-driven tests.

---

### Task 1: Record Turn Evidence

**Files:**
- Modify: `internal/react/session.go`
- Test: `internal/react/session_test.go`

- [ ] **Step 1: Write failing test**
- [ ] **Step 2: Run test to verify it fails**
- [ ] **Step 3: Record assistant native tool calls onto the active turn**
- [ ] **Step 4: Run test to verify it passes**

### Task 2: Add Retryable Completion Errors

**Files:**
- Modify: `internal/react/loop.go`
- Test: `internal/react/loop_test.go`

- [ ] **Step 1: Write failing test for auto-reprompt on non-compliant final answer**
- [ ] **Step 2: Run test to verify it fails**
- [ ] **Step 3: Add typed retryable completion error handling and one retry path**
- [ ] **Step 4: Buffer final answers until completion check passes**
- [ ] **Step 5: Run tests to verify they pass**

### Task 3: Enforce Repo-Grounded Completion Compliance

**Files:**
- Create: `internal/runtime/completion_enforcement.go`
- Modify: `internal/runtime/chat.go`
- Test: `internal/runtime/chat_test.go`

- [ ] **Step 1: Write failing tests for the observed bad turns**
- [ ] **Step 2: Run tests to verify they fail**
- [ ] **Step 3: Add evidence extraction and claim classification**
- [ ] **Step 4: Reject missing inspection, edit, validation, and fake blockage claims**
- [ ] **Step 5: Run tests to verify they pass**

### Task 4: Verify End-to-End Behavior

**Files:**
- Modify: `internal/react/loop_test.go`
- Modify: `internal/runtime/chat_test.go`

- [ ] **Step 1: Add end-to-end tests covering retry then success and retry then hard failure**
- [ ] **Step 2: Run targeted runtime and react test suites**
- [ ] **Step 3: Run broader package verification for touched packages**
