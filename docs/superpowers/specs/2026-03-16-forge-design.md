# forge — Design Spec
**Date:** 2026-03-16
**Status:** Approved

---

## Overview

`forge` is a terminal UI tool that takes a natural language idea and produces production-ready code by passing it through a pipeline of writer and auditor LLM agents. A writer agent implements, an auditor critiques, a cheap summarizer distills each exchange into shared memory. Four sequential passes (correctness → refactor → security → prod-ready) with N configurable rounds per pass. Output is code + audit trail on disk.

**Platform:** macOS (primary). Linux supported. Windows out of scope for v1.

---

## Technology Stack

- **Language:** Go
- **TUI framework:** Bubble Tea (Charm ecosystem)
- **Styling:** Lipgloss
- **Markdown rendering:** Glamour
- **LLM SDKs:** Anthropic Go SDK, OpenAI Go SDK
- **Config format:** TOML
- **Distribution:** single compiled binary

---

## Architecture

```
┌─────────────────────────────────────────┐
│                  forge                  │
│                                         │
│  ┌──────────┐      ┌──────────────────┐ │
│  │  Input   │─────▶│  Session Runner  │ │
│  │  Screen  │      │                  │ │
│  └──────────┘      │  Pass 1: correct │ │
│                    │  Pass 2: refactor│ │
│  ┌──────────┐      │  Pass 3: security│ │
│  │   TUI    │◀─────│  Pass 4: prod    │ │
│  │  View    │      └──────────────────┘ │
│  │(split/   │               │           │
│  │ yolo)    │      ┌────────▼─────────┐ │
│  └──────────┘      │   LLM Registry   │ │
│                    │  claude / openai │ │
│                    │  (pluggable)     │ │
│                    └──────────────────┘ │
└─────────────────────────────────────────┘
         │ on complete
         ▼
   <output_dir>/<timestamp>/
   ├── code/
   ├── summary-store.md
   ├── audit-log.md
   └── session.json
```

### Components

- **Session Runner** — owns the state machine, orchestrates passes and rounds, spawns subagents, coordinates writes to summary store
- **LLM Registry** — thin driver abstraction over provider SDKs; each model is a registered driver implementing a `Driver` interface
- **TUI** — Bubble Tea app with two view modes (split pane / yolo), driven by session events streamed over channels
- **Summarizer** — cheap model (haiku/gpt-4o-mini) that runs after each round and distills the exchange into the summary store
- **Output Writer** — manages `<output_dir>/<timestamp>/` directory, writes code files on each round, finalises audit log and session metadata on completion

---

## LLM Integration

### Core Types

```go
// Role identifies the speaker in a conversation.
type Role string

const (
    RoleSystem    Role = "system"
    RoleUser      Role = "user"
    RoleAssistant Role = "assistant"
)

// Message is a single turn in a conversation sent to an LLM.
type Message struct {
    Role    Role
    Content string  // plain text; files are inlined by the runner before sending
}

// Token is a single streamed chunk from an LLM response.
type Token struct {
    Text string  // the delta text for this chunk
    Done bool    // true on the final chunk; Text may be empty
    Err  error   // non-nil if the stream failed
}
```

### Driver Interface

```go
type Driver interface {
    // Name returns the canonical model identifier (e.g. "claude-sonnet-4-6").
    Name() string

    // Stream sends messages to the model and streams response tokens to out.
    // The caller closes ctx to cancel. The driver closes out when done or on error.
    Stream(ctx context.Context, messages []Message, out chan<- Token) error
}
```

Two drivers ship by default: `claude` (Anthropic SDK) and `openai` (OpenAI SDK). Adding further providers requires implementing this interface and registering the driver — nothing else changes.

### Role Assignment & Model Cycling

Writer and auditor are independent model slots on the input screen. `Tab` alternates **focus** between the writer and auditor slots — the focused slot is highlighted with a border. `Left`/`Right` arrow keys cycle through registered models for the currently focused slot. `Tab` only moves focus; it does not cycle models. If only one provider is configured, the unfocused slot shows the same model as read-only and arrow keys have no effect on it.

### Code Extraction from Writer Output

The writer is instructed via its system prompt to wrap all code in fenced blocks with a filename annotation:

~~~
```go:internal/limiter/limiter.go
package limiter
...
```
~~~

The runner parses all fenced blocks from the writer's completed response. Each block is written to `<output_dir>/<timestamp>/code/<filename>`. Filenames must be relative paths and must not contain `..` (enforced by the runner). Prose outside fenced blocks is discarded for code purposes but forwarded to the summarizer. If the writer produces no fenced blocks, a warning is appended to `session.json` and the round continues with unchanged code.

