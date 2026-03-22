# Chat Frontend Redesign Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Upgrade forge chat rendering with boxed tool calls, inline diffs, accent-bordered agent text, and token stats — for both console and live modes.

**Architecture:** Shared `internal/agent/format/` package defines structured types (`Span`, `Line`, `Style`) and formatting functions. Console mode converts to ANSI strings via `ToANSI()`. Live mode maps `Style` to `tcell.Style`. Diffs flow from tools via `LastDiff()` type assertion, not embedded in LLM-facing return values.

**Tech Stack:** Go, tcell (live mode), ANSI escape codes (console mode)

**Spec:** `docs/superpowers/specs/2026-03-22-chat-frontend-redesign.md`

## Current Status Update

This redesign has effectively been implemented in the live chat UI, and a follow-up performance pass has also been completed.

### Implemented
- boxed / structured tool rendering in the live chat surface
- accent-styled agent output rendering
- inline diff/result presentation with expansion support
- richer status/timeline/header treatment
- token/runtime stats surfacing
- copy helpers, theme toggles, footer legend, and other usability polish from the follow-up chat UI work

### Performance work completed after rollout
A regression review found that the richer UI had introduced extra render cost, especially during long streaming sessions. The following fixes have now been applied in the live TUI:
- per-pane wrapped-line caches
- cached wrapped-line start metadata for selection/search/mouse interactions
- lightweight spinner-only repaint path instead of full-screen redraw on every spinner tick
- cached syntax-highlighted code-line rendering
- indexed search-highlight lookup by wrapped line number
- reduced string churn in hot token/tool-result event paths
- better event burst draining/coalescing to reduce redundant renders

### Current state
- the main redesign goals are complete
- the major live-mode performance regressions identified during follow-up testing have been addressed
- `go test ./internal/tui/...` is passing after the optimization work

### Remaining optional work
If another performance pass is needed later, the best remaining candidates are:
1. row-level pane body render caching
2. dirty-region rendering for non-spinner updates
3. frame-budgeted render coalescing under extreme streaming loads
4. chunked transcript storage for very long sessions

### New UX follow-up requested
A new live-chat usability pass is also desired on top of the redesign work:
- promote `/stats` from a transient flash message into a dedicated overlay/panel
- change `Esc` semantics so first `Esc` cancels/stops the active run and returns to command-ready idle state
- require a second `Esc` while already idle to exit the app
- add `@file` context selection with an overlay that searches repo/current-dir files as the user types
- support explicit full-path `@...` entries for direct context attachment
- persist selected context files in chat session snapshots if that can be done cleanly
- verify/fix the `/sessions` picker regression where only the last session is shown

This work should be treated as follow-up UX integration on the current live TUI rather than a separate redesign track.

---

## File Map

| File | Action | Responsibility |
|------|--------|---------------|
| `internal/agent/format/format.go` | Create | Style/Span/Line types, ToolBox, Diff, AgentLine, Stats, Approval, Truncate, ToANSI |
| `internal/agent/format/format_test.go` | Create | Tests for all format functions |
| `internal/agent/format/tcell.go` | Create | StyleToTcell() mapping |
| `internal/agent/render.go` | Modify | Update RenderTarget interface, rewrite Renderer methods, add lastExpandable |
| `internal/agent/event_render.go` | Modify | Update ToolResult/Stats signatures, emit new event fields |
| `internal/agent/agent.go` | Modify | Add timing, LastDiff() extraction, pass diff/isError to ToolResult |
| `internal/agent/tools/edit.go` | Modify | Add lastDiff field + LastDiff() method |
| `internal/agent/tools/write.go` | Modify | Add lastDiff field + LastDiff() method |
| `internal/tui/chatlive.go` | Modify | styleToCellStyle(), accent border, colored diffs in right pane, stats, /expand |
| `internal/llm/types.go` | Modify | Add EventStats kind, IsError/Content/Duration/Usage fields to Event |
| `cmd/forge/main.go` | Modify | Add /expand command to console REPL |

---

## Chunk 1: Format Package Foundation

### Task 1: Format Types and AgentLine

**Files:**
- Create: `internal/agent/format/format.go`
- Create: `internal/agent/format/format_test.go`

- [ ] **Step 1: Write tests for AgentLine and ToANSI**

