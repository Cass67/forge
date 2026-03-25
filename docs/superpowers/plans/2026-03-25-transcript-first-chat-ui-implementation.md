# Transcript-First Chat UI Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the dashboard-style chat UI with a single transcript-first surface and make delegated results/actionability flow produce useful inline answers without English fallback patching.

**Architecture:** Keep the redesign localized to the existing seams. `internal/tui` owns transcript presentation, a single inline working line, `/expand`, and removal of the default tools pane; `internal/agent/delegate_result.go` owns structured summary selection so the UI consumes already-meaningful text; `internal/agent/agent.go` owns interpretive auto-chaining and reusable scout context via a stable topic key instead of phrase heuristics.

**Tech Stack:** Go, Bubble Tea, Lip Gloss, existing `internal/llm` event stream, Go test/build tooling.

---

## Chunk 1: Transcript-First TUI

### Task 1: Lock in the new transcript behavior with failing tests

**Files:**
- Modify: `internal/tui/chatmodel_test.go`
- Modify: `internal/tui/view_test.go`
- Modify: `internal/tui/chatmsg_test.go`

- [ ] **Step 1: Add transcript-first regression tests for live progress and completion**

Add/adjust tests to cover:
- exactly one live `MsgWorking` line for sequential subagent/runtime progress updates
- no `Recent activity • <role>` headers
- no appended `Agent complete • <timestamp>` status message on successful turns
- delegate results render as the useful inline answer message rather than a separate completion banner
- `/expand` with no payload leaves the transcript unchanged and surfaces deterministic `nothing to expand`
- `/expand` expands directly below the owning answer message
- a new expandable result replaces the previous payload tracked by `lastExpandable`

- [ ] **Step 2: Run the focused TUI tests to verify the new expectations fail**

Run: `go test ./internal/tui -run 'TestChatModel(Progress|HandlesDoneEvent|DelegateResult)|TestViewOutput|TestChatMessage'`
Expected: FAIL on the old recent-activity blocks, status banners, and boxed/dashboard assumptions.

### Task 2: Simplify transcript rendering and remove default tools-pane behavior

**Files:**
- Modify: `internal/tui/chatmsg.go`
- Modify: `internal/tui/chattheme.go`
- Modify: `internal/tui/chatmodel.go`

- [ ] **Step 1: Simplify `ChatMessage` rendering for transcript-first output**

Implement:
- lighter user/agent message blocks
- compact single-line/bare multiline working messages
- non-dominant status rendering for rare error/info cases only

- [ ] **Step 2: Remove the default side tools pane from the chat layout**

Implement:
- full-width transcript in `View()`
- chat width/content width calculations that no longer reserve 30% for tools
- no default `toolsVisible=true`
- help/command text that no longer advertises toggling a tools pane
- remove or retire `/tools`, `/toggle tools`, tools-pane focus handling, and split-pane rendering paths so the old pane is not reachable behind hidden commands

- [ ] **Step 3: Collapse progress into one live working line**

Implement:
- replace recent-activity block aggregation with a single replace-in-place working message
- handoffs update the same working line instead of freezing old blocks
- successful delegate completion removes or supersedes the working line

- [ ] **Step 4: Keep detail-on-demand through `/expand` only**

Implement:
- `/expand` with no payload leaves transcript unchanged and sets deterministic `nothing to expand`
- `/expand` with payload appends a full-width inline detail message below the relevant result
- latest expandable payload replaces the previous one

- [ ] **Step 5: Run the focused TUI tests to verify the transcript redesign passes**

Run: `go test ./internal/tui -run 'TestChatModel(Progress|HandlesDoneEvent|DelegateResult|Expand)|TestViewOutput|TestChatMessage'`
Expected: PASS

### Task 3: Clean up compatibility edges without reintroducing the panel

**Files:**
- Modify: `internal/tui/chatmodel.go`
- Modify: `internal/tui/chatshared.go`
- Modify: `internal/tui/chatmodel_test.go`

- [ ] **Step 1: Preserve snapshot/session compatibility with the new single-pane layout**

Implement:
- safe loading of old `ToolsVisible` / `ToolsBuf` snapshots without restoring the old split-pane UI
- transcript restoration continues to show the saved conversation content inline

- [ ] **Step 2: Add regression tests for retired tools-pane affordances**

Cover:
- old tools-pane toggle commands no longer re-enable a side pane
- tools-pane focus/scroll paths do not affect the transcript-first default
- snapshot restore does not reopen a split-pane layout
- [ ] **Step 3: Run the broader TUI package tests**

Run: `go test ./internal/tui`
Expected: PASS, or clearly identified unrelated/pre-existing failures.

- [ ] **Step 4: Commit the transcript-first TUI slice**

```bash
git add internal/tui/chatmodel.go internal/tui/chatmsg.go internal/tui/chattheme.go internal/tui/chatmodel_test.go internal/tui/chatmsg_test.go internal/tui/view_test.go docs/superpowers/plans/2026-03-25-transcript-first-chat-ui-implementation.md
git commit -m "feat: redesign chat tui as transcript first"
```

## Chunk 2: Delegate Result Summaries And Auto-Chaining

### Task 4: Lock in delegate-summary extraction with failing tests

**Files:**
- Modify: `internal/agent/delegate_result_test.go`

- [ ] **Step 1: Add role-aware summary extraction tests**

