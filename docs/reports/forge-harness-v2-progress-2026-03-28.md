# Forge Harness v2 Progress - 2026-03-28

## Scope Covered

- Executed `docs/plans/2026-03-28-kernel-architecture-fixes-v2.md` through Chunk 4 implementation and verification gates.
- Completed Task 8 trace-audit additions and verified:
  - `thread_phase`
  - `claim_guard_status`
  - `workspace_policy_action`
  - `tool_call_count`
- Completed Task 9 regression hardening:
  - Added `internal/harness/testdata/preview_branch_claim_regression.json`
  - Added fixture-driven regression test coverage in `internal/harness/regression_test.go`
  - Extended stress corpus for additional branch/apply phrasing variants.

## Additional Failures Found During Final Gate

1. Runtime policy regression outside git repos:
- Symptom: runtime tests failed with `detect current branch: exit status 128`.
- Root cause: workspace policy always attempted branch detection even in non-git temp dirs.
- Fix:
  - Added `gitutil.IsRepository`.
  - Workspace policy now no-ops for non-git directories.
  - Added tests:
    - `TestIsRepository`
    - `TestWorkspacePolicyNoopOutsideGitRepo`

2. Live routing gap (`branch + /theme` in active preview follow-up):
- Symptom (from `/tmp/forge-manual-v2-20260328.jsonl`): request was routed to `HARNESS MODE: inspect` and forced `read_file /theme`.
- Root cause: preview supersede path classification ran before branch-workflow follow-up classification.
- Fix:
  - Prioritized branch-workflow follow-up classification before preview supersede classification in `internal/harness/thread.go`.
  - Added regression coverage:
    - `TestClassifyActivePreviewThreadBranchWorkflowWithPathHintUsesActionIntent`
    - fixture update in `preview_branch_claim_regression.json`
    - stress corpus variant for `i choose the first mockup, make a branch and bring it to life in /theme`.

## Verification Evidence

- `go test ./internal/harness -run TestTrace.* -count=1`
- `go test ./internal/harness -run TestRegression.* -count=1`
- `go test ./internal/harness -run TestLargePromptStressCorpusRoutesConsistently -count=1`
- `go test ./internal/runtime -run TestWorkspacePolicyNoopOutsideGitRepo -count=1`
- `go test ./internal/runtime -run 'TestRunChatTurnCompletesComplexVisiblePreviewTurn|TestChatTranscriptPreviewHarnessSurvivesFiftyTurns|TestDebugChatDoesNotEnterAltScreen' -count=1`
- `go test ./... -count=1`
- `go build -o ./forge ./cmd/forge`

All commands above passed in this run.

## Manual Runtime Notes

- Live debug run (`/tmp/forge-manual-v2-20260328.jsonl`) confirmed:
  - visible progress updates are emitted while waiting and while tools run;
  - claim-evidence tracing fields are present in harness trace logs;
  - verified preview output path is produced before response completion.
- Live model latency remains variable (long wait periods before first model tool call/response), but now yields continuous user-visible progress updates instead of silent spinner-only behavior.