```go
// internal/agent/format/format_test.go
package format

import (
    "strings"
    "testing"
)

func TestAgentLine(t *testing.T) {
    line := AgentLine("hello world")
    if len(line.Spans) != 2 {
        t.Fatalf("expected 2 spans, got %d", len(line.Spans))
    }
    if line.Spans[0].Style != StyleAccentBorder {
        t.Errorf("expected accent border style, got %v", line.Spans[0].Style)
    }
    if line.Spans[0].Text != " │ " {
        t.Errorf("expected ' │ ', got %q", line.Spans[0].Text)
    }
    if line.Spans[1].Text != "hello world" {
        t.Errorf("expected 'hello world', got %q", line.Spans[1].Text)
    }
}

func TestAgentLineEmpty(t *testing.T) {
    line := AgentLine("")
    if len(line.Spans) != 2 {
        t.Fatalf("expected 2 spans, got %d", len(line.Spans))
    }
    if line.Spans[1].Text != "" {
        t.Errorf("expected empty text, got %q", line.Spans[1].Text)
    }
}

func TestLineToANSI(t *testing.T) {
    line := Line{Spans: []Span{
        {Text: "hello", Style: StyleNormal},
        {Text: " world", Style: StyleSuccess},
    }}
    result := LineToANSI(line, true)
    if !strings.Contains(result, "hello") {
        t.Error("missing hello")
    }
    if !strings.Contains(result, "world") {
        t.Error("missing world")
    }
    if !strings.Contains(result, "\033[32m") {
        t.Error("missing green ANSI code for success")
    }
}

func TestLineToANSINoColor(t *testing.T) {
    line := Line{Spans: []Span{
        {Text: "hello", Style: StyleSuccess},
    }}
    result := LineToANSI(line, false)
    if strings.Contains(result, "\033[") {
        t.Error("should not contain ANSI codes when colors disabled")
    }
    if result != "hello" {
        t.Errorf("expected 'hello', got %q", result)
    }
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/agent/format/ -v`
Expected: compilation error — package doesn't exist yet

- [ ] **Step 3: Implement types and AgentLine**

```go
// internal/agent/format/format.go
package format

import (
    "fmt"
    "os"
    "strings"
    "time"
)

type Style int

const (
    StyleNormal Style = iota
    StyleDim
    StyleBold
    StyleToolBlue
    StyleToolPurple
    StyleToolOrange
    StyleToolCyan
    StyleDiffAdd
    StyleDiffRemove
    StyleDiffHunk
    StyleAccentBorder
    StyleSuccess
    StyleError
    StyleWarning
    StyleStats
)

type Span struct {
    Text  string
    Style Style
}

type Line struct {
    Spans []Span
}

type ToolStatus int

const (
    StatusRunning ToolStatus = iota
    StatusSuccess
    StatusError
    StatusPending
)

func AgentLine(text string) Line {
    return Line{Spans: []Span{
        {Text: " │ ", Style: StyleAccentBorder},
        {Text: text, Style: StyleNormal},
    }}
}

// use256Color returns true if terminal supports 256 colors.
func use256Color() bool {
    if ct := os.Getenv("COLORTERM"); ct == "truecolor" || ct == "24bit" {
        return true
    }
    return strings.Contains(os.Getenv("TERM"), "256color")
}

// styleToANSI returns the ANSI escape code for a style.
func styleToANSI(s Style) string {
    ext := use256Color()
    switch s {
    case StyleDim:
        return "\033[2m"
    case StyleBold:
        return "\033[1m"
    case StyleToolBlue:
        if ext {
            return "\033[38;5;75m"
        }
        return "\033[34m"
    case StyleToolPurple:
        if ext {
            return "\033[38;5;141m"
        }
        return "\033[35m"
    case StyleToolOrange:
        if ext {
            return "\033[38;5;215m"
        }
        return "\033[33m"
    case StyleToolCyan:
        if ext {
            return "\033[38;5;110m"
        }
        return "\033[36m"
    case StyleDiffAdd:
        if ext {
            return "\033[32m\033[48;5;22m"
        }
        return "\033[32m"
    case StyleDiffRemove:
        if ext {
            return "\033[31m\033[48;5;52m"
        }
        return "\033[31m"
    case StyleDiffHunk:
        return "\033[2m"
    case StyleAccentBorder:
        if ext {
            return "\033[38;5;75m"
        }
        return "\033[34m"
    case StyleSuccess:
        return "\033[32m"
    case StyleError:
        return "\033[31m"
    case StyleWarning:
        return "\033[33m"
    case StyleStats:
        return "\033[2m"
    default:
        return ""
    }
}

const ansiReset = "\033[0m"

func LineToANSI(line Line, colors bool) string {
    var sb strings.Builder
    for _, span := range line.Spans {
        if colors {
            code := styleToANSI(span.Style)
            if code != "" {
                sb.WriteString(code)
                sb.WriteString(span.Text)
                sb.WriteString(ansiReset)
                continue
            }
        }
        sb.WriteString(span.Text)
    }
    return sb.String()
}

func ToANSI(lines []Line, colors bool) string {
    var sb strings.Builder
    for i, line := range lines {
        if i > 0 {
            sb.WriteByte('\n')
        }
        sb.WriteString(LineToANSI(line, colors))
    }
    return sb.String()
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/agent/format/ -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/agent/format/
git commit -m "feat: add format package with Style types, AgentLine, and ToANSI"
```

