# New Tools Design

**Date:** 2026-03-22
**Status:** Approved

## Overview

Add 8 new baked-in tools to forge's chat agent, bringing the total from 6 to 14. All tools follow the existing `Tool` struct pattern in `internal/agent/tools/` and plug into the existing registry, approval system, and system prompt generation with zero protocol changes.

## New Tools

### web_fetch (web.go)

Fetch a URL and return its content as text.

- **Params:** `url` (string, required), `max_length` (int, optional, default 50000)
- **Auto-approve:** yes
- Stdlib `net/http`, 30s timeout, max 5 redirects
- Content-type handling: JSON returned as-is, HTML stripped to text via `golang.org/x/net/html` tokenizer, plain text returned raw
- Binary content types rejected with error message
- Truncates output at `max_length` bytes with "... truncated" suffix
- **SSRF protection:** validate scheme is http/https only, resolve hostname and reject private/reserved IPs (127.0.0.0/8, 10.0.0.0/8, 172.16.0.0/12, 192.168.0.0/16, 169.254.0.0/16, ::1, link-local IPv6) before connecting

### web_search (web.go)

Search the web via Brave Search API.

- **Params:** `query` (string, required), `count` (int, optional, default 5, max 10)
- **Auto-approve:** yes
- Calls `https://api.search.brave.com/res/v1/web/search`
- API key from `BRAVE_API_KEY` env var or `keys.brave` in config.toml
- Returns formatted results: title, URL, snippet per result
- Graceful degradation: if no key configured, returns "web_search unavailable: set BRAVE_API_KEY or keys.brave in config"

### git_status (git.go)

Show working tree status.

- **Params:** none
- **Auto-approve:** yes
- Runs `exec.Command("git", "status", "--porcelain")` in workDir

### git_diff (git.go)

Show changes in working tree or between refs.

- **Params:** `ref` (string, optional, default "HEAD")
- **Auto-approve:** yes
- Runs `exec.Command("git", "diff", ref)` in workDir — argument array, no shell interpolation
- Note: `git diff HEAD` shows staged + unstaged changes vs HEAD (differs from bare `git diff` which shows only unstaged). Default of HEAD is intentional so the LLM sees the full picture.
- Output truncated at 50KB

### git_log (git.go)

Show recent commit history.

- **Params:** `count` (int, optional, default 10)
- **Auto-approve:** yes
- Runs `exec.Command("git", "log", "--oneline", "-n", strconv.Itoa(count))` in workDir

### git_commit (git.go)

Stage all changes and commit.

- **Params:** `message` (string, required)
- **Auto-approve:** no (requires user approval)
- All git commands use `exec.Command` argument arrays, never shell strings
- Before committing, runs `git status --porcelain` to capture what will be staged
- Approval action shows the commit message as summary and the `git status` output as detail so the user can see exactly what will be committed
- Relies on `.gitignore` for excluding sensitive files (same as running `git add -A` manually)
- Executes: `git add -A` then `git commit -m <message>` as separate exec calls

### glob (glob.go)

Find files matching a glob pattern with `**` support.

- **Params:** `pattern` (string, required, e.g. `**/*.go`), `path` (string, optional, default ".")
- **Auto-approve:** yes
- Uses `github.com/bmatcuk/doublestar/v4` for `**` support
- **Path safety:** validates `path` via `ResolvePath(workDir, path)` from `safety.go` before globbing; filters results to only include paths within workDir
- Respects `chat.ignore_dirs` config (skips .git, node_modules, etc.)
- Limits output to 500 entries

### think (think.go)

Scratchpad for the agent to reason through a problem without producing visible output.

- **Params:** `thought` (string, required)
- **Auto-approve:** yes
- Returns `"ok"` — value is in the LLM having a structured place to plan

## Security

All tools follow these principles:

- **No shell interpolation:** git tools use `exec.Command` with argument arrays, never `sh -c` with string concatenation
- **Path containment:** glob and all file-related tools validate paths via `ResolvePath()` to stay within workDir
- **SSRF protection:** web_fetch validates URLs against private IP ranges before connecting
- **Scheme restriction:** web_fetch only allows http:// and https:// schemes
- **Approval gates:** destructive operations (git_commit) require explicit user approval with full context shown

## Config Changes

Add to `Keys` struct in `internal/config/config.go`:

```go
Brave string `toml:"brave"`
```

Add `BraveKey()` method following existing env-var-override pattern (`BRAVE_API_KEY`).

## Registration

Extract a `registerTools` helper to avoid duplicating 14 registration lines across `RunChatLive` and `RunChatConsole`:

```go
func registerTools(reg *tools.Registry, workDir string, cfg *config.Config, approve tools.ApprovalFunc) {
    reg.Register(tools.NewReadFile(workDir))
    reg.Register(tools.NewWriteFile(workDir, approve))
    reg.Register(tools.NewEditFile(workDir, approve))
    reg.Register(tools.NewListDir(workDir, cfg.Chat.IgnoreDirs))
    reg.Register(tools.NewSearch(workDir))
    reg.Register(tools.NewRunCommand(workDir, cfg.Chat.CommandTimeout, approve, approve))
    reg.Register(tools.NewWebFetch())
    reg.Register(tools.NewWebSearch(cfg.BraveKey()))
    reg.Register(tools.NewGlob(workDir, cfg.Chat.IgnoreDirs))
    reg.Register(tools.NewGitStatus(workDir))
    reg.Register(tools.NewGitDiff(workDir))
    reg.Register(tools.NewGitLog(workDir))
    reg.Register(tools.NewGitCommit(workDir, approve))
    reg.Register(tools.NewThink())
}
```

Note: `RunChatConsole` passes a different `forcePrompt` to `NewRunCommand` — the helper handles the common case and `RunChatConsole` can override `run_command` registration if needed.

## New Dependencies

- `github.com/bmatcuk/doublestar/v4` — glob `**` pattern matching
- `golang.org/x/net/html` — HTML tag stripping for web_fetch (new dependency, not currently transitive)

## Files Changed

| File | Change |
|------|--------|
| `internal/agent/tools/web.go` | New: web_fetch, web_search |
| `internal/agent/tools/git.go` | New: git_status, git_diff, git_log, git_commit |
| `internal/agent/tools/glob.go` | New: glob |
| `internal/agent/tools/think.go` | New: think |
| `internal/agent/tools/web_test.go` | Tests for web_fetch, web_search |
| `internal/agent/tools/git_test.go` | Tests for git tools |
| `internal/agent/tools/glob_test.go` | Tests for glob |
| `internal/agent/tools/think_test.go` | Tests for think |
| `internal/config/config.go` | Add Brave key to Keys struct + accessor |
| `internal/runtime/chat.go` | Extract registerTools helper, register new tools |
| `go.mod` / `go.sum` | New dependencies |

## What Doesn't Change

- Tool protocol (XML-wrapped JSON tool calls)
- Agent loop (`internal/agent/agent.go`)
- System prompt generation (`Registry.Describe()`)
- Approval system
- TUI rendering
- Event types
