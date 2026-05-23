# Turn Contract Kernel Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Make Forge fail closed for real work: no turn may complete until runtime evidence proves required tools/actions/artifacts/verification completed, or Forge reports an explicit failure.

**Architecture:** Add a small runtime-owned Turn Contract Kernel to the current native-tool ReAct runtime. The kernel derives a per-turn contract from user intent, records evidence from model output/tool execution/delegation/provider failures, gates assistant final text before render/persist, and persists contract state in the durable protocol. This restores the essential enforcement semantics removed with the legacy harness/completion-enforcement stack without reviving that old architecture wholesale.

**Tech Stack:** Go, existing `internal/react` ReAct loop, durable protocol/session store, native `llm.NativeToolCall`, existing tool registry, existing side-effect intent/gate code, existing live acceptance tests in `cmd/forge`.

---

## Non-Negotiable Success Criteria

- Forge may retry, ask for clarification, or fail visibly.
- Forge must not mark a real-work turn complete when requested artifacts/actions are missing.
- Assistant prose is never completion evidence by itself.
- Unknown model tools are retryable model-output failures, not normal tool results.
- Raw tool-call markup in assistant text is invalid final output.
- Child/provider failure cannot be silently converted into success.
- Plans and requested artifacts become runtime state with gates.
- The first simple real-work run after this should either complete correctly or fail with a concrete, actionable runtime error.

## Reference Evidence

Before implementing, read these commits/files for context:

- Removed completion enforcement: `git show 83ab921:internal/runtime/completion_enforcement.go`
- Removal commit: `git show --stat f99051e`
- Removed harness contracts: `git show 94e1c74^:internal/harness/contracts.go`
- Removed harness outcomes/thread ledger: `git show 94e1c74^:internal/harness/outcome.go`, `git show 94e1c74^:internal/harness/thread.go`
- Current side-effect gate code: `internal/react/side_effect_intent.go`, `internal/react/loop.go`
- Current durable protocol: `internal/protocol/items.go`, `internal/sessionstore/replay.go`
- Latest failure log: `/Users/cass/forge-output/threads/thread-1779576380304108000.jsonl`

---

## Task List

Implement these tasks sequentially with TDD and review after each:

1. Freeze the existing broken behavior as acceptance fixtures.
2. Add durable Turn Contract data model.
3. Build intent-to-contract derivation.
4. Record evidence from tool calls and tool results.
5. Reject unknown tools as retryable model-output failures.
6. Reject raw tool markup in assistant text.
7. Gate artifact writes until files exist and content is plausible.
8. Make delegation evidence structured and non-optional.
9. Enforce plan state consistency.
10. Centralize final completion validation.
11. Unify SideEffectIntent with TurnContract.
12. Live acceptance burn-in for kindergarten failures.
13. Remove prompt-only reliance and document contract.
14. Final full verification and review.

## Definition Of Done

The work is done only when:

- The failure fixture from `thread-1779576380304108000` no longer classifies as successful completion.
- All burn-in acceptance tests pass.
- Full `go test -count=1 ./...`, `just build`, and `git diff --check` pass.
- Code review finds no Critical/Important issues.
- A manual scratch-repo “write a plan” smoke test cannot false-complete.