---

### Task 2: ToolBox and Diff Formatters

**Files:**
- Modify: `internal/agent/format/format.go`
- Modify: `internal/agent/format/format_test.go`

- [ ] **Step 1: Write tests for Diff**

```go
func TestDiff(t *testing.T) {
    diff := "--- a/foo.go\n+++ b/foo.go\n-old line\n+new line\n same line\n"
    lines := Diff(diff)
    if len(lines) == 0 {
        t.Fatal("expected lines")
    }
    // Check first line is hunk/header style
    foundAdd := false
    foundRemove := false
    for _, line := range lines {
        for _, span := range line.Spans {
            if span.Style == StyleDiffAdd {
                foundAdd = true
            }
            if span.Style == StyleDiffRemove {
                foundRemove = true
            }
        }
    }
    if !foundAdd {
        t.Error("expected StyleDiffAdd span")
    }
    if !foundRemove {
        t.Error("expected StyleDiffRemove span")
    }
}
```

- [ ] **Step 2: Write tests for ToolBox**

```go
func TestToolBoxCompact(t *testing.T) {
    lines := ToolBox("read_file", "main.go", "", StatusSuccess, 60)
    ansi := ToANSI(lines, false)
    if !strings.Contains(ansi, "read_file") {
        t.Error("missing tool name")
    }
    if !strings.Contains(ansi, "main.go") {
        t.Error("missing summary")
    }
    if !strings.Contains(ansi, "┌") {
        t.Error("missing top border")
    }
    if !strings.Contains(ansi, "└") {
        t.Error("missing bottom border")
    }
}

func TestToolBoxExpanded(t *testing.T) {
    diff := "-old\n+new\n"
    lines := ToolBox("edit_file", "main.go", diff, StatusSuccess, 60)
    ansi := ToANSI(lines, false)
    if !strings.Contains(ansi, "edit_file") {
        t.Error("missing tool name")
    }
    if !strings.Contains(ansi, "old") {
        t.Error("missing diff content")
    }
}

func TestToolBoxRunCommandFail(t *testing.T) {
    lines := ToolBox("run_command", "go test", "FAIL: TestFoo", StatusError, 60)
    ansi := ToANSI(lines, false)
    if !strings.Contains(ansi, "✗") {
        t.Error("missing error indicator")
    }
}
```

- [ ] **Step 3: Write tests for Truncate, Stats, Approval**

```go
func TestTruncate(t *testing.T) {
    input := "line1\nline2\nline3\nline4\nline5\n"
    truncated, wasTruncated := Truncate(input, 3)
    if !wasTruncated {
        t.Error("expected truncation")
    }
    if !strings.Contains(truncated, "line1") {
        t.Error("missing line1")
    }
    if !strings.Contains(truncated, "/expand") {
        t.Error("missing /expand hint")
    }
}

func TestTruncateNoOp(t *testing.T) {
    input := "line1\nline2\n"
    truncated, wasTruncated := Truncate(input, 5)
    if wasTruncated {
        t.Error("should not truncate")
    }
    if truncated != input {
        t.Errorf("expected unchanged input")
    }
}

func TestStats(t *testing.T) {
    line := Stats(2300*time.Millisecond, 1200, 340)
    ansi := LineToANSI(line, false)
    if !strings.Contains(ansi, "2.3s") {
        t.Errorf("missing duration, got %q", ansi)
    }
    if !strings.Contains(ansi, "1.2k") {
        t.Error("missing input tokens")
    }
}

func TestStatsNoTokens(t *testing.T) {
    line := Stats(5*time.Second, 0, 0)
    ansi := LineToANSI(line, false)
    if !strings.Contains(ansi, "5.0s") {
        t.Error("missing duration")
    }
    if strings.Contains(ansi, "↑") {
        t.Error("should not show tokens when zero")
    }
}
```

- [ ] **Step 4: Run tests to verify they fail**

Run: `go test ./internal/agent/format/ -v`
Expected: FAIL — functions not defined

- [ ] **Step 5: Implement Diff, ToolBox, Truncate, Stats, Approval**

Add to `internal/agent/format/format.go`:

