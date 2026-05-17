# Bulletproof Runtime Durability Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make Forge runtime state synchronously durable, replayable, and observable when persistence fails.

**Architecture:** Keep append-only JSONL as the source of truth. Harden `sessionstore` for filesystem integrity and metadata, then make `react.Session` persist every semantic runtime item and rebuild session state from those items.

**Tech Stack:** Go, JSONL, standard library filesystem APIs, existing `protocol`, `sessionstore`, `react`, and `runtime` packages.

---

## Tasks

- [x] Harden JSONL metadata and corrupt-log behavior.

  Files: `internal/sessionstore/jsonl_store.go`, `internal/sessionstore/store.go`, `internal/sessionstore/jsonl_store_test.go`.

  Verification: `go test -count=1 ./internal/sessionstore`.

- [x] Make the durable sink reopen-safe and error-observable.

  Files: `internal/sessionstore/live_session.go`, `internal/sessionstore/live_session_test.go`, `internal/react/session.go`, `internal/react/session_test.go`.

  Verification: `go test -count=1 ./internal/sessionstore ./internal/react`.

- [x] Persist complete session semantics.

  Files: `internal/react/session.go`, `internal/react/session_test.go`.

  Verification: `go test -count=1 ./internal/react`.

- [x] Add replay hydration for runtime state.

  Files: `internal/sessionstore/replay.go`, `internal/sessionstore/replay_test.go`, `internal/react/session.go`, `internal/react/session_test.go`.

  Verification: `go test -count=1 ./internal/sessionstore ./internal/react`.

- [x] Wire runtime metadata and stats durability.

  Files: `internal/runtime/chat.go`, `internal/runtime/chat_test.go`, `internal/react/loop.go`, `internal/react/loop_test.go`.

  Verification: `go test -count=1 ./internal/runtime ./internal/react`.

- [x] Run full verification and inspect final diff.

  Verification: `go test -count=1 ./internal/protocol ./internal/sessionstore ./internal/react ./internal/runtime ./internal/tui`, `go test -count=1 ./...`, `just build`, `git diff --check`.

## Notes

No secrets are read, printed, or persisted. Generated output directories from tests/build are removed before completion. No commit is created unless explicitly requested.
