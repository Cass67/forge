# Chat Frontend Redesign

Upgrade the forge chat rendering in both console and live (split-pane) modes to provide rich, structured output with boxed tool calls, inline diffs, accent-bordered agent text, and token stats.

## Goals

- Visual hierarchy that makes tool calls, diffs, and agent reasoning instantly distinguishable
- Shared formatting logic so both console and live modes benefit from the same improvements
- Smart tool result visibility: reads/lists compact, writes/edits show full diffs
- `/expand` command to show full tool output on demand
- Real-time streaming with accent border on agent text

## Architecture: Shared Format Package

Create `internal/agent/format/` with a dual-output design. The package defines structured types that both renderers consume — console mode converts to ANSI strings, live mode converts to tcell styles.

### Structured Types

```go
package format

// Style represents a semantic text style.
type Style int

const (
    StyleNormal Style = iota
    StyleDim
    StyleBold
    StyleToolBlue     // read_file, list_dir, search
    StyleToolPurple   // edit_file
    StyleToolOrange   // write_file
    StyleToolCyan     // run_command
    StyleDiffAdd      // green text + green bg tint
    StyleDiffRemove   // red text + red bg tint
    StyleDiffHunk     // dim hunk header
    StyleAccentBorder // blue left border character
    StyleSuccess      // green
    StyleError        // red
    StyleWarning      // yellow
    StyleStats        // dim
)

type Span struct {
    Text  string
    Style Style
}

type Line struct {
    Spans []Span
}

// ToolStatus for tool result state.
type ToolStatus int

const (
    StatusRunning ToolStatus = iota
    StatusSuccess
    StatusError
    StatusPending
)
```

### Functions

All functions return `[]Line` (structured) so both renderers can consume them:

```go
// ToolBox renders a bordered tool call box.
// Compact tools (read_file, list_dir, search) get a single-line box.
// Expanded tools (edit_file, write_file) get a multi-line box with diff/preview.
// width is the terminal width for proper box sizing.
func ToolBox(name, summary, detail string, status ToolStatus, width int) []Line

// Diff renders a unified diff with hunk headers and colored lines.
// This is the display formatter. It takes raw diff text (as produced by
// simpleDiff() in the tools package) and converts it to styled lines.
func Diff(diff string) []Line

// AgentLine renders a single line with left accent border (blue │).
func AgentLine(line string) Line

// Stats renders duration and token usage on one dim line.
func Stats(duration time.Duration, inputTokens, outputTokens int) Line

// Approval renders the inline approve/deny prompt.
func Approval(action, path string) Line

// Truncate truncates output to maxLines and appends a "show more" hint.
// Returns the truncated string and whether truncation occurred.
// The caller stores the original string if wasTruncated is true.
func Truncate(output string, maxLines int) (truncated string, wasTruncated bool)

// ToANSI converts structured lines to an ANSI-escaped string for console output.
func ToANSI(lines []Line) string

// LineToANSI converts a single structured line to an ANSI-escaped string.
func LineToANSI(line Line) string
```

### Renderer Integration

**Console mode (`Renderer`):** Calls format functions, then `ToANSI()` to get printable strings. Writes to stdout.

**Live mode (`chatlive.go`):** Calls format functions, then maps `Style` enum values to `tcell.Style` objects for direct screen rendering. A helper function `styleToCellStyle(s format.Style) tcell.Style` lives in `chatlive.go`.

This way the formatting logic (what to show, how to structure it) is shared, while the rendering backend (ANSI vs. tcell) is separate.

## Diff Data Flow

Diffs are generated at tool execution time but kept separate from the LLM-facing return value to avoid polluting the context window.

### How it works

1. `edit_file` and `write_file` tools already compute diffs internally (via `simpleDiff()` in `internal/agent/tools/write.go`). Currently the diff is only passed through `Action.Detail` for the approval flow.

2. The tool `Execute()` return value (which goes into LLM history) stays clean: `"edited <path>"` / `"wrote N bytes to <path>"`.

3. The diff is passed to the renderer through a separate parameter on `RenderTarget.ToolResult`. The agent loop in `agent.go` extracts the diff from the tool execution context and passes it through.

### Implementation

The `RenderTarget` interface gains a `diff` parameter:

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

The tool `Execute()` method returns two values as it does today — `(result string, err error)`. The diff is obtained separately. For edit_file and write_file, the agent loop captures the diff by:

1. Adding a `LastDiff() string` method to the tool interface (optional, checked via type assertion).
2. After `Execute()`, the agent checks if the tool has `LastDiff()` and passes it to `ToolResult()`.

```go
// In agent.go tool execution loop:
result, err := tool.Execute(ctx, call.Args)
var diff string
if ld, ok := tool.(interface{ LastDiff() string }); ok {
    diff = ld.LastDiff()
}
a.renderer.ToolResult(call.Name, truncateResult(result), diff, err != nil)
```

Note: `simpleDiff()` (in `internal/agent/tools/write.go`) generates the raw diff text. `format.Diff()` (new, in the format package) converts that text into styled `[]Line` for display. These are separate concerns.