```go
// toolStyle returns the accent color style for a tool name.
func toolStyle(name string) Style {
    switch name {
    case "edit_file":
        return StyleToolPurple
    case "write_file":
        return StyleToolOrange
    case "run_command":
        return StyleToolCyan
    default:
        return StyleToolBlue
    }
}

// isExpandedTool returns true if the tool should show detail content.
func isExpandedTool(name string) bool {
    return name == "edit_file" || name == "write_file"
}

func Diff(raw string) []Line {
    var lines []Line
    for _, s := range strings.Split(raw, "\n") {
        if s == "" {
            continue
        }
        switch {
        case strings.HasPrefix(s, "---") || strings.HasPrefix(s, "+++"):
            lines = append(lines, Line{Spans: []Span{{Text: "  " + s, Style: StyleDiffHunk}}})
        case strings.HasPrefix(s, "@@"):
            lines = append(lines, Line{Spans: []Span{{Text: "  " + s, Style: StyleDiffHunk}}})
        case strings.HasPrefix(s, "+"):
            lines = append(lines, Line{Spans: []Span{{Text: "  " + s, Style: StyleDiffAdd}}})
        case strings.HasPrefix(s, "-"):
            lines = append(lines, Line{Spans: []Span{{Text: "  " + s, Style: StyleDiffRemove}}})
        default:
            lines = append(lines, Line{Spans: []Span{{Text: "  " + s, Style: StyleDim}}})
        }
    }
    return lines
}

func ToolBox(name, summary, detail string, status ToolStatus, width int) []Line {
    if width < 20 {
        width = 20
    }
    innerW := width - 4 // account for "│ " and " │"
    ts := toolStyle(name)

    statusText := ""
    statusStyle := StyleSuccess
    switch status {
    case StatusSuccess:
        statusText = "✓"
    case StatusError:
        statusText = "✗"
        statusStyle = StyleError
    case StatusRunning:
        statusText = "..."
        statusStyle = StyleDim
    case StatusPending:
        statusText = "?"
        statusStyle = StyleWarning
    }

    // Top border
    var lines []Line
    topBar := "┌" + strings.Repeat("─", width-2) + "┐"
    lines = append(lines, Line{Spans: []Span{{Text: topBar, Style: StyleDim}}})

    // Header line: │ ● tool_name  summary     status │
    headerParts := []Span{
        {Text: "│ ", Style: StyleDim},
        {Text: "● ", Style: ts},
        {Text: name, Style: ts},
    }
    if summary != "" {
        headerParts = append(headerParts, Span{Text: "  " + summary, Style: StyleDim})
    }
    if statusText != "" {
        // Calculate padding
        usedLen := 2 + 2 + len(name)
        if summary != "" {
            usedLen += 2 + len(summary)
        }
        statusLen := 1 + len(statusText)
        pad := innerW - usedLen - statusLen
        if pad < 1 {
            pad = 1
        }
        headerParts = append(headerParts, Span{Text: strings.Repeat(" ", pad), Style: StyleNormal})
        headerParts = append(headerParts, Span{Text: statusText, Style: statusStyle})
    }
    headerParts = append(headerParts, Span{Text: " │", Style: StyleDim})
    lines = append(lines, Line{Spans: headerParts})

    // Detail content (for expanded tools or failed commands)
    if detail != "" && (isExpandedTool(name) || status == StatusError) {
        diffLines := Diff(detail)
        for _, dl := range diffLines {
            row := []Span{{Text: "│", Style: StyleDim}}
            row = append(row, dl.Spans...)
            padLen := width - 2 - spanTextLen(dl.Spans)
            if padLen > 0 {
                row = append(row, Span{Text: strings.Repeat(" ", padLen), Style: StyleNormal})
            }
            row = append(row, Span{Text: "│", Style: StyleDim})
            lines = append(lines, Line{Spans: row})
        }
    }

    // Bottom border
    botBar := "└" + strings.Repeat("─", width-2) + "┘"
    lines = append(lines, Line{Spans: []Span{{Text: botBar, Style: StyleDim}}})

    return lines
}

func spanTextLen(spans []Span) int {
    n := 0
    for _, s := range spans {
        n += len(s.Text)
    }
    return n
}

func Truncate(output string, maxLines int) (string, bool) {
    lines := strings.Split(output, "\n")
    if len(lines) <= maxLines {
        return output, false
    }
    truncated := strings.Join(lines[:maxLines], "\n")
    remaining := len(lines) - maxLines
    truncated += fmt.Sprintf("\n... (%d more lines, /expand to show)", remaining)
    return truncated, true
}

func Stats(duration time.Duration, inputTokens, outputTokens int) Line {
    s := fmt.Sprintf(" ⏱ %.1fs", duration.Seconds())
    if inputTokens > 0 || outputTokens > 0 {
        s += fmt.Sprintf(" · ↑%s ↓%s tokens", formatCount(inputTokens), formatCount(outputTokens))
    }
    return Line{Spans: []Span{{Text: s, Style: StyleStats}}}
}

func formatCount(n int) string {
    if n >= 1000 {
        return fmt.Sprintf("%.1fk", float64(n)/1000)
    }
    return fmt.Sprintf("%d", n)
}

func Approval(action, path string) Line {
    return Line{Spans: []Span{
        {Text: " " + action + "?", Style: StyleWarning},
        {Text: " [y]es", Style: StyleSuccess},
        {Text: " [n]o", Style: StyleError},
    }}
}
```

