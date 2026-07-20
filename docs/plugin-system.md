# Forge Plugin System

Forge plugins are Go packages compiled into the Forge binary. They register tools, hook handlers, skills, agents, model providers, MCP servers, and slash commands by implementing interfaces on a `Plugin` type and calling `plugin.Register(p)` from `init()`.

No subprocess, no JSON-RPC, no JS shim. Write Go, build Forge, done.

## Quick Start

**plugin/hello/hello.go** — the minimal plugin:

```go
package hello

import (
    "context"
    "fmt"

    "forge/internal/plugin"
)

type helloPlugin struct{}

func (helloPlugin) Name() string { return "hello" }

func (helloPlugin) Tools() []plugin.Tool {
    return []plugin.Tool{{
        Name:        "hello",
        Description: "Say hello to someone by name",
        Parameters: []plugin.Param{{Name: "name", Type: "string", Description: "Name to greet", Required: true}},
        Execute: func(ctx context.Context, args map[string]any) (string, error) {
            name, _ := args["name"].(string)
            return fmt.Sprintf("Hello, %s!", name), nil
        },
    }}
}

func init() { plugin.Register(helloPlugin{}) }
```

**plugins/imports.go** — register the package:

```go
package plugins

import (
    _ "forge/plugin/hello" // side-effect: registers tool at init()
)
```

**cmd/forge/main.go** — import the plugin set:

```go
import _ "forge/plugins"
```

Rebuild, run `forge`, then ask the model: "Say hello to Cass" — it calls `hello` tool.

---

## Plugin Kinds

A single Go package implements one or more provider interfaces on a `Plugin`. Each is optional. The registry introspects which interfaces the plugin satisfies and collects them.

### Tool Plugin

Implement `ToolProvider` to expose tools the model can invoke.

```go
type helloPlugin struct{}

func (helloPlugin) Tools() []plugin.Tool {
    return []plugin.Tool{{
        Name:        "docker_sandbox_run",
        Description: "Run a command in an ephemeral Docker sandbox.",
        Parameters: []plugin.Param{
            {Name: "image", Type: "string", Description: "Docker image", Required: true},
            {Name: "command", Type: "string", Description: "Shell command", Required: true},
            {Name: "timeout_seconds", Type: "integer", Description: "Max execution time", Required: false},
        },
        Execute: func(ctx context.Context, args map[string]any) (string, error) {
            // ... run docker, capture output ...
            return output, nil
        },
    }}
}
```

**Tool struct:**

| Field | Type | Required | Description |
|---|---|---|---|
| `Name` | `string` | Yes | Unique tool name, snake_case (max 64 chars) |
| `Description` | `string` | Yes | What the tool does. Read by the model — be specific. |
| `Parameters` | `[]Param` | No | JSON Schema-style parameter definitions |
| `Execute` | `ToolFunc` | Yes | `func(ctx, args) (output string, err error)` |

**Param struct:**

| Field | Type | Required | Description |
|---|---|---|---|
| `Name` | `string` | Yes | Parameter name, snake_case |
| `Type` | `string` | Yes | JSON type: `"string"`, `"number"`, `"integer"`, `"boolean"`, `"array"`, `"object"` |
| `Description` | `string` | Yes | What the parameter does |
| `Required` | `bool` | No | Whether parameter is required |
| `Default` | `any` | No | Optional default value |
| `Enum` | `[]string` | No | Allowed values for string parameters |

Shorthand helpers: `plugin.StringParam`, `plugin.OptStringParam`, `plugin.BoolParam`, `plugin.OptBoolParam`, `plugin.IntParam`, `plugin.OptIntParam`.

### Hook Plugin

Implement `HookProvider` to subscribe to lifecycle events.

```go
type helloPlugin struct{}

func (helloPlugin) Hooks() []plugin.Hook {
    return []plugin.Hook{{
        Point: plugin.PointBeforeTool,
        Handler: func(ctx context.Context, e plugin.HookEvent) []plugin.HookResult {
            snapshot, ok := e.Snapshot.(hooks.ToolCallSnapshot)
            if !ok || snapshot.ToolName != "docker_sandbox_run" {
                return nil
            }
            cmd, _ := snapshot.Args["command"].(string)
            if strings.Contains(cmd, "rm -rf /") {
                return []plugin.HookResult{{Block: &plugin.HookBlock{
                    Message: "Destructive command blocked by sandbox guard",
                }}}
            }
            return nil
        },
    }}
}
```

**Available hook points** (from `plugin.HookPoint`):