## Timing and Usage Data

### Tracking

The agent loop tracks timing with `turnStart := time.Now()` at the top of `Run()`. Stats are emitted on ALL exits — success, max-turns, and error:

```go
func (a *Agent) Run(ctx context.Context, userMessage string) error {
    turnStart := time.Now()
    defer func() {
        duration := time.Since(turnStart)
        a.renderer.Stats(duration, a.getUsage())
    }()
    // ... existing loop ...
}

func (a *Agent) getUsage() llm.Usage {
    if ur, ok := a.driver.(llm.UsageReporter); ok {
        return ur.LastUsage()
    }
    return llm.Usage{} // zero value — renderer shows duration only
}
```

### Live mode

The `EventRenderer` emits an `EventStats` event carrying duration and usage. The TUI displays it in the status bar.

### RenderTarget implementation notes

Both `Renderer` (console) and `EventRenderer` (live) must update:
- `ToolResult` signature changes from `(name, output string)` to `(name, output, diff string, isError bool)`
- `Stats` method is new
- `LiveApproval()` in `event_render.go` also calls `ToolResult` — must be updated to the new signature

## /expand Implementation

`lastExpandableOutput` is stored on the `Renderer` (console) and `chatLiveModel` (live), NOT on the Agent. The renderer is the one that calls `format.Truncate()`, so it naturally has access to the full output at the point of truncation.

### Console mode

- `Renderer` gains a `lastExpandable string` field
- When `ToolResult` truncates output (via `format.Truncate()`), it stores the full original in `lastExpandable`
- `Renderer.LastExpandable() string` accessor for the REPL to call on `/expand`
- Reset when the next `ToolCall` is received (start of new tool activity)

### Live mode

- `chatLiveModel` gains a `lastExpandable string` field
- When `handleEvent` processes a truncated `EventToolResult`, it stores the full content from `ev.Content`
- `/expand` slash command appends the stored content to `toolsBuf` and auto-scrolls
- Clears on next user input submission

## Color Palette

ANSI 256-color codes for modern terminals with basic 16-color fallback.

| Element | 256-color ANSI | 16-color Fallback |
|---------|---------------|-------------------|
| Agent text border | `\033[38;5;75m` (blue) | `\033[34m` (blue) |
| Agent text body | `\033[97m` (bright white) | `\033[97m` (bright white) |
| read/search/list | `\033[38;5;75m` (blue) | `\033[34m` (blue) |
| edit_file | `\033[38;5;141m` (purple) | `\033[35m` (magenta) |
| write_file | `\033[38;5;215m` (orange) | `\033[33m` (yellow) |
| run_command | `\033[38;5;110m` (cyan) | `\033[36m` (cyan) |
| Diff additions | `\033[32m` + bg `\033[48;5;22m` | `\033[32m` (green, no bg) |
| Diff removals | `\033[31m` + bg `\033[48;5;52m` | `\033[31m` (red, no bg) |
| Diff hunk header | `\033[2m` (dim) | `\033[2m` (dim) |
| Box border | `\033[2m` (dim) | `\033[2m` (dim) |
| Success | `\033[32m` (green) | `\033[32m` (green) |
| Error | `\033[31m` (red) | `\033[31m` (red) |
| Approval | `\033[33m` (yellow) | `\033[33m` (yellow) |
| Stats | `\033[2m` (dim) | `\033[2m` (dim) |

**Color detection:** Use 256-color if `COLORTERM` is `truecolor`/`24bit` OR `TERM` contains `256color`. Fall back to 16-color otherwise. Respect `NO_COLOR` env var — when set, strip all color codes.

**Tool icon:** Use `●` (U+25CF, filled circle) for tool call icons — consistent with existing codebase and reliable across terminal emulators.

## Console Mode Elements

### Agent Text (Streaming)

Each line of agent output gets a left blue border as it streams:

```
 │ I'll look at the parse function first, then add proper error handling.
```

The `│` character is blue. Text is bright white. Applied per-line during token streaming — the streaming filter in `agent.go` calls `format.AgentLine()` for each complete line.

### Compact Tool Box (read_file, list_dir, search, run_command success)

Single-line bordered box:

```
 ┌──────────────────────────────────────────────────────┐
 │ ● read_file  internal/parser.go             ✓ 128 lines │
 └──────────────────────────────────────────────────────┘
```

- Icon `●` and tool name in tool-specific color
- Path/args in dim
- Status on the right: `✓ N lines`, `✓ N matches`, `✓ N entries`, `✓ exit 0`
- Width adapts to terminal width (from `ToolBox` width parameter)

### Expanded Tool Box (edit_file, write_file, run_command failure)

Multi-line bordered box with inline diff:

```
 ┌──────────────────────────────────────────────────────┐
 │ ● edit_file  internal/parser.go                          │
 │                                                          │
 │  @@ -43,6 +43,10 @@                                     │
 │  func ParseToolCalls(text string) ([]ToolCall, string) { │
 │ -    calls := parse(text)                                 │
 │ +    calls, err := parse(text)                            │
 │ +    if err != nil {                                      │
 │ +        return nil, "", fmt.Errorf("parse: %w", err)     │
 │ +    }                                                    │
 │                                                          │
 │  apply changes? [y]es [n]o                               │
 └──────────────────────────────────────────────────────┘
```