- [ ] **Step 6: Run tests to verify they pass**

Run: `go test ./internal/agent/format/ -v`
Expected: PASS

- [ ] **Step 7: Verify build**

Run: `go build ./...`
Expected: clean build

- [ ] **Step 8: Commit**

```bash
git add internal/agent/format/
git commit -m "feat: add ToolBox, Diff, Truncate, Stats, Approval formatters"
```

---

## Chunk 2: Wire Format Into Renderers

### Task 3: Update Event Types

**Files:**
- Modify: `internal/llm/types.go` (lines 51-93)

- [ ] **Step 1: Add new event kind and fields**

Add `EventStats` to the EventKind constants (after line 66):

```go
EventStats      EventKind = "stats"
```

Add fields to the Event struct (after line 92):

```go
IsError  bool
Content  string          // full tool output (for /expand), diff content
Duration time.Duration   // turn duration (EventStats)
Usage    Usage           // token usage (EventStats)
```

- [ ] **Step 2: Verify build**

Run: `go build ./...`
Expected: clean build (new fields have zero values, existing code unaffected)

- [ ] **Step 3: Commit**

```bash
git add internal/llm/types.go
git commit -m "feat: add EventStats kind and IsError/Content/Duration/Usage fields to Event"
```

---

### Task 4: Update RenderTarget Interface and Renderer

**Files:**
- Modify: `internal/agent/render.go` (lines 19-125)

- [ ] **Step 1: Update RenderTarget interface**

Replace the interface at lines 19-26:

```go
type RenderTarget interface {
    AgentToken(text string)
    AgentText(text string)
    ToolCall(name, summary string)
    ToolResult(name, output, diff string, isError bool)
    Stats(duration time.Duration, usage llm.Usage)
    Error(msg string)
}
```

- [ ] **Step 2: Rewrite Renderer methods to use format package**

Replace the Renderer struct and methods. Key changes:
- Add `lastExpandable string` field and `LastExpandable() string` accessor
- `AgentToken()` calls `format.AgentLine()` + `format.LineToANSI()`
- `ToolCall()` renders compact box header via `format.ToolBox()` with StatusRunning
- `ToolResult()` renders full box. If diff is non-empty, uses expanded box with `format.Diff()`. Calls `format.Truncate()` and stores full output in `lastExpandable` if truncated.
- `Stats()` calls `format.Stats()` + `format.LineToANSI()`
- Remove old `Diff()`, `Info()`, `Done()` methods (not in interface, only used by old code)
- Keep `Prompt()` method (called directly on concrete type)

```go
import (
    "fmt"
    "io"
    "time"

    "forge/internal/agent/format"
    "forge/internal/llm"
)

type Renderer struct {
    out            io.Writer
    width          int
    colors         bool
    lastExpandable string
}

func NewRenderer(out io.Writer, width int, colors bool) *Renderer {
    return &Renderer{out: out, width: width, colors: colors}
}

func (r *Renderer) AgentToken(text string) {
    // Stream each line with accent border
    lines := strings.Split(text, "\n")
    for i, line := range lines {
        if i < len(lines)-1 {
            // Complete line — render with border
            styled := format.AgentLine(line)
            fmt.Fprintln(r.out, format.LineToANSI(styled, r.colors))
        } else if line != "" {
            // Partial line (no newline yet) — buffer with border
            styled := format.AgentLine(line)
            fmt.Fprint(r.out, format.LineToANSI(styled, r.colors))
        }
    }
}

func (r *Renderer) AgentText(text string) {
    r.AgentToken(text)
}

func (r *Renderer) ToolCall(name, summary string) {
    // Just a newline separator before tool boxes
    fmt.Fprintln(r.out)
}

func (r *Renderer) ToolResult(name, output, diff string, isError bool) {
    status := format.StatusSuccess
    if isError {
        status = format.StatusError
    }

    detail := diff
    if detail == "" && isError {
        detail = output
    }

    // Truncate detail for display, store full for /expand
    displayDetail := detail
    if detail != "" {
        truncated, wasTruncated := format.Truncate(detail, 20)
        if wasTruncated {
            r.lastExpandable = detail
            displayDetail = truncated
        }
    }

    lines := format.ToolBox(name, output, displayDetail, status, r.width)
    fmt.Fprintln(r.out, format.ToANSI(lines, r.colors))
}

func (r *Renderer) Stats(duration time.Duration, usage llm.Usage) {
    line := format.Stats(duration, usage.InputTokens, usage.OutputTokens)
    fmt.Fprintln(r.out, format.LineToANSI(line, r.colors))
}

func (r *Renderer) Error(msg string) {
    line := format.Line{Spans: []format.Span{
        {Text: " ✗ " + msg, Style: format.StyleError},
    }}
    fmt.Fprintln(r.out, format.LineToANSI(line, r.colors))
}

func (r *Renderer) LastExpandable() string { return r.lastExpandable }
func (r *Renderer) ClearExpandable()       { r.lastExpandable = "" }

func (r *Renderer) Prompt() {
    if r.colors {
        fmt.Fprintf(r.out, "\n\033[1;32mforge>\033[0m ")
    } else {
        fmt.Fprint(r.out, "\nforge> ")
    }
}
```

