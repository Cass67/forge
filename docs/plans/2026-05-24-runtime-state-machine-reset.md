# Runtime State Machine Reset

Live failure analysis on 2026-05-24 showed repeated tool-call, routing, and completion regressions despite many local hardening fixes. The recurring issue is architectural: Forge still lets model behavior coordinate recovery and pacing instead of enforcing an explicit runtime-owned task state machine.

## Problems To Remove

1. Persistent task state should be authoritative.
   `continue` should always resume the active unfinished task, not reclassify from text.

2. Tool phases should be explicit state machine transitions.
   Example: `inspect -> edit -> verify -> summarize`, with bounded exits. Do not derive allowed tools primarily from the last user string.

3. Exploration should have hard budgets.
   After N reads, searches, or no-op tool calls, Forge should force synthesis, ask a question, or require an edit. Do not rely on prompt nudges.

4. Provider, auth, and model failures should not reset task intent.
   A failed model call should preserve task state and retry or fallback without letting the next `continue` become answer-only chat.

5. `think` should be budgeted or disabled in hot loops.
   A single large reasoning/tool payload after enough evidence has been gathered is a runtime failure, not progress.

6. Contract and evidence should be task-level, not turn-level.
   A user task such as “fix repo” owns subsequent `continue` turns until satisfied, cleared, or cancelled.

## Non-Goal

Do not keep adding prompt-only instructions or one-off lexical exceptions as the primary defense. Regression tests are useful, but the fix needs to simplify the runtime state model so these failure classes cannot recur through a different phrasing.

## Commit History Assessment

Recent history shows Forge is going in circles around the same runtime failure family.

Since `948af53 feat: derive turn contracts from user intent`, the core runtime/test surface grew by thousands of lines, mostly in `internal/react/loop.go`, `internal/react/loop_test.go`, and `internal/react/turn_contract.go`. The sequence is dominated by patches that harden one edge of the same underlying behavior:

- `19c99b9 fix: retry unavailable model tools`
- `8804e7e fix: reject raw tool markup completions`
- `b9d2161 feat: gate required artifact completion`
- `0fa3e4a feat: require structured delegation evidence`
- `e8f3076 feat: enforce plan state completion gates`
- `20160e5 feat: mirror side effects into turn contracts`
- `d18c5d1 fix: enforce turn contract evidence before completion`
- `3cf6c58 fix: classify real work and embedded tool markup`
- `da254a3 fix: harden real-work routing gates`
- `0112d11 fix: harden tool-call recovery`
- `a0c76f8 fix: tighten tool policy gaps`
- `e04d405 fix: harden output and failure visibility`
- `e8adb41 fix: curb slow continuation loops`

The pattern is not convergence:

1. Add durable contracts.
2. Add evidence.
3. Add gates.
4. Add delegation evidence.
5. Mirror side effects into contracts.
6. Enforce completion.
7. Add routing exceptions.
8. Add recovery exceptions.
9. Add tool visibility exceptions.
10. Add loop and thrash exceptions.

The same bug shape keeps returning with different symptoms:

- wrong intent
- wrong tools
- stale delegation phase
- missing evidence
- false completion
- tool-call mismatch
- over-exploration
- continuation drift
- provider failure resetting task context
- output/read handle confusion

These patches are useful perimeter tripwires, but they do not remove the center failure: the runtime still lets `last user text + model choice + prompt feedback` coordinate task progress. Until task state owns phase, tool budget, recovery, and completion, new real runs can find new paths around the current guards.

Honest assessment: this is circular hardening, not a stable architecture. The next meaningful step is a runtime-owned task state machine that makes the patch lattice unnecessary.