### Multi-File Output

The writer may produce multiple fenced blocks per response, each with a different filename. All are extracted and written. Files not mentioned in a writer response are left unchanged on disk.

### Context File Attachment

The input screen shows an attachment section. Pressing `a` opens an inline text prompt:

```
  Attach file: [_________________________]
  (enter) confirm   (esc) cancel
```

The user types an absolute or `~/`-relative path. On enter: if the file exists and is readable, it appears in the attachment list; if not, an inline error is shown and the prompt stays open. Attached files are listed below the prompt with `(x)` to remove. There is no limit on the number of attached files for v1.

Attached files are read at session start and inlined into the writer's first message (pass 1, round 1) as fenced code blocks with a preamble note. From round 2 onward the runner uses code files from `<output_dir>/<timestamp>/code/` as the ground truth.

### Language Hint

The language hint field (default: `auto`) is injected into the writer's system prompt for all passes and all rounds. When `auto`, the writer infers language from the prompt and context files. When set explicitly (e.g. `go`, `python`, `typescript`), the system prompt instructs the writer to produce that language. The auditor is not constrained by this field.

### Context Strategy (Summary Store Pattern)

The runner constructs each agent's message list as:

```
[system message: role-specific system prompt + pass instructions]
[user message: inlined current code files + full summary store text]
```

**Code inlining:** The runner reads all files under `<output_dir>/<timestamp>/code/` and inlines them as fenced blocks before sending to any agent. No agent has filesystem access.

No raw conversation history is ever passed. The summary store is the sole shared memory between rounds and passes.

### Summarizer Message Schema

The summarizer receives a two-message conversation:

```
[system message: "You are a concise technical summarizer. Given a writer's code output and an auditor's critique from one round, produce a structured summary entry with four sections: Writer, Auditor, Decisions, Outstanding. Be brief. No code blocks."]
[user message:
  "WRITER OUTPUT:\n<full writer response text>\n\nAUDITOR CRITIQUE:\n<full auditor response text>"]
```

The summarizer's system prompt is baked in (not configurable). The response is appended verbatim to `summary-store.md` under the appropriate pass/round heading.

---

## Agent Pipeline

### Session State Machine

```
[startup] → [idle] → [input] → [running] ⇄ [paused]
                                    │
                              [complete] or [aborted]
```

States:
- `startup` — startup checks running (auth ping etc); user sees a loading screen
- `idle` — checks passed; input screen shown
- `input` — user is filling the input form
- `running` — passes and rounds executing
- `paused` — user pressed `p`; current in-flight API call is awaited to completion (not cancelled), then execution halts before the next subagent spawn; `p` resumes from the next round
- `complete` — all passes done; Done screen shown
- `aborted` — fatal error or user quit mid-session; partial output written to disk; Abort screen shown

### The 4 Passes

| Pass | Writer goal | Auditor focus |
|------|-------------|---------------|
| 1. Correctness | Implement the idea | Logic errors, missing cases, does it work |
| 2. Refactor | Clean it up | Naming, structure, DRY, complexity |
| 3. Security | Harden it | OWASP top 10, injection, auth, secrets, input validation |
| 4. Prod-ready | Final polish | Error handling, logging, config, graceful shutdown |

Each pass hands its output to the next via the summary store and code files on disk.

### Round Loop (Sequential)

Within each pass, writer and auditor execute **sequentially** — the auditor runs only after the writer's output has been written to disk:

```
round N:
  1. runner inlines code + summary store → writer call
     writer streams tokens → TUI left pane (split) or yolo feed
     on stream complete → parse fenced blocks → write code files

  2. runner inlines updated code + summary store → auditor call
     auditor streams tokens → TUI right pane (split) or yolo feed
     on stream complete → critique buffered

  3. summarizer call (writer text + auditor text)
     on complete → append entry to summary-store.md

  4. update session.json
```

**TUI display:** In split pane mode, the left pane shows the writer streaming first, then goes idle while the right pane shows the auditor streaming. The panes are not simultaneously active — the layout is persistent, but streaming is one pane at a time. In yolo mode, tokens are labelled and appended sequentially.

### System Prompts

Pass system prompts are baked in via `go:embed` in `prompts/`. Configurable per-pass via the `[passes]` config section pointing to external markdown files.

---

## TUI Layout & Interaction

### Screen 0 — Startup