- [ ] **Step 3: Verify build**

Run: `go build ./...`
Expected: compile errors in agent.go and event_render.go (ToolResult signature changed). This is expected — we fix them in the next tasks.

- [ ] **Step 4: Commit (even with known downstream breakage)**

```bash
git add internal/agent/render.go
git commit -m "feat: rewrite Renderer to use format package with boxed tool calls"
```

---

### Task 5: Update EventRenderer

**Files:**
- Modify: `internal/agent/event_render.go` (lines 1-79)

- [ ] **Step 1: Update ToolResult, add Stats, fix LiveApproval**

```go
func (r *EventRenderer) ToolResult(name, output, diff string, isError bool) {
    r.events <- llm.Event{
        Kind:    llm.EventToolResult,
        Agent:   name,
        Text:    output,
        Content: diff,
        IsError: isError,
    }
}

func (r *EventRenderer) Stats(duration time.Duration, usage llm.Usage) {
    r.events <- llm.Event{
        Kind:     llm.EventStats,
        Duration: duration,
        Usage:    usage,
    }
}
```

Update `LiveApproval()` — the inner call to `r.events` for tool result needs to match:

```go
func (r *EventRenderer) LiveApproval() tools.ApprovalFunc {
    return func(action tools.Action) (bool, error) {
        r.events <- llm.Event{
            Kind:  llm.EventToolCall,
            Agent: action.Tool,
            Text:  fmt.Sprintf("[approval needed] %s", action.Summary),
        }
        r.approvalCh <- action
        approved := <-r.responseCh
        if approved {
            r.events <- llm.Event{Kind: llm.EventToolResult, Agent: action.Tool, Text: "approved"}
        } else {
            r.events <- llm.Event{Kind: llm.EventToolResult, Agent: action.Tool, Text: "denied"}
        }
        return approved, nil
    }
}
```

Add import for `time` and `llm`.

- [ ] **Step 2: Verify build**

Run: `go build ./...`
Expected: compile errors in agent.go only (ToolResult call site). Expected — fixed next.

- [ ] **Step 3: Commit**

```bash
git add internal/agent/event_render.go
git commit -m "feat: update EventRenderer for new ToolResult signature and Stats"
```

---

### Task 6: Update Agent Loop (timing, diff, isError)

**Files:**
- Modify: `internal/agent/agent.go` (lines 43-161)
- Modify: `internal/agent/tools/edit.go`
- Modify: `internal/agent/tools/write.go`

- [ ] **Step 1: Add LastDiff() to edit and write tools**

In `internal/agent/tools/edit.go`, the tool is created via `NewEditFile()` which returns a `Tool` struct with an `Execute` closure. We need to capture the diff. Wrap the closure to store it:

Add to `internal/agent/tools/edit.go` — modify `NewEditFile` to return a `*EditFileTool` that implements both `Tool`-like behavior and `LastDiff()`:

Actually, since the tools use the `Tool` struct pattern (not a custom type), the simplest approach is to add a `DiffCapture` wrapper. But even simpler: store the diff on a package-level isn't right either.

The cleanest approach: add a `lastDiff` field accessible via closure. Create a small wrapper:

In `internal/agent/tools/edit.go`, change the return to wrap the Tool with a diffCapture:

```go
// At the top of edit.go, add a type:
type diffCapturingTool struct {
    Tool
    lastDiff string
}

func (t *diffCapturingTool) LastDiff() string {
    d := t.lastDiff
    t.lastDiff = ""
    return d
}
```

Then in `NewEditFile()`, create a `*diffCapturingTool`, and in the Execute closure, set `dt.lastDiff = diff` before returning.

Same pattern in `write.go`.

- [ ] **Step 2: Update agent.go tool execution loop**

In `agent.go`, after `tool.Execute()` (around line 139), add LastDiff extraction and timing:

```go
// At top of Run():
turnStart := time.Now()

// Add defer for stats:
defer func() {
    duration := time.Since(turnStart)
    a.renderer.Stats(duration, a.getUsage())
}()
```

In the tool execution loop (around line 139):