| Point | Fires when | Snapshot type |
|---|---|---|
| `PointSessionStart` | Session begins | `SessionSnapshot` |
| `PointSessionEnd` | Session ends | `SessionSnapshot` |
| `PointPermissionRequest` | Tool needs approval | `PermissionSnapshot` |
| `PointBeforeTool` | Before tool execution | `ToolCallSnapshot` |
| `PointAfterTool` | After tool execution | `ToolResultSnapshot` |
| `PointPreCompact` | Before context compaction | `CompactSnapshot` |
| `PointPostCompact` | After context compaction | `CompactSnapshot` |
| `PointTurnComplete` | LLM turn finishes | `TurnSnapshot` |
| `PointPromptContext` | System prompt assembles | `PromptContextSnapshot` |
| `PointChatMessage` | Message added to history | `ChatMessageSnapshot` |

**HookResult** — return a slice of `plugin.HookResult`, each with one of:

| Field | Effect |
|---|---|
| `Overlay *HookOverlay` | Inject content into system prompt at named key |
| `Note *HookNote` | Show informational note to user |
| `Block *HookBlock` | Block the action. Chain stops here. |

`Priority` values: `hooks.PriorityLow` (0), `hooks.PriorityNormal` (1), `hooks.PriorityHigh` (2).

### Skill Plugin

Implement `SkillProvider` to provide skills the agent loads on-demand. Equivalent to Pi's `SKILL.md` directories.

```go
func (helloPlugin) Skills() []plugin.Skill {
    return []plugin.Skill{{
        Name:        "docker-sandbox",
        Description: "Set up and use Docker sandboxes for isolated code execution. Use when the user asks to run untrusted code, test scripts in clean environments, or install packages safely.",
        Body:        dockerSandboxSkillMarkdown,
    }}
}
```

**Skill struct:**

| Field | Type | Description |
|---|---|---|
| `Name` | `string` | Lowercase letters, numbers, hyphens. 1-64 chars. |
| `Description` | `string` | When the model should load this skill. Be specific — this is the trigger. Max 1024 chars. |
| `Body` | `string` | Full markdown body loaded when skill activates. |
| `Dir` | `string` | Directory path for relative references to scripts/assets |

### Agent Plugin

Implement `AgentProvider` to register a custom agent with its own system prompt and tool whitelist.

```go
func (helloPlugin) Agents() []plugin.Agent {
    return []plugin.Agent{{
        Name:         "code-reviewer",
        Description:  "Reviews code changes for bugs, security issues, and style problems",
        SystemPrompt: "You are a thorough code reviewer. Focus on: correctness, security, performance, readability. Be concise and specific.",
        Tools:        []string{"read", "grep", "find", "code_search", "git_diff", "git_log"},
        Model:        "", // empty = use session default
        Fallbacks:    []string{},
    }}
}
```

### MCP Server Plugin

Implement `MCPServerProvider` to register MCP servers the agent can discover.

```go
func (helloPlugin) MCPServers() []plugin.MCPServer {
    return []plugin.MCPServer{{
        Name:    "postgres",
        Command: []string{"npx", "-y", "@anthropic/mcp-server-postgres", "postgresql://localhost/mydb"},
        Env: map[string]string{
            "PGPASSWORD": "${PGPASSWORD}", // resolved from env at startup
        },
    }}
}
```

### Slash Command Plugin

Implement `CommandProvider` to register slash commands (e.g. `/plugin` in the chat UI).

```go
func (helloPlugin) Commands() []plugin.Command {
    return []plugin.Command{{
        Name:        "plugin",
        Description: "Manage forge plugins",
        Handler: func(ctx context.Context, args string) (string, error) {
            // custom slash command logic
            return output, nil
        },
    }}
}
```

### Model Provider Plugin

Implement `ProviderProvider` to add custom LLM providers.

```go
func (helloPlugin) Providers() []plugin.Provider {
    return []plugin.Provider{{
        Name: "my-provider",
        Models: []plugin.ModelDef{{
            ID: "my-model-1", Name: "My Model 1", ContextWindow: 128000,
        }},
        BaseURL: "https://api.example.com",
        ResolveAPIKey: func(ctx context.Context) (string, error) {
            return os.Getenv("MY_API_KEY"), nil
        },
    }}
}
```

---

## Plugin Layout

One Go package per plugin. Multiple plugins can live in the same module.

```
forge/
├── plugin/                    # plugin packages
│   ├── hello/
│   │   └── hello.go
│   ├── sandbox/
│   │   ├── sandbox.go         # tool + skill + command registrations
│   │   ├── session.go
│   │   └── sandbox_test.go
│   └── git_guard/
│       └── git_guard.go       # hook registration
├── plugins/
│   └── imports.go             # imports all plugins for side effects
└── cmd/forge/
    └── main.go                 # imports plugins package
```

