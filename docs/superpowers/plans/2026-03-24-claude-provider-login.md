# Claude Provider Login Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a separate unsupported `claude` provider that uses Claude.ai OAuth login with pasted callback/code input, while preserving `anthropic` as the API-key provider.

**Architecture:** Introduce a Forge-owned Claude OAuth manager and session store, add a new `claude` provider/backend and model routing path, then extend the provider picker to drive a paste-based browser auth flow. Keep the existing Anthropic API-key driver intact and isolate unsupported Claude.ai behavior behind a distinct provider ID.

**Tech Stack:** Go, Bubble Tea TUI, Forge auth storage, Claude web auth plus Anthropic message endpoints, PKCE OAuth

---

### Task 1: Write Claude Auth Tests First

**Files:**
- Create: `internal/claudeauth/auth_test.go`
- Create: `internal/claudeauth/auth.go`
- Test: `internal/claudeauth/auth_test.go`

- [ ] **Step 1: Write failing tests for authorize URL, pasted input parsing, Forge-only load, and refresh helpers**
- [ ] **Step 2: Run `go test ./internal/claudeauth` and verify failures are about missing implementation**
- [ ] **Step 3: Implement minimal Claude auth package with PKCE, paste parsing, session load/store, and refresh support**
- [ ] **Step 4: Run `go test ./internal/claudeauth` and make it pass**
- [ ] **Step 5: Commit the auth package**

### Task 2: Add Claude Auth Storage

**Files:**
- Modify: `internal/auth/store.go`
- Modify: `internal/chatshared.go`
- Test: `internal/claudeauth/auth_test.go`

- [ ] **Step 1: Write/extend failing tests that require Claude session persistence in Forge auth storage**
- [ ] **Step 2: Run targeted tests and verify failure**
- [ ] **Step 3: Add Claude token fields and clear/set helpers without affecting other providers**
- [ ] **Step 4: Re-run targeted tests and verify pass**
- [ ] **Step 5: Commit storage changes**

### Task 3: Add Provider Backend And Model Routing

**Files:**
- Modify: `internal/bootstrap/runtime.go`
- Modify: `internal/bootstrap/preflight.go`
- Modify: `internal/bootstrap/resolve_test.go`

- [ ] **Step 1: Write failing tests for Claude backend visibility, readiness, and explicit/unqualified model routing**
- [ ] **Step 2: Run `go test ./internal/bootstrap/...` and verify failures**
- [ ] **Step 3: Implement `claude` provider backend and routing preference rules**
- [ ] **Step 4: Re-run `go test ./internal/bootstrap/...` and verify pass**
- [ ] **Step 5: Commit bootstrap changes**

### Task 4: Add Claude Subscription Driver

**Files:**
- Create: `internal/llm/drivers/claude_oauth.go`
- Modify: `internal/llm/drivers/claude_test.go`
- Modify: `internal/bootstrap/runtime.go`

- [ ] **Step 1: Write failing driver tests for bearer-auth Claude requests and refresh-on-expiry behavior**
- [ ] **Step 2: Run targeted driver tests and verify failure**
- [ ] **Step 3: Implement the OAuth-backed Claude driver with the same `llm.Driver` contract**
- [ ] **Step 4: Re-run targeted driver tests and verify pass**
- [ ] **Step 5: Commit driver changes**

### Task 5: Add TUI Claude Login Flow

**Files:**
- Modify: `internal/tui/chatmodel.go`
- Modify: `internal/tui/chatmodel_test.go`
- Modify: `internal/tui/chatshared.go`

- [ ] **Step 1: Write failing TUI tests for Claude provider sign-in, pasted callback/code submission, save, switch, and delete**
- [ ] **Step 2: Run `go test ./internal/tui/...` and verify failures**
- [ ] **Step 3: Implement provider picker changes, paste handling, and status refresh**
- [ ] **Step 4: Re-run `go test ./internal/tui/...` and verify pass**
- [ ] **Step 5: Commit TUI changes**

### Task 6: Verify End To End

**Files:**
- Modify: any touched files from prior tasks

- [ ] **Step 1: Run `go build ./...`**
- [ ] **Step 2: Run `go test ./...`**
- [ ] **Step 3: Fix any regressions found**
- [ ] **Step 4: Re-run `go build ./...` and `go test ./...` until green**
- [ ] **Step 5: Commit final cleanup**
