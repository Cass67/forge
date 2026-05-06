# OpenCode Plugin Compatibility: Full Support

## Summary

Extend forge's OpenCode plugin compatibility from basic tools/hooks to full SDK support, including session APIs, lifecycle hooks, and agent registration — making forge a first-class host for any OpenCode-compatible plugin.

## Motivation

Forge currently ships an OpenCode compatibility shim (`opencode-host.mjs`) that:
- Extracts plugin tools and `tool.execute.before`/`after` hooks
- Stubs out everything else (`session`, `provider`, `model`, `file`, `find`, `$`, `tui`) with "Unsupported" errors

This means plugins like oh-my-openagent that depend on these APIs are severely degraded. The fix is three layers, all generic — none are oh-my-openagent-specific.

## Design

### Layer 1: Compat Shim Fixes (`internal/plugins/opencode_host.go`)

Replace all "Unsupported" stubs in the embedded Node.js script with real implementations that proxy to forge via new internal JSON-RPC methods.

| Plugin API | RPC Method | Forge Backend |
|---|---|---|
| `client.session.*` (create/get/delete/messages/todo/prompt/abort/shell/fork/children/status) | `internal_session_*` | `react.Session`, command exec |
| `client.provider.list()` | `internal_provider_list` | Provider catalog |
| `client.model.list()` | `internal_model_list` | Available models |
| `client.file.list/read/status` | `internal_file_*` | fs operations |
| `client.find.text/files/symbols` | `internal_find_*` | grep/glob/LSP |
| `client.tool.ids/list` | `internal_tool_list` | Tool registry |
| `client.config.get/update` | `internal_config_*` | Config load/save |
| `client.app.agents/log` | `internal_app_*` | Agent list, log |
| `client.tui.showToast` | `internal_tui_toast` | TUI notifications |
| `client.event.subscribe` | `internal_event_subscribe` | Basic event stream |

Additionally fix `PluginInput`:
- Add `worktree` (git worktree root)
- Fix `project` to match OpenCode's `Project` type shape

### Layer 2: Protocol Hooks (`internal/plugins/protocol.go`, `manager.go`)

Add new hook points to the JSON-RPC protocol. Plugins advertise support in initialize and receive callbacks at each point.

**New hook points:**
- `chat_message`, `chat_params`, `chat_headers` — per-message interception
- `permission_ask` — tool execution authorization
- `auth`, `provider` — provider-level plugins
- `session_start`, `session_end`, `pre_compact`, `post_compact`, `turn_complete` — lifecycle
- `event` — general lifecycle event bus (`session.created`, `turn.ended`, etc.)

Wire each hook point into its corresponding forge runtime location:
- `chat.go` — chat message lifecycle hooks
- `loop.go` — turn lifecycle, tool execution lifecycle hooks
- `session.go` — session lifecycle hooks
- `config.go` — provider/auth hooks

Hook results use the existing overlay/note/block model.

### Layer 3: Agent Registration (`protocol.go`, `manager.go`, `agent_pool.go`)

Plugins declare agents in initialize response via new `"agents"` capability:

```jsonc
{
  "capabilities": ["tools", "hooks", "agents"],
  "agents": [
    {
      "name": "sisyphus",
      "description": "Primary coding agent",
      "system_prompt": "...",
      "model": { "primary": "anthropic/claude-opus-4.7", "fallbacks": ["..."] },
      "model_family": "claude",
      "tools": "*"
    }
  ]
}
```

Forge's `AgentPool.Spawn(role)` resolves plugin-defined agents:
- Looks up agent by name in plugin registrations
- Uses plugin's system prompt, model chain, tool filter
- Runs in forge's standard ReAct loop
- Exposed via existing `spawn_agent`/`wait_agent` tools

**User overrides** via config.toml:

```toml
[plugins.agent_overrides.sisyphus]
  model = "openai/gpt-5.5"
  fallbacks = ["opencode-go/kimi-k2.6"]
```

Plugin provides defaults; forge config overrides model routing. System prompts and model family logic stay intact.

## What Stays Unsupported

- `$` (BunShell) — Bun-specific, not portable
- `createOpencodeServer()` — requires OpenCode daemon
- `experimental_workspace` — OpenCode-specific workspace system
- Full SSE event streaming with OpenCode event types — forge emits its own event types

## Testing

- Unit tests for each new RPC handler in `internal_plugin_test.go`
- Integration test with oh-my-openagent plugin verifying tools, hooks, and agent registration
- Existing `opencode_host_test.go` and `manager_test.go` continue passing

## Migration

No breaking changes. Existing plugins continue working. New capabilities are opt-in via the initialize handshake.
