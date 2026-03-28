# Forge Harness Audit Remediation - 2026-03-27

## Scope

This note re-audits the harness after the post-realignment remediation pass and maps the previously identified gaps to the code now in tree.

References:

- `docs/reports/forge-harness-architecture-investigation-2026-03-27.md`
- `docs/superpowers/plans/2026-03-27-harness-control-plane-realignment.md`
- `docs/superpowers/plans/2026-03-27-harness-audit-remediation-and-prompt-validation.md`

## Closed Gaps

### 1. Main-path malformed tool markup no longer depends on prefix-only detection

Fixed in:

- `internal/agent/agent.go`
- `internal/agent/agent_test.go`
- `internal/harness/local.go`
- `internal/harness/local_test.go`
- `internal/harness/strictlocal_test.go`

What changed:

- main-path malformed-tool recovery now triggers on any raw tool markup with zero parsed calls, not only tool markup at byte zero
- local and strict-local executors fail closed on any raw tool-call residue, including prose-prefixed malformed output

Regression coverage:

- `TestAgentRetriesProsePrefixedMalformedMainToolTurn`
- `TestAgentExecutorRejectsProsePrefixedMalformedVisibleCollaborationToolMarkup`
- `TestStrictAgentExecutorRejectsProsePrefixedMalformedToolMarkup`

### 2. Strict-local inspect turns now preserve inspect-specific contracts

Fixed in:

- `internal/harness/strictlocal.go`
- `internal/harness/strictlocal_test.go`
- `internal/agent/system.go`

What changed:

- strict-local now switches to the inspect prompt for read-only inspect turns
- strict-local uses the inspect tool registry for those turns instead of the full default tool registry
- the strict-local system prompt now only advertises preview guidance when the active tool registry actually contains the preview lifecycle tools

Regression coverage:

- `TestStrictAgentExecutorUsesInspectPromptAndToolsForVisibleInspectTurns`

### 3. Preview and artifact lifecycle is now host-owned and session-visible

Fixed in:

- `internal/agent/tools/artifact.go`
- `internal/agent/tools/artifact_test.go`
- `internal/agent/tools/preview.go`
- `internal/agent/tools/preview_test.go`
- `internal/harness/types.go`
- `internal/harness/session.go`
- `internal/harness/session_test.go`
- `internal/runtime/chat.go`
- `internal/runtime/chat_test.go`

What changed:

- added `artifact_read`
- fixed JSON-number port coercion for `preview_server_ensure`
- added preview/artifact snapshots to harness session state
- local and strict-local execution now capture preview/artifact state only when the current turn actually used those tools
- preview runtime is now closed on chat teardown

Regression coverage:

- `TestArtifactReadReturnsTrackedContentByHandle`
- `TestPreviewServerEnsureUsesRequestedPortFromJSONNumber`
- `TestSessionApplyStoresRecentPreviewAndArtifactState`
- `TestSessionRecentPreviewAndArtifactExpireAfterOneTurn`

### 4. Debug logging now recognizes strict-local turns

Fixed in:

- `internal/runtime/chat_debug.go`
- `internal/runtime/chat_debug_test.go`

What changed:

- strict visible-collaboration requests are now normalized in debug logs the same way hidden-worker and inspect turns already were

Regression coverage:

- `TestEnableChatDebugNormalizesStrictLocalResponses`

### 5. Preview follow-ups no longer depend only on the original lexical routing trigger

Fixed in:

- `internal/harness/classifier.go`
- `internal/harness/planner_test.go`

What changed:

- recent preview/artifact session state can now promote referential follow-ups back onto the strict visible path even when the follow-up prompt omits the original preview-specific trigger phrasing

Regression coverage:

- `TestPlanKeepsPreviewFollowUpsOnStrictLocalPath`
- `TestPlanKeepsReferentialPreviewTroubleshootingOnStrictLocalPath`

### 6. Strict-local preview edits now recover from HTML-heavy malformed calls and stale repeat edits

Fixed in:

- `internal/agent/parse.go`
- `internal/agent/agent_test.go`
- `internal/agent/tools/edit.go`
- `internal/agent/tools/edit_test.go`

What changed:

- strict-turn tolerant tool-call repair now composes three repairs for one-tool visible turns:
  - literal newline/tab/CR escaping inside JSON strings
  - escaping of likely bare inner quotes inside string payloads such as HTML attributes
  - appending missing trailing `}` / `]` delimiters outside string literals
- `edit_file` now reports when the requested replacement is already present instead of always returning a hard `old_text not found` failure for stale repeats

Regression coverage:

- `TestStrictLocalSalvagesNameLessEditFileToolTurnWithBareHTMLQuotesLiteralNewlinesAndMissingOuterBraceWithoutRetryTax`
- `TestEditFileReportsReplacementAlreadyPresentWhenOldTextIsStale`

### 7. Preview-thread follow-up routing is now hardened against lexical collisions and replay/action confusion

Fixed in:

- `internal/harness/classifier.go`
- `internal/harness/classifier_test.go`
- `internal/harness/stress_corpus_test.go`

What changed:

- typo-tolerant token normalization no longer allows short insert/delete matches to collapse ordinary words like `three` into scope tokens like `tree`
- active preview-thread action follow-ups now recognize additional imperative preview verbs such as `put` / `render` / `bring`
- replay requests like `show it on the web page again` and `refresh the preview page` now stay on the answer/replay path instead of being mistaken for edit requests
- the large prompt stress corpus now includes explicit coverage for word-number choice prompts and replay-vs-action preview follow-ups

Regression coverage:

- `TestInferRequestScopeDoesNotTreatThreeAsDirectoryTree`
- `TestClassifyPreviewThreadModificationFollowUpsStayVisible`
- `TestClassifyPreviewThreadReplayFollowUpsStayVisible`
- `TestLargePromptStressCorpusRoutesConsistently`

### 8. Active harness root prompts no longer get compacted mid-turn during long strict-local sessions

Fixed in:

- `internal/agent/agent.go`
- `internal/agent/agent_test.go`
- `internal/runtime/chat_transcript_test.go`

What changed:

- history compaction now leaves in-flight harness root prompts (`HARNESS MODE:`) and worker objective prompts (`OBJECTIVE:`) intact
- this prevents the current turn's `USER REQUEST:` block from being truncated away while the agent is still taking tool turns
- long preview/edit conversations now keep seeing the active control-plane prompt instead of silently falling back onto stale summarized context mid-turn

Regression coverage:

- `TestEnforceHistoryBudgetDoesNotCompactActiveHarnessRootPrompt`
- `TestChatTranscriptPreviewHarnessSurvivesFiftyTurns`

### 9. Strict-local skill use and visible progress are now host-managed instead of provider-imagined

Fixed in:

- `internal/agent/agent.go`
- `internal/agent/event_render.go`
- `internal/agent/progress.go`
- `internal/agent/system.go`
- `internal/harness/strictlocal.go`
- `internal/runtime/chat.go`
- `internal/runtime/chat_test.go`

What changed:

- worker and strict-local prompts now describe a host-managed skill catalog instead of telling the model to load skill documents through a nonexistent runtime path
- strict-local now injects required/auto skill context through the same host-side mechanism workers already use and records that use in the observation/debug path
- visible strict-local turns now emit a quiet progress line at turn start and on tool calls, so the transcript is no longer silent until the final answer lands

Regression coverage:

- `TestBuildWorkerSystemPromptUsesHostManagedSkillCatalog`
- `TestBuildStrictLocalSystemPromptUsesHostManagedSkillCatalog`
- `TestStrictAgentExecutorUsesInjectedSkillContext`
- `TestRunChatTurnKernelVisibleTurnAvoidsStrictSkillLoop`
- `TestRunChatTurnKernelVisibleTurnEmitsProgressBeforeFinalAnswer`

## Remaining Heuristics

One architectural compromise remains:

- the initial decision that a brand-new user prompt is a preview/visible-collaboration request is still heuristic rather than fully host-derived

That is materially less dangerous than before because:

- malformed visible-path output now fails closed and retries
- preview/artifact state is now carried across turns
- preview follow-ups can stay on the strict path without repeating the exact original wording
- strict-local no longer advertises a fake self-service skill-loading path
- visible turns now show host progress instead of staying blank while the provider works

But it is still heuristic for the very first preview request in a brand-new thread. The next validation loop should stress this heavily with diverse prompt wording rather than assume the current lexicon is sufficient.

