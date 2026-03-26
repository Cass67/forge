# Chat Semantic Highlighting And Header Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ship balanced semantic highlighting across Forge's plain-text TUI surfaces and replace the startup strip with a calmer Codex-like split-rail header without breaking wrapping, scrolling, or copy-friendly transcript behavior.

**Architecture:** Add one conservative semantic rendering module in `internal/tui` that classifies plain-text spans once and renders them per profile (`prose`, `status`, `trace`). Route transcript prose, status rows, trace views, and legacy running/live panes through that shared helper, keep fenced code, diffs, inline code, and pre-styled ANSI content on their current paths, then rebuild the startup header as a small card with deterministic width fallbacks and dynamic layout height in `chatmodel.go`.

**Tech Stack:** Go, Bubble Tea, Lip Gloss, existing `internal/tui` viewport/wrap helpers, Go test tooling.

---

## File Map

### Shared semantic rendering core

- Create: `internal/tui/semantic.go`
  Purpose: define token kinds, semantic spans, surface profiles, conservative plain-text tokenization, and ANSI-safe profile rendering helpers.
- Create: `internal/tui/semantic_test.go`
  Purpose: lock the approved token classes, bypass rules, representative examples, and width-safe rendering behavior.
- Modify: `internal/tui/codeblock.go`
  Purpose: make the plain non-fenced message path the single shared entry point for semantic rendering while fenced code and diffs continue to bypass it.
- Modify: `internal/tui/codeblock_test.go`
  Purpose: verify prose blocks gain semantic styling while fenced blocks and diff blocks remain on the current renderer path.

### Transcript and auxiliary surface integration

- Modify: `internal/tui/chatmsg.go`
  Purpose: route user/assistant prose, working rows, and status rows through semantic rendering profiles without reintroducing boxed message chrome.
- Modify: `internal/tui/chatmsg_test.go`
  Purpose: cover semantic rendering for transcript prose, status rows, and working rows.
- Modify: `internal/tui/traceview.go`
  Purpose: apply the `trace` profile in the dock and overlay while preserving width and log-path rendering.
- Modify: `internal/tui/traceview_test.go`
  Purpose: verify label/value separation, semantic tokens, and width-safe trace wrapping.
- Modify: `internal/tui/running.go`
  Purpose: apply restrained semantic rendering inside the split-pane running surface without widening panes or breaking scroll math.
- Modify: `internal/tui/running_test.go`
  Purpose: verify semantic rendering keeps split panes within terminal bounds and highlights high-signal tokens.
- Modify: `internal/tui/live.go`
  Purpose: route legacy live-pane content through the same semantic renderer used by the running surface.
- Create: `internal/tui/live_test.go`
  Purpose: add coverage for semantic rendering in the legacy live surface, which currently has no dedicated tests.

### Header card and shell layout

- Modify: `internal/tui/chatstats.go`
  Purpose: replace the one-line strip with the approved split-rail header card, add truncation helpers, and expose deterministic narrow-width layout behavior.
- Modify: `internal/tui/chatstats_test.go`
  Purpose: lock the 72-column split rail, 64-column collapsed rail, and 48-column stacked header behaviors plus truncation rules.
- Modify: `internal/tui/chatmodel.go`
  Purpose: replace the fixed one-line header height with rendered-header-aware layout math, remove the old helper line, and keep the empty-state startup copy to `Forge is ready.` only.
- Modify: `internal/tui/chatmodel_test.go`
  Purpose: verify dynamic header height, startup copy, and no-overflow layout behavior.
- Modify: `internal/tui/view_test.go`
  Purpose: lock the new startup appearance in the main chat view and ensure default view keeps transcript/composer layout intact.
- Modify: `internal/tui/pipeline_test.go`
  Purpose: stop assuming a one-line header so the viewport pipeline keeps reflecting the real chat layout.
- Modify: `internal/tui/viewport_pipeline_test.go`
  Purpose: keep viewport calculations aligned with the new rendered header height during regression diagnostics.

## Chunk 1: Shared Semantic Rendering

Execution note: follow `@superpowers/test-driven-development` inside each task, and use `@superpowers/verification-before-completion` before the chunk commits.

### Task 1: Build the semantic tokenizer and renderer core

**Files:**
- Create: `internal/tui/semantic.go`
- Create: `internal/tui/semantic_test.go`

- [ ] **Step 1: Write failing semantic-core tests from the approved examples**