```
╔══════════════════════════════════════════════════╗
║  forge  v0.1.0                                   ║
╠══════════════════════════════════════════════════╣
║                                                  ║
║  Checking configuration...                       ║
║                                                  ║
║  ✓ ANTHROPIC_API_KEY                             ║
║  ✓ OPENAI_API_KEY                                ║
║  ● Pinging claude-sonnet-4-6...                  ║
║                                                  ║
╚══════════════════════════════════════════════════╝
```

Each check is shown as it completes. On error:

```
║  ✗ claude-sonnet-4-6: auth failed (401)          ║
║                                                  ║
║  Check ANTHROPIC_API_KEY and try again.  (q)quit ║
```

### Screen 1 — Input

```
╔══════════════════════════════════════════════════╗
║  forge  v0.1.0                                   ║
╠══════════════════════════════════════════════════╣
║                                                  ║
║  What do you want to build?                      ║
║  ┌────────────────────────────────────────────┐  ║
║  │ a rate limiter middleware for a Go HTTP    │  ║
║  │ server with redis backend_                 │  ║
║  └────────────────────────────────────────────┘  ║
║                                                  ║
║  Context files:  (a) attach                      ║
║                                                  ║
║  Writer:  [ claude-sonnet-4-6 ] ←focus           ║
║  Auditor: [ gpt-4o            ]                  ║
║                                                  ║
║  Rounds per pass: [3]   Language hint: [auto]    ║
║                                                  ║
║  (enter) Start   (tab) Shift slot focus  (←/→) Cycle ║
║  (q) Quit                                        ║
╚══════════════════════════════════════════════════╝
```

**`rounds_per_pass` validation:** minimum 1, maximum 10. Non-integer or out-of-range input is rejected with an inline error; field reverts to previous valid value.

### Screen 2a — Split Pane (default)

```
╔══════════════════════╦═══════════════════════════╗
║ PASS 2/4: refactor   ║  AUDIT  round 2/3         ║
║ round 2/3  claude    ║  gpt-4o          [idle]   ║
╠══════════════════════╬═══════════════════════════╣
║ func NewLimiter(...) ║                           ║
║   r := &Limiter{     ║  (waiting for writer)     ║
║     rate: rate,      ║                           ║
║   }                  ║                           ║
║   return r, nil      ║                           ║
║ }                    ║                           ║
║ [streaming...]       ║                           ║
╠══════════════════════╩═══════════════════════════╣
║ [v] yolo  [p] pause  [s] snapshot  [q] quit      ║
╚══════════════════════════════════════════════════╝
```

Writer streams in the left pane. Right pane shows `(waiting for writer)` until writer is done, then auditor streams in the right pane.

### Screen 2b — Yolo Mode

```
╔══════════════════════════════════════════════════╗
║ PASS 2/4: refactor  round 2/3     [v] split view ║
╠══════════════════════════════════════════════════╣
║ WRITER (claude)                                  ║
║ > Here's the refactored rate limiter...          ║
║                                                  ║
║ AUDITOR (gpt-4o)                                 ║
║ > The refactor is cleaner but I see an issue...  ║
║                                                  ║
║ WRITER (claude)  [streaming...]                  ║
║ > Good catch. Fixing error propagation now...    ║
╠══════════════════════════════════════════════════╣
║ [v] split  [p] pause  [s] snapshot  [q] quit     ║
╚══════════════════════════════════════════════════╝
```

Tokens labelled and appended sequentially. View auto-scrolls; user can scroll back.

### Screen 3 — Done

```
╔══════════════════════════════════════════════════╗
║  forge  — session complete                       ║
╠══════════════════════════════════════════════════╣
║  ✓ Pass 1: correctness   (3 rounds)              ║
║  ✓ Pass 2: refactor      (3 rounds)              ║
║  ✓ Pass 3: security      (3 rounds)              ║
║  ✓ Pass 4: prod-ready    (3 rounds)              ║
║                                                  ║
║  Output: /Users/cass/forge-output/2026-03-16T... ║
║    code/                                         ║
║    audit-log.md                                  ║
║    session.json                                  ║
║                                                  ║
║  (o) open in Finder   (n) new session  (q) quit  ║
╚══════════════════════════════════════════════════╝
```

`(o)` runs `open <output_dir>/<timestamp>` (macOS). On Linux, `xdg-open`. Done screen always shows the resolved absolute path.

### Screen 4 — Error Overlay

Shown when an API call fails. Overlaid on the current running screen.

