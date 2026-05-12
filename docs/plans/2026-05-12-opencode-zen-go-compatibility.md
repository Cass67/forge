# OpenCode Zen Go Compatibility Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Make Forge route and serialize OpenCode Go models according to OpenCode's documented model SDK/API families instead of one-off model patches.

**Architecture:** Add a small built-in compatibility layer for OpenCode Zen/Go model capabilities. Runtime model lists and driver request serialization should consult model capabilities for supported wire APIs and request quirks, while unsupported SDK families fail clearly instead of sending malformed OpenAI-compatible requests.

**Tech Stack:** Go, Forge bootstrap/modelcatalog/runtime code, OpenAI-compatible driver, existing Go tests.

---

### Task 1: Add OpenCode Go Capability Table Tests

**Files:**
- Modify: `internal/bootstrap/resolve_test.go`
- Modify: `internal/bootstrap/runtime.go`

**Step 1: Write failing tests**

Add tests that assert the built-in `opencode-go` model list includes all OpenCode Go docs models and excludes unsupported SDK families from OpenAI-compatible execution until supported.

**Step 2: Run test to verify it fails**

Run: `go test ./internal/bootstrap -run 'OpenCodeGo|Compat' -count=1`

Expected: FAIL because current curated list is incomplete and does not encode model families.

**Step 3: Implement minimal metadata/list update**

Add built-in OpenCode Go model metadata for documented chat-compatible models, plus explicit unsupported entries for Anthropic/Alibaba/unknown families.

**Step 4: Run test to verify it passes**

Run: `go test ./internal/bootstrap -run 'OpenCodeGo|Compat' -count=1`

Expected: PASS.

### Task 2: Capability-Drive Chat Request Quirks

**Files:**
- Modify: `internal/llm/drivers/openai_internal_test.go`
- Modify: `internal/llm/drivers/openai.go`

**Step 1: Write failing tests**

Add table-driven tests for all OpenCode Go OpenAI-compatible chat models. Assert `tool_choice` and assistant tool-call replay `reasoning_content` behavior is driven by model capability, not hardcoded Kimi-only logic.

**Step 2: Run test to verify it fails**

Run: `go test ./internal/llm/drivers -run 'OpenCodeGo|Kimi|DeepSeek|ToolChoice|ReasoningContent' -count=1`

Expected: FAIL for models not covered by current model-specific branches.

**Step 3: Implement minimal capability checks**

Replace exact model branches with OpenCode Go capability predicates for required `tool_choice` and interleaved reasoning replay fields.

**Step 4: Run test to verify it passes**

Run: `go test ./internal/llm/drivers -run 'OpenCodeGo|Kimi|DeepSeek|ToolChoice|ReasoningContent' -count=1`

Expected: PASS.

### Task 3: Fail Clearly for Unsupported OpenCode Go SDK Families

**Files:**
- Modify: `internal/bootstrap/runtime.go`
- Modify: `internal/bootstrap/resolve_test.go`

**Step 1: Write failing tests**

Add tests for `opencode-go/minimax-m2.7`, `opencode-go/minimax-m2.5`, `opencode-go/qwen3.6-plus`, `opencode-go/qwen3.5-plus`, and currently undocumented live extras. Assert Forge does not silently route them through the generic OpenAI-compatible driver.

**Step 2: Run test to verify it fails**

Run: `go test ./internal/bootstrap -run 'OpenCodeGoUnsupported|DriverForModel' -count=1`

Expected: FAIL because they currently can be selected if discovered and would route as generic chat.

**Step 3: Implement minimal unsupported handling**

Make built-in unsupported OpenCode Go models absent from selectable models or return no driver with a clear preflight/selection path. Avoid adding Anthropic/Alibaba driver support in this pass.

**Step 4: Run test to verify it passes**

Run: `go test ./internal/bootstrap -run 'OpenCodeGoUnsupported|DriverForModel' -count=1`

Expected: PASS.

### Task 4: Verify End-to-End

**Files:**
- No new files.

**Step 1: Run targeted tests**

Run: `go test ./internal/bootstrap ./internal/llm/drivers ./internal/react ./internal/react/tools ./internal/runtime ./internal/skills -timeout 120s`

Expected: PASS.

**Step 2: Run full suite and build**

Run: `go test ./... -timeout 120s`

Run: `just build`

Run: `git diff --check`

Run: `gitleaks git --redact`

Expected: all pass.
