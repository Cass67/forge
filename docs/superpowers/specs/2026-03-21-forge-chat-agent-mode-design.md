# forge chat — Agentic Mode

## Overview

A new `forge chat` command that turns forge into a conversational coding agent. The LLM gets 6 tools (read_file, write_file, edit_file, list_dir, search, run_command), operates on the user's real working directory, and streams a scrolling conversation in the terminal. Approval-based by default — file writes show diffs, commands require confirmation — with `--yolo` to skip approvals. Optional `--live` flag switches to the existing split-pane view for longer autonomous runs.

The existing batch writer/auditor pipeline stays untouched. Both modes share driver infrastructure, config, retry, and usage tracking.

## Architecture

```
cmd/forge/main.go
  "chat" subcommand -> runChat()
        |
internal/agent/agent.go
  Agent loop: send messages -> parse tool calls -> execute -> feed results back
  Holds: Driver, ToolRegistry, ApprovalFunc, conversation history
        |
internal/agent/tools/
  read.go  write.go  edit.go  list.go  search.go  command.go
  Each implements Tool interface
        |
internal/agent/approval.go
  ApprovalFunc — prompts user or auto-yes
  Shows diffs for writes, command text for exec
```

## Tool Interface

```go
// Tool defines a single tool the agent can call.
type Tool struct {
    Name        string
    Description string
    Parameters  []ParameterDef
    Execute     func(ctx context.Context, args map[string]any) (string, error)
}

// ParameterDef describes one parameter.
type ParameterDef struct {
    Name        string
    Type        string // "string", "int", "bool"
    Description string
    Required    bool
}

// Registry holds available tools.
type Registry struct {
    tools map[string]Tool
}

func (r *Registry) Register(t Tool)
func (r *Registry) Get(name string) (Tool, bool)
func (r *Registry) All() []Tool
func (r *Registry) Describe() string // formatted for system prompt injection
```

## Agent Loop

The core loop replaces the writer/auditor ping-pong with a single-agent tool-use cycle.

```go
type Agent struct {
    driver      llm.Driver
    tools       *Registry
    approve     ApprovalFunc
    history     []llm.Message
    systemPrompt string
    workDir     string
    maxTurns    int
    events      chan<- Event // optional, for live mode
}

func (a *Agent) Run(ctx context.Context, userMessage string) error
func (a *Agent) Chat(ctx context.Context, input <-chan string, render RenderFunc) error
```

### Run loop (single message):

1. Append user message to history
2. Build messages: system prompt + history
3. Call `driver.Stream()` (wrapped in retry driver) — collect full response
4. Parse response for `<tool_call>` blocks (ignore any inside markdown code fences)
5. If no tool calls: display response as final answer, return
6. For each tool call found in the response:
   - Look up tool in registry
   - Parse JSON args
   - If tool requires approval: call `ApprovalFunc`
     - If denied: append "Tool call denied by user" as result
     - If approved: execute tool, collect result string
7. Append assistant message (with tool calls) to history
8. Append tool results as a user message to history
9. Go to step 2
10. Safety: abort if turns exceed `maxTurns`

### Context management:

Conversation history grows with every tool call. To prevent exceeding context windows:
- Tool results are truncated to 30KB before appending to history
- When estimated token count exceeds 80% of model context, older tool results in history are replaced with one-line summaries (e.g., "[read_file main.go: 142 lines]")
- The system prompt and most recent 4 messages are always preserved in full
- `maxTurns` acts as a hard safety cap (default 50)

### Chat loop (interactive REPL):

1. Print prompt `forge>`
2. Read user input line
3. If empty or "exit"/"quit": return
4. Call `Run(ctx, input)`
5. Go to step 1

### Signal handling:

- First Ctrl+C during tool execution: cancels the current tool via context cancellation
- Second Ctrl+C (or Ctrl+C at the prompt): exits the REPL
- Ctrl+C during LLM streaming: cancels the current generation, returns to prompt

## Tool Call Protocol

Since the `Driver` interface is text-in/text-out with no native function calling, tool use is prompt-based:

```
You have access to the following tools:

## read_file
Read a file's contents. Returns content with line numbers.
Parameters:
  - path (string, required): file path relative to working directory
  - start_line (int, optional): first line to read (1-indexed)
  - end_line (int, optional): last line to read

## write_file
Create or overwrite a file.
Parameters:
  - path (string, required): file path
  - content (string, required): full file content

## edit_file
Make a search-and-replace edit within a file.
Parameters:
  - path (string, required): file path
  - old_text (string, required): exact text to find (must be unique in file)
  - new_text (string, required): replacement text

## list_dir
List directory contents.
Parameters:
  - path (string, optional, default "."): directory path
  - recursive (bool, optional, default false): list recursively

## search
Search for a pattern across files.
Parameters:
  - pattern (string, required): regex pattern
  - path (string, optional, default "."): directory to search
  - glob (string, optional): file pattern filter (e.g. "*.go")

## run_command
Execute a shell command.
Parameters:
  - command (string, required): command to run

To call a tool, use this exact format:

<tool_call>
{"name": "tool_name", "args": {"param": "value"}}
</tool_call>

You may call multiple tools in sequence. After each tool call, you will receive the result.
Wait for results before making decisions based on them.
```

The agent loop parses `<tool_call>...</tool_call>` blocks from streamed output. The parser collects all tool_call blocks from a single response and executes them sequentially, feeding all results back in a single tool-results message before the next LLM call. Tool_call blocks inside markdown code fences (triple backticks) are ignored — only top-level blocks are executed. Text outside tool_call blocks is the agent's reasoning/explanation and gets displayed directly.

## Approval

```go
type Action struct {
    Tool    string // tool name
    Summary string // one-line description
    Detail  string // diff content, command text, or file content
}

type ApprovalFunc func(action Action) (approved bool, err error)
```

### Default approval rules:

| Tool | Approval |
|------|----------|
| read_file | auto |
| list_dir | auto |
| search | auto |
| write_file | prompt with diff |
| edit_file | prompt with diff |
| run_command | prompt showing command |

### Approval prompt format:

For file writes/edits:
```
● edit_file server.go
  @@ -23,6 +23,9 @@
  -    json.NewDecoder(r.Body).Decode(&item)
  +    if err := json.NewDecoder(r.Body).Decode(&item); err != nil {
  +        http.Error(w, "invalid request body", 400)
  +        return
  +    }
  apply? [y/n] _
```

For commands:
```
● run_command
  go test ./...
  run? [y/n] _
```

### --yolo mode:

`ApprovalFunc` returns `(true, nil)` unconditionally. All tool calls execute without prompting.

## Terminal Rendering (default conversational mode)

No framework. Plain `fmt.Print` + `bufio.Scanner` + ANSI colors.

```go
type Renderer struct {
    out    io.Writer
    width  int
    colors bool
}

func (r *Renderer) AgentText(text string)           // bright white
func (r *Renderer) ToolCall(name, summary string)    // dimmed, prefixed with ●
func (r *Renderer) ToolResult(name, output string)   // dimmed, truncated
func (r *Renderer) Diff(path, diff string)           // red/green unified diff
func (r *Renderer) Approval(prompt string) bool      // shows prompt, reads y/n
func (r *Renderer) UserPrompt() string               // "forge> " prompt, reads line
func (r *Renderer) Error(msg string)                 // red
func (r *Renderer) Done(summary string)              // green checkmark
```

Streaming: as tokens arrive from `driver.Stream()`, the renderer prints agent text immediately. When a `<tool_call>` block starts being detected, buffering begins until the block is complete, then the tool is executed and result rendered.

## Live Mode (--live)

When `--live` is passed, the agent loop emits events to a channel instead of rendering inline. The existing `tui.RunLive` infrastructure displays them in the split-pane tcell view.

Left pane: agent reasoning text.
Right pane: tool calls and their output.

Uses the existing `llm.Event` types plus new event kinds:

```go
EventToolCall   EventKind = "tool_call"   // Agent field = tool name, Text = JSON args
EventToolResult EventKind = "tool_result" // Agent field = tool name, Text = result output
```

The live mode adapter wraps the agent's render functions to emit events instead of printing.

## The 6 Tools — Implementation Detail

### read_file

- Resolves path relative to workDir via `filepath.Abs` + `filepath.Clean` + `filepath.EvalSymlinks`
- Rejects any resolved path that does not have workDir as prefix (catches `..`, symlink escapes, absolute paths)
- If start_line/end_line provided, reads only that range
- Returns content prefixed with line numbers: `  1 | package main`
- Files over 200KB: error suggesting line range
- Binary file detection: error with message
- Auto-approved

### write_file

- Resolves path relative to workDir, rejects escape
- Creates parent directories
- If file exists: generates unified diff (old vs new)
- If file is new: shows "new file" with content preview (first 20 lines)
- Requires approval (shows diff)
- After write: reports bytes written

### edit_file

- Reads current file content
- Finds `old_text` in file — if not found or multiple matches, returns descriptive error string as tool result (not a Go error) so the LLM can adjust (e.g., "edit_file failed: old_text matched 3 locations in server.go; provide more surrounding context to make the match unique")
- Replaces with `new_text`
- Generates unified diff of the change (with context lines)
- Requires approval (shows diff)
- Writes file back

