# Forge Plugin System

Forge plugins are Go packages compiled into the Forge binary. They register tools, hook handlers, skills, agents, model providers, MCP servers, and CLI commands through a simple `init()`-based registration API.

No subprocess, no JSON-RPC, no JS shim. Write Go, build Forge, done.

## Quick Start

**plugin/hello/hello.go** — the minimal tool plugin:

```go
package hello

import (
    "context"
    "fmt"

    "forge/internal/plugin"
    "forge/internal/hooks"
)

func init() {
    plugin.RegisterTool(plugin.Tool{
        Name:        "hello",
        Description: "Say hello to someone by name",
        Parameters: []plugin.Parameter{
            {Name: "name", Type: "string", Description: "Name to greet", Required: true},
        },
        Execute: func(ctx context.Context, args map[string]any) (string, error) {
            name, _ := args["name"].(string)
            return fmt.Sprintf("Hello, %s!", name), nil
        },
    })
}
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

A single Go package can mix-and-match any of these registrars. Each is optional.

### Tool Plugin

Register tools the model can invoke. Equivalent to Pi's `pi.registerTool()`.

```go
func init() {
    plugin.RegisterTool(plugin.Tool{
        Name:        "docker_sandbox_run",
        Description: "Run a command in an ephemeral Docker sandbox. Use for untrusted code execution, package installs, or isolated script runs.",
        Parameters: []plugin.Parameter{
            {Name: "image", Type: "string", Description: "Docker image to use", Required: true},
            {Name: "command", Type: "string", Description: "Shell command to run", Required: true},
            {Name: "timeout_seconds", Type: "number", Description: "Max execution time", Required: false},
        },
        Execute: func(ctx context.Context, args map[string]any) (string, error) {
            image, _ := args["image"].(string)
            cmd, _ := args["command"].(string)
            timeout, _ := args["timeout_seconds"].(float64)
            if timeout == 0 {
                timeout = 30
            }
            // ... spin up Docker container, exec, capture output ...
            return output, nil
        },
    })
}
```

**Tool struct:**

| Field | Type | Required | Description |
|---|---|---|---|
| `Name` | `string` | Yes | Unique tool name, snake_case (max 64 chars) |
| `Description` | `string` | Yes | What the tool does and when to use it. This is read by the model — be specific. |
| `Parameters` | `[]Parameter` | No | JSON Schema-style parameter definitions |
| `Execute` | `func(ctx, args) (string, error)` | Yes | Execution function. Returns output string or error. Errors are surfaced to the model. |
| `AutoApprove` | `bool` | No | If true, tool runs without user approval prompt |

**Parameter struct:**

| Field | Type | Required | Description |
|---|---|---|---|
| `Name` | `string` | Yes | Parameter name, snake_case |
| `Type` | `string` | Yes | JSON type: `"string"`, `"number"`, `"boolean"`, `"object"`, `"array"` |
| `Description` | `string` | Yes | What the parameter does |
| `Required` | `bool` | No | Whether parameter is required |
| `Enum` | `[]string` | No | Allowed values for string parameters |

### Hook Plugin

React to lifecycle events. Equivalent to Pi's `pi.on()`.

```go
func init() {
    plugin.RegisterHook(hooks.PointBeforeTool, "docker_sandbox_guard", func(ctx context.Context, event hooks.Event) []hooks.Result {
        // Block dangerous commands even inside sandbox
        snapshot, ok := event.Snapshot.(hooks.ToolCallSnapshot)
        if !ok || snapshot.ToolName != "docker_sandbox_run" {
            return nil
        }
        cmd, _ := snapshot.Args["command"].(string)
        if strings.Contains(cmd, "rm -rf /") {
            return []hooks.Result{
                hooks.BlockResult{Message: "Destructive command blocked by sandbox guard", Provenance: "docker_sandbox_guard"},
            }
        }
        return nil
    })
}
```

**Available hook points** (from `hooks.Point*`):

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
| `PointChatParams` | API request params built | `ChatParamsSnapshot` |
| `PointChatHeaders` | API request headers built | `ChatHeadersSnapshot` |
| `PointEvent` | Any event emitted | `EventSnapshot` |

**Hook results** — return one or more:

| Result | Effect |
|---|---|
| `OverlayResult{Key, Content, Priority}` | Inject content into system prompt |
| `NoteResult{Message, Priority}` | Show informational note to user |
| `BlockResult{Message}` | Block the action. Chain stops here. |
| `nil` | No action — passes through |

**Priority:** `hooks.PriorityLow`, `PriorityNormal`, `PriorityHigh`.

### Skill Plugin

Provide skills the agent loads on-demand. Equivalent to Pi's `SKILL.md` directories.

```go
func init() {
    plugin.RegisterSkill(plugin.Skill{
        Name:        "docker-sandbox",
        Description: "Set up and use Docker sandboxes for isolated code execution. Use when the user asks to run untrusted code, test scripts in clean environments, or install packages safely.",
        Body: `## Docker Sandbox

## Setup
Run once per machine:
` + "`" + "`" + "`" + `bash
docker pull alpine:latest
docker pull python:3.12-slim
` + "`" + "`" + "`" + `

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
`,
    })
}
```

**Skill struct:**

| Field | Type | Description |
|---|---|---|
| `Name` | `string` | Lowercase letters, numbers, hyphens. 1-64 chars. |
| `Description` | `string` | When the model should load this skill. Be specific — this is the trigger. Max 1024 chars. |
| `Body` | `string` | Full markdown body loaded when skill activates. Can reference scripts, assets, etc. |
| `AllowedTools` | `[]string` | Optional: restrict what tools this skill can use |
| `DisableModelInvocation` | `bool` | If true, skill hidden from system prompt; must use `/skill:name` |

### Agent Plugin

Register a custom agent with its own system prompt and tool whitelist.

```go
func init() {
    plugin.RegisterAgent(plugin.Agent{
        Name:         "code-reviewer",
        Description:  "Reviews code changes for bugs, security issues, and style problems",
        SystemPrompt: "You are a thorough code reviewer. Focus on: correctness, security, performance, readability. Be concise and specific.",
        Tools:        []string{"read", "grep", "find", "code_search", "git_diff", "git_log"},
        Model:        "", // empty = use session default
        Fallbacks:    []string{},
    })
}
```

### MCP Server Plugin

Register MCP servers the agent can discover.

```go
func init() {
    plugin.RegisterMCPServer(plugin.MCPServer{
        Name:    "postgres",
        Command: []string{"npx", "-y", "@anthropic/mcp-server-postgres", "postgresql://localhost/mydb"},
        Env: map[string]string{
            "PGPASSWORD": "${PGPASSWORD}", // resolved from env at startup
        },
    })
}
```

### Command Plugin

Register CLI subcommands on the `forge` binary.

```go
func init() {
    plugin.RegisterCommand(plugin.Command{
        Name:        "plugin",
        Description: "Manage forge plugins",
        Handler: func(ctx context.Context, args []string) error {
            // custom CLI logic
            return nil
        },
    })
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
│   ├── docker_sandbox/
│   │   ├── docker_sandbox.go  # tool + skill registrations
│   │   └── docker_sandbox_test.go
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
    _ "forge/plugin/docker_sandbox"
    _ "forge/plugin/git_guard"
)
```

Adding a plugin to a Forge build is a single import line.

---

## Registration Reference

All functions in `forge/internal/plugin`:

```go
package plugin

