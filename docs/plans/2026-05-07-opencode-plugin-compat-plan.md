# OpenCode Plugin Compatibility Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Extend forge's OpenCode plugin compat shim from basic tools/hooks to full SDK support (session, provider, model, file, find, events, agent registration).

**Architecture:** Three layers: (1) Replace stubs in the Node.js compat shim with real implementations proxied via new JSON-RPC methods, (2) Add lifecycle hook points to the plugin protocol, (3) Add agent registration capability so plugins can define custom agents integrated into forge's spawn_agent delegation.

**Tech Stack:** Go (forge runtime), Node.js (compat shim embedded in Go), JSON-RPC over stdin/stdout (plugin protocol).

---

### Task 1: Add internal RPC handler dispatch to forge plugin protocol

**Files:**
- Modify: `internal/plugins/client.go:82-95` (add support for handling incoming internal RPC from plugin)
- Modify: `internal/plugins/protocol.go:37-42` (add internal RPC method params types)

**Step 1: Add internal RPC method dispatch in opencode_host.go**

The embedded Node.js script needs to send `internal_*` RPC calls to forge and receive responses. Add a `sendInternalRPC(method, params)` function to the Node.js host and a dispatch handler on the Go side that intercepts `internal_*` method calls from the plugin.

In `client.go`, modify `callSync()` (line 97) to detect `internal_*` methods and handle them with a Go-side dispatcher, sending the response back.

In `protocol.go`, add these types:

```go
// Internal RPC types for OpenCode compat shim
type internalSessionCreateParams struct {
    AgentType string `json:"agent_type,omitempty"`
}

type internalSessionCreateResult struct {
    SessionID string `json:"session_id"`
}

type internalSessionPromptParams struct {
    SessionID string `json:"session_id"`
    Prompt    string `json:"prompt"`
}

type internalSessionPromptResult struct {
    Output string `json:"output"`
}

type internalProviderListResult struct {
    Providers []internalProvider `json:"providers"`
}

type internalProvider struct {
    ID     string          `json:"id"`
    Models []internalModel `json:"models"`
}

type internalModel struct {
    ID          string `json:"id"`
    Name        string `json:"name"`
    Description string `json:"description,omitempty"`
}

type internalFileReadParams struct {
    Path   string `json:"path"`
    Offset int    `json:"offset,omitempty"`
    Limit  int    `json:"limit,omitempty"`
}

type internalFileReadResult struct {
    Content string `json:"content"`
}

type internalFileListParams struct {
    Pattern string `json:"pattern"`
}

type internalFileListResult struct {
    Files []string `json:"files"`
}

type internalFindTextParams struct {
    Pattern string `json:"pattern"`
    Path    string `json:"path,omitempty"`
}

type internalFindTextResult struct {
    Matches []string `json:"matches"`
}

type internalToolListResult struct {
    Tools []internalToolInfo `json:"tools"`
}

type internalToolInfo struct {
    Name        string `json:"name"`
    Description string `json:"description"`
}

type internalAppAgentsResult struct {
    Agents []internalAgentInfo `json:"agents"`
}

type internalAgentInfo struct {
    Name        string `json:"name"`
    Description string `json:"description"`
}

type internalConfigGetResult struct {
    Config map[string]any `json:"config"`
}

type internalConfigUpdateParams struct {
    Updates map[string]any `json:"updates"`
}
```

**Step 2: Add dispatch handler in Go**

In `client.go`, add after line 95 (call method):

