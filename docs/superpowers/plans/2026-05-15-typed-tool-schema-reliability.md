# Typed Tool Schema Reliability Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace fragile flat tool contracts with typed schemas that validate tool arguments before execution.

**Architecture:** Add structured schema support to `agenttools.Tool` and `llm.ToolDef`, use it for OpenAI provider schemas, and validate incoming tool args centrally in the ReAct loop. Migrate `update_plan` to structured `steps` and remove `steps_json` entirely.

**Tech Stack:** Go, existing Forge ReAct runner, OpenAI-compatible JSON Schema, Go unit tests.

---

### Task 1: Add Typed Tool Schema Model

**Files:**
- Modify: `internal/agent/tools/registry.go`
- Modify: `internal/llm/types.go`
- Test: `internal/agent/tools/registry_test.go`

- [ ] Add `Schema *ToolSchema` to `agenttools.Tool` and `llm.ToolDef`.
- [ ] Add `ToolSchema` fields: `Type`, `Description`, `Properties`, `Items`, `Required`, `Enum`, `AdditionalProperties`.
- [ ] Update `Registry.ToLLMToolDefs` to pass through explicit schemas and continue generating flat schemas for existing tools.
- [ ] Test that explicit schema survives registry conversion.

### Task 2: Use Typed Schema In Provider Output

**Files:**
- Modify: `internal/llm/drivers/openai.go`
- Test: `internal/llm/drivers/openai_internal_test.go`

- [ ] Update `toolDefSchema` to use `ToolDef.Schema` when present.
- [ ] Keep flat parameter schema generation for tools without a schema.
- [ ] Test nested `update_plan.steps` schema renders as array of objects with required fields and enum status.

### Task 3: Central Recursive Tool Validation

**Files:**
- Modify: `internal/react/loop.go`
- Test: `internal/react/loop_test.go`

- [ ] Replace scalar-only `validateToolArgs` with recursive schema validation when `tool.Schema` is present.
- [ ] Validate required fields, unknown fields, object, array, string, integer, boolean, and enum.
- [ ] Keep flat parameter validation fallback.
- [ ] Test missing nested fields and invalid enum are recoverable tool results.

### Task 4: Migrate update_plan To Structured Steps

**Files:**
- Modify: `internal/react/tools/update_plan.go`
- Test: `internal/react/tools/update_plan_test.go`
- Test: `internal/react/loop_test.go`

- [ ] Define `update_plan` schema with `steps` as array of objects: required `step`, required `status`, optional `blocker`.
- [ ] Accept `steps` structured input in `Execute`.
- [ ] Remove `steps_json` from the tool contract and execution path.
- [ ] Test structured input succeeds and missing `steps` produces recoverable validation feedback.

### Task 5: Verify Relevant Packages

**Files:**
- No code changes.

- [ ] Run `go test -count=1 ./internal/agent/tools ./internal/react ./internal/tui ./internal/llm/drivers`.
- [ ] Inspect diff for unrelated changes.
