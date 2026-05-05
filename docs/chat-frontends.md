# Chat Frontends

This repo currently has two chat UI implementations. Bubble Tea is the active path; the `tcell` implementation is legacy.

## Current state

`forge chat` uses the Bubble Tea frontend unconditionally.

Relevant entrypoints:

- [cmd/forge/main.go](/Users/cass/git/forge/cmd/forge/main.go)
- [internal/runtime/chat.go](/Users/cass/git/forge/internal/runtime/chat.go)

The runtime path is:

1. `runChat()`
2. `runtime.RunChatLive(setup)`
3. `tui.RunChatLive(...)` → delegates to `tui.RunChatLiveBubbleTea(...)` ([chatshared.go:118-119](/Users/cass/git/forge/internal/tui/chatshared.go))

## Active chat frontend (Bubble Tea)

The active frontend is the Bubble Tea implementation:

- [internal/tui/chatmodel.go](/Users/cass/git/forge/internal/tui/chatmodel.go) — primary chat surface
- [internal/tui/chatlive_bubbletea.go](/Users/cass/git/forge/internal/tui/chatlive_bubbletea.go) — Bubble Tea integration
- [internal/tui/chatshared.go](/Users/cass/git/forge/internal/tui/chatshared.go) — shared types, session I/O, provider helpers
- [internal/tui/chatmsg.go](/Users/cass/git/forge/internal/tui/chatmsg.go) — message rendering
- [internal/tui/chatmodel_test.go](/Users/cass/git/forge/internal/tui/chatmodel_test.go)

Features:

- split panes
- `/stats` overlay
- session save/restore UI
- search
- file picker
- model picker
- live token stats
- live Copilot quota
- best-effort Codex usage lookup

## Legacy chat frontend (tcell)

The older `tcell` implementation still exists in the repo, but it is no longer the active runtime path for `forge chat`:

- [internal/tui/chatlive.go](/Users/cass/git/forge/internal/tui/chatlive.go)
- [internal/tui/chatlive_render.go](/Users/cass/git/forge/internal/tui/chatlive_render.go)
- [internal/tui/chatlive_render_overlays.go](/Users/cass/git/forge/internal/tui/chatlive_render_overlays.go)
- [internal/tui/chatlive_commands.go](/Users/cass/git/forge/internal/tui/chatlive_commands.go)
- [internal/tui/chatlive_overlays.go](/Users/cass/git/forge/internal/tui/chatlive_overlays.go)
- [internal/tui/chatlive_mouse.go](/Users/cass/git/forge/internal/tui/chatlive_mouse.go)

Historically:

- `forge chat --live` used the `tcell` live UI
- plain `forge chat` used the Bubble Tea wrapper

Now both paths use Bubble Tea. The `--live` flag is compatibility-only.

## Why this matters

If you are continuing chat UI work in Forge, **`chatmodel.go` and `chatshared.go` are the real surface area.** Do not spend time extending the tcell `chatlive*` files unless you intentionally want to revive the legacy implementation.

## Recent related work

Recent chat work is concentrated in the Bubble Tea path:

- session token totals in the header and `/stats`
- best-effort Codex ChatGPT allowance lookup in `/stats`
- OpenAI/Copilot context-path observability
- restored-session rendering fixes
- error-path normalization

Related docs:

- [docs/chat-ui-improvements.md](/Users/cass/git/forge/docs/chat-ui-improvements.md)
- [docs/codex-usage.md](/Users/cass/git/forge/docs/codex-usage.md)
