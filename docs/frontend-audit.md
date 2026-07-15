# Forge Frontend Design Audit: Task Progress, Todos, and Inline Diffs

## Executive Summary

The Forge frontend has a solid Bubble Tea foundation with good theme support and semantic highlighting, but it falls short of modern standards (OpenCode, Claude Code) in three critical areas:

1. **Task progress is invisible** — plans exist as text messages but offer no visual progress indication
2. **TODO/checklists are plain text** — no checkboxes, progress bars, or state-driven styling
3. **Diffs are primitive** — only basic code-block coloring, no inline or side-by-side diff views
4. **Information hierarchy is flat** — the Agent View replaces chat instead of complementing it

## Current State Analysis

### 1. Task Progress / Plans

**Current Implementation:**
- Plans are `MsgPlan` messages upserted into the chat stream (`chatmodel.go`)
- Rendered with structured plan step parsing, icons, and progress bars (`plan.go`)
- The plan is also pinned above the chat viewport as a sticky plan (`renderStickyPlan`)
- Plan state changes are parsed with regex and rendered with appropriate icons and colors

**Code Reference:**
```go
// plan.go — plan parsing and rendering
parsePlanSteps()  // extracts structured steps from plan content
renderPlanContent()  // renders with icons, colors, progress bar
renderStickyPlan()  // pinned plan above chat viewport
formatPlanStepLine()  // single step with icon + stylized status
formatPlanProgressBar()  // ASCII progress bar
```

**What's done:**
- ✅ Sticky plan pinned above chat — doesn't scroll away
- ✅ Parsed `[completed]` / `[in_progress]` / `[blocked]` / `[pending]` tags with icons
- ✅ Progress bar with completion ratio (e.g., "3/7 steps")
- ✅ Plan state changes use distinct colors (✓ completed, in_progress active, ○ pending)

### 2. TODO / Checklist Rendering

**Current Implementation:**
- Markdown list items are detected (`codeblock.go`) and styled with a bullet marker
- The semantic tokenizer (`semantic.go`) handles prose but has no concept of task states
- Agent tasks (`chatAgentTaskState`) track status and render as bordered card-style rows in a bottom panel (`agentpanel.go`)
- Task state colors (TaskActive, TaskCompleted, TaskBlocked, TaskPending) are defined in all 8 theme variants

**Code Reference:**
```go
// agentpanel.go — Agent task panel with bordered cards
renderAgentTaskPanel()  // renders active tasks as bottom panel
formatTaskPanelCard()   // each task as a bordered card with icon, role, tool, elapsed
agentTaskPanelHeight()  // accounts for card borders in layout budget
```

**What's done:**
- ✅ Task status as visual badges with icons (✓, ⟳, ⚠, ○)
- ✅ Colored status indicators per theme
- ✅ Task list in normal chat UI as bottom panel
- ✅ Elapsed time updates at each render cycle (120ms tick)

### 3. Inline Diffs

**Current Implementation:**
- Diff blocks get enhanced rendering with line numbers, file headers, hunk headers, and word-level highlighting (`diff.go`)
- The diff log is generated per round (`output/diff.go`) and surfaced in the review screen
- A diff preview overlay shows when a tool action requires user approval
- File changes are tracked and displayed as a "Recent Tools" panel and file changes summary

**Code Reference:**
```go
// diff.go — Enhanced diff rendering
enhancedDiffBlock()        // line numbers, file headers, +/- styling
wordDiff()                 // LCS-based word-level highlighting
renderWordHighlightedLine() // renders highlighted changed words

// toolcards.go — Tool call cards and file changes panel
renderToolCardsPanel()      // recent tool calls as bordered cards
renderFileChangesPanel()    // file changes summary

// chatmodel.go — Approval overlay
renderApprovalOverlay()    // diff preview when pending approval
```

**What's done:**
- ✅ Inline word-level highlighting within changed lines (LCS-based)
- ✅ Line numbers, file headers, hunks
- ✅ File changes panel showing modified/added/deleted counts
- ✅ Diff log surfaced in review screen
- ✅ Diff preview overlay for approval mode

### 4. Layout and Information Architecture

**Current Layout (Normal Mode):**
```
┌─────────────────────────────────────┐
│ Status Header (model, dir, tokens)  │
├─────────────────────────────────────┤
│  Plan (sticky, pinned)              │
├─────────────────────────────────────┤
│                                     │
│  Chat History / Agent View (Tab)    │
│  (single pane, scrollable)          │
│                                     │
├─────────────────────────────────────┤
│  Agent Task Panel (bordered cards)  │
├─────────────────────────────────────┤
│  Recent Tools (bordered cards)      │
├─────────────────────────────────────┤
│  File Changes (summary)             │
├─────────────────────────────────────┤
│ · ⟳ running tests... (live slot)   │
├─────────────────────────────────────┤
│ > user input / approval overlay     │
├─────────────────────────────────────┤
│ footer stats (tokens, plan 3/7)     │
└─────────────────────────────────────┘
```

**What's done:**
- ✅ Sticky plan pinned above chat — doesn't scroll away
- ✅ Agent task panel with bordered card-style rows
- ✅ Tool call cards showing recent tools
- ✅ File changes panel summary
- ✅ Approval diff overlay
- ✅ Plan progress in stats footer
- ✅ Stacked live progress lines
- ✅ Code block previews in running screen
- ✅ Plan steps and file changes tracked in running screen