```
╔══════════════════════════════════════════════════╗
║  ⚠  API error — openai                          ║
║                                                  ║
║  rate limit exceeded (429)                       ║
║  Pass 3, Round 2 — auditor call                  ║
║                                                  ║
║  (e) retry   (q) abort and save                  ║
╚══════════════════════════════════════════════════╝
```

**Retry boundary:** `(e)` retries only the specific call that failed (writer, auditor, or summarizer). If the writer streamed partial tokens before failing, the writer pane is cleared and the call restarts from scratch. If the auditor failed, the writer output is already on disk and only the auditor call is re-sent.

**Retry counter:** Each individual API call has its own consecutive failure counter, reset to zero on success. Failures on the writer do not count toward the auditor's counter. Three consecutive failures on the same specific call trigger automatic abort.

**`q` from error overlay:** The error overlay is shown when an API call has already failed — there is no in-flight call at this point. `(q)` from the error overlay writes whatever is currently on disk and shows the Abort screen. This is the same outcome as `q` during running state; the difference is that running-state `q` also cancels an active context before writing.

### Screen 5 — Abort Screen

Shown after automatic 3× failure abort or after `(q)` from error overlay.

```
╔══════════════════════════════════════════════════╗
║  forge  — session aborted                        ║
╠══════════════════════════════════════════════════╣
║  ✓ Pass 1: correctness   (3 rounds)              ║
║  ✓ Pass 2: refactor      (3 rounds)              ║
║  ✗ Pass 3: security      (2/3 rounds — aborted)  ║
║                                                  ║
║  Reason: openai API failed 3× (429)              ║
║                                                  ║
║  Partial output saved to:                        ║
║  /Users/cass/forge-output/2026-03-16T...         ║
║                                                  ║
║  (o) open in Finder   (n) new session  (q) quit  ║
╚══════════════════════════════════════════════════╝
```

### Keybindings

| Key | Screen | Action |
|-----|--------|--------|
| `v` | Running | Toggle split pane / yolo mode |
| `p` | Running | Pause (awaits current in-flight call to finish, then halts before next subagent spawn) |
| `p` | Paused  | Resume from next subagent call |
| `s` | Running / Paused | Snapshot: copy the current `code/` directory to `<session-dir>/snapshots/<timestamp>/`. Preserves a point-in-time copy that won't be overwritten by future rounds. If called mid-stream, copies whatever is on disk from the previous completed round. |
| `e` | Error overlay | Retry the specific failed API call (writer, auditor, or summarizer — not the full round) |
| `tab` | Input | Shift focus between writer and auditor model slots |
| `←` / `→` | Input | Cycle model for the currently focused slot |
| `a` | Input | Open file path prompt to attach context file |
| `o` | Done / Abort | Open output directory (`open` on macOS, `xdg-open` on Linux) |
| `n` | Done / Abort | New session — return to input screen preserving model selections, rounds, and language hint; clear prompt text and context files |
| `q` | Any except running / error overlay | Quit immediately |
| `q` | Running | Cancel active context immediately (in-flight streaming tokens lost), write current disk state, show Abort screen |
| `q` | Error overlay | No in-flight call; write current disk state, show Abort screen |

---

## Data Flow & Session Lifecycle

```
1. USER INPUT
   prompt + optional attached files + rounds-per-pass (1–10)
   writer model + auditor model + language hint

2. SESSION INIT
   resolve output path: <output_dir>/<timestamp>/
   create code/ subdirectory
   write session.json (initial state — see schema below)
   create summary-store.md (empty)
   inline context files into pass 1 round 1 writer message

3. FOR EACH PASS (1→4):
   FOR EACH ROUND (1→N):
     a. runner reads all files under code/
        constructs writer messages
        spawns Writer → streams tokens to TUI
        on complete: parse fenced blocks → write/overwrite code files
        on no fenced blocks: append warning to session.json, continue

     b. runner reads updated code files
        constructs auditor messages
        spawns Auditor → streams tokens to TUI
        on complete: critique text buffered

     c. spawns Summarizer (writer text + auditor text)
        on complete: append round entry to summary-store.md
        on failure: append placeholder entry, append warning to session.json

     d. update session.json pass/round state

4. SESSION COMPLETE
   generate audit-log.md via final summarizer call on full summary-store.md
   update session.json status → "complete", set completed_at
   display Done screen
```

### Output Path Resolution

The timestamp subdirectory is always created under the configured `output_dir` (default `./output`). The path is resolved at session init and used as the absolute base for all writes. Tilde expansion is applied to `output_dir` at config load time.

