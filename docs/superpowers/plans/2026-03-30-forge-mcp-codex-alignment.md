# Forge MCP Codex Alignment

## Goal

Add first-class MCP support to Forge following Codex's architecture:
- config-backed MCP server definitions
- namespaced dynamic tools from external MCP servers
- optional MCP resource tools
- approval/auth handling at the host layer
- `context7` as the first configured server, not a special-case built into runtime

## Why

Forge currently has no MCP subsystem. The live chat path only exposes static built-in tools from `internal/agent/tools` and the React runner consumes a fixed `tools.Registry`.

Codex does substantially more:
- stores MCP servers in config
- merges configured and plugin-provided servers
- initializes MCP alongside auth at session startup
- exposes MCP tools as namespaced dynamic tools
- supports OAuth/login and per-tool approval
- exposes MCP resources and resource templates when servers provide them

If Forge wants real `context7` support and future MCP servers, it needs that substrate rather than another one-off integration.

## Source Shape To Copy

Primary upstream reference: Codex.

Key Codex components:
- config and CLI management: `codex-rs/cli/src/mcp_cmd.rs`
- MCP manager / effective server set: `codex-rs/core/src/mcp/mod.rs`
- session startup integration: `codex-rs/core/src/codex.rs`
- dynamic/namespaced tool spec conversion: `codex-rs/core/src/tools/spec.rs`
- MCP tool call approval/auth/sanitization: `codex-rs/core/src/mcp_tool_call.rs`

OpenCode also supports MCP, but Codex is the better fit for Forge because it is more explicit about config, approvals, namespaced tools, and resource support.

## Target Architecture

### 1. Config

Add `[mcp_servers.<name>]` support to Forge config with two transport shapes:
- stdio:
  - `command = ["cmd", "arg1", ...]`
  - optional `env = { KEY = "VALUE" }`
- remote:
  - `url = "https://.../mcp"`
  - optional `headers = { ... }`
  - optional auth settings later

Minimum fields:
- `enabled`
- `type`
- `timeout_ms`
- transport-specific fields

Do not add a fake `context7` shortcut field.

### 2. MCP Manager

Add `internal/mcp` with:
- config model normalization
- server lifecycle
- connected-client cache keyed by server name
- snapshot methods for:
  - tools
  - resources
  - resource templates

Start with:
- local stdio transport
- remote streamable HTTP transport

### 3. Dynamic Tool Surface

Translate remote MCP tool definitions into Forge-native `llm.ToolDef` entries at runtime.

Tool naming:
- use namespaced tool names
- format: `mcp__<server>__<tool>`

This must land in the live native tool path, not prompt text injection.

### 4. Execution Path

When the model calls `mcp__context7__...` or another namespaced MCP tool:
- dispatch through the MCP manager
- return sanitized text/structured output back into the normal React loop
- log/debug it like other native tool calls

This should be implemented as a dynamic tool execution layer, not as static `Tool.Execute` closures registered at compile time.

### 5. Resource Tools

Add the Codex-style resource helpers when MCP servers are configured:
- `list_mcp_resources`
- `list_mcp_resource_templates`
- `read_mcp_resource`

Hide them when no MCP servers are configured.

### 6. Auth And Approval

First pass:
- support approval routing for MCP tool calls through Forge's existing approval gate
- represent MCP tool actions clearly with server name, tool name, and summarized args

Second pass in same branch if feasible:
- add OAuth/login flow for remote MCP servers that advertise auth requirements

If OAuth is too large for the first implementation slice, land the MCP substrate with:
- explicit status reporting
- a clean "authentication required" failure
- config compatibility that can accept stored credentials later

## Implementation Order

### Task 1: Config model and tests

Files:
- `internal/config/config.go`
- `internal/config/config_test.go`

Add:
- MCP server config structs
- TOML load/save coverage

### Task 2: MCP package skeleton

Files:
- `internal/mcp/*.go`

Add:
- server config normalization
- manager
- transport abstraction
- connection lifecycle

Write tests with fake MCP servers where possible.

### Task 3: Dynamic tool integration

Files:
- `internal/llm/types.go` if needed
- `internal/react/loop.go`
- `internal/runtime/chat.go`

Add:
- dynamic tool defs from MCP snapshot into the live native tool set
- namespaced execution routing

### Task 4: MCP resource tools

Files:
- `internal/react/tools/` or `internal/agent/tools/` depending on runtime fit

Add:
- `list_mcp_resources`
- `list_mcp_resource_templates`
- `read_mcp_resource`

### Task 5: Approval and debug visibility

Files:
- `internal/react/approval.go`
- `internal/runtime/chat_debug.go`
- transcript/debug tests

Add:
- MCP approval summaries
- debug logging of server/tool/result

### Task 6: Context7

After the substrate exists:
- add `context7` as a normal configured MCP server
- verify its namespaced tools appear and execute

## Testing Strategy

Required tests:
- config load/save for MCP server blocks
- manager handles empty/no-config state
- namespaced MCP tools appear only when servers are configured
- MCP resource tools are hidden when no servers are configured
- MCP tool execution routes through the native React path
- approval/debug metadata includes server and tool name

Verification:
- targeted tests for `internal/config`, `internal/mcp`, `internal/runtime`, `internal/react`
- full `go test ./...`

## Non-Goals

Not in the first pass:
- plugin-provided MCP server manifests
- every Codex MCP feature
- memory pollution policy for MCP/web calls
- MCP-specific TUI panels

The branch goal is first-class MCP support on Forge's live runtime path, with `context7` working through that system.