```go
result, err := tool.Execute(ctx, call.Args)

var diff string
if ld, ok := tool.(interface{ LastDiff() string }); ok {
    diff = ld.LastDiff()
}

if err != nil {
    a.renderer.ToolResult(call.Name, fmt.Sprintf("error: %v", err), diff, true)
} else {
    a.renderer.ToolResult(call.Name, truncateResult(result), diff, false)
}
```

Add `getUsage()` method:

```go
func (a *Agent) getUsage() llm.Usage {
    if ur, ok := a.driver.(llm.UsageReporter); ok {
        return ur.LastUsage()
    }
    return llm.Usage{}
}
```

- [ ] **Step 3: Update AgentToken streaming to use format.AgentLine**

In agent.go, the streaming filter (around line 94) currently calls `a.renderer.AgentToken(line + "\n")`. This stays as-is — the `Renderer.AgentToken()` method now internally applies the accent border via `format.AgentLine()`.

No change needed here — the Renderer handles it.

- [ ] **Step 4: Verify full build**

Run: `go build ./...`
Expected: clean build

- [ ] **Step 5: Run all tests**

Run: `go test ./...`
Expected: all pass

- [ ] **Step 6: Commit**

```bash
git add internal/agent/agent.go internal/agent/tools/edit.go internal/agent/tools/write.go
git commit -m "feat: wire diff capture and timing into agent loop"
```

---

## Chunk 3: Live Mode + /expand

### Task 7: tcell Style Mapping

**Files:**
- Create: `internal/agent/format/tcell.go`

- [ ] **Step 1: Create StyleToTcell mapping**

```go
package format

import "github.com/gdamore/tcell/v2"

func StyleToTcell(s Style) tcell.Style {
    base := tcell.StyleDefault
    switch s {
    case StyleDim:
        return base.Foreground(tcell.GetColor("#8b949e"))
    case StyleBold:
        return base.Bold(true).Foreground(tcell.GetColor("#f0f6fc"))
    case StyleToolBlue:
        return base.Foreground(tcell.GetColor("#58a6ff"))
    case StyleToolPurple:
        return base.Foreground(tcell.GetColor("#d2a8ff"))
    case StyleToolOrange:
        return base.Foreground(tcell.GetColor("#f0883e"))
    case StyleToolCyan:
        return base.Foreground(tcell.GetColor("#79c0ff"))
    case StyleDiffAdd:
        return base.Foreground(tcell.GetColor("#56d364")).Background(tcell.GetColor("#0f2d16"))
    case StyleDiffRemove:
        return base.Foreground(tcell.GetColor("#f85149")).Background(tcell.GetColor("#3d1117"))
    case StyleDiffHunk:
        return base.Foreground(tcell.GetColor("#8b949e"))
    case StyleAccentBorder:
        return base.Foreground(tcell.GetColor("#58a6ff"))
    case StyleSuccess:
        return base.Foreground(tcell.GetColor("#56d364"))
    case StyleError:
        return base.Foreground(tcell.GetColor("#f85149"))
    case StyleWarning:
        return base.Foreground(tcell.GetColor("#e3b341"))
    case StyleStats:
        return base.Foreground(tcell.GetColor("#8b949e"))
    default:
        return base.Foreground(tcell.GetColor("#f0f6fc"))
    }
}
```

- [ ] **Step 2: Verify build**

Run: `go build ./...`
Expected: clean build

- [ ] **Step 3: Commit**

```bash
git add internal/agent/format/tcell.go
git commit -m "feat: add tcell style mapping for live mode"
```

---

### Task 8: Update chatlive.go

**Files:**
- Modify: `internal/tui/chatlive.go` (handleEvent, render, handleSlashCommand)

- [ ] **Step 1: Add new fields to chatLiveModel**

Add to the struct (around line 28):

```go
lastExpandable string
statsDuration  time.Duration
statsUsage     llm.Usage
```

- [ ] **Step 2: Update handleEvent for new event types**

Replace `handleEvent` (lines 373-395):

