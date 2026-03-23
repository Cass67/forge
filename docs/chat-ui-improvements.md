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
- input panel reframed as an active **forge prompt** instead of passive status
- dedicated status strip shown above the input panel
- forge input can now be submitted while the agent is running
- prompt label is now `forge>`
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
- forge input submitted during a running turn is appended into history instead of being lost
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
1. [x] verify busy-time forge input is handled correctly by the runtime/backend, not just the TUI
2. [x] evaluate whether queued forge input should also appear in the Tools pane or header timeline
3. [x] improve copy/select support for agent code blocks and tool output
4. [x] tune colors/themes for lower-contrast terminals
5. [x] consider a richer widget framework if this screen keeps growing in complexity

## Follow-up polish completed
- runtime now queues busy-time forge input explicitly and applies it serially after the active turn completes
- queued forge input is mirrored into the Tools pane and surfaced in a lightweight header/status timeline
- copy helpers now try the system clipboard first (`pbcopy`, `wl-copy`, `xclip`, `xsel`) and fall back to timestamped exports in the chat sessions directory
- added `/copy agent`, `/copy tools`, `/copy code`, and `/copy result`
- added a lower-contrast theme with `F2`, `/theme`, `/theme low`, and `/theme default`
- added an in-panel footer legend for the most useful interactive shortcuts
- current implementation now has clearer helper boundaries for theme/timeline/export behavior, while keeping the existing tcell-based screen stable

## Performance investigation and status
A regression review was done after the recent chat UI changes because the live TUI felt noticeably slower during streaming and with longer histories.

### Root causes found
- full-screen redraw on every spinner tick while busy
- repeated full-buffer wrapping of the Agent and Tools panes during render
- repeated wrapped-line metadata recomputation for scroll/search/selection/mouse paths
- repeated Chroma tokenization for visible code lines on every repaint
- linear search-highlight lookup per visible row
- hot event paths doing repeated string appends/formatting during token and tool-result storms

### Optimizations completed
- added per-pane wrapped-content caches for:
  - wrapped lines
  - wrapped line starts
  - width/content invalidation
- switched scroll, search, selection, mouse hit-testing, and scrollbar calculations to reuse cached wrapped content
- stopped doing a full render on spinner-only ticks; spinner updates now use a lightweight chrome/header refresh path
- replaced repeated syntax highlighting work with a bounded cache of styled code-line render segments
- indexed visible search highlights by wrapped line number instead of scanning matches per row
- reduced hot-path string churn in token and tool-result handling using `strings.Builder`
- improved cache invalidation coverage when pane buffers are cleared/restored/appended
- drained event bursts more aggressively so rapid streams coalesce into fewer renders

### Current status
- the major regression sources have been addressed
- busy-time responsiveness should now be materially better, especially with:
  - long chat histories
  - visible code blocks
  - rapid token streaming
  - tool-heavy turns
- the live header now also surfaces session timing/token stats and best-effort Copilot premium quota info when available
- `/stats` now opens a durable overlay and shows the latest turn duration, token counts, cumulative session tokens, Copilot premium info, and best-effort Codex ChatGPT allowance info when available
- `go test ./internal/tui/...` is currently passing after the performance changes

See also: `docs/codex-usage.md` for the Codex private-backend allowance integration notes.

### Remaining high-value performance work
The current implementation is much better, but there is still room for another pass if the UI needs to be pushed harder:

1. row-level caching for visible pane body rendering
2. dirty-region rendering instead of repainting all visible body rows on each non-spinner update
3. frame-budgeted render coalescing during very heavy streaming bursts
4. chunked/log-structured pane storage instead of repeatedly rebuilding large strings for very long sessions
5. additional profiling to find any remaining hot loops under real-world transcripts

## Updated validation run
Commands to run after the performance work:
- `gofmt -w internal/tui/chatlive.go internal/tui/chatlive_render.go internal/tui/chatlive_commands.go internal/tui/chatlive_mouse.go`
- `go test ./internal/tui/...`

## Updated suggested next steps
Recommended next pass:
1. benchmark the live chat UI with long transcripts and sustained token streaming
2. add row-level or dirty-region body render caching if repaint cost is still visible
3. consider chunked pane buffers if very long sessions still cause append/copy overhead
4. add lightweight profiling hooks or debug counters so future regressions are easier to spot

## Relax / usability follow-up requested
Completed UX follow-up for the live chat TUI:

- `/stats` now opens a real overlay/panel instead of relying on a short-lived flash message
- `Esc` now stops/cancels the active agent turn and returns the UI to an idle command state; a subsequent `Esc` while idle exits the app
- added `@file`-style context insertion so typing `@` opens a picker of matching files from the repo/current working tree
- `@file` insertion also supports explicit paths on `Enter` when the typed path resolves inside the working tree
- selected context files are now persisted in chat session snapshots

### Notes
- verified and fixed the `/sessions` picker regression so it no longer collapses to only the last session
- the current `@file` picker is intentionally a small first pass built into the live TUI overlay flow
