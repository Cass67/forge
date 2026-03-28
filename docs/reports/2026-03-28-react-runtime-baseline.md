# React Runtime Baseline (Phase 0)

Date: 2026-03-28
Branch: `forge/north-star-codex-opencode`

## Scope

- Establish baseline coverage and regression fixtures before/while migrating to React runtime.
- Confirm transcript and multi-turn stress coverage remains runnable under test.

## Baseline Fixtures In Place

- Prompt-boundary refusal transcript:
  - `TestChatTranscriptPromptBoundaryResponseIsVisible`
- Repository review corpus (100+ prompt variants):
  - `TestChatTranscriptRepoReviewCorpusStaysUsefulAcrossFollowUp`
  - corpus assertion: `len(prompts) >= 100`
- Long multi-turn preview flow (50 turns):
  - `TestChatTranscriptPreviewDesignConversationSustainsFiftyTurns`
  - assertion: `len(steps) == 50`

## Verification Evidence

Commands run:

```bash
go test ./internal/runtime -count=1
go test ./... -count=1
go build -o ./forge ./cmd/forge
```

Result: all commands exited successfully.

## Baseline Failure Classes Tracked

- misroute / wrong runtime lane
- silent waits without progress heartbeats
- retry-loop collapse and malformed-tool retries
- ungrounded side-effect claims (branch/commit/preview)
- protected-branch mutation risk