Add tests that cover:
- `TokenizePlain` classifies `command`, `path`, `env`, `number`, `status-*`, and `label` spans using the examples in the approved spec
- command spans stop at deterministic clause boundaries such as newline, `|`, `&&`, `||`, and `;`
- Windows paths such as `C:\work\forge`, home-relative paths such as `~/src/forge`, and parent-relative paths such as `../internal/tui` classify as `path`
- `${FORGE_THEME}` and `$FORGE_THEME` classify as `env`, while uppercase prose words outside assignment/key-value context stay plain
- config identifiers such as `FORGE_THEME=low` or `config: FORGE_THEME` classify conservatively only in key/value-like contexts
- trailing punctuation does not prevent classification, but punctuation outside the token stays plain
- `approved` in normal prose stays plain while `status: approved` can highlight under the structured-context rule
- inline code, URLs, and already-styled ANSI spans are left untouched
- ambiguous prose stays plain
- rendered semantic output has the same printable width as the unstyled input

Example target:

```go
func TestTokenizePlainRepresentativeExamples(t *testing.T) {
	got := TokenizePlain("status: approved in 1.2s")
	assertKinds(t, got, []semanticKind{
		semanticLabel,
		semanticPlain,
		semanticStatusGood,
		semanticPlain,
		semanticNumber,
	})
}
```

- [ ] **Step 2: Run the focused semantic-core tests and verify they fail**

Run: `go test ./internal/tui -run 'Test(TokenizePlain|RenderSemantic)' -count=1`

Expected: FAIL because the semantic tokenizer and renderer do not exist yet.

- [ ] **Step 3: Implement the conservative semantic core**

Implement:
- `semanticKind`, `semanticSpan`, and `semanticProfile` in `internal/tui/semantic.go`
- `func TokenizePlain(text string) []semanticSpan`
- `func RenderSemantic(spans []semanticSpan, profile semanticProfile, theme chatTheme) string`
- `func RenderSemanticPlain(text string, profile semanticProfile, theme chatTheme) string`
- a deterministic command-boundary helper so tests do not depend on fuzzy English heuristics

Implementation notes:
- keep ANSI escapes as opaque spans
- treat URLs as plain text in this slice
- keep profile intensity restrained: `trace` > `prose`, `status` calmer than `trace`
- use existing theme color families rather than inventing a new palette system

- [ ] **Step 4: Run the focused semantic-core tests and verify they pass**

Run: `go test ./internal/tui -run 'Test(TokenizePlain|RenderSemantic)' -count=1`

Expected: PASS

- [ ] **Step 5: Commit the semantic core**

```bash
git add internal/tui/semantic.go internal/tui/semantic_test.go
git commit -m "feat: add shared semantic renderer for tui text"
```

### Task 2: Route transcript prose, status rows, and message blocks through the shared semantic path

**Files:**
- Modify: `internal/tui/codeblock.go`
- Modify: `internal/tui/codeblock_test.go`
- Modify: `internal/tui/chatmsg.go`
- Modify: `internal/tui/chatmsg_test.go`

- [ ] **Step 1: Add failing transcript/message rendering tests**

Cover:
- non-fenced prose highlights `go test ./...` and `./internal/tui`
- fenced code blocks and diff blocks still render exactly as code blocks
- inline code remains opaque inside normal transcript prose
- `MsgStatus` uses the `status` profile so `status:` is dimmer than `approved` and `1.2s`
- `MsgWorking` remains restrained while still highlighting high-signal tokens
- the integration tests assert that styling is actually present on the expected substrings, not just that the raw text survived rendering

Example target:

```go
func TestRenderMessageContentStylesCommandsAndPaths(t *testing.T) {
	theme := lookupThemeForTest(t, "default")
	got := renderMessageContent("Run go test ./... from ./internal/tui", 60, theme)
	assertStyledSubstring(t, got, "go test ./...", theme.AccentSecondary)
	assertStyledSubstring(t, got, "./internal/tui", theme.AccentPrimary)
}
```

- [ ] **Step 2: Run the focused transcript/message tests and verify they fail**

Run: `go test ./internal/tui -run 'Test(RenderMessageContent|ChatMessageRender)' -count=1`

Expected: FAIL because plain blocks and status rows still use flat text rendering.

- [ ] **Step 3: Implement shared plain-text entry routing**

Implement:
- `renderMessageContent` sends only non-fenced blocks through `RenderSemanticPlain(..., profileProse, ...)`
- fenced code blocks and diffs bypass semantic rendering completely
- `ChatMessage.Render` uses:
  - `profileProse` for user/assistant/forge prose
  - `profileStatus` for `MsgStatus`
  - `profileStatus` with restrained prefix styling for `MsgWorking`

Keep existing transcript spacing and indentation behavior intact.

- [ ] **Step 4: Run the focused transcript/message tests and verify they pass**

Run: `go test ./internal/tui -run 'Test(RenderMessageContent|ChatMessageRender)' -count=1`

Expected: PASS

- [ ] **Step 5: Run chunk-level TUI verification**

Run: `go test ./internal/tui -count=1`