```go
// handleInternalRPC dispatches internal_* methods to Go-side handlers for the compat shim
func (c *client) handleInternalRPC(manager *Manager, method string, params json.RawMessage) (any, error) {
    switch method {
    case "internal_provider_list":
        return manager.internalProviderList()
    case "internal_model_list":
        return manager.internalModelList()
    case "internal_tool_list":
        return manager.internalToolList()
    case "internal_file_list":
        var p internalFileListParams
        json.Unmarshal(params, &p)
        return manager.internalFileList(p)
    case "internal_file_read":
        var p internalFileReadParams
        json.Unmarshal(params, &p)
        return manager.internalFileRead(p)
    case "internal_file_status":
        return manager.internalFileStatus()
    case "internal_find_text":
        var p internalFindTextParams
        json.Unmarshal(params, &p)
        return manager.internalFindText(p)
    case "internal_app_agents":
        return manager.internalAppAgents()
    case "internal_config_get":
        return manager.internalConfigGet()
    case "internal_tui_toast":
        // no-op for now, forge has TUI
        return map[string]any{"ok": true}, nil
    default:
        return nil, fmt.Errorf("unsupported internal RPC: %s", method)
    }
}
```

**Step 3: Modify callSync to handle internal RPCs**

In `client.go`, modify `callSync` at line 97 to detect internal methods. After decoding the request method:

```go
// In caller (manager.go or startClient), pass a handleInternalRPC func
```

Actually, the cleaner approach: the opencode_host.mjs sends internal RPCs as regular JSON-RPC calls to forge, and forge detects the `internal_` prefix and dispatches. We can handle this by having the Go side check the method name from the plugin and dispatch internally.

Better approach: Keep the client clean. Add a new method `handlePluginsInternal` to Manager that processes internal RPC requests from the plugin process. The plugin sends internal calls as standard JSON-RPC requests on stdout, and the Go side treats them as incoming requests.

