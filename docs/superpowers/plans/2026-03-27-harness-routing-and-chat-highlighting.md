# Harness Routing And Chat Highlighting Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Fix repo-review misrouting for "what would you change"-style prompts, reject no-op editor completions, and improve chat rendering so inline script/path/command names highlight semantically without odd header background artifacts.

**Architecture:** Extend the harness evaluation matcher with targeted review phrases and backstop regressions, then harden the editor worker contract so a completed edit must include at least one concrete change. In the TUI, stop treating inline-code spans as opaque blobs and instead render their contents with semantic token styling while keeping delimiters readable; remove redundant message-level background painting where the pane already owns the surface.

**Tech Stack:** Go, Bubble Tea, Lipgloss, existing harness/TUI unit tests.

---

## Chunk 1: Harness Routing And Contract

### Task 1: Expand evaluative review phrase coverage

**Files:**
- Modify: `internal/harness/classifier.go`
- Test: `internal/harness/classifier_test.go`
- Test: `internal/harness/repo_review_prompt_corpus_test.go`
- Test: `internal/harness/testdata/debuglogs/repo-inspect-stall.jsonl`

- [ ] **Step 1: Write the failing tests**

Add cases for:
- `look over this repo and tell me if there is anything you would change`
- close variants such as `what would you change in this repo`, `anything you'd change in this directory`, and focused-file review phrasings that should stay on inspect.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/harness -run 'TestClassifyVagueScopedReviewPrompts|TestRepoReviewPromptCorpusRoutesToEvaluativeInspect|TestRegressionFixturesRouteWithoutEscalation' -count=1`
Expected: FAIL because the new review phrases route to `implement` or miss `WantsEvaluation`.

- [ ] **Step 3: Write minimal implementation**

Broaden the scoped-evaluation phrase matching in `internal/harness/classifier.go` without widening real action requests.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/harness -run 'TestClassifyVagueScopedReviewPrompts|TestRepoReviewPromptCorpusRoutesToEvaluativeInspect|TestRegressionFixturesRouteWithoutEscalation' -count=1`
Expected: PASS.

### Task 2: Reject zero-change completed editor results

**Files:**
- Modify: `internal/harness/contracts.go`
- Test: `internal/harness/contracts_test.go`

- [ ] **Step 1: Write the failing test**

Add a contract test that `WorkerEditor` rejects `{"status":"complete","changes":[]...}`.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/harness -run 'TestValidateWorkerResultRejectsEditorCompleteWithoutChanges|TestValidateWorkerResultParsesEditorPayload' -count=1`
Expected: FAIL because empty `changes` currently validates.

- [ ] **Step 3: Write minimal implementation**

Require at least one `changes` entry when editor status is `complete`, while still allowing `blocked` payloads to report no edits.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/harness -run 'TestValidateWorkerResultRejectsEditorCompleteWithoutChanges|TestValidateWorkerResultParsesEditorPayload' -count=1`
Expected: PASS.

## Chunk 2: TUI Highlighting And Header Surface

### Task 3: Render inline code semantically instead of as one opaque span

**Files:**
- Modify: `internal/tui/semantic.go`
- Modify: `internal/tui/codeblock.go`
- Test: `internal/tui/semantic_test.go`
- Test: `internal/tui/codeblock_test.go`

- [ ] **Step 1: Write the failing tests**

Add tests proving inline code can color:
- commands like `` `go test ./...` ``
- paths like `` `./internal/tui` ``
- script names like `` `runner.sh` `` and `` `verify_cpe_transfer_and_ack.sh` ``
and does not collapse the whole inline span to one generic style.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/tui -run 'Test(TokenizePlain|RenderMessageContent|RenderSemantic).*Inline|TestRenderMessageContentStylesCommandsAndPaths' -count=1`
Expected: FAIL because inline code is currently opaque.

- [ ] **Step 3: Write minimal implementation**

Render inline-code bodies through the semantic tokenizer and style only the delimiters/plain text needed for readability.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/tui -run 'Test(TokenizePlain|RenderMessageContent|RenderSemantic).*Inline|TestRenderMessageContentStylesCommandsAndPaths' -count=1`
Expected: PASS.

### Task 4: Remove the odd per-message header background artifact

**Files:**
- Modify: `internal/tui/chatmsg.go`
- Test: `internal/tui/chatmsg_test.go`

- [ ] **Step 1: Write the failing test**

Add a test that message headers do not inject redundant background fill/style fragments beyond the pane surface.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/tui -run 'TestChatMessageRender(Header|PaintsAppBackground|HeaderAvoidsRedundantBackground)' -count=1`
Expected: FAIL because headers currently paint their own background.

- [ ] **Step 3: Write minimal implementation**

Let the chat pane own the background surface and simplify message header rendering accordingly.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/tui -run 'TestChatMessageRender(Header|PaintsAppBackground|HeaderAvoidsRedundantBackground)' -count=1`
Expected: PASS with updated assertions.

## Chunk 3: Focused Verification

### Task 5: Run the relevant suites

**Files:**
- Modify: `internal/harness/classifier.go`
- Modify: `internal/harness/contracts.go`
- Modify: `internal/tui/semantic.go`
- Modify: `internal/tui/codeblock.go`
- Modify: `internal/tui/chatmsg.go`

- [ ] **Step 1: Run harness tests**

Run: `go test ./internal/harness -count=1`
Expected: PASS.

- [ ] **Step 2: Run TUI tests**

Run: `go test ./internal/tui -count=1`
Expected: PASS.

- [ ] **Step 3: Run combined targeted verification**

Run: `go test ./internal/harness ./internal/tui -count=1`
Expected: PASS.