```go
func (m *chatLiveModel) handleEvent(ev llm.Event) {
    switch ev.Kind {
    case llm.EventToken:
        // Add accent border to agent text
        lines := strings.Split(ev.Text, "\n")
        for i, line := range lines {
            if i < len(lines)-1 {
                m.agentBuf += " │ " + line + "\n"
            } else if line != "" {
                m.agentBuf += " │ " + line
            }
        }
        m.agentScrl = m.agentMaxScroll()

    case llm.EventToolCall:
        ts := toolIcon(ev.Agent)
        m.toolsBuf += fmt.Sprintf("%s %s %s\n", ts, ev.Agent, ev.Text)
        m.toolsScrl = m.toolsMaxScroll()

    case llm.EventToolResult:
        if ev.IsError {
            m.toolsBuf += fmt.Sprintf("  ✗ %s\n", ev.Text)
        } else {
            // Show diff if available
            if ev.Content != "" {
                diffLines := strings.Split(ev.Content, "\n")
                for _, dl := range diffLines {
                    if strings.HasPrefix(dl, "+") {
                        m.toolsBuf += fmt.Sprintf("  %s\n", dl)
                    } else if strings.HasPrefix(dl, "-") {
                        m.toolsBuf += fmt.Sprintf("  %s\n", dl)
                    }
                }
                truncated, wasTruncated := format.Truncate(ev.Content, 10)
                if wasTruncated {
                    m.lastExpandable = ev.Content
                    // Show truncated version
                    m.toolsBuf += fmt.Sprintf("  ... /expand for full diff\n")
                }
            }
            m.toolsBuf += fmt.Sprintf("  ✓ %s\n", ev.Text)
        }
        m.toolsScrl = m.toolsMaxScroll()

    case llm.EventError:
        m.toolsBuf += fmt.Sprintf("  ✗ %s\n", ev.Text)
        m.toolsScrl = m.toolsMaxScroll()

    case llm.EventStats:
        m.statsDuration = ev.Duration
        m.statsUsage = ev.Usage

    case llm.EventDone:
        m.busy = false
        m.status = "ready"
    }
}

func toolIcon(name string) string {
    switch name {
    case "edit_file":
        return "●"
    case "write_file":
        return "●"
    case "run_command":
        return "●"
    default:
        return "●"
    }
}
```

- [ ] **Step 3: Update status line in render() to show stats**

In the `render()` method (around line 418), update the status text to include stats when available:

```go
statusText := fmt.Sprintf(" forge chat (%s) — %s  turn %d  [%s]", m.model, shortPath(m.workDir), m.turn, m.status)
if m.statsDuration > 0 && !m.busy {
    stats := fmt.Sprintf("  ⏱ %.1fs", m.statsDuration.Seconds())
    if m.statsUsage.InputTokens > 0 {
        stats += fmt.Sprintf(" ↑%d ↓%d", m.statsUsage.InputTokens, m.statsUsage.OutputTokens)
    }
    statusText += stats
}
```

- [ ] **Step 4: Add /expand to slash commands**

In `handleSlashCommand()` (around line 252), add before the default case:

```go
case input == "/expand":
    if m.lastExpandable != "" {
        m.toolsBuf += "\n" + m.lastExpandable + "\n"
        m.toolsScrl = m.toolsMaxScroll()
        m.lastExpandable = ""
        m.flash = "expanded"
    } else {
        m.flash = "nothing to expand"
    }
```

Update the `/help` flash to include `/expand`.

- [ ] **Step 5: Clear lastExpandable on new input**

In `handleKey` where input is submitted (around line 206), add:

```go
m.lastExpandable = ""
m.statsDuration = 0
m.statsUsage = llm.Usage{}
```

- [ ] **Step 6: Add format import**

Add `"forge/internal/agent/format"` to imports. Also add `"forge/internal/llm"` if not already present.

- [ ] **Step 7: Verify build**

Run: `go build ./...`
Expected: clean build

- [ ] **Step 8: Run all tests**

Run: `go test ./...`
Expected: all pass

- [ ] **Step 9: Commit**

```bash
git add internal/tui/chatlive.go
git commit -m "feat: update live mode with accent borders, colored diffs, stats, /expand"
```

---

### Task 9: Add /expand to Console REPL

**Files:**
- Modify: `cmd/forge/main.go` (runChatConsole, lines 1151-1206)

- [ ] **Step 1: Add /expand handler**

In the slash command switch in `runChatConsole()` (around line 1151), add:

```go
case input == "/expand":
    if exp := renderer.LastExpandable(); exp != "" {
        fmt.Fprintln(os.Stdout, exp)
        renderer.ClearExpandable()
    } else {
        renderer.Error("nothing to expand")
    }
```

- [ ] **Step 2: Update /help text**

Update `printChatHelp()` to include `/expand`.

- [ ] **Step 3: Verify build**

Run: `go build ./...`
Expected: clean build

- [ ] **Step 4: Run all tests**

Run: `go test ./...`
Expected: all pass

- [ ] **Step 5: Commit**

```bash
git add cmd/forge/main.go
git commit -m "feat: add /expand command to console REPL"
```

---

### Task 10: Final Build Verification

- [ ] **Step 1: Full test suite**

Run: `go test ./...`
Expected: all pass

- [ ] **Step 2: Build binary**

Run: `go build -o forge ./cmd/forge`
Expected: clean build

- [ ] **Step 3: Quick smoke test**

Run: `./forge chat --help` or verify chat mode launches

- [ ] **Step 4: Final commit if any cleanup needed**

```bash
git add -A
git commit -m "chore: final cleanup for chat frontend redesign"
```