### Disk Layout

```
<output_dir>/2026-03-16T02-14-00/
├── code/
│   └── <output files>               ← written/overwritten each round
├── snapshots/                        ← created only if user presses (s)
│   └── 2026-03-16T02-18-00/
│       └── <point-in-time copy of code/>
├── summary-store.md                  ← raw append log, one entry per round
├── audit-log.md                      ← final human-formatted report (complete sessions only)
└── session.json                      ← full session metadata
```

### summary-store.md

Raw append log updated after every round. Used as shared memory by agents.

```markdown
## Pass 1 · Round 1
**Writer:** Implemented rate limiter with redis backend.
**Auditor:** Missing error handling on redis.Do(). Magic number 1000 on line 12.
**Decisions:** Token bucket algorithm. Redis key: ratelimit:<ip>.
**Outstanding:** Error propagation, extract constants.
```

On summarizer failure, a placeholder is written:

```markdown
## Pass 1 · Round 2
**[summarizer failed — entry unavailable]**
```

### audit-log.md

Generated at session end by a final summarizer call. The call uses the same `Driver.Stream` interface; all tokens are buffered before writing the file. The call is not shown in the TUI — a "generating audit log..." status line is shown on the Done screen while it runs.

**Audit-log summarizer message schema:**

```
[system message:
  "You are a technical writer. Given the full round-by-round audit log from a
   code generation session, produce a concise structured markdown report with
   these sections: ## What Was Built, ## Key Decisions, ## Security Issues Found
   and Resolved, ## Remaining Concerns. If there are no remaining concerns, omit
   that section. Be specific. No code blocks."]

[user message:
  "FULL SESSION LOG:\n<full contents of summary-store.md>"]
```

The system prompt is embedded in `prompts/audit-log.md`. This is the human deliverable. It is generated only on successful session completion (`status: complete`). On abort, the audit-log summarizer call is skipped — `summary-store.md` and `session.json` are the only records available. The Abort screen does not mention `audit-log.md`.

### session.json Schema

Initial state (written at session init):

```json
{
  "id": "2026-03-16T02-14-00",
  "prompt": "a rate limiter middleware...",
  "language_hint": "auto",
  "writer": "claude-sonnet-4-6",
  "auditor": "gpt-4o",
  "summarizer": "claude-haiku-4-5-20251001",
  "rounds_per_pass": 3,
  "context_files": ["main.go"],
  "passes": [],
  "status": "running",
  "warnings": [],
  "started_at": "2026-03-16T02:14:00Z",
  "completed_at": null
}
```

Mid-session (updated after each round, `passes` grows):

```json
"passes": [
  { "name": "correctness", "rounds_completed": 2, "status": "running" }
]
```

Completed state:

```json
"passes": [
  { "name": "correctness",  "rounds_completed": 3, "status": "complete" },
  { "name": "refactor",     "rounds_completed": 3, "status": "complete" },
  { "name": "security",     "rounds_completed": 3, "status": "complete" },
  { "name": "prod",         "rounds_completed": 3, "status": "complete" }
],
"status": "complete",
"completed_at": "2026-03-16T02:28:43Z"
```

Aborted state:

```json
"passes": [
  { "name": "correctness", "rounds_completed": 3, "status": "complete" },
  { "name": "security",    "rounds_completed": 2, "status": "aborted" }
],
"status": "aborted",
"abort_reason": "openai API failed 3 consecutive times (429)",
"completed_at": "2026-03-16T02:22:11Z"
```

---

## Configuration

**`~/.config/forge/config.toml`:**

```toml
[models]
writer     = "claude-sonnet-4-6"
auditor    = "gpt-4o"
summarizer = "claude-haiku-4-5-20251001"

[session]
rounds_per_pass = 3          # valid range: 1–10
output_dir      = "~/forge-output"

[keys]
# prefer env vars — these are fallbacks
anthropic = ""
openai    = ""

[passes]
# optional overrides for built-in pass prompts (path to markdown file)
# correctness = "~/.config/forge/prompts/correctness.md"
```

**Environment variables:** `ANTHROPIC_API_KEY`, `OPENAI_API_KEY` — take precedence over config file.

---

## Error Handling