### list_dir

- Resolves path relative to workDir
- Non-recursive: `os.ReadDir`, format as list with type indicators (dir/, file)
- Recursive: `filepath.WalkDir`, tree-style output
- Skips: `.git`, `node_modules`, `__pycache__`, `.venv`, `vendor` (configurable)
- Caps at 500 entries with "... and N more" truncation
- Auto-approved

### search

- Tries `rg` first (ripgrep), falls back to `grep -rn`
- Pattern is regex
- Glob filter passed as `--glob` to rg or `--include` to grep
- Returns `file:line:content` format
- Caps at 100 matches with "... N more matches" truncation
- Respects .gitignore
- Auto-approved

### run_command

- Runs via `exec.Command("sh", "-c", command)`
- Sets working directory to workDir
- Captures stdout + stderr combined
- Timeout from config (default 60s), enforced via context
- Returns exit code + output
- Output capped at 50KB with truncation notice
- Requires approval (even in --yolo mode, commands matching destructive patterns like `rm -rf /`, `sudo`, pipe-to-shell `| sh`/`| bash` still prompt for confirmation)
- Approval prompt always shows the full untruncated command

## Config Additions

```toml
[chat]
model = ""              # empty = use models.writer
max_turns = 50          # safety limit per user message
command_timeout = 60    # seconds
yolo = false            # default approval mode
ignore_dirs = [".git", "node_modules", "__pycache__", ".venv", "vendor"]
```

Environment variable overrides:
- `FORGE_CHAT_MODEL` overrides `chat.model`
- `FORGE_CHAT_YOLO=1` overrides `chat.yolo`

## CLI Interface

```
forge chat [flags]

Flags:
  --yolo          Skip all approval prompts
  --live          Use split-pane live view
  --model MODEL   Override chat model
  -C PATH         Set working directory (default: cwd)
```

## System Prompt

```
You are forge, a coding agent. You work in the user's project directory.

Working directory: {workDir}
Files detected: {file count and primary language}

{tool descriptions from Registry.Describe()}

Guidelines:
- Read files before editing them. Understand what you're changing.
- Use edit_file for surgical changes to existing files. Use write_file only for new files or complete rewrites.
- After making changes, run relevant tests or build commands to verify.
- Explain what you're doing and why before making changes.
- If something fails, read the error, diagnose, and fix. Don't repeat the same failing approach.
- Ask the user for clarification if the request is ambiguous.
```

## New File Layout

```
internal/agent/
    agent.go          — Agent struct, Run/Chat loop, message management
    agent_test.go     — Tests with mock driver
    approval.go       — ApprovalFunc, default and yolo implementations
    approval_test.go
    parse.go          — Tool call block parser (<tool_call> extraction)
    parse_test.go
    render.go         — Terminal renderer (colors, diffs, prompts)
    render_test.go
    system.go         — System prompt builder
    tools/
        registry.go   — Tool interface, Registry, Describe()
        registry_test.go
        read.go       — read_file implementation
        read_test.go
        write.go      — write_file implementation
        write_test.go
        edit.go       — edit_file implementation
        edit_test.go
        list.go       — list_dir implementation
        list_test.go
        search.go     — search implementation
        search_test.go
        command.go    — run_command implementation
        command_test.go
        safety.go     — Path resolution, escape detection, binary check
        safety_test.go
```

## Config Changes

Add to `internal/config/config.go`:

```go
type ChatConfig struct {
    Model          string   `toml:"model"`           // empty = use models.writer
    MaxTurns       int      `toml:"max_turns"`       // default 50
    CommandTimeout int      `toml:"command_timeout"`  // seconds, default 60
    Yolo           bool     `toml:"yolo"`            // default false
    IgnoreDirs     []string `toml:"ignore_dirs"`     // default [".git", "node_modules", ...]
}
```

Add `Chat ChatConfig` field to `Config` struct. Set defaults in `setDefaults()`.

## What Stays the Same

- `forge` (no subcommand) — batch writer/auditor TUI
- `forge improve` — batch codebase improvement
- `forge list` / `forge show` — session history
- All drivers, config, retry, usage tracking — shared
- No changes to internal/session/, internal/tui/, or internal/output/

## Future Extensions (not in this spec)

- Session persistence for chat (save/resume conversations)
- MCP server support (expose forge tools via Model Context Protocol)
- Multi-file edit transactions (atomic apply/rollback)
- Native function calling when driver supports it (Claude, OpenAI)
- Chat history in `forge show`