## Implementation Status

| Priority | Feature | Status |
|----------|---------|--------|
| P1 | Sticky plan above chat scroll | ✅ Done |
| P1 | Parse `[completed]`/`[in_progress]` tags | ✅ Done |
| P2 | Agent task badges in status header | ✅ Done |
| P2 | Rich task cards in Agent View (bordered cards) | ✅ Done |
| P2 | Task state theme colors | ✅ Done |
| P2 | Live elapsed timer (tick-based refresh) | ✅ Done |
| P3 | File changes panel | ✅ Done |
| P3 | Enhanced diff block rendering (line nums) | ✅ Done |
| P3 | Word-level diff highlighting | ✅ Done |
| P3 | Diff preview overlay for approval mode | ✅ Done |
| P3 | Surface diff log in UI (review screen) | ✅ Done |
| P4 | Merge running screen into chat UI | ✅ Done — ChatModel-based pipeline with toggleable two-pane view |
| P4 | Show code previews instead of folding | ✅ Done |
| P4 | Plan and file changes in running screen | ✅ Done |
| P4 | Live file preview in third pane | ✅ Done — toggleable file content preview in pipeline view |
| P5 | Stacked progress lines | ✅ Done |
| P5 | Progress bar in stats footer | ✅ Done |
| P5 | Tool call cards | ✅ Done |

## Remaining Work

All items from the audit have been implemented. See the "Future Ideas" section below for potential next steps.

### P4: Running Screen Overhaul (Done)

The running screen (`running.go`) has been merged into the chat UI:

1. **Merge running screen into chat UI** — `forge make` now uses `ChatModel` internally via `RunLivePipeline()`. The pipeline shows events as chat messages by default, and you can toggle to the traditional two-pane writer/auditor view with `Ctrl+P` or `v` (when chat input is empty). No separate CLI flag needed — it's an in-app toggle.

2. **Live file preview in third pane** — when in the two-pane pipeline view, press `p` to open a file preview pane showing the content of the most recently modified file. The pane updates as files change. Arrow keys scroll the preview; `p` or `Escape` closes it. Files larger than 1 MB and binary files are gracefully refused.

### Pipeline Mode Controls

| Key | Action |
|-----|--------|
| `Ctrl+P` | Toggle between chat view and pipeline view |
| `v` | Switch to pipeline view (from chat view, input empty) |
| `←` / `→` | Focus left/right pane (pipeline view) |
| `↑` / `↓` | Scroll focused pane (pipeline view) |
| `p` | Toggle file preview pane (pipeline view) |
| `Space` | Advance turn (manual mode) |
| `Enter` | Advance turn (manual mode) or quit (when pipeline done) |
| `q` | Quit (when pipeline done/aborted) |
| `Ctrl+C` | Abort pipeline

## Conclusion

The Forge frontend has addressed **all** items from the original audit. The chat UI now provides:

- **Persistent plan display** above the chat that doesn't scroll away
- **Visual task tracking** with bordered cards, status icons, and theme colors
- **Rich diff rendering** with line numbers, word-level highlighting, and approval overlay
- **Tool visibility** through tool call cards and file changes panels
- **Progress visibility** with stacked live status, plan progress bar, and file change counts
- **Pipeline/audit mode** with toggleable two-pane writer/auditor view integrated into ChatModel (`ctrl+p` or `v`)
- **Live file preview** in the pipeline view showing file content as it's modified (`p`)
- **Unified architecture** — both chat and pipeline modes use ChatModel, eliminating the legacy separate running screen

## Pipeline Mode Reference

When running `forge make`, the pipeline uses ChatModel with an integrated pipeline view:

### Key Bindings

| Key | Mode | Action |
|-----|------|--------|
| `Ctrl+P` | Any | Toggle between chat view and two-pane pipeline view |
| `v` | Chat view | Switch to pipeline view (when input is empty) |
| `↑`/`↓` | Pipeline view | Scroll current pane |
| `←`/`→` | Pipeline view | Switch focus between writer (left) and auditor (right) panes |
| `p` | Pipeline view | Toggle file preview pane |
| `PgUp`/`PgDn` | Pipeline view / File preview | Scroll by page |
| `q` | Pipeline view / Done | Quit (or when pipeline is complete) |
| `Enter` | Done | Quit when pipeline is complete |
| `Home`/`End` | Pipeline view | Jump to top/bottom of current pane |
| `Space` / `Enter` | Pipeline view (manual mode) | Advance to next turn |
| `m` | Pipeline view | Toggle manual mode |
| `c` | Pipeline view | Clear both panes |

### Architecture

The pipeline integration lives in `internal/tui/chatpipeline.go`:

- **`RunLivePipeline()`** — entry point replacing `RunLive()`, creates a ChatModel configured for pipeline events
- **`handlePipelineLLMEvent()`** — processes pipeline-specific events (pass start/end, round start/end, feedback request)
- **`handlePipelineKey()`** — manages all pipeline key bindings
- **`pipelineRenderView()`** / **`pipelineRenderTwoPane()`** — rendering for both chat and two-pane pipeline views
- **`loadFilePreview()`** / **`toggleFilePreview()`** — file preview management
- Pipeline state tracking fields on `ChatModel` (`pipelineActive`, `pipelineViewActive`, buffer strings, scroll state, etc.)

The remaining gaps are the full running screen migration to chat UI and live file preview, which are architectural efforts rather than feature gaps.