| Error | Behaviour |
|-------|-----------|
| API key missing | Fail at startup with message: `error: ANTHROPIC_API_KEY not set` |
| Startup auth ping fails | Show failure on startup screen, exit with message |
| API call fails (rate limit / timeout / 5xx) | Show error overlay, offer `(e) retry` or `(q) abort` |
| Same API call fails 3× | Automatic abort — show Abort screen, write partial output |
| Writer produces no fenced code blocks | Append warning to session.json, continue with unchanged code |
| Summarizer fails | Write placeholder to summary-store.md, append warning to session.json, continue |
| Output dir unwritable | Fail at session start before any API calls |
| User hits `q` mid-session | Graceful stop, write current state to disk, show Abort screen |
| User pauses then quits | Same as mid-session quit |

### Startup Checks

1. **API key presence** — check env vars and config for each configured provider
2. **Auth ping** — send a `messages` API call with `max_tokens: 1` using the configured writer model (Anthropic) and auditor model (OpenAI). Pass = 200 response. Failure = show provider name + HTTP status + hint on startup screen, then exit
3. **Output directory** — verify `output_dir` is writable (create + delete temp file)
4. **Config parse** — warn on unknown TOML keys to stderr; do not exit

---

## Project Structure

```
forge/
├── cmd/
│   └── forge/
│       └── main.go
├── internal/
│   ├── tui/          # Bubble Tea app, all screens, keybindings
│   ├── session/      # state machine, pass runner, round loop
│   ├── llm/          # Driver interface + claude/openai implementations
│   ├── summarizer/   # summary store read/write/append, summarizer agent
│   ├── output/       # disk writer, code file parser, audit log formatter
│   └── config/       # TOML config loader, key resolution, startup checks
├── prompts/          # built-in pass + summarizer system prompts (go:embed)
│   ├── correctness.md
│   ├── refactor.md
│   ├── security.md
│   ├── prod.md
│   ├── summarizer.md    # per-round summarizer system prompt
│   └── audit-log.md     # end-of-session audit log system prompt
├── docs/
│   └── superpowers/
│       └── specs/
│           └── 2026-03-16-forge-design.md
├── go.mod
├── go.sum
└── README.md
```

---

## Extension Points & Modularity

This section documents where future changes can be made without cascading rewrites. The design is intentionally structured around these seams.

### 1. New LLM Providers
Implement `Driver` and register it. No other code changes. The session runner, TUI, and summarizer are all provider-agnostic.

### 2. New or Reordered Passes
Passes are defined as a slice of `Pass` structs in `internal/session/`. Adding, removing, or reordering passes is a data change, not a structural one. Each pass carries its name, system prompt path, and writer/auditor role assignment. The round loop is generic.

### 3. New View Modes
The session runner communicates with the TUI exclusively via a typed event channel (`chan Event`). The TUI consumes events and renders — it does not call back into the runner. Adding a third view mode (e.g. diff view, compact status-only) means adding a new render path that reads the same event stream. No runner changes required.

```go
type EventKind string
const (
    EventToken    EventKind = "token"    // streaming token from writer or auditor
    EventRoundEnd EventKind = "round_end"
    EventPassEnd  EventKind = "pass_end"
    EventError    EventKind = "error"
    EventDone     EventKind = "done"
    EventAbort    EventKind = "abort"
)

type Event struct {
    Kind   EventKind
    Agent  string   // "writer", "auditor", "summarizer"
    Text   string   // token text or message
    Pass   int
    Round  int
    Err    error
}
```

### 4. Per-Pass Round Counts
Currently all passes share one `rounds_per_pass` value. The `Pass` struct includes a `Rounds int` field but v1 config only sets it globally. Future: expose per-pass config without touching the runner.

### 5. Convergence / Early Exit
The round loop calls a `ShouldContinue(round int, auditText string) bool` hook before each round. In v1 this always returns `true` until rounds are exhausted. A future convergence detector replaces this hook — no loop changes needed.

### 6. Web UI / Alternative Frontends
The session runner is a standalone goroutine that owns no UI state. It accepts a context and an `chan Event` output. Swapping the Bubble Tea TUI for a web socket server, a REST API, or a headless runner requires only a new consumer of the event channel.

### 7. Persistent Projects / Resume
`session.json` already records full state at each round. Resume support in a future version reads this file and fast-forwards the runner to the last completed round. No schema changes needed; `rounds_completed` and `status` per pass are sufficient.

---

## Out of Scope (v1)

- Web UI
- Persistent named projects / resume
- Convergence-based early exit
- Parallel pass execution
- Git integration
- Diff viewer (audit log covers this)
- Remote/cloud execution
- Per-pass round count configuration (all passes use the same `rounds_per_pass`)
- Windows support