**`plugins/imports.go`:**

```go
package plugins

import (
    _ "forge/plugin/hello"
    _ "forge/plugin/sandbox"
    _ "forge/plugin/git_guard"
)
```

Adding a plugin to a Forge build is a single import line.

---

## Registration Reference

All in `forge/internal/plugin`:

```go
package plugin

// Core
type Plugin interface { Name() string }
func Register(p Plugin)

// Provider interfaces — implement any subset
type ToolProvider     interface { Tools() []Tool }
type HookProvider     interface { Hooks() []Hook }
type SkillProvider    interface { Skills() []Skill }
type AgentProvider    interface { Agents() []Agent }
type MCPServerProvider interface { MCPServers() []MCPServer }
type CommandProvider  interface { Commands() []Command }
type ProviderProvider interface { Providers() []Provider }

// Configurable: receives [plugins.settings] table at load
type Configurable interface { Configure(settings map[string]any) }

// Standalone helpers for simple init()-based registration
func RegisterTool(t Tool)
func RegisterHook(h Hook)

// Query
func GetAllTools()    []Tool
func GetAllHooks()    []Hook
func GetAllSkills()   []Skill
func GetAllAgents()   []Agent
func GetAllMCPServers() []MCPServer
func GetAllCommands() []Command
func GetAllProviders() []Provider
func GetPlugin(name string) *PluginInfo
```

All registrations use `init()` for auto-discovery. No manual wiring.

---

## Plugin Configuration

Plugins can be enabled/disabled in `forge.toml`:

```toml
[[plugins]]
id = "sandbox"
enabled = true

[[plugins]]
id = "hello"
enabled = false       # disable without removing import
```

Native plugins are controlled purely by `enabled`. No `command`, `source`, or timeout fields needed — they run in-process.

### Plugin Settings

A plugin that implements the `Configurable` interface receives its `[plugins.settings]` table once at load:

```go
func (s *sandboxPlugin) Configure(settings map[string]any) {
    if img, ok := settings["image"].(string); ok {
        s.defaultImage = img
    }
}
```

```toml
[[plugins]]
id = "sandbox"
enabled = true

[plugins.settings]
default_on = true
image = "golang:1.26"
dockerfile = "Dockerfile.dev"
```

The sandbox plugin's image precedence: explicit `image` > `dockerfile` > project auto-detect.

---

## Migration from OpenCode Plugins

If you had an OpenCode (JS) plugin that:

1. **Registered a tool** → implement `ToolProvider` returning `[]Tool`, or use `plugin.RegisterTool(Tool{...})`
2. **Intercepted events** → implement `HookProvider` returning `[]Hook`, or use `plugin.RegisterHook(Hook{Point: ..., Handler: ...})`
3. **Provided system prompt overlays** → return `HookResult{Overlay: &HookOverlay{Key: ..., Content: ...}}` from a `PointPromptContext` hook
4. **Exposed skills** → implement `SkillProvider` returning `[]Skill{...}`
5. **Needed npm dependencies** → rewrite in Go or call external binaries via `plugin.Exec()`
6. **Defined agents** → implement `AgentProvider` returning `[]Agent{...}`

The JSON-RPC transport for external plugins is preserved for non-Go use cases. Set `kind = "external"` in config and provide a `command` array.

---

## Example: Docker Sandbox Plugin (Complete)

