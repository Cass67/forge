# Chat UI Improvements

## Branch
- `improve-chat-ui`

## What changed
Frontend chat TUI in `internal/tui/chatlive.go` was heavily improved.

### Layout / visuals
- boxed **Agent**, **Tools**, and **Input** panels
- cleaner top header with:
  - model
  - cwd
  - status
  - turn count
  - timing
  - token counts
- footer help text in input area
- visible draggable divider handle
- pane title badges with follow state

### Scrolling / mouse
- enabled mouse support via `screen.EnableMouse()`
- mouse wheel scrolling
- click-to-focus left/right panes
- visible right-edge scrollbars in both panes
- clickable scrollbar tracks
- draggable scrollbar thumbs
- scrollbar arrow caps
- page jump behavior when clicking above/below thumb
- draggable center divider to resize panes

### Text rendering / wrapping
- display-width-aware clipping using `go-runewidth`
- safer input rendering with horizontal viewport
- improved cursor placement in long input
- continuation-aware wrapping for pane content
- reduced text spill outside pane boundaries

### Input UX
- boxed input panel
- click-to-place cursor in input line
- busy/approval rendering improved
- flash/help messaging improved

### Model picker
- mouse wheel navigation
- click row to select model
- click outside to close
- improved title bar
- model count display
- current model checkmark
- better footer help text

### Streaming / follow behavior
- autoscroll follow state for **Agent** and **Tools**
- if at bottom, streaming output follows
- if user scrolls away, follow turns off
- follow state shown in pane titles/header labels

### Tools pane readability
- clearer grouping of tool events
- separators before tool call groups
- structured result/status formatting
- improved empty-state copy

## Validation run
Commands run successfully:
- `gofmt -w internal/tui/chatlive.go`
- `go test ./internal/tui ./...`

## Suggested commit message
- `improve chat tui layout, mouse scrolling, scrollbars, and pane usability`

## Suggested next steps
If continuing later, best remaining improvements:
1. copy/select support
2. keyboard shortcuts help overlay
3. pane search
4. theme tuning
5. eventually migrate this screen to Bubble Tea/Lip Gloss widgets
