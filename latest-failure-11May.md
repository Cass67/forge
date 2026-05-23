# Latest Forge Failure Analysis

## Summary

Forge failed a simple user request because the runtime still treats high-risk workflows as model-managed sequences instead of enforced transactions.

The recent planning, bulletproofing, durability, and delegation work made Forge better at recording what happened. It did not make Forge reliably enforce what must happen before mutating files, committing, or pushing.

This failure was not caused by a missing plan. The plans already describe the contract Forge violated.

## Incident

User request sequence:

- Compare Forge and Codex by reading source, not just README.
- Write the comparison to a document.
- Commit it to main and push.

Actual outcome:

- Forge wrote an initial `FORGE_VS_CODEX.md` document.
- A child agent committed more than the requested file.
- Commit `ac8b9b3` included `AI-1.md`, `FORGE_VS_CODEX.md`, `internal/react/loop.go`, and `internal/react/loop_test.go`.
- The commit was created on branch `forge/write-forge-vs-codex-md-20260518202954`, not main.
- The commit was not pushed.
- After the failed handoff, Forge overwrote `FORGE_VS_CODEX.md` with the child agent's failure report.

The final state was worse than incomplete: the intended document content was replaced by operational error text.

## What The Plans Already Said

The latest docs already covered the safety contract this run violated.

- `docs/reliability-security-roadmap.md` says child handoffs are evidence, not workflow completion.
- `docs/reliability-security-roadmap.md` says blocking handoffs must keep parent tools available and prevent final synthesis until resolved.
- `docs/reliability-security-roadmap.md` says accidental-write incidents require parent diff/status and file-content inspection before repair.
- `docs/reliability-security-roadmap.md` says parent state must record pending delegated actions such as write, verify, commit, ask user, or no action.
- `docs/superpowers/plans/2026-05-16-forge-robustness-roadmap.md` says commits must stay focused, unrelated dirty changes must not be staged, and `git diff --cached` must be inspected before every commit.
- `docs/plans/2026-05-17-forge-fireproof-stability-design.md` says durable runtime state should let Forge tell the truth about completed, failed, interrupted, and recoverable work.

The failure therefore is not a planning gap. It is an enforcement and acceptance gap.

## Why This Keeps Happening

Forge is still built around this assumption:

```text
The model will follow the workflow correctly if the prompt, docs, and tool state are good enough.
```

That assumption is false for side effects.

For file writes, commits, and pushes, the runtime must enforce invariants. The model cannot be trusted to remember every gate, interpret every child handoff correctly, and preserve user intent under degraded tool availability.

The recent work improved these areas:

- Durable event recording.
- Tool schema validation.
- Agent state visibility.
- Handoff parsing.
- Recovery metadata.
- Output handles.
- Checkpoints and diagnostics.

But the latest run shows these improvements remained mostly observational. Forge recorded the bad handoff, but did not make the dangerous next actions impossible.

## Root Cause

The root cause is missing user-intent-scoped transaction enforcement.

Forge had enough information to know the safe target set: `FORGE_VS_CODEX.md` only. It also had enough information to know the child agent reported an over-commit incident and an unresolved push action.

The runtime should have enforced these gates:

- The child agent must not commit unrelated files.
- The parent must reject or quarantine commits whose file set differs from the user-requested target set.
- A handoff report must never be written into the user's requested artifact path.
- `commit to main and push` must verify branch, staged files, commit contents, push result, and remote state.
- Final success must be blocked until those verifications pass.

Instead, these checks were left to model behavior. The model failed.

## Acceptance Gap

The test matrix appears to cover component behaviors, not the full real workflow.

Covered classes include:

- Child-agent state tracking.
- Delegated read-only audit plus parent write.
- Handoff state recording.
- Tool schema reliability.
- Durable event replay.
- Secret redaction and tool-boundary safety.

The failing workflow needs an acceptance test shaped like this:

```text
dirty worktree exists
user asks to write one doc
user asks to commit to main and push
child agent reports accidental extra files and unresolved push
parent must not overwrite the target doc with handoff text
parent must not claim completion
parent must inspect status, diff, commit contents, branch, and push state
commit must contain only allowed files
push must be verified against the remote branch
```

Without that gate, the system can pass the existing reliability suite and still fail a simple real task five minutes later.

## Correct Reliability Bar

For high-risk workflows, Forge needs runtime-owned transactions rather than prompt-owned procedures.

Required invariant:

```text
If the user asks for a scoped side effect, Forge must bind the requested target set and reject side effects outside that set unless the user explicitly approves the expansion.
```

For write/commit/push, this means:

- Capture the requested artifact path or file allowlist before mutation.
- Preserve user artifact content separately from control-plane handoff reports.
- Stage only the allowlisted files or hunks.
- Inspect staged diff before commit.
- Verify the commit file list after commit.
- Verify the active branch and intended destination before push.
- Verify the remote contains the pushed commit after push.
- Refuse final success if any gate is unresolved.

## Conclusion

Forge keeps returning to patches because the hardening work has not yet moved the critical boundary from model intent to runtime enforcement.

The plans were directionally correct. The sign-off was premature because it certified pieces of the system, not the complete user-visible transaction. Until Forge enforces side-effect invariants in code, the same class of failure will recur under new wording, new tools, or new child-agent paths.
