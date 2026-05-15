# Typed Tool Schema Reliability Design

## Goal

Make Forge's tool system reliable by using one canonical tool contract for provider schemas, pre-execution validation, and tool execution inputs.

## Problem

Forge currently relies on flat scalar `ParameterDef` metadata. That causes native tool-calling failures when a tool needs structured input, most visibly `update_plan.steps_json`, which asks the model to encode JSON inside a string. Missing or malformed required arguments then leak as low-level runtime errors like `steps_json is required` or `empty path`.

## Design

Add optional structured schema support to tool definitions while preserving existing flat `ParameterDef` compatibility. Each tool may expose a JSON-schema-like object with nested properties, required fields, arrays, enums, and `additionalProperties: false`. Provider tool definitions are generated from that schema. The ReAct runtime validates tool arguments against the same schema before execution.

Validation failures become normal tool results associated with the original tool call ID, so the model can correct the call in the next step. Infrastructure failures and malformed top-level JSON remain terminal errors.

## Initial Scope

- Add schema types in `internal/agent/tools` and `internal/llm`.
- Generate OpenAI-compatible schemas from structured tool schemas when present.
- Validate object, array, string, integer, boolean, enum, required, and unknown fields centrally before execution.
- Migrate `update_plan` to structured `steps` input.
- Remove `steps_json`; it is intentionally not retained as a fallback because JSON-in-a-string is the failure mode.
- Add focused tests for provider schema output and recoverable validation failures.

## Out Of Scope

- Rewriting the full ReAct/session message representation into OpenCode-style message parts.
- Replacing every tool definition in one pass.
- Changing provider auth or local LLM setup.

## Success Criteria

- Missing `read_file.path` does not execute the tool and does not abort the turn.
- Missing or malformed `update_plan.steps` does not abort the turn.
- OpenAI tool schema advertises structured `update_plan.steps` rather than only `steps_json`.
- Existing flat tools continue working.
- `go test -count=1 ./internal/react ./internal/tui ./internal/llm/drivers ./internal/agent/tools` passes.
