# Next steps: Bubble Tea chat parity audit

Context: the Bubble Tea refactor restored several regressions, but the new `ChatModel` still needs a structured parity pass against legacy `chatlive` on `main`.

## Already restored

- Tabbed help overlay in Bubble Tea chat
  - tabs: Keys / Chat Commands / CLI Skills
  - `F1` and `/help` open it
- Pane-edge scrollbars
  - chat pane scrollbar
  - tools pane scrollbar
- Session commands
  - `/sessions`
  - `/save [name]`
  - `/restore [name]`
- Chat/tools pane width fix when tools are visible

## Immediate user goal

- Verify `/tools` panel behavior interactively in a real terminal session.
- If the tools pane still causes blank or broken chat rendering, inspect runtime sizing with actual window dimensions and compare against `internal/tui/chatlive_render.go` behavior.

## Recommended next task

Perform a **main vs Bubble Tea parity audit**:

Compare legacy files on `main`:
- `internal/tui/chatlive.go`
- `internal/tui/chatlive_commands.go`
- `internal/tui/chatlive_overlays.go`
- `internal/tui/chatlive_render.go`
- `internal/tui/chatlive_render_overlays.go`
- `internal/tui/chatlive_sessions.go`

Against current Bubble Tea path:
- `internal/tui/chatmodel.go`
- `internal/tui/chatlive_bubbletea.go`

## Features/areas to audit

### Commands
- [ ] `/sessions` rename/delete parity
- [ ] `/save` behavior parity
- [ ] `/restore` behavior parity
- [ ] `/model` and `/models` parity
- [ ] `/skills` output parity
- [ ] `/theme` parity
- [ ] `/tools` parity
- [ ] `/clear` parity
- [ ] `/exit` / `/quit` parity
- [ ] any additional slash commands present on `main`

### Overlays / pickers
- [ ] sessions overlay actions beyond restore
- [ ] model picker parity
- [ ] help overlay copy parity
- [ ] any file/context picker parity
- [ ] search/history overlays if present on `main`

### Session handling
- [ ] autosave behavior parity
- [ ] restore fidelity parity
- [ ] saved snapshot format compatibility
- [ ] latest/default naming behavior

### Layout / rendering
- [ ] pane resizing parity
- [ ] tools pane independent scrolling/focus
- [ ] chat pane scrolling parity
- [ ] mouse behavior parity
- [ ] overlay stacking behavior parity
- [ ] low-contrast theme parity

### Interaction behavior
- [ ] keyboard shortcuts parity
- [ ] approval prompt parity
- [ ] busy/spinner/status behavior parity
- [ ] cancel/escape behavior parity

## Suggested approach

1. Make a checklist of legacy features from `main`.
2. Mark each as:
   - present
   - partially present
   - missing
3. Restore missing features in small slices with tests.
4. Run:
   - `gofmt -w internal/tui/chatmodel.go internal/tui/chatmodel_test.go`
   - `go test ./internal/tui/...`
5. Then test interactively in terminal.

## Notes from current session

- A prior `/sessions` bug came from using a value receiver for picker refresh; session-mutating helpers must update real model state.
- The blank chat pane with tools visible was likely caused by mismatched content width vs bordered-pane inner width; width math was corrected in `chatmodel.go`.
- Some runtime-only issues may still require interactive testing, especially around narrow terminal sizes and toggling `/tools` on/off.