// Tools
func RegisterTool(tool Tool)

// Hooks
func RegisterHook(point hooks.Point, name string, handler hooks.Handler)

// Skills
func RegisterSkill(skill Skill)

// Agents
func RegisterAgent(agent Agent)

// MCP Servers
func RegisterMCPServer(server MCPServer)

// CLI Commands
func RegisterCommand(cmd Command)

// Model Providers
func RegisterProvider(name string, config ModelProviderConfig)
```

All registrations use `init()` for auto-discovery. No manual wiring.

---

## Accessing Runtime Services

Plugins get a `context.Context` in their `Execute` and hook handler functions. Use the context to access Forge services:

```go
import "forge/internal/plugin"

func init() {
    plugin.RegisterTool(plugin.Tool{
        Name: "my_tool",
        Execute: func(ctx context.Context, args map[string]any) (string, error) {
            // Access the agent's working directory
            cwd := plugin.CWDFromContext(ctx)

            // Read a file (uses Forge's tool infrastructure, respects security policies)
            content, err := plugin.ReadFile(ctx, "path/to/file")

            // Run a shell command (uses Forge's approval system)
            output, err := plugin.Exec(ctx, "ls", "-la")

            // Access config
            cfg := plugin.ConfigFromContext(ctx)

            return output, nil
        },
    })
}
```

**Context helpers:**

| Function | Returns | Description |
|---|---|---|
| `CWDFromContext(ctx)` | `string` | Agent's working directory |
| `ConfigFromContext(ctx)` | `*config.Config` | Full Forge configuration |
| `ReadFile(ctx, path)` | `(string, error)` | Read file respecting security policies |
| `Exec(ctx, cmd, args...)` | `(string, error)` | Run command with approval integration |
| `SessionIDFromContext(ctx)` | `string` | Current session ID |
| `ToolRegistryFromContext(ctx)` | `*tools.Registry` | Full tool registry |

---

## Testing Plugins

Plugins are plain Go. Test with standard `go test`:

```go
package docker_sandbox

import (
    "context"
    "testing"
)

