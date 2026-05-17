# Forge Fireproof Stability Design

Date: 2026-05-17

## Goal

Make Forge resilient to interrupted turns, long-running tools, large outputs, edit regressions, and process restarts by introducing a small stability kernel around the existing React loop and session store.

The design keeps model behavior and tool semantics intact where possible. It adds explicit runtime state, durable events, bounded tool execution, and resumable replay so Forge can tell the truth about what happened and what remains recoverable.

## Approved Design Summary

- Active turn invariant: exactly one active turn may own model/tool execution at a time. Turn-scoped contexts are created at turn start, propagated into tool execution, and cleared on terminal completion, failure, cancellation, or interruption.
- Durable event log: session history is mirrored into ordered protocol items with stable turn IDs, tool calls, tool results, stats, checkpoints, compaction events, failures, and terminal turn-complete records.
- Tool timeout and cancellation: native tools execute through a bounded orchestrator that classifies results as succeeded, failed, timed out, or cancelled. User cancellation and per-tool deadlines propagate through the active turn context.
- Output handles and `read_output`: large tool outputs are stored by content-addressed handle instead of being forced into the transcript. Tool results carry handle, hash, truncation, and byte metadata; `read_output` retrieves bounded, redacted slices on demand.
- Checkpoints: mutating tools create one checkpoint before the first mutation in a turn, with changed-file scope recorded in the protocol log. Restores are scoped to affected paths rather than unrelated workspace state.
- Post-edit diagnostics: writes, edits, patch application, scratchpad changes, git operations, and other configured mutations can trigger bounded validation. Failures are fed back to the next model step as runtime diagnostic feedback instead of silently continuing.
- Resumable replay: replay reconstructs turns from protocol items. Turns with activity but no terminal item are marked `resumable`; malformed duplicate terminal records are rejected.

## Boundaries

- This work does not replace the model driver, approval policy, permission model, or TUI rendering architecture.
- This work does not introduce cross-process locking beyond the active turn and session-store invariants already needed by the local runtime.
- Output handles are local session artifacts, not a remote artifact store or long-term public API.

## Acceptance Shape

- A cancelled or timed-out tool cannot keep a turn alive indefinitely.
- A restart can identify completed, failed, interrupted, and resumable turns from durable records.
- Large output remains accessible without bloating prompts or exposing unredacted secrets.
- Mutating tools leave enough checkpoint and diagnostic evidence for recovery.
- Runtime state, persisted protocol items, and model-visible recovery feedback agree about turn status.
