# Chat Frontends

This repo currently has two chat UI implementations, but only one of them is now active for `forge chat`.

## Current state

`forge chat` now uses the live split-pane `tcell` UI unconditionally.

Relevant entrypoints:

- [cmd/forge/main.go](/Users/cass/git/forge/cmd/forge/main.go)
- [internal/runtime/chat.go](/Users/cass/git/forge/internal/runtime/chat.go)

The runtime path is:

1. `runChat()`
2. `runtime.RunChatLive(setup)`
3. `tui.RunChatLive(...)`

## Active chat frontend

The active frontend is the custom `tcell` implementation:

- [internal/tui/chatlive.go](/Users/cass/git/forge/internal/tui/chatlive.go)
- [internal/tui/chatlive_render.go](/Users/cass/git/forge/internal/tui/chatlive_render.go)
- [internal/tui/chatlive_render_overlays.go](/Users/cass/git/forge/internal/tui/chatlive_render_overlays.go)
- [internal/tui/chatlive_commands.go](/Users/cass/git/forge/internal/tui/chatlive_commands.go)
- [internal/tui/chatlive_overlays.go](/Users/cass/git/forge/internal/tui/chatlive_overlays.go)
- [internal/tui/chatlive_mouse.go](/Users/cass/git/forge/internal/tui/chatlive_mouse.go)

This is the richer chat UI with:

- split panes
- `/stats` overlay
- session save/restore UI
- search
- file picker
- model picker
- live token stats
- live Copilot quota
- best-effort Codex usage lookup

## Legacy chat frontend

The older Bubble Tea chat implementation still exists in the repo, but it is no longer the active runtime path for `forge chat`:

- [internal/tui/chatmodel.go](/Users/cass/git/forge/internal/tui/chatmodel.go)
- [internal/tui/chatlive_bubbletea.go](/Users/cass/git/forge/internal/tui/chatlive_bubbletea.go)

Historically:

- `forge chat --live` used the `tcell` live UI
- plain `forge chat` used the Bubble Tea wrapper

Now:

- `forge chat` and `forge chat --live` both end up on the `tcell` live UI

The `--live` flag is effectively compatibility-only at this point.

## Why this matters

If you are continuing chat UI work in Forge, the `chatlive*` files are the real surface area.

Do not spend time extending `chatmodel.go` unless you intentionally want to keep the legacy Bubble Tea implementation alive.

## Suggested cleanup

There is a straightforward cleanup pass still available:

1. remove [internal/tui/chatlive_bubbletea.go](/Users/cass/git/forge/internal/tui/chatlive_bubbletea.go)
2. remove [internal/tui/chatmodel.go](/Users/cass/git/forge/internal/tui/chatmodel.go)
3. remove [internal/tui/chatmodel_test.go](/Users/cass/git/forge/internal/tui/chatmodel_test.go)
4. remove any remaining references to the Bubble Tea chat path
5. either remove `--live` or keep it as a no-op alias for compatibility

## Recent related work

Recent chat work is concentrated in the live `tcell` path:

- session token totals in the header and `/stats`
- best-effort Codex ChatGPT allowance lookup in `/stats`
- OpenAI/Copilot context-path observability
- restored-session rendering fixes
- error-path normalization

Related docs:

- [docs/chat-ui-improvements.md](/Users/cass/git/forge/docs/chat-ui-improvements.md)
- [docs/codex-usage.md](/Users/cass/git/forge/docs/codex-usage.md)
