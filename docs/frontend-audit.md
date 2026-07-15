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
- Plans are `MsgPlan` messages upserted into the chat stream (`chatmodel.go:420`)
- Rendered as plain text with semantic prose highlighting (`chatmsg.go:70-85`)
- Plan steps like `[completed] Inspect loop` and `[in_progress] Tighten prompt` are just list items
- No progress percentage, no visual completion state
- The plan message scrolls away as new chat messages arrive

**Code Reference:**
```go
// chatmsg.go:70-85 — MsgPlan rendering
if m.Kind == MsgPlan {
    header := strings.TrimSpace(m.Header)
    if header == "" { header = "Plan" }
    body := RenderSemanticPlain(strings.TrimSpace(m.Content), profileProse, theme)
    // ... renders as standard prose with accent color header
}
```

**Problems:**
- No sticky/persistent plan display — users scroll away from the plan and lose context
- No visual parsing of `[completed]` / `[in_progress]` / `[blocked]` / `[pending]` tags
- No progress bar or completion ratio (e.g., "3/7 steps")
- Plan state changes don't animate or draw attention

### 2. TODO / Checklist Rendering

**Current Implementation:**
- Markdown list items are detected (`codeblock.go:282-312`) and styled with a bullet marker
- The semantic tokenizer (`semantic.go`) handles prose but has no concept of task states
- Agent tasks (`chatAgentTaskState`) track status but render as flat text:
  ```
  - task-123 (scout): running; elapsed 45s; read_file ./main.go
  ```

**Code Reference:**
```go
// chatmodel.go:1332-1371 — Agent task line formatting
func formatChatAgentTaskLine(task chatAgentTaskState, now time.Time) string {
    parts := []string{fmt.Sprintf("- %s (%s): %s", id, role, status)}
    // ... appends time, elapsed, last tool, result
    return strings.Join(parts, "; ")
}
```

**Problems:**
- Task status is a text string, not a visual badge (no colored pills for running/done/error)
- No iconography (✓, ○, ✗, ⟳) to scan status quickly
- Task list is hidden behind Agent View (Tab), not visible during normal chat
- No nesting or grouping of sub-tasks
- Elapsed time is plain text, not a live updating timer

### 3. Inline Diffs

**Current Implementation:**
- Diff blocks in markdown fences get line-prefix coloring (`codeblock.go:389-398`):
  - `+` lines → Success color (green)
  - `-` lines → Error color (red)
  - `@@` lines → AccentSecondary (yellow)
- Diff log is generated per round (`output/diff.go:35-80`) but stored in text files, not surfaced in the TUI

**Code Reference:**
```go
// codeblock.go:389-398
if strings.EqualFold(lang, "diff") || strings.EqualFold(lang, "patch") {
    switch {
    case strings.HasPrefix(line, "+"):
        style = style.Foreground(theme.Success)
    case strings.HasPrefix(line, "-"):
        style = style.Foreground(theme.Error)
    case strings.HasPrefix(line, "@@"):
        style = style.Foreground(theme.AccentSecondary)
    }
}
```

**Problems:**
- No side-by-side diff view (OpenCode shows before/after panels)
- No inline word-level highlighting within changed lines
- Diff blocks are just colored text — no line numbers, no file headers, no hunks
- Changes made by the agent are invisible in real-time; users must open files or review after
- No "Files changed" summary panel showing modified/added/deleted counts
- Diff log is written to disk but never shown in the running or chat UI

### 4. Layout and Information Architecture

**Current Layout (Normal Mode):**
```
┌─────────────────────────────────────┐
│ Status Header (model, dir, tokens)  │
├─────────────────────────────────────┤
│                                     │
│  Chat History / Agent View (Tab)    │
│  (single pane, scrollable)           │
│                                     │
├─────────────────────────────────────┤
│ · thinking... (live progress slot) │
├─────────────────────────────────────┤
│ > user input                        │
├─────────────────────────────────────┤
│ footer stats                        │
└─────────────────────────────────────┘
```

**Problems:**
- The Agent View completely **replaces** the chat pane when activated (`chatmodel.go:1252`)
- No persistent sidebar for active tasks or current plan
- Live progress is a single line at the bottom — insufficient for multi-step work
- No "current operation" panel showing what the agent is doing right now
- The running screen (`live.go`, `running.go`) is a separate legacy view with two panes, but it's primitive compared to the chat UI and doesn't show plans/tasks at all

## Recommendations

### P1: Rich Plan / TODO Panel

**Goal:** Make the current plan and task progress visually prominent and persistent.

**Implementation:**

1. **Add a persistent plan sidebar** (right side, ~25-30 chars wide, collapsible):
   ```
   ┌──────────────────────────┬──────────────┐
   │                          │  Plan        │
   │  Chat                    │  ─────────   │
   │                          │  ○ Step 1    │
   │                          │  ✓ Step 2    │
   │                          │  ⟳ Step 3    │
   │                          │  ○ Step 4    │
   │                          │              │
   │                          │  ████████░░  │
   │                          │   3/4 done   │
   └──────────────────────────┴──────────────┘
   ```

2. **Parse plan step states** from `update_plan` content:
   - `[completed]` → ✓ + dim/strikethrough styling
   - `[in_progress]` → ⟳ + bold + accent color + subtle animation
   - `[blocked]` → ⚠ + warning color
   - `[pending]` → ○ + dim

3. **Add `PlanWidget` component** in `internal/tui/`:
   ```go
   type PlanWidget struct {
       Steps       []PlanStep
       Width       int
       Collapsed   bool
   }
   
   type PlanStep struct {
       Label    string
       State    StepState // pending, active, completed, blocked
       Depth    int       // for nested sub-tasks
   }
   ```