```go
package sandbox

import (
    "context"
    "fmt"
    "os/exec"
    "strings"
    "time"

    "forge/internal/hooks"
    "forge/internal/plugin"
)

type sandboxPlugin struct{}

func (sandboxPlugin) Name() string { return "docker_sandbox" }

func (sandboxPlugin) Tools() []plugin.Tool {
    return []plugin.Tool{{
        Name:        "docker_sandbox_run",
        Description: "Run a command in an ephemeral Docker container. Use for isolated code execution, package installs, or untrusted scripts. Container is destroyed after command completes.",
        Parameters: []plugin.Param{
            {Name: "image", Type: "string", Description: "Docker image (alpine:latest, python:3.12-slim, node:20-slim)", Required: true},
            {Name: "command", Type: "string", Description: "Shell command to execute", Required: true},
            {Name: "timeout_seconds", Type: "integer", Description: "Max execution time (default 30, max 300)", Required: false},
            {Name: "network", Type: "string", Description: "Docker network mode (none, bridge, host; default none)", Required: false},
        },
        Execute: sandboxExecute,
    }}
}

func (sandboxPlugin) Skills() []plugin.Skill {
    return []plugin.Skill{{
        Name:        "docker-sandbox",
        Description: "Set up and use Docker sandboxes for isolated code execution. Use when the user asks to run untrusted code, test scripts in clean environments, install packages safely, or needs a disposable Linux environment.",
        Body:        sandboxSkillBody,
    }}
}

func (sandboxPlugin) Hooks() []plugin.Hook {
    return []plugin.Hook{{
        Point: plugin.PointBeforeTool,
        Handler: sandboxGuard,
    }}
}

func sandboxExecute(ctx context.Context, args map[string]any) (string, error) {
    image, _ := args["image"].(string)
    command, _ := args["command"].(string)
    timeout, _ := args["timeout_seconds"].(float64)
    network, _ := args["network"].(string)

    if image == "" || command == "" {
        return "", fmt.Errorf("image and command are required")
    }
    if timeout == 0 {
        timeout = 30
    }
    if timeout > 300 {
        return "", fmt.Errorf("timeout must be <= 300 seconds")
    }
    if network == "" {
        network = "none" // secure default: no network
    }

    ctx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
    defer cancel()

    cmd := exec.CommandContext(ctx,
        "docker", "run", "--rm",
        "--network="+network,
        image,
        "sh", "-c", command,
    )

    output, err := cmd.CombinedOutput()
    if ctx.Err() == context.DeadlineExceeded {
        return string(output), fmt.Errorf("command timed out after %.0f seconds", timeout)
    }
    if err != nil {
        return string(output), fmt.Errorf("command failed: %w", err)
    }
    return string(output), nil
}

func sandboxGuard(ctx context.Context, e plugin.HookEvent) []plugin.HookResult {
    snapshot, ok := e.Snapshot.(hooks.ToolCallSnapshot)
    if !ok || snapshot.ToolName != "docker_sandbox_run" {
        return nil
    }
    cmd, _ := snapshot.Args["command"].(string)
    destructive := []string{
        "rm -rf /", "mkfs.", "dd if=/dev/zero",
        "> /dev/sda", ":(){ :|:& };:", // fork bomb
    }
    for _, d := range destructive {
        if strings.Contains(cmd, d) {
            return []plugin.HookResult{{Block: &plugin.HookBlock{
                Message:    fmt.Sprintf("Destructive command blocked: %q matches dangerous pattern %q", cmd, d),
            }}}
        }
    }
    return nil
}

var sandboxSkillBody = `## Docker Sandbox

## Setup
Run once per machine:
` + "```" + `bash
docker pull alpine:latest
docker pull python:3.12-slim
` + "```" + `

## Usage
Ask the agent to run code in a sandbox:
- "Run this Python script in a sandbox"
- "Install pandas and test this data pipeline"
- "Execute this shell command in an isolated container"

## Images
- alpine:latest — minimal Linux, good for shell scripts
- python:3.12-slim — Python 3.12 with pip
- node:20-slim — Node.js 20 with npm

## Notes
- Sandboxes are ephemeral — destroyed after command completes
- Default timeout is 30 seconds, max 5 minutes
- No network access by default — use --network flag if needed
`
```

---

## Why Compile-In Instead of Dynamic Loading?

1. **Go's `plugin` package** (`.so` loading) is Linux-only, requires CGo, and breaks on compiler version mismatches.
2. **Subprocess JSON-RPC** is preserved for dynamic/external use (`kind = "external"`).
3. **Compile-in** means zero runtime overhead, full type safety, static analysis, and trivial distribution (single binary).
4. This matches how Pi works (TypeScript modules compiled/transpiled at load time by jiti). No one expects to add plugins to a running Pi process.

Plugin authors contribute a Go package, add an import line, rebuild. Forge's build takes ~5 seconds.

---

## Package Distribution

Plugins can be distributed as:

- **Go module** — `import "github.com/user/forge-plugin-docker-sandbox"` in `plugins/imports.go`
- **Local package** — `import "forge/plugin/my_plugin"` with code in `plugin/my_plugin/`
- **In-repo** — contribute directly to the Forge repo under `plugin/`

No npm, no git cloning, no `pi install`. Go modules handle everything.

---

## Summary: What Plugin Authors Need

1. Create a Go type implementing one or more provider interfaces (`ToolProvider`, `HookProvider`, `SkillProvider`, `AgentProvider`, `MCPServerProvider`, `CommandProvider`, `ProviderProvider`)
2. Call `plugin.Register(myPlugin{})` from `init()`
3. Add one import line to `plugins/imports.go`
4. Rebuild Forge

That's it. No manifest files, no JSON-RPC protocol, no JS shim.
