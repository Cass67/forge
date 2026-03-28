# Forge Harness Debug Log - 2026-03-27

## Scope

This report captures the current harness/debugging state after the latest replay against the fresh debug log and the latest focused test run.

## Source Logs

- `/tmp/forge-debug.jsonl`
- `/tmp/forge-harness-stress-20260327T000014.jsonl`
- `/tmp/forge-transcript-regressions-20260327T000014.jsonl`
- [forge-harness-stress-20260327T000014.md](/Users/mcassidy/Documents/OPC/git/other/forge/docs/reports/forge-harness-stress-20260327T000014.md)
- [forge-transcript-regressions-20260327T000014.md](/Users/mcassidy/Documents/OPC/git/other/forge/docs/reports/forge-transcript-regressions-20260327T000014.md)

## Latest Debug Log Findings

### User Inputs

| Time (UTC) | Input |
|---|---|
| 2026-03-27T00:15:41Z | `do i need to cleanup this repo` |
| 2026-03-27T00:16:44Z | `okdoke` |

### Classification Results

| Time (UTC) | Family | Reason | Topic |
|---|---|---|---|
| 2026-03-27T00:16:30Z | `inspect` | `inspection language` | `workspace:repository` |
| 2026-03-27T00:16:45Z | `answer` | `default answer path` | `` |

### Turn Timing

| Time (UTC) | Duration | Input Tokens | Output Tokens |
|---|---:|---:|---:|
| 2026-03-27T00:16:30Z | `48.999225166s` | `7262` | `675` |
| 2026-03-27T00:16:45Z | `700.924875ms` | `872` | `7` |

### Concrete Behavior Problem

- The first turn completed with a useful repo-cleanup answer, but it was slow.
- The second turn was wrong:
  - Forge had just ended with an offer: inspect tracked root artifacts and tell the user what is safe to remove, ignore, move, or keep.
  - The user replied with `okdoke`.
  - Instead of consuming that offer as a continuation, the harness reclassified the turn as a plain `answer` and Forge replied with `okdoke`.

### Inspect Loop Observation

The first turn still needed inspect-turn correction nudges before the final answer:

- `Inspect turns must not mix visible prose with tool calls. Use tool calls only while gathering evidence.`
- `Stop mixing prose with inspect tool calls. Emit one tool call only until you are ready to answer.`
- `Inspect turns are tool calls only until evidence gathering is complete.`

This is a latency and robustness smell, but it is separate from the `okdoke` continuation bug.

## Root Cause Hypothesis

The continuation bug appears to be in `internal/harness/runner.go`.

Current behavior:

- `inferPendingInspectOffer(...)` only stores a pending inspect action when:
  - observation is complete
  - `class.Family == FamilyAnswer`
  - the assistant response looks like an offer to inspect

Why that fails here:

- The first turn is an `inspect` family turn, not an `answer` family turn.
- Even though the inspect response ends with a follow-on offer, no `PendingAction` is stored.
- The next user message (`okdoke`) therefore has no explicit pending action to consume and falls back to raw reclassification, which lands on `answer/default answer path`.

## Reproduced Failing Tests

Fresh focused run:

```text
go test ./internal/harness ./internal/runtime -count=1
```

Current failures:

```text
--- FAIL: TestRunnerInspectOfferCreatesPendingActionForNextTurn (0.00s)
    runner_test.go:545: expected pending action: harness.SessionState{... PendingAction: harness.PendingAction{SetAtTurn:0, Family:"", TopicKey:"", TaskText:"", ...}}

--- FAIL: TestRunnerContinuationResumesInspectOfferFromInspectTurn (0.00s)
    runner_test.go:645: family = "answer"
```

Meaning:

- The new regression test confirms no pending action is being stored from an inspect-family offer.
- The follow-up regression test confirms the continuation falls through to `answer` instead of resuming the offered inspect task.

## Worktree State At Capture Time

Modified files:

- `internal/harness/classifier.go`
- `internal/harness/classifier_test.go`
- `internal/harness/local.go`
- `internal/harness/local_test.go`
- `internal/harness/runner_test.go`
- `internal/runtime/chat_transcript_test.go`
- `internal/harness/stress_corpus_test.go`

## Previously Green Checks

These were green before the new failing continuation regressions were added:

- `go test ./internal/harness -run 'TestLargePromptStressCorpusRoutesConsistently' -count=1`
- `go test ./internal/runtime -run 'TestChatTranscript' -count=1`
- `go test ./... -count=1`
- `go build -o ./forge ./cmd/forge`

## Next Fix To Make

The next implementation change should be:

1. Let inspect-family replies that end in an inspect offer create `PendingAction`.
2. Re-run the two focused runner tests until they pass.
3. Re-run:
   - `go test ./internal/harness ./internal/runtime -count=1`
   - `go test ./... -count=1`
   - `go build -o ./forge ./cmd/forge`

## Summary

This is not a generic wording problem.

It is a specific state-management bug:

- explicit follow-on inspect offers are being emitted,
- but the harness only persists them for `answer` turns,
- so short acknowledgements like `ok`, `sure`, and `okdoke` have no stored intent to resume.

## Resolution Applied

The harness was updated in two structural places:

1. Inspect-family turns can now persist a follow-on inspect offer as `PendingAction`, not just answer-family turns.
2. Pending-action continuation detection now accepts short low-information acknowledgements generically, while still refusing to override explicit new requests like `fix it`, scoped inspect requests, prompt-boundary questions, process questions, research requests, or verification/debug requests.

The inspect-offer parser was also widened from exact verb matching to inspect-related stems, so assistant phrasing like `inspection` is accepted without hardcoding the full sentence.

## Verification After Fix

Fresh verification completed after the patch:

```text
go test ./internal/harness -run 'TestRunnerInspectOfferCreatesPendingActionForNextTurn|TestRunnerContinuationResumesInspectOfferFromInspectTurn|TestRunnerContinuationResumesConcreteInspectOffer|TestRunnerAnswerOfferCreatesInspectPendingActionFromConcreteTarget|TestClassifyPendingActionContinuationUsesStoredTask|TestClassifyReferentialPendingActionContinuationUsesStoredTask|TestClassifyOpaquePendingActionContinuationUsesStoredTask|TestClassifyPendingActionContinuationDoesNotOverrideExplicitImplementation' -count=1
ok  	forge/internal/harness	0.779s

go test ./internal/harness ./internal/runtime -count=1
ok  	forge/internal/harness	0.331s
ok  	forge/internal/runtime	19.705s

go test ./... -count=1
ok  	forge/cmd/forge	0.847s
...
ok  	forge/internal/runtime	20.530s
ok  	forge/internal/tui	2.256s

go build -o ./forge ./cmd/forge
exit 0
```

## Result

The specific failure from the fresh log is fixed by state handling rather than another user-word patch:

- the first inspect turn now stores the follow-on inspect offer
- a short acknowledgement like `sounds good` resumes that stored action
- an explicit new request like `fix it` no longer gets swallowed by the pending-action continuation path