4. **Make the plan sticky** — render it in a dedicated region that doesn't scroll with chat, or pin it as the first visible message when active.

### P2: Agent Task Dashboard

**Goal:** Replace the text-based Agent View with a visual task dashboard.

**Implementation:**

1. **Visual task badges** in the status header:
   ```
   forge • claude-sonnet-4 • [⟳ 2 tasks] • ~/project
   ```

2. **Rich task list rendering** in the Agent View:
   ```
   Agent Tasks ─────────────────
   
   ┌────────────────────────┐
   │ ⟳ scout: read_file     │  ← green border for active
   │   ./main.go            │
   │   12s elapsed          │
   └────────────────────────┘
   
   ┌────────────────────────┐
   │ ✓ scout: list_dir      │  ← dim for completed
   │   0.8s • 3 files       │
   └────────────────────────┘
   ```

3. **Add task state icons and colors** to `chatTheme`:
   ```go
   TaskActive    lipgloss.Color
   TaskCompleted lipgloss.Color
   TaskBlocked   lipgloss.Color
   TaskPending   lipgloss.Color
   ```

4. **Live elapsed timer** — update elapsed time every second for running tasks instead of static text.

### P3: Inline Diff Viewer

**Goal:** Show file changes as they happen, with rich visual diffing.

**Implementation:**

1. **File changes panel** in Agent View or as a sidebar:
   ```
   Changes ───────────────────
   +  src/main.go     (12 +, 3 -)
   +  src/config.yml  (new)
   ~  README.md       (1 +, 1 -)
   ```

2. **Inline diff rendering** within code blocks:
   - Add `renderDiffBlock()` to `codeblock.go` that renders unified diff with:
     - Line numbers for old and new
     - Word-level highlight within changed lines (using background colors)
     - File header styling
   - Example:
     ```
     ┌─ src/main.go ─────────────────┐
     │  12 │  12 │  old line          │
     │  13 │     │ - removed line     │  ← red bg
     │     │  13 │ + new line here    │  ← green bg
     │  14 │  14 │  unchanged         │
     └────────────────────────────────┘
     ```

3. **Diff preview overlay** — when agent proposes changes, show a modal/overlay with the diff before user confirmation (if approval mode is on).

4. **Surface diff log in UI** — read the per-round diff log (`internal/output/diff.go`) and display it as a collapsible section in the final review screen or in the chat as a `MsgForge` message.

### P4: Running Screen Overhaul

**Goal:** Bring the running screen (`live.go`) up to the same standard as the chat UI.

**Problems with current running screen:**
- Two-pane split is primitive (`live.go:220`)
- Code blocks are folded into `[code: filename N lines]` — users can't see what's being written
- No plan or task progress visible
- No file changes tracking
- Status line is just text: `pass 1/4 correctness round 2/3 writer manual:off`

**Implementation:**

1. **Migrate running screen to use the chat UI components** — the chat UI already has better rendering, themes, and layout. The running screen should be a mode of the chat UI, not a separate Bubble Tea program.

2. **Add live file preview** in a third pane or sidebar showing files as they're modified:
   ```
   ┌──────────┬──────────┬──────────┐
   │ Writer   │ Auditor  │ Files    │
   │          │          │          │
   │          │          │ ■ main.go│
   │          │          │ □ util.go│
   │          │          │          │
   └──────────┴──────────┴──────────┘
   ```

3. **Show active plan steps** in the running screen status area or as a persistent overlay.

### P5: Progress Indicator Improvements

**Current live progress** (`chatmodel.go:5441-5455`):
- Single line with spinner: `⟳ running go test ./...`
- Disappears when empty

**Recommended improvements:**

1. **Stacked progress lines** — show up to 3 concurrent operations:
   ```
   ⟳ running go test ./...
   ○ waiting for model output
   ✓ read_file completed
   ```

2. **Progress bar for known-quantity work** — if a plan has N steps, show `███████░░░ 7/10` in the status footer.

3. **Tool call visualization** — when agent calls a tool, show a compact card:
   ```
   ┌────────────────────────┐
   │ read_file              │
   │ ./internal/tui/...     │
   │ 142 lines • 2.1K       │
   └────────────────────────┘
   ```

## Implementation Priority

| Priority | Feature | Effort | Impact |
|----------|---------|--------|--------|
| P1 | Plan sidebar with visual step states | Medium | High |
| P1 | Parse `[completed]`/`[in_progress]` tags | Low | High |
| P2 | Agent task badges in status header | Low | Medium |
| P2 | Rich task cards in Agent View | Medium | Medium |
| P3 | File changes panel | Medium | High |
| P3 | Enhanced diff block rendering | Medium | High |
| P4 | Merge running screen into chat UI | High | High |
| P5 | Stacked progress lines | Low | Medium |
| P5 | Tool call cards | Medium | Medium |

## Quick Wins (can be done in <1 day each)

1. **Color-code plan step states** — in `renderMessageContent`, detect `[completed]` / `[in_progress]` / `[blocked]` and apply colors from theme
2. **Add progress bar to plan message** — count total/completed steps and render a simple ASCII bar
3. **Add task count to status header** — `m.agentTasks` length in `buildStatusLine1`
4. **Show diff block line numbers** — enhance `renderCodeBlockBody` for `diff` lang to show old/new line numbers
5. **Don't fold code blocks in running screen** — in `foldForDisplay`, keep the first ~5 lines of code blocks visible instead of completely folding them

## Conclusion

The Forge frontend has good bones — Bubble Tea, Lipgloss, semantic highlighting, and a working chat UI. But it lacks the **information hierarchy** and **visual density** that modern tools provide. The biggest gap is that **work progress is text-based and ephemeral** rather than **visual and persistent**. Addressing the plan sidebar, task dashboard, and diff viewer would close most of the gap with OpenCode and Claude Code.
