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
  - timing
  - token counts
- running throbber in the header/status area while the agent is busy
- live elapsed timer shown while a turn is running
- footer help text in input area
- visible draggable divider handle
- default pane split tuned to roughly **70/30** instead of **50/50**
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
- tools pane can be hidden entirely so the Agent pane expands full width

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
- input panel reframed as **Steering** instead of passive status
- dedicated status strip shown above the steering panel
- steering input can now be submitted while the agent is running
- added `/toggle tools`, `/toggle tools on`, and `/toggle tools off`
- added session commands: `/save [name]`, `/restore [name]`, and `/sessions`

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

## Additional follow-up improvements
Completed after the initial pass:

### Keyboard help overlay
- modal shortcuts help overlay
- open with `F1`, `Ctrl-K`, or `/help`
- plain `?` no longer opens help while typing
- close with `Esc`, `Enter`, `q`, or click

### Pane search
- search focused pane with `Ctrl-F`, `/find`, or `/find <text>`
- next/previous match navigation with `n` / `N`
- search status shown in pane title badges
- in-pane match highlighting for visible matches

### History preservation
- stopped clearing Agent/Tools panes between submitted interactions
- each turn now appends a lightweight visible separator instead of blanking the screen
- removed noisy `Turn X` markers from Agent/Tools history updates
- prior turns remain scrollable until `/clear`
- steering injections during a running turn are appended into history instead of being lost
- session snapshots can restore prior chat state across restarts
- chat autosaves on exit to `last-session`

### Agent formatting
- agent prose is rendered in a bubble-style block
- fenced code blocks are rendered separately from prose
- fenced code blocks use Chroma syntax highlighting when possible
- code block headers/footers show clearer structure and optional language labels

### Session management
- chat sessions can be saved to user-local JSON snapshots
- `/restore` loads the newest saved session by default
- `/sessions` opens an in-app picker to browse and restore sessions
- session picker supports keyboard and mouse selection
- sessions are shown newest first based on actual save time
- session picker shows timestamps for each saved session
- selected sessions can be renamed with `r`
- selected sessions can be deleted with `d`
- `last-session` is protected from rename/delete because it is used for autosave-on-exit

## Validation run
Commands to run after these changes:
- `gofmt -w internal/tui/chatlive.go`
- `go test ./internal/tui ./...`

## Suggested commit message
- `improve chat tui layout, tools pane toggle, help shortcuts, and running status`

## Suggested next steps
Recommended next testing / polish pass:
1. [x] verify busy-time steering is handled correctly by the runtime/backend, not just the TUI
2. [x] evaluate whether steering injections should also appear in the Tools pane or header timeline
3. [x] improve copy/select support for agent code blocks and tool output
4. [x] tune colors/themes for lower-contrast terminals
5. [x] consider a richer widget framework if this screen keeps growing in complexity

## Follow-up polish completed
- runtime now queues busy-time steering explicitly and applies it serially after the active turn completes
- steering is mirrored into the Tools pane and surfaced in a lightweight header/status timeline
- copy helpers now try the system clipboard first (`pbcopy`, `wl-copy`, `xclip`, `xsel`) and fall back to timestamped exports in the chat sessions directory
- added `/copy agent`, `/copy tools`, `/copy code`, and `/copy result`
- added a lower-contrast theme with `F2`, `/theme`, `/theme low`, and `/theme default`
- added an in-panel footer legend for the most useful interactive shortcuts
- current implementation now has clearer helper boundaries for theme/timeline/export behavior, while keeping the existing tcell-based screen stable