Expected: PASS

- [ ] **Step 6: Commit transcript semantic rendering**

```bash
git add internal/tui/codeblock.go internal/tui/codeblock_test.go internal/tui/chatmsg.go internal/tui/chatmsg_test.go
git commit -m "feat: highlight semantic tokens in transcript text"
```

## Chunk 2: Surface Integration And Header Card

Execution note: follow `@superpowers/test-driven-development` inside each task, and use `@superpowers/verification-before-completion` before the chunk commits.

### Task 3: Integrate semantic rendering into trace, running, and legacy live surfaces

**Files:**
- Modify: `internal/tui/traceview.go`
- Modify: `internal/tui/traceview_test.go`
- Modify: `internal/tui/running.go`
- Modify: `internal/tui/running_test.go`
- Modify: `internal/tui/live.go`
- Create: `internal/tui/live_test.go`

- [ ] **Step 1: Add failing auxiliary-surface tests**

Cover:
- trace dock and overlay render `tool_call:` as a dim label and `forge -d` / `/tmp/forge-debug.jsonl` as semantic values
- semantic styling in trace views does not produce lines wider than the available width, using an ANSI-aware printable-width helper such as `ansiPrintableWidth(line) <= width`
- running panes highlight high-signal tokens without breaking split-pane width bounds
- legacy live panes render semantic tokens in pane bodies and keep existing scroll/focus behavior
- the integration tests assert that styling is actually present on expected trace/pane substrings rather than only checking raw text
- pre-styled ANSI trace spans pass through unchanged, keep their original ANSI sequences intact, and are not re-tokenized by the semantic helper
- include a concrete pass-through fixture such as `\x1b[31merror\x1b[0m` so the preserved ANSI sequence is asserted directly

Example target:

```go
func TestRenderTraceOverlayPanelUsesTraceProfile(t *testing.T) {
	theme := lookupThemeForTest(t, "default")
	got := renderTraceOverlayPanel(theme, "tool_call: forge -d\nstatus: approved", "/tmp/forge-debug.jsonl", 100, 24)
	assertStyledSubstring(t, got, "tool_call:", theme.TextDim)
	assertStyledSubstring(t, got, "forge -d", theme.AccentSecondary)
}
```

- [ ] **Step 2: Run the focused auxiliary-surface tests and verify they fail**

Run: `go test ./internal/tui -run 'Test(RenderTrace|Running|Live)' -count=1`

Expected: FAIL because trace/running/live surfaces still render flat text.

- [ ] **Step 3: Implement semantic rendering for non-transcript surfaces**

Implement:
- trace dock and overlay use `profileTrace` through the shared semantic renderer
- running panes use `profileStatus` so continuous updates stay calmer than trace while still surfacing high-signal tokens
- running panes render semantic text before width fitting so pane widths still clamp correctly
- legacy live panes use the same `profileStatus` helper path as running panes
- width calculations continue to rely on stripped text or existing ANSI-aware helpers rather than styled-string length

- [ ] **Step 4: Run the focused auxiliary-surface tests and verify they pass**

Run: `go test ./internal/tui -run 'Test(RenderTrace|Running|Live)' -count=1`

Expected: PASS

- [ ] **Step 5: Commit semantic integration for trace and pane surfaces**

```bash
git add internal/tui/traceview.go internal/tui/traceview_test.go internal/tui/running.go internal/tui/running_test.go internal/tui/live.go internal/tui/live_test.go
git commit -m "feat: extend semantic highlighting to trace and pane views"
```

### Task 4: Replace the status strip with the approved startup header card

**Files:**
- Modify: `internal/tui/chatstats.go`
- Modify: `internal/tui/chatstats_test.go`
- Modify: `internal/tui/chatmodel.go`
- Modify: `internal/tui/chatmodel_test.go`
- Modify: `internal/tui/view_test.go`
- Modify: `internal/tui/pipeline_test.go`
- Modify: `internal/tui/viewport_pipeline_test.go`

- [ ] **Step 1: Add failing header and layout tests**

Cover:
- `renderStatusHeader` renders the split-rail card at 72 columns and above
- 56 to 71 columns collapse to a one-line wordmark rail
- below 56 columns hide the ASCII mark and render a stacked metadata card
- model values truncate on the right, workdir values truncate on the left after home-relative shortening
- long model values end with `…` when truncated, and long workdir values begin with `…` while preserving the visible tail segments
- workdirs under `$HOME` are converted to `~/...` before any left-side truncation logic runs
- model and workdir rows remain single-line in every width mode
- no header line exceeds the available width in any width mode, using an ANSI-aware printable-width helper such as `ansiPrintableWidth(line) <= width`
- `View()` empty state keeps only `Forge is ready.` and removes the old helper lines
- chat layout stays within the terminal height after moving from a one-line header to a multi-line card

