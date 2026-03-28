# Forge Init Bootstrap Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let a fresh Forge install start successfully by bootstrapping the base config directory and handling the zero-provider state inside the app instead of exiting.

**Architecture:** Add a small bootstrap helper that ensures the Forge config scaffold exists before config loading, including a commented custom-provider template that does not register a real provider. Update chat setup and the chat TUI so startup can proceed with no active model, but user prompts are blocked until a provider/model is configured through the existing picker flow.

**Tech Stack:** Go, Bubble Tea TUI, existing Forge bootstrap/runtime/config packages

---

## Chunk 1: Tests First

### Task 1: Scaffold creation tests

**Files:**
- Modify: `internal/bootstrap/preflight_test.go`
- Create: `internal/bootstrap/init_test.go`

- [ ] **Step 1: Write the failing scaffold test**

Add a test that points Forge config home at a temp directory, calls the new bootstrap helper, and expects:
- the Forge config dir to exist
- the `providers/` dir to exist
- the provider template file to exist
- the template file not to define an active `[model_providers.*]` block

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/bootstrap -run 'TestEnsureConfigScaffold'`
Expected: FAIL because the helper does not exist yet

### Task 2: Zero-model chat setup test

**Files:**
- Modify: `internal/runtime/chat_test.go`

- [ ] **Step 1: Write the failing chat-setup test**

Add a test that stubs token/config loading so no providers are configured and verifies `BuildChatSetup(...)` returns a non-nil setup with:
- empty `ChatModel`
- nil `Driver`
- populated provider options
- no error

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/runtime -run 'TestBuildChatSetupAllowsNoConfiguredModels'`
Expected: FAIL because setup is currently nil or returns an error path

### Task 3: Chat prompt guard test

**Files:**
- Modify: `internal/tui/chatmodel_test.go`

- [ ] **Step 1: Write the failing empty-state submit test**

Add a test that creates a `ChatModel` with no model selected, enters normal user text, submits, and expects:
- no busy state
- no command sent to the input channel
- a flash/status message telling the user to configure a provider/model first

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/tui -run 'TestChatModelBlocksPromptWithoutConfiguredModel'`
Expected: FAIL because the current submit path tries to run normally

## Chunk 2: Implementation

### Task 4: Bootstrap config scaffold

**Files:**
- Modify: `internal/bootstrap/runtime.go`
- Create: `internal/bootstrap/init.go`

- [ ] **Step 1: Implement the scaffold helper**

Create the helper that ensures:
- `~/.config/forge/`
- `~/.config/forge/providers/`
- a commented provider template file

The helper must be idempotent and must not overwrite user files.

- [ ] **Step 2: Call the helper from config loading**

Ensure `bootstrap.LoadConfig()` performs scaffold init before reading/validating config.

- [ ] **Step 3: Run bootstrap tests**

Run: `go test ./internal/bootstrap`
Expected: PASS

### Task 5: Allow zero-provider chat startup

**Files:**
- Modify: `internal/runtime/chat.go`

- [ ] **Step 1: Update chat setup behavior**

If no configured models are available:
- still return a `ChatSetup`
- leave `ChatModel` empty
- leave `Driver` nil
- preserve provider options so the picker works

- [ ] **Step 2: Keep model switching activation-based**

Do not invent a fake model. Existing `SwitchModel` should remain the path that creates a driver after the user configures a provider.

- [ ] **Step 3: Run runtime tests**

Run: `go test ./internal/runtime`
Expected: PASS

### Task 6: Guard prompt submission in empty state

**Files:**
- Modify: `internal/tui/chatmodel.go`

- [ ] **Step 1: Add minimal submit guard**

When the current model is empty:
- block non-slash user prompt submission
- leave the app idle
- set a clear flash message pointing to `/provider` or `/models`

- [ ] **Step 2: Run TUI tests**

Run: `go test ./internal/tui`
Expected: PASS

## Chunk 3: Verification

### Task 7: Focused verification

**Files:**
- Modify: none

- [ ] **Step 1: Run focused package tests**

Run: `go test ./internal/bootstrap ./internal/runtime ./internal/tui`
Expected: PASS

### Task 8: Changed-file confidence check

**Files:**
- Modify: none

- [ ] **Step 1: Run broader test verification if practical**

Run: `go test ./...`
Expected: PASS, or clearly identify any pre-existing unrelated failures
