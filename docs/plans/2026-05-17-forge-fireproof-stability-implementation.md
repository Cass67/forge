# Forge Fireproof Stability Implementation

Date: 2026-05-17

## Status

Tasks 1-13 are accepted. Task 14 closes the work with documentation, formatting, and final verification.

## Implemented Areas

- Active turn ownership now lives in `internal/react.Session`, with explicit begin, phase update, cancellation, snapshot, and end semantics.
- Native tool execution is routed through a bounded tool orchestrator that reports success, failure, timeout, and cancellation using the active turn context.
- Protocol items in `internal/protocol` cover turn context, messages, tool calls, tool results, retries, failures, stats, compactions, checkpoints, and terminal turn completion.
- `internal/sessionstore` persists live session items and replays ordered items into completed, failed, interrupted, or resumable turns.
- Large output support adds content-addressed output storage, handle metadata on tool results, and the hidden auto-approved `read_output` tool for bounded reads.
- Mutating tool paths create checkpoints before edits and record checkpoint items with changed-file scope.
- Post-edit validators run with timeout, output caps, and process-group cleanup; failed diagnostics are fed back into session history for the next model step.
- Workspace/session-store plumbing supports checkpoint storage, output storage, and recovery-oriented replay without requiring whole-repo snapshots.

## Implementation Drift From Initial Shape

- Output handles are stored under the local session output root and verified by SHA-256 metadata; they are intentionally not exposed as durable remote artifacts.
- Replay is conservative: turns with activity but no terminal item become `resumable`, while duplicate terminal records are treated as corrupt input.
- Checkpoint restore scope is path-focused, matching the mutating tool's changed files instead of reverting unrelated workspace changes.
- Post-edit diagnostics are model feedback, not automatic self-repair; the next model step decides how to respond.

## Final Verification For Task 14

The required final checks are:

- `gofmt` on changed Go files.
- `go test ./internal/react ./internal/llm ./internal/sessionstore ./internal/agent/tools ./internal/protocol ./internal/workspace ./internal/plugins -count=1`.
- `just test`.
- `just build`.
- Diff/status inspection, with generated `cmd/forge/output` artifacts removed if present.
