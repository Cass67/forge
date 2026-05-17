# Bulletproof Runtime Durability Design

## Goal

Make chat/runtime state durable enough that Forge can recover from process exits, provider/tool failures, and persistence failures without losing the authoritative transcript of what happened. The durable log is the source of truth for replay, debugging, and future resume UI.

## Scope

This pass hardens the existing JSONL protocol and session store. It does not replace JSONL with a database and does not build a full session picker UI. It makes the persisted record complete, error-aware, replayable, and covered by failure-path tests.

## Architecture

Runtime code emits typed `protocol.Item` records into `react.Session`. When a durable sink is configured, each item is synchronously appended to `sessionstore.JSONLThreadStore` after redaction/truncation policy is applied. The JSONL files remain append-only for turn events, with sidecar metadata for thread listings and resume entry points.

The session store owns filesystem durability guarantees: strict permissions, atomic metadata writes, fsync on appended JSONL data, close-error propagation, corrupted-line reporting, ordered replay, and safe thread forking. `react.Session` owns runtime semantics: assigning turn IDs, recording user/assistant/tool/result/failure/terminal events, tracking persistence errors, and rebuilding in-memory state from durable items.

## Required Behavior

Every meaningful chat transition must create a durable item: session metadata, user inputs, queued-input state, assistant messages, assistant tool calls, native tool results, classified failures, interrupted/completed turns, compaction summaries, and stats.

Persistence failures must not disappear. If a sink append fails, `Session` remembers the last persistence error and exposes it in snapshots.

Replay must rebuild enough state for resume/debugging: history messages, turn records, recent inputs, terminal statuses, failed/interrupted state, assistant/tool message ordering, and compaction summary. Replay rejects malformed terminal sequences rather than silently inventing state.

## Testing Strategy

Durability tests cover JSONL append/read across store reopen, metadata update/read/list behavior, corrupt JSONL line diagnostics, replay reconstruction, multiple-terminal rejection, durable sink append errors, queued input, assistant tool-call, tool result, failure, interrupt, stats, compaction, and runtime output-dir integration.

Final verification is `go test -count=1 ./...` plus `just build`.