Cover:
- scout bare JSON yields source/trigger summary instead of `Evidence gathered.`
- architect bare JSON yields severity/actionability/next-check summary instead of `Architect output ready.`
- doctor and builder structured artifacts prefer extracted summaries over generic defaults
- specific non-generic `message` still wins while the artifact remains expandable context
- placeholder normalization handles whitespace/case/trailing-period variants for `evidence gathered`, `architect output ready`, `diagnosis ready`, `implementation complete`, `recommendations ready`, and `plan ready`

- [ ] **Step 2: Run delegate-result tests to verify the new expectations fail**

Run: `go test ./internal/agent -run 'TestParseDelegateOutcome'`
Expected: FAIL on the current generic placeholder behavior.

### Task 5: Implement role-aware summary selection in `delegate_result.go`

**Files:**
- Modify: `internal/agent/delegate_result.go`
- Modify: `internal/agent/agent.go`
- Modify: `internal/agent/event_render.go`
- Modify: `internal/tui/chatmodel.go`
- Modify: `internal/tui/chatmodel_test.go`

- [ ] **Step 1: Add generic-message suppression and artifact parsing helpers**

Implement:
- normalized generic placeholder detection
- role-aware extraction from JSON artifact payloads
- single-owner `DisplayText()` / `ContextText()` behavior so UI code does not guess

- [ ] **Step 2: Keep raw artifact text available for `/expand` / dispatch context**

Implement:
- extracted summary for visible display
- original artifact string preserved as context text when structured content exists
- `ContextText()` propagated through the event-render path so the transcript can expand a summarized delegate result inline

- [ ] **Step 3: Add or update an end-to-end TUI test for delegate expansion**

Cover:
- a specific non-generic delegate message stays visible inline
- the structured artifact remains available to `/expand`

- [ ] **Step 4: Run delegate-result and delegate-expansion tests**

Run:
- `go test ./internal/agent -run 'TestParseDelegateOutcome'`
- `go test ./internal/tui -run 'TestChatModel.*Expand'`
Expected: PASS

### Task 6: Lock in interpretive auto-chain behavior with failing tests

**Files:**
- Modify: `internal/agent/agent_test.go`

- [ ] **Step 1: Add dispatch tests for interpretive vs trace flows**

Cover:
- interpretive question with scout evidence auto-chains once to architect in the same turn
- evidence-only trace question still stops after scout
- architect blocked after scout falls back to scout evidence plus a short note
- reusable scout evidence uses a stable topic key and does not depend on English fallback phrases
- only current-turn or immediately-prior-turn scout evidence may be reused
- different-topic follow-ups do not reuse the previous scout artifact
- no-stable-topic-key cases do not reuse prior evidence

- [ ] **Step 2: Run focused dispatch tests to verify the new behavior fails**

Run: `go test ./internal/agent -run 'TestDispatch.*(Interpret|Trace|Topic|Fallback)'`
Expected: FAIL before the dispatcher changes.

### Task 7: Implement topic-aware interpretive auto-chain in `agent.go`

**Files:**
- Modify: `internal/agent/agent.go`

- [ ] **Step 1: Store dispatch metadata needed for same-topic evidence reuse**

Implement:
- per-role result/artifact bookkeeping extended with `topic_key`
- stable topic derivation from scout artifact fields (`source_file[:source_line]`, `source`, fallback normalized task label)
- explicit reuse bounds limited to the current turn and immediately prior turn only

- [ ] **Step 2: Add dispatch intent classification and one-hop auto-chain**

Implement:
- classify user turns into `trace`, `interpret`, `debug`, `implement` using routing-level logic
- when an `interpret` turn completes scout evidence without an explicit next role, enqueue one architect follow-on with scout context
- prevent more than one synthetic architect follow-on per user turn

- [ ] **Step 3: Add architect-fallback handling after scout success**

Implement:
- if architect blocks/errors/times out after scout evidence exists, surface the scout summary plus one short interpretation-unavailable note

- [ ] **Step 4: Run focused dispatch tests**

Run: `go test ./internal/agent -run 'TestDispatch.*(Interpret|Trace|Topic|Fallback)'`
Expected: PASS

### Task 8: Verify, inspect live behavior, and commit

**Files:**
- Modify: `internal/agent/agent.go`
- Modify: `internal/agent/delegate_result.go`
- Modify: `internal/agent/agent_test.go`
- Modify: `internal/agent/delegate_result_test.go`

- [ ] **Step 1: Run the full targeted verification suite**

Run:
- `go test ./internal/agent`
- `go test ./internal/tui`
- `go build -o ./forge ./cmd/forge`

Expected:
- all commands exit 0

- [ ] **Step 2: Run the fresh reproduction and inspect the debug log**

Run the relevant repro command that creates the fresh `/tmp` debug JSONL, then inspect only the new log file for:
- no generic placeholder final messages where structured artifacts contain actionable fields
- no extra completion banners dominating the transcript
- interpretive follow-up turns reaching architect output in the same turn when appropriate

- [ ] **Step 3: Commit the agent/result slice**

```bash
git add internal/agent/agent.go internal/agent/delegate_result.go internal/agent/agent_test.go internal/agent/delegate_result_test.go
git add -f docs/superpowers/plans/2026-03-25-transcript-first-chat-ui-implementation.md
git commit -m "feat: improve delegate summaries and transcript flow"
```