One residual live-behavior caveat also remains:

- subjective design feedback like "still too blue" can still cause extra tool turns while the model/provider explores alternatives, but the host path now stays on strict-local, recovers from malformed payloads, and finishes with a verified preview instead of stalling on raw tool markup

## Verification At Audit Time

Fresh verification completed after the remediation pass:

- `go test ./internal/agent ./internal/agent/tools ./internal/harness ./internal/runtime -count=1`
- `go test ./... -count=1`
- `go build -o ./forge ./cmd/forge`

Additional verification after the strict-local malformed-edit follow-up fixes:

- `go test ./internal/agent -run 'TestStrictLocalSalvages.*EditFile.*' -count=1`
- `go test ./internal/agent/tools -run 'TestEditFile(Basic|NotFound|ReportsReplacementAlreadyPresentWhenOldTextIsStale|MultipleMatches|Denied)' -count=1`
- `go test ./internal/harness -run 'TestLargePromptStressCorpusRoutesConsistently|TestPlanKeepsPreview.*' -count=1`
- `go test ./internal/runtime -run 'TestChatTranscript|TestEnableChatDebug.*' -count=1`
- `go test ./internal/agent ./internal/agent/tools ./internal/harness ./internal/runtime -count=1`
- `go test ./... -count=1`
- `go build -o ./forge ./cmd/forge`

Additional verification after the preview-thread routing hardening:

- `go test ./internal/harness -run 'TestInferRequestScopeDoesNotTreatThreeAsDirectoryTree|TestClassifyPreviewThread(Modification|Replay)FollowUpsStayVisible' -count=1`
- `go test ./internal/harness -run 'TestLargePromptStressCorpusRoutesConsistently' -count=1`
- `go test ./... -count=1`
- `go build -o ./forge ./cmd/forge`

Additional verification after the long-session history-compaction fix:

- `go test ./internal/agent -run 'TestEnforceHistoryBudgetDoesNotCompactActiveHarnessRootPrompt' -count=1`
- `go test ./internal/runtime -run 'TestChatTranscriptPreviewHarnessSurvivesFiftyTurns' -count=1`
- `FORGE_TRANSCRIPT_LOG_PATH=/tmp/forge-long-transcript-single.jsonl go test ./internal/runtime -run 'TestChatTranscriptPreviewHarnessSurvivesFiftyTurns' -count=1`
- `go test ./... -count=1`
- `go build -o ./forge ./cmd/forge`

Additional verification after the strict-local skill/progress ownership fix:

- `go test ./internal/agent -run 'TestBuild(Worker|StrictLocal)SystemPromptUsesHostManagedSkillCatalog|TestProgressLine' -count=1`
- `go test ./internal/harness -run 'TestStrictAgentExecutorUsesInjectedSkillContext' -count=1`
- `go test ./internal/runtime -run 'TestRunChatTurnKernelVisibleTurn(AvoidsStrictSkillLoop|EmitsProgressBeforeFinalAnswer)' -count=1`
- `go test ./internal/agent ./internal/agent/tools ./internal/harness ./internal/runtime ./internal/tui -count=1`
- `go test ./... -count=1`
- `go build -o ./forge ./cmd/forge`

Latest live validation:

- Log: `/tmp/forge-live-validation-20260327T0008.jsonl`
- `start a preview for themes_preview.html and tell me the verified url`
  - completed on `strict_local`
  - used `preview_server_ensure`
  - returned verified live URL `http://127.0.0.1:51159/themes_preview.html`
- `is it still up?`
  - completed on `strict_local`
  - used `preview_server_status`
  - confirmed the same live preview URL
- `the header is still too blue, fix that and show me again`
  - completed on `strict_local`
  - used `read_file`, `write_file`, `edit_file`, and `preview_server_ensure`
  - returned a final preview-confirming response without any malformed-tool nudge in the log

Latest long-session validation:

- Log: `/tmp/forge-long-transcript-single.jsonl`
- 50 consecutive kernel transcript turns on the preview/strict-local path completed successfully
- final turns included:
  - `pick three others, no neon`
  - `put that on the web page`
  - `more colors on git diff and file/numeral detection`
  - `show it on the web page again`
- all 50 turns returned concrete preview responses without malformed tool markup or stale-context drift