Example target:

```go
func TestRenderStatusHeaderFallsBackAtNarrowWidths(t *testing.T) {
	data := chatStatusData{Model: "openai/gpt-5.4", WorkDir: "/Users/mcassidy/Documents/OPC/git/other/forge"}
	wide := strippedLine(renderStatusHeader(lookupThemeForTest(t, "default"), data, 80))
	medium := strippedLine(renderStatusHeader(lookupThemeForTest(t, "default"), data, 64))
	narrow := strippedLine(renderStatusHeader(lookupThemeForTest(t, "default"), data, 48))
	if !strings.Contains(wide, "FORGE") || strings.Contains(narrow, "FORGE") {
		t.Fatalf("unexpected width fallbacks:\nwide=%q\nnarrow=%q", wide, narrow)
	}
}
```

- [ ] **Step 2: Run the focused header/layout tests and verify they fail**

Run: `go test ./internal/tui -run 'Test(RenderStatusHeader|ChatModelView(Header|Fits)|DefaultChatView)' -count=1`

Expected: FAIL because the current header is a one-line strip and the old helper lines still render in the empty state.

- [ ] **Step 3: Implement the header card and dynamic layout math**

Implement:
- truncation helpers in `chatstats.go` for model and workdir display
- the approved split-rail header card with a restrained ASCII `FORGE` mark
- deterministic width breakpoints:
  - `>=72`: split rail
  - `56-71`: collapsed single-line wordmark rail
  - `<56`: stacked metadata card without the ASCII mark
- a rendered-header-aware height helper in `chatmodel.go` so chat body, mouse context, and composer placement stop assuming `chatHeaderHeight = 1`
- empty-state startup copy reduced to `Forge is ready.` only

- [ ] **Step 4: Run the focused header/layout tests and verify they pass**

Run: `go test ./internal/tui -run 'Test(RenderStatusHeader|ChatModelView(Header|Fits)|DefaultChatView)' -count=1`

Expected: PASS

- [ ] **Step 5: Commit the header redesign**

```bash
git add internal/tui/chatstats.go internal/tui/chatstats_test.go internal/tui/chatmodel.go internal/tui/chatmodel_test.go internal/tui/view_test.go internal/tui/pipeline_test.go internal/tui/viewport_pipeline_test.go
git commit -m "feat: redesign forge startup header card"
```

### Task 5: Verify the full slice and do the live manual check

**Potentially modify:**
- Modify: `internal/tui/semantic.go`
- Modify: `internal/tui/semantic_test.go`
- Modify: `internal/tui/chatstats.go`
- Modify: `internal/tui/chatstats_test.go`
- Modify: `internal/tui/chatmodel.go`
- Modify: `internal/tui/chatmodel_test.go`
- Modify: `internal/tui/traceview.go`
- Modify: `internal/tui/traceview_test.go`
- Modify: `internal/tui/running.go`
- Modify: `internal/tui/running_test.go`
- Modify: `internal/tui/live.go`
- Modify: `internal/tui/live_test.go`

- [ ] **Step 1: Run the package-level verification suite**

Run:
- `go test ./internal/tui -count=1`
- `go test ./... -count=1`
- `go build -o ./forge ./cmd/forge`

Expected:
- all commands exit 0

- [ ] **Step 2: Run the live UI smoke check against a fresh debug log**

Manual check:
- start default chat and verify the new header, empty state, and semantic transcript rendering
- start `forge -d` and verify the trace dock/overlay profile with a fresh `/tmp/forge-debug.jsonl`
- confirm a representative line such as `tool_call: forge -d`, `status: approved in 1.2s`, and `/tmp/forge-debug.jsonl` is easier to scan without widening the layout

- [ ] **Step 3: Fix any verification fallout and re-run the affected tests**

Run the narrowest failing test/package commands first, then re-run:
- `go test ./internal/tui -count=1`
- `go test ./... -count=1`

Expected: PASS

- [ ] **Step 4: Commit the completed semantic-highlighting and header slice**

```bash
git add internal/tui/semantic.go internal/tui/semantic_test.go internal/tui/codeblock.go internal/tui/codeblock_test.go
git add internal/tui/chatmsg.go internal/tui/chatmsg_test.go internal/tui/traceview.go internal/tui/traceview_test.go
git add internal/tui/running.go internal/tui/running_test.go internal/tui/live.go internal/tui/live_test.go
git add internal/tui/chatstats.go internal/tui/chatstats_test.go internal/tui/chatmodel.go internal/tui/chatmodel_test.go
git add internal/tui/view_test.go internal/tui/pipeline_test.go internal/tui/viewport_pipeline_test.go
git commit -m "feat: ship semantic tui highlighting and startup header card"
```