func TestSandboxTool(t *testing.T) {
    // The tool's Execute is a plain function — call it directly
    // Find the registered tool from the global registry
    tools := plugin.RegisteredTools()
    sandbox, ok := tools["docker_sandbox_run"]
    if !ok {
        t.Fatal("tool not registered")
    }

    output, err := sandbox.Execute(context.Background(), map[string]any{
        "image":   "alpine:latest",
        "command": "echo hello",
    })
    if err != nil {
        t.Fatal(err)
    }
    if output != "hello\n" {
        t.Fatalf("unexpected output: %q", output)
    }
}
```

For hooks, test the handler directly:

```go
func TestSandboxGuardBlocksDangerous(t *testing.T) {
    event := hooks.Event{
        Point: hooks.PointBeforeTool,
        Snapshot: hooks.ToolCallSnapshot{
            ToolName: "docker_sandbox_run",
            Args:     map[string]any{"command": "rm -rf / --no-preserve-root"},
        },
    }
    results := sandboxGuardHandler(context.Background(), event)
    if len(results) == 0 {
        t.Fatal("expected block result, got none")
    }
    block, ok := results[0].(hooks.BlockResult)
    if !ok {
        t.Fatalf("expected BlockResult, got %T", results[0])
    }
    if block.Message == "" {
        t.Fatal("expected non-empty block message")
    }
}
```

---

## Plugin Configuration

Plugins can be enabled/disabled in `forge.toml`:

```toml
[[plugins]]
id = "docker_sandbox"
kind = "native"       # native = compiled Go, external = JSON-RPC process
enabled = true

[[plugins]]
id = "hello"
kind = "native"
enabled = false       # disable without removing import
```

Native plugins are controlled purely by `enabled`. No `command`, `source`, or timeout fields needed — they run in-process.

### Plugin Settings

A plugin that implements the `Configurable` interface receives its `[plugins.settings]` table once at load:

```go
// implement on your plugin type
func (Plugin) Configure(settings map[string]any) { ... }
```

```toml
[[plugins]]
id = "sandbox"
kind = "native"
enabled = true

[plugins.settings]
default_on = true              # start session mode automatically
image = "golang:1.26"          # explicit image, or:
dockerfile = "Dockerfile.dev"  # built on demand, content-hash tagged
```

The sandbox plugin's image precedence: explicit `image` > `dockerfile` > project auto-detect.

---

## Migration from OpenCode Plugins

If you had an OpenCode (JS) plugin that:

1. **Registered a tool** → use `plugin.RegisterTool(Tool{...})`
2. **Intercepted events** → use `plugin.RegisterHook(point, name, handler)`
3. **Provided system prompt overlays** → use `plugin.RegisterHook(PointPromptContext, ...)` returning `OverlayResult`
4. **Exposed skills** → use `plugin.RegisterSkill(Skill{...})`
5. **Needed npm dependencies** → rewrite in Go or call external binaries via `plugin.Exec()`

The JSON-RPC transport for external plugins is preserved for non-Go use cases. Set `kind = "external"` in config and provide a `command` array.

---

## Example: Docker Sandbox Plugin (Complete)

```go
package docker_sandbox

import (
    "context"
    "fmt"
    "os/exec"
    "strings"
    "time"

    "forge/internal/hooks"
    "forge/internal/plugin"
)

func init() {
    // Register the sandbox tool
    plugin.RegisterTool(plugin.Tool{
        Name:        "docker_sandbox_run",
        Description: "Run a command in an ephemeral Docker container. Use for isolated code execution, package installs, or untrusted scripts. Container is destroyed after command completes.",
        Parameters: []plugin.Parameter{
            {Name: "image", Type: "string", Description: "Docker image (alpine:latest, python:3.12-slim, node:20-slim)", Required: true},
            {Name: "command", Type: "string", Description: "Shell command to execute", Required: true},
            {Name: "timeout_seconds", Type: "number", Description: "Max execution time (default 30, max 300)", Required: false},
            {Name: "network", Type: "string", Description: "Docker network mode (none, bridge, host; default none)", Required: false},
        },
        Execute: sandboxExecute,
    })

    // Register the skill that teaches the model how to use it
    plugin.RegisterSkill(plugin.Skill{
        Name:        "docker-sandbox",
        Description: "Set up and use Docker sandboxes for isolated code execution. Use when the user asks to run untrusted code, test scripts in clean environments, install packages safely, or needs a disposable Linux environment.",
        Body:        sandboxSkillBody,
    })

    // Guard against obviously destructive commands
    plugin.RegisterHook(hooks.PointBeforeTool, "docker_sandbox_guard", sandboxGuard)
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

func sandboxGuard(ctx context.Context, event hooks.Event) []hooks.Result {
    snapshot, ok := event.Snapshot.(hooks.ToolCallSnapshot)
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
            return []hooks.Result{
                hooks.BlockResult{
                    Message:    fmt.Sprintf("Destructive command blocked: %q matches dangerous pattern %q", cmd, d),
                    Provenance: "docker_sandbox_guard",
                },
            }
        }
    }
    return nil
}

var sandboxSkillBody = `## Docker Sandbox
...
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

1. Create a Go package with an `init()` function
2. Call `plugin.RegisterTool()`, `plugin.RegisterHook()`, `plugin.RegisterSkill()`, etc.
3. Add one import line to `plugins/imports.go`
4. Rebuild Forge

That's it. No manifest files, no JSON-RPC protocol, no JS shim.