- Diff lines: `+` green with green bg tint, `-` red with red bg tint
- Context lines in dim
- Approval prompt inside the box at the bottom
- write_file creating a new file: header shows `new file` in yellow
- Content truncated to 20 lines with `... (N more lines, /expand to show)`
- run_command on failure: shows stderr/stdout instead of diff, `✗ exit 1` in red

### Stats Line

After each agent turn completes:

```
 ⏱ 4.7s · ↑2.1k ↓890 tokens
```

Dim text. If driver doesn't implement `UsageReporter`, shows only duration: `⏱ 4.7s`

### /expand Command

Typing `/expand` in the REPL prints the last truncated tool result in full to stdout without a box. Resets when the next tool call starts.

## Live Mode (Split-Pane) Changes

### Left Pane (Agent)

Agent text streams with the same blue accent border, rendered via tcell:
- Blue `│` character at column 0 of each content line
- Text in bright white

### Right Pane (Tools)

Compact activity log with inline diffs for writes/edits:

```
 Tools
 ● read_file internal/parser.go ✓
 ● search "ParseToolCalls" ✓ 3
 ● edit_file internal/parser.go
   @@ -43,6 +43,10 @@
   - calls := parse(text)
   + calls, err := parse(text)
   + if err != nil {
   ... (4 more)
   ✓ applied
 ● run_command go test ... ✓
```

No box borders in the right pane (too narrow). Tool name in tool-specific color, diff lines colored. Compact but informative.

### /expand in Live Mode

`/expand` appends the full output to the right pane's `toolsBuf` below the truncated entry and auto-scrolls to show it. Clears on next user input submission.

### Status Bar

Add token stats to the status line after turn completes:

```
 forge chat (model) — ~/git/forge  turn 2  [ready]  ⏱ 4.7s ↑2.1k ↓890
```

## Event Changes

Add to `llm.Event` in `internal/llm/types.go`:

```go
// New event kind
EventStats EventKind = "stats"

// New fields on Event struct
IsError  bool          // true if tool result is an error
Content  string        // diff content, full command output (for /expand)
Duration time.Duration // turn duration (for EventStats)
Usage    Usage         // token usage (for EventStats)
```

Field is named `Content` (not `Detail`) to avoid collision with `tools.Action.Detail`.

Existing event kinds and their usage unchanged:
- `EventToolCall`: Agent field = tool name, Text field = summary (no change)
- `EventToolResult`: Agent field = tool name, Text field = display text, Content field = diff/full output, IsError = whether it failed
- `EventStats`: Duration + Usage fields populated

## Files Changed

| File | Change |
|------|--------|
| `internal/agent/format/format.go` | **New.** Structured types (Span, Line, Style, ToolStatus), formatting functions, ToANSI converter |
| `internal/agent/format/format_test.go` | **New.** Tests for all formatting functions |
| `internal/agent/format/tcell.go` | **New.** `StyleToTcell(s Style) tcell.Style` mapping for live mode |
| `internal/agent/render.go` | Update `RenderTarget` interface: `ToolResult(name, output, diff string, isError bool)`, add `Stats(duration, usage)`. Rewrite `Renderer` methods to use format package + `ToANSI()`. Add `lastExpandable` field + `LastExpandable()` accessor. Remove existing `Diff()` method (replaced by format package) |
| `internal/agent/event_render.go` | Update `ToolResult` signature to match new interface (4 params). Update `LiveApproval()` to use new signature. Add `Stats()` method emitting `EventStats`. Add `Content`/`IsError` to event emission |
| `internal/agent/agent.go` | Add `turnStart` timing with deferred `Stats()` call. Add `LastDiff()` type assertion after tool execution. Pass diff and isError to `ToolResult`. Stream via `format.AgentLine` |
| `internal/agent/tools/edit.go` | Add `lastDiff string` field and `LastDiff() string` method |
| `internal/agent/tools/write.go` | Add `lastDiff string` field and `LastDiff() string` method |
| `internal/tui/chatlive.go` | Add `styleToCellStyle()` helper. Update `handleEvent` for new event fields (Content, IsError, EventStats). Render accent border in left pane. Render colored diffs in right pane. Show stats in status bar. Add `lastExpandable` field. Handle `/expand` slash command |
| `internal/llm/types.go` | Add `EventStats` kind. Add `IsError bool`, `Content string`, `Duration time.Duration`, `Usage Usage` fields to Event |
| `cmd/forge/main.go` | Add `/expand` command handler to console REPL (calls `renderer.LastExpandable()`). Live mode `/expand` handled in `chatlive.go` |

## Out of Scope

- Syntax highlighting inside code blocks (future)
- Animated spinners (terminal compatibility concerns)
- Collapsible sections (not feasible in raw terminal)
- Mouse interaction in console mode
- Light theme support (dark terminal assumed)