Modify `client.callSync()` to also handle incoming requests from the plugin (bidirectional). After writing the request, the Go side reads from stdin, and if the response has id=-1 (signal from plugin indicating it's an internal request), it processes the request and sends back a response.

Simplest approach: Since the Node.js host can't easily send new RPC calls over stdin/stdout in the same JSON-RPC stream, add a side-channel file descriptor or, simpler, have the Go side expose the internal methods as environment setup data that gets passed at initialize time, and the Node.js host does things directly.

Actually, the SIMPLEST approach: The Node.js host already runs in the same process space. Instead of RPC back to forge, have the Node.js host do file I/O, glob, etc. directly using Node.js built-ins (fs, child_process). For session/provider calls, stub them meaningfully based on environment.

Let me redesign this to be simpler.

---

### Task 1 (Revised): Implement direct Node.js implementations for stubbed APIs

**Files:**
- Modify: `internal/plugins/opencode_host.go:17-265` (the embedded Node.js script)

**Step 1: Implement direct fs/file/find implementations in Node.js**

Replace the unsupported stubs in `createCompatClient()` with real Node.js implementations that work without forge RPC. Node.js built-in `fs`, `path`, and `child_process` can handle most of these.

```javascript
// In createCompatClient(), replace stubs with:

import fs from "node:fs";
import path from "node:path";

function createCompatClient() {
  const unsupported = (name) => async () => {
    throw new Error("Unsupported OpenCode client API: " + name + ". Forge OpenCode compatibility currently supports simple plugin tools only.");
  };
  const noop = async () => {};

  return {
    _client: {
      getConfig: () => ({}),
      setConfig: () => {},
    },
    tui: {
      showToast: noop,
    },
    provider: {
      list: () => [],
    },
    model: {
      list: () => [],
    },
    session: {
      create: unsupported("client.session.create"),
      get: unsupported("client.session.get"),
      delete: unsupported("client.session.delete"),
      prompt: unsupported("client.session.prompt"),
      shell: unsupported("client.session.shell"),
      abort: unsupported("client.session.abort"),
      fork: unsupported("client.session.fork"),
      children: unsupported("client.session.children"),
      status: unsupported("client.session.status"),
      todo: unsupported("client.session.todo"),
      messages: unsupported("client.session.messages"),
      diff: unsupported("client.session.diff"),
      init: unsupported("client.session.init"),
    },
    agent: new Proxy({}, {
      get: (_target, prop) => unsupported("client.agent." + String(prop)),
    }),
    file: {
      list: async (params) => { /* glob using fs.readdirSync */ },
      read: async (params) => { /* read file using fs.readFileSync */ },
      status: async () => { /* git status via child_process */ },
    },
    find: {
      text: async (params) => { /* grep via child_process */ },
      files: async (params) => { /* glob */ },
      symbols: unsupported("client.find.symbols"),
    },
    tool: {
      ids: () => Object.keys(tools),
      list: () => tools,
    },
    app: {
      agents: async () => [],
      log: async (msg) => { process.stderr.write("[plugin] " + msg + "\n"); },
    },
    event: {
      subscribe: unsupported("client.event.subscribe"),
    },
    global: {
      event: unsupported("client.global.event"),
    },
  };
}
```

**Step 2: Fix PluginInput shape**

Add `worktree` and fix `project` to match OpenCode SDK shape:

```javascript
function createPluginInput(pluginID) {
  return {
    directory: cwd,
    project: cwd,          // string for now; full Project type would need forge to provide it
    worktree: cwd,          // git worktree root, same as cwd for now
    serverUrl: "forge://opencode-compat",
    client: createCompatClient(),
    $: async () => {
      throw new Error("Unsupported OpenCode runtime API: shell helper $");
    },
    skills: [],
    pluginID,
    experimental_workspace: undefined,
  };
}
```

**Step 3: Pass forge tool names to host for tool.list lookup**

Extend the `initialize` response to include forge's tool list so the host can report real tools. In `client.go`, pass available tool names via an env var or extra initialize field. Simplest: pass as `FORGE_TOOLS` env:

In `client.go:68-74`, add env with available tool names. Or simpler: expose via a `tool_names` field in initialize params. Update:

```go
type initializeParams struct {
    ProtocolVersion int      `json:"protocol_version"`
    PluginID        string   `json:"plugin_id"`
    CWD             string   `json:"cwd"`
    Capabilities    []string `json:"capabilities"`
    ForgeTools      []string `json:"forge_tools,omitempty"`  // NEW
}
```

Then in `opencode_host.go`, read `params.forge_tools` and expose them through `client.tool.list()`.

**Step 4: Write tests**

Test the Node.js file/glob/grep implementations in `opencode_host_test.go`.

---

### Task 2: Add lifecycle hook points to plugin protocol

**Files:**
- Modify: `internal/plugins/protocol.go:129-139` (hookPoint function)
- Modify: `internal/plugins/manager.go:189-191` (hookRegistrationPoints)
- Modify: `internal/plugins/manager.go:332-347` (hookParams event mapping)
- Modify: `internal/plugins/opencode_host.go:180-185` (supportedHooks)

**Step 1: Add new hook points to forge's hook system**

In `hooks/types.go`, add:

```go
const (
    // ... existing ...
    PointChatMessage   Point = "chat_message"
    PointChatParams    Point = "chat_params"
    PointChatHeaders   Point = "chat_headers"
    PointEvent         Point = "event"
)
```

**Step 2: Add hook support to the OpenCode compat host**

In `opencode_host.go`, modify `callHook()` (line 155-178) to handle new hook types mapping:

```javascript
async function callHook(params) {
    // ... existing before_tool/after_tool handling ...
    
    // New hooks
    if (params.point === "chat_message" && typeof instance["chat.message"] === "function") {
        // ... call instance["chat.message"]
    }
    if (params.point === "chat_params" && typeof instance["chat.params"] === "function") {
        // ... 
    }
    if (params.point === "chat_headers" && typeof instance["chat.headers"] === "function") {
        // ...
    }
    if (params.point === "permission_request" && typeof instance["permission.ask"] === "function") {
        // ...
    }
    if (params.point === "event" && typeof instance["event"] === "function") {
        await instance["event"]({ event: params.event });
        return {};
    }
    // ... etc
}
```

**Step 3: Wire hook points in chat.go**

In `internal/runtime/chat.go` around line 340-347 where hooks are configured, add new hook triggers. At the appropriate lifecycle points:

- `chat.message` — after system prompt built, before sending to LLM
- `chat.params` — when model params are resolved
- `session_start` / `session_end` — at session lifecycle boundaries  
- `turn_complete` — after each ReAct turn ends
- `pre_compact` / `post_compact` — around memory compaction
- `permission_request` — before tool approval UI

**Step 4: Extend hookRegistrationPoints**

In `manager.go:189`:

```go
func hookRegistrationPoints() []hooks.Point {
    return []hooks.Point{
        hooks.PointPromptContext,
        hooks.PointBeforeTool,
        hooks.PointAfterTool,
        hooks.PointChatMessage,
        hooks.PointChatParams,
        hooks.PointChatHeaders,
        hooks.PointPermissionRequest,
        hooks.PointSessionStart,
        hooks.PointSessionEnd,
        hooks.PointPreCompact,
        hooks.PointPostCompact,
        hooks.PointTurnComplete,
        hooks.PointEvent,
    }
}
```

**Step 5: Add event payload to hookParams**

In `manager.go:346`, add event payload support:

```go
func hookParams(point hooks.Point, event hooks.Event) hookCallParams {
    params := hookCallParams{Point: string(point)}
    // ... existing tool mapping ...
    if point == hooks.PointEvent || point == hooks.PointSessionStart || point == hooks.PointSessionEnd || point == hooks.PointTurnComplete {
        params.Event = eventToMap(event)
    }
    return params
}
```

Add `Event` field to `hookCallParams`:

```go
type hookCallParams struct {
    Point    string         `json:"point"`
    ToolName string         `json:"tool_name,omitempty"`
    Args     map[string]any `json:"args,omitempty"`
    Status   string         `json:"status,omitempty"`
    Error    string         `json:"error,omitempty"`
    Event    map[string]any `json:"event,omitempty"` // NEW
}
```

**Step 6: Run existing tests**

```bash
go test ./internal/plugins/... -v -timeout 60s
go test ./internal/hooks/... -v -timeout 60s
go test ./cmd/forge/... -v -timeout 60s
```

---

### Task 3: Agent registration API

**Files:**
- Modify: `internal/plugins/protocol.go:44-47` (initializeResult to include agents)
- Modify: `internal/plugins/manager.go:34-79` (Start to collect agents, new struct)
- Modify: `internal/plugins/client.go:68-74` (initialize to pass agent info)
- Modify: `internal/plugins/opencode_host.go:53-75` (dispatch to handle agent listing)
- Modify: `internal/react/agent_pool.go:21` (SpawnFunc to accept agent config)
- Modify: `internal/config/config.go:125-136` (PluginConfig to include agent_overrides)

**Step 1: Add agent type to protocol**

In `protocol.go`:

```go
type agentDef struct {
    Name        string   `json:"name"`
    Description string   `json:"description,omitempty"`
    SystemPrompt string  `json:"system_prompt,omitempty"`
    Model       string   `json:"model,omitempty"`
    Fallbacks   []string `json:"fallbacks,omitempty"`
    ModelFamily string   `json:"model_family,omitempty"`
    Tools       any      `json:"tools,omitempty"` // "*" or []string
}

type initializeResult struct {
    Tools  []toolDef   `json:"tools,omitempty"`
    Hooks  []string    `json:"hooks,omitempty"`
    Agents []agentDef  `json:"agents,omitempty"`  // NEW
}
```

**Step 2: Collect agents from plugin in manager.go**

In `manager.go`, add to `pluginState`:

```go
type pluginState struct {
    config config.PluginConfig
    client *client
    tools  []pluginTool
    hooks  map[hooks.Point]struct{}
    agents []agentDef          // NEW
}
```

In `Start()`, collect agents from initResult:

```go
// After line 68 in manager.go
state := &pluginState{
    config: cfg,
    client: client,
    tools:  tools,
    hooks:  normalizeHooks(initResult.Hooks),
    agents: initResult.Agents,   // NEW
}
```

**Step 3: Add agent lookup to AgentPool**

Add agent registry to `AgentPool`:

```go
type AgentPool struct {
    mu     sync.Mutex
    next   int
    jobs   map[string]*agentJob
    spawn  SpawnFunc
    agents map[string]agentDef  // NEW: plugin-defined agents
}

func (p *AgentPool) RegisterAgents(agents []agentDef) {
    p.mu.Lock()
    defer p.mu.Unlock()
    if p.agents == nil {
        p.agents = make(map[string]agentDef)
    }
    for _, a := range agents {
        p.agents[strings.ToLower(a.Name)] = a
    }
}

func (p *AgentPool) GetAgent(name string) (agentDef, bool) {
    p.mu.Lock()
    defer p.mu.Unlock()
    a, ok := p.agents[strings.ToLower(name)]
    return a, ok
}
```

**Step 4: Wire agent lookup into Spawn**

In `AgentPool.Spawn()`, after resolving role, look up agent definition and pass to spawn func:

```go
func (p *AgentPool) Spawn(ctx context.Context, role, task string) (string, error) {
    // ... existing validation ...
    role = MapSpawnRole(role)
    
    // Look up plugin-defined agent
    if agent, ok := p.GetAgent(role); ok {
        // Use agent's system prompt, model, tool filter
    }
    // ... continue with spawn ...
}
```

**Step 5: Add agent_overrides to PluginConfig**

In `config/config.go` `PluginConfig`:

```go
type PluginConfig struct {
    // ... existing fields ...
    AgentOverrides map[string]AgentOverride `toml:"agent_overrides,omitempty"` // NEW
}

type AgentOverride struct {
    Model     string   `toml:"model"`
    Fallbacks []string `toml:"fallbacks,omitempty"`
}
```

**Step 6: Expose agent definitions in opencode_host.mjs**

In `opencode_host.go`, modify `dispatch("initialize", ...)` to include agent definitions from the plugin:

```javascript
// After loading instance, inspect for agent definitions
async function getAgents(instance) {
    if (instance.agents && Array.isArray(instance.agents)) {
        return instance.agents;
    }
    // oh-my-openagent pattern: agents defined in instance.config or similar
    if (instance.config && instance.config.agents) {
        return instance.config.agents;
    }
    return [];
}
```

Return agents in initialize:

```javascript
case "initialize":
    cwd = params.cwd || process.cwd();
    await ensurePlugin(params.plugin_id || "opencode");
    return {
        tools: /* ... */,
        hooks: supportedHooks(instance),
        agents: await getAgents(instance),
    };
```

**Step 7: Run tests**

```bash
go test ./internal/plugins/... -v -timeout 60s
go test ./internal/react/... -v -timeout 60s
go test ./cmd/forge/... -v -timeout 60s
```

---

### Task 4: End-to-end integration test

**Files:**
- Modify: `internal/plugins/opencode_host_test.go`

**Step 1: Add test that verifies full compat shim**

Write a test plugin that exercises file I/O, glob, and tool listing through the compat shim. Verify no "Unsupported" errors.

**Step 2: Add test for agent registration**

Write a test plugin that declares an agent. Verify forge's AgentPool can spawn it.

**Step 3: Run all tests**

```bash
go test ./... -timeout 120s
```

---

### Task 5: Rebuild and smoke test with oh-my-openagent

**Step 1: Rebuild forge**

```bash
go build -o ./bin/forge ./cmd/forge/
```

**Step 2: Reinstall plugin with fixed pipeline**

```bash
./bin/forge plugin remove oh-my-openagent
rm -rf ~/.config/forge/plugins/opencode/oh-my-openagent
./bin/forge plugin install https://github.com/code-yeongyu/oh-my-openagent.git
```

**Step 3: Verify plugin startup**

Run forge chat and verify the plugin initializes without "Unsupported" errors.

**Step 4: Commit**

```bash
git add -A
git commit -m "feat: full OpenCode plugin compat - session APIs, lifecycle hooks, agent registration"
```
