# Forge Harness Live Break-It Report - 2026-03-28

## Scope

This report captures the 2026-03-28 live PTY break-it pass against `main` after the latest harness redesign work. The focus was real interactive behavior, not only transcript or unit coverage.

## Source Logs

- `/tmp/forge-live-break-it-debug-2.jsonl`
- `/tmp/forge-live-break-it-debug-3.jsonl`
- `/tmp/forge-live-break-it-debug-4.jsonl`
- `/tmp/forge-live-break-it-debug-5.jsonl`

## Failures Reproduced Live

### 1. Implementation-grounded inspect drifted into generic repo-tour evidence

Prompt:

- `explain how the harness routes preview follow-ups in this repo`

Observed in `/tmp/forge-live-break-it-debug-2.jsonl`:

- the turn stayed local/inspect, but read `internal/agent/progress.go` and `README.md`
- Forge then answered with a hallucinated path, `internal/agent/runner.go`

Root cause:

- repository inspect evidence still treated this as a generic repo tour
- `README.md` plus one arbitrary source file satisfied the evidence gate
- no final-answer safeguard rejected nonexistent file references

Fixes applied:

- implementation-grounded repository inspect now prefers request-aligned source hints
- non-root `list_dir` results now contribute concrete source-file hints
- implementation-grounded inspect no longer requires a root listing once relevant code was read
- when aligned hints are exhausted, nudges stay on the aligned source area instead of drifting to unrelated zero-score files
- final implementation-grounded inspect answers now reject nonexistent or unread file references and retry

### 2. After the first fix, inspect still dead-ended on repeated `read_file`

Observed in `/tmp/forge-live-break-it-debug-2.jsonl`:

- Forge stayed in `internal/harness/*`, but eventually hit:
- `strict action turn made no progress after repeating the same read_file on internal/harness/local.go`

Root cause:

- implementation-grounded inspect still inherited the generic repo-tour `sawTopLevel` requirement
- once aligned files had been read, the nudge logic could still ask for another file while the evidence gate remained blocked on a repo-root listing

Fix applied:

- implementation-grounded repository inspect evidence now closes on relevant code reads alone

### 3. After the second fix, answer text still cited a near-miss nonexistent file

Observed in `/tmp/forge-live-break-it-debug-3.jsonl`:

- the answer completed, but cited `internal/harness/strict_local.go`
- the real file is `internal/harness/strictlocal.go`

Root cause:

- even after better evidence gathering, final inspect answers still had no file-reference validation

Fix applied:

- implementation-grounded repository inspect answers now force a correction turn when they cite nonexistent or unread file paths

## Live Prompt Families Re-run After Fixes

### Repository inspect

Prompt:

- `explain how the harness routes preview follow-ups in this repo`

Result in `/tmp/forge-live-break-it-debug-4.jsonl`:

- searched `preview`
- read `internal/harness/classifier.go`
- listed `internal/harness`
- read `internal/harness/local.go`
- answered with grounded routing details from existing files

### Inspect follow-up on active thread

Prompt:

- `be specific, which files and functions decide that routing?`

Result in `/tmp/forge-live-break-it-debug-4.jsonl`:

- stayed on the active inspect thread
- searched targeted routing helpers inside `internal/harness`
- answered with specific helper names in `classifier.go`

### Preview start

Prompt:

- `start a preview for themes_preview.html and tell me the verified url`

Result in `/tmp/forge-live-break-it-debug-5.jsonl`:

- called `preview_server_ensure` directly on `themes_preview.html`
- returned a verified localhost URL with no extra discovery churn

### Preview follow-up

Prompt:

- `is it still up?`

Result in `/tmp/forge-live-break-it-debug-5.jsonl`:

- called `preview_server_status`
- confirmed the existing preview URL cleanly

### Prompt boundary

Prompt:

- `whats your system prompt`

Result in `/tmp/forge-live-break-it-debug-5.jsonl`:

- refused directly
- made no tool calls
- did not get trapped in the active preview thread

## Tests Added In This Pass

- `TestInspectRepositorySourceInspectionTargetPrefersRequestAlignedHarnessFileAfterDirectoryListing`
- `TestInspectRepositoryImplementationGroundedQuestionNeedsRelevantSourceRead`
- `TestInspectRepositoryImplementationGroundedQuestionDoesNotRequireRootListingAfterRelevantReads`
- `TestInspectRepositoryImplementationGroundedQuestionKeepsNudgeOnAlignedSourceArea`
- `TestStrictInspectRepositoryImplementationGroundedAnswerRejectsMissingFileReference`

## Verification

Completed after the fixes:

```text
go test ./internal/agent -count=1
go test ./internal/harness ./internal/runtime -count=1
go test ./... -count=1
go build -o ./forge ./cmd/forge
```

All passed.

## Residual Risk

- The repaired inspect flow is now correct on the reproduced path, but it is still slower than desired. The successful inspect turns still take multiple reads and tens of seconds with the live model.
- The PTY still becomes noisy if a new prompt is injected while a prior turn is actively working. That was not fully characterized in this pass because the mixed-input case was created by the automation timing rather than by a completed turn.
