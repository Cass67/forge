# Best Of Claude For Forge Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Bring the strongest Claude Code security, context, plugin, and terminal ergonomics into Forge without reintroducing Forge's previous brittle intent-classifier behavior.

**Architecture:** Build from Forge's current deterministic runtime: `internal/react` approval gates, `internal/agent/tools` tool execution, `internal/hooks`, `internal/plugins`, `internal/resilience`, and Bubble Tea TUI. Add scoped policy and local analyzers before any LLM classifier, then make the LLM classifier a narrow action-permission decision source rather than a user-intent, mode, or completion-enforcement system.

**Tech Stack:** Go, Forge ReAct runtime, config TOML, hooks, plugin JSON-RPC, Bubble Tea/Bubbles viewport, existing resilience packages, existing Go test suite.

---

## Context Summary

Relevant Forge files:

- `internal/react/approval.go`: central approval gate, policies, rule decisions, guardian review integration, safety branch creation.
- `internal/react/approval_config.go`: maps `config.Config.Approval` into runtime approval config.
- `internal/react/shell_rules.go`: existing structured shell-rule matching for approval rules and known-safe commands.
- `internal/agent/tools/guardian_review.go`: deterministic approval guardian; currently warns/blocks only a small set of destructive or contextless actions.
- `internal/agent/tools/command.go`: `run_command` execution, destructive heuristics, approval callsite.
- `internal/agent/tools/read.go`, `write.go`, `edit.go`, `apply_patch.go`, `search.go`, `glob.go`: file and search tool boundaries.
- `internal/agent/tools/ignore_guard.go`: existing ignore/secret-file boundary used by read/search paths.
- `internal/config/config.go`, `internal/config/validate.go`: config shape and validation.
- `internal/hooks/types.go`, `internal/hooks/runtime.go`: lifecycle hooks, including permission and compact hook points.
- `internal/react/session.go`, `internal/react/compact.go`, `internal/react/loop.go`: history, compact summary, compaction trigger, loop circuit breaker.
- `internal/resilience/*`: circuit breakers, budget tracker, error taxonomy, recovery manager.
- `internal/plugins/*`: existing plugin manager, JSON-RPC protocol, OpenCode host bridge, plugin tools/hooks/agents.
- `internal/tui/chatmodel.go`, `internal/tui/chatrecords.go`, `internal/tui/live.go`, `internal/tui/traceview.go`: Bubble Tea rendering and transcript surfaces.

Claude reference files checked in `~/git/claude-code-analysis`:

- `05-security/permissions-yolo-deep-dive.md`: 7-stage permission flow, scoped rules, auto classifier, denial tracking.
- `05-security/deep-security-audit.md`: permission logging, hooks, gitleaks-style secret integration.
- `05-security/team-memory-security.md`: high-confidence gitleaks-style rules and redacted logging posture.
- `04-systems/compaction-algorithms-deep-dive.md`: six compaction strategies, circuit breakers, post-compact reinjection.
- `06-tools-and-plugins/plugin-system-deep-dive.md`: plugin manifest, marketplace/cache, scope merge, validation, homograph/path traversal defenses.
- `01-architecture/terminal-ui-deep-dive.md`: screen buffers, diffing, sanitized ANSI semantics, frame diffing.

Classifier history lessons from Forge commits:

- `fbe9f6b refactor: remove heuristic dispatch flow gates`: removed role/flow gates based on natural-language request classification.
- `83de21a Remove mode classification and completion enforcement`: removed regex intent classification and post-hoc completion-enforcement loops because they created brittle behavior and tool-use pressure.
- `d961b81 fix: tighten classifier follow-up detection` and `3a75551 fix: classify plain-language dispatch follow-ups`: show prior classifier changes were bug-prone and expanded over time.

Design rule for this plan:

- Do not classify user intent.
- Do not infer mode from wording.
- Do not enforce final-answer shape with retry loops.
- Do classify only concrete pending tool actions after deterministic policy, sandbox, command/path, and secret checks have produced facts.

## Implementation Order

Use this order, matching the user's selected gaps: scoped permissions, secret scanning, auto permission classifier, compaction, plugin lifecycle, and TUI performance.

1. Scoped permission and policy rules, based on Claude gap 2.
2. Secret scanning integrated into permissions, based on Claude gap 8.
3. Action-scoped auto permission classifier, based on Claude gap 1.
4. Multi-tier context compaction, based on Claude gap 3.
5. Local plugin manifest and lifecycle, based on Claude gap 4.
6. Targeted TUI performance upgrades, based on Claude gap 6.

Defer full bash AST parsing from gap 9. Add only the minimum command-risk facts needed to feed permissions and classifier safely.

## Design Options Considered

### Option A: Rules And Local Analyzers First, Classifier Later

This keeps deterministic policy as the source of truth. The LLM classifier sees a narrow action summary plus risk facts and can return `allow`, `deny`, or `ask` only after static checks pass.

Trade-off: slower path to flashy auto mode, but far less likely to recreate the removed intent-classifier pain.

Recommendation: use this option.

### Option B: Claude-Like Full Classifier Early

This adds auto mode first and lets side-model decisions replace many prompts quickly.

Trade-off: faster UX impact, but fragile without scoped policy, command facts, and denial tracking. This is closest to the history that hurt Forge.

Do not use this option.

### Option C: Full Platform Rewrite

This would redesign settings, permissions, plugins, compaction, and rendering around Claude's architecture.

Trade-off: highest ceiling, highest churn. It conflicts with Forge's current working foundations and small-correct-change principle.

Do not use this option.

---

## Phase 1: Scoped Permission And Policy Rules

**Goal:** Make Forge's approval model source-aware and auditable before adding more automation.

**Files:**

- Create: `internal/permissions/scope.go`
- Create: `internal/permissions/rules.go`
- Create: `internal/permissions/rules_test.go`
- Create: `internal/permissions/load.go`
- Create: `internal/permissions/load_test.go`
- Modify: `internal/config/config.go`
- Modify: `internal/config/validate.go`
- Modify: `internal/config/config_test.go`
- Modify: `internal/config/validate_test.go`
- Modify: `internal/react/approval.go`
- Modify: `internal/react/approval_config.go`
- Modify: `internal/react/approval_config_test.go`

### Task 1: Define Permission Scopes And Rule Types

**Step 1: Write failing tests**

Add tests in `internal/permissions/rules_test.go` for:

- scope precedence: managed, user, project, local, session, cli
- deny beats ask beats allow when rules match at the same effective level
- tool-wide rule: `write_file`
- command rule: `run_command(git status:*)`
- path rule: `write_file(docs/**/*.md)`
- unknown tools fail validation

Run: `go test ./internal/permissions -run TestPermissionRule`

Expected: FAIL because package and types do not exist.

**Step 2: Implement rule model**

Create `internal/permissions/rules.go` with:

```go
package permissions

type Scope string

const (
    ScopeManaged Scope = "managed"
    ScopeUser    Scope = "user"
    ScopeProject Scope = "project"
    ScopeLocal   Scope = "local"
    ScopeSession Scope = "session"
    ScopeCLI     Scope = "cli"
)

type Behavior string

const (
    BehaviorAllow Behavior = "allow"
    BehaviorAsk   Behavior = "ask"
    BehaviorDeny  Behavior = "deny"
)

type Rule struct {
    Scope    Scope
    Behavior Behavior
    Tool     string
    Pattern  string
    Source   string
}

type Action struct {
    Tool    string
    Summary string
    Detail  string
    Path    string
}

type Decision struct {
    Behavior Behavior
    Rule     Rule
    Matched  bool
}
```

Use existing `internal/react` shell-rule semantics where possible; move only if needed. Do not break current approval behavior.

**Step 3: Add merge/evaluate behavior**

Implement deterministic evaluation:

- aggregate all scopes in precedence order
- within a matched source, deny beats ask beats allow
- return no match when no rule applies
- keep the existing `ApprovalOnRequest` behavior as fallback

**Step 4: Verify**

Run: `go test ./internal/permissions`

Expected: PASS.

**Step 5: Commit**

```bash
git add internal/permissions
git commit -m "permissions: add scoped rule evaluation"
```

### Task 2: Load Scoped Rules From Config

**Step 1: Write failing config tests**

Add tests for TOML like:

```toml
[[permissions.project.rules]]
behavior = "deny"
tool = "run_command"
pattern = "rm:*"

[[permissions.user.rules]]
behavior = "allow"
tool = "run_command"
pattern = "go test:*"
```

Run: `go test ./internal/config ./internal/permissions -run Test.*Permission`

Expected: FAIL because config fields do not exist.

**Step 2: Extend config shape**

Modify `internal/config/config.go` with additive fields only:

```go
type PermissionRuleConfig struct {
    Behavior string `toml:"behavior"`
    Tool     string `toml:"tool"`
    Pattern  string `toml:"pattern"`
}

type PermissionScopeConfig struct {
    Rules []PermissionRuleConfig `toml:"rules"`
}

type PermissionsConfig struct {
    Managed PermissionScopeConfig `toml:"managed"`
    User    PermissionScopeConfig `toml:"user"`
    Project PermissionScopeConfig `toml:"project"`
    Local   PermissionScopeConfig `toml:"local"`
    Session PermissionScopeConfig `toml:"session"`
    CLI     PermissionScopeConfig `toml:"cli"`
}
```

Add `Permissions PermissionsConfig `toml:"permissions"`` to `Config`.

**Step 3: Validate config**

Modify `internal/config/validate.go` to reject:

- empty behavior
- unknown behavior
- empty tool
- unsupported path traversal patterns where applicable

**Step 4: Bridge into approval gate**

Modify `internal/react/approval_config.go` so existing `[approval]` rules still work, but new `[permissions]` rules become `ApprovalRule` entries or are evaluated by the new `permissions` package from `ApprovalGate.Approve`.

Prefer a small adapter over a full approval rewrite.

**Step 5: Verify**

Run: `go test ./internal/config ./internal/react ./internal/permissions`

Expected: PASS.

**Step 6: Commit**

```bash
git add internal/config internal/react internal/permissions
git commit -m "config: load scoped permission rules"
```

---

## Phase 2: Secret Scanning Integrated Into Permissions

**Goal:** Block or redact secrets at tool boundaries before classifier or user prompts can accidentally expose them.

**Files:**

- Create: `internal/secrets/scanner.go`
- Create: `internal/secrets/scanner_test.go`
- Create: `internal/secrets/policy.go`
- Create: `internal/secrets/policy_test.go`
- Modify: `internal/agent/tools/read.go`
- Modify: `internal/agent/tools/write.go`
- Modify: `internal/agent/tools/edit.go`
- Modify: `internal/agent/tools/apply_patch.go`
- Modify: `internal/agent/tools/command.go`
- Modify: `internal/react/approval.go`
- Modify: `internal/config/config.go`
- Modify: `internal/config/validate.go`

### Task 3: Add High-Confidence Secret Scanner

**Step 1: Write failing scanner tests**

Test only synthetic dummy values. Do not use real-looking live credentials.

Cases:

- GitHub PAT-like dummy token detected
- AWS access key-like dummy token detected
- OpenAI/Anthropic style dummy key detected
- random UUID not detected
- scanner returns rule IDs and byte ranges, never matched values in public formatting

Run: `go test ./internal/secrets -run TestScanner`

Expected: FAIL because package does not exist.

**Step 2: Implement scanner**

Create `internal/secrets/scanner.go` with:

```go
package secrets

type Match struct {
    RuleID string
    Start  int
    End    int
}

type Scanner struct {
    rules []Rule
}

func NewDefaultScanner() *Scanner
func (s *Scanner) Scan(text string) []Match
func Redact(text string, matches []Match) string
```

Start with high-confidence rules only. Prefer false negatives over noisy false positives for the first slice.

**Step 3: Verify**

Run: `go test ./internal/secrets`

Expected: PASS.

**Step 4: Commit**

```bash
git add internal/secrets
git commit -m "security: add local secret scanner"
```

### Task 4: Gate Tool Boundaries With Secret Policy

**Step 1: Write failing integration tests**

Add tests in relevant tool packages for:

- `write_file` blocks writing a file containing a dummy secret by default
- `edit_file` blocks replacement text containing a dummy secret
- `read_file` redacts matching secret spans in returned content unless policy is `block`
- `run_command` redacts matching secret spans in command output before returning to the model
- approval detail redacts secret spans before prompting/logging

Run: `go test ./internal/agent/tools ./internal/react -run 'Test.*Secret|Test.*Redact'`

Expected: FAIL.

**Step 2: Add config policy**

Add TOML support:

```toml
[security.secrets]
read = "redact"
write = "block"
command_output = "redact"
approval_detail = "redact"
```

Allowed values: `allow`, `redact`, `ask`, `block`.

Default:

- reads: redact
- writes: block
- command output: redact
- approval detail: redact

**Step 3: Wire scanner into tools**

Use smallest changes:

- In `read.go`, scan after reading and before line-number formatting.
- In `write.go` and `edit.go`, scan proposed content before approval detail is built.
- In `apply_patch.go`, scan patch text before approval.
- In `command.go`, scan result before truncation and return.
- In `approval.go`, scan/redact `Action.Detail` before `ApprovalUpdate` and prompt output.

Do not print secret values in errors. Use messages like `blocked: content matched secret rule github-pat`.

**Step 4: Verify**

Run: `go test ./internal/secrets ./internal/agent/tools ./internal/react ./internal/config`

Expected: PASS.

**Step 5: Commit**

```bash
git add internal/secrets internal/agent/tools internal/react internal/config
git commit -m "security: gate tool content with secret policy"
```

---

## Phase 3: Balanced Action-Scoped Auto Permission Classifier

**Goal:** Reduce approval fatigue without restoring brittle intent classifiers.

**Files:**

- Create: `internal/permissions/risk.go`
- Create: `internal/permissions/risk_test.go`
- Create: `internal/permissions/classifier.go`
- Create: `internal/permissions/classifier_test.go`
- Create: `internal/permissions/classifier_prompt.go`
- Create: `internal/permissions/denials.go`
- Create: `internal/permissions/denials_test.go`
- Modify: `internal/react/approval.go`
- Modify: `internal/react/approval_updates.go`
- Modify: `internal/react/approval_test.go`
- Modify: `internal/runtime/chat.go`
- Modify: `internal/config/config.go`
- Modify: `internal/config/validate.go`

### Task 5: Add Deterministic Action Risk Facts

**Step 1: Write failing risk tests**

Cases:

- `go test ./...` is low risk
- `git status --short` is read-only
- `git commit` is mutating but not destructive
- `rm -rf /` is destructive and classifier-immune block
- `curl ... | sh` is destructive and classifier-immune block
- write to `.git/config` or secret-adjacent path is classifier-immune ask/block

Run: `go test ./internal/permissions -run TestActionRisk`

Expected: FAIL.

**Step 2: Implement risk facts**

Create `internal/permissions/risk.go`:

```go
type RiskLevel string

const (
    RiskLow         RiskLevel = "low"
    RiskMedium      RiskLevel = "medium"
    RiskHigh        RiskLevel = "high"
    RiskDestructive RiskLevel = "destructive"
)

type RiskFacts struct {
    Level             RiskLevel
    MutatesWorkspace  bool
    TouchesGitState   bool
    TouchesSecrets    bool
    Network           bool
    Destructive       bool
    ClassifierImmune  bool
    Reasons           []string
}

func AnalyzeAction(action Action) RiskFacts
```

Keep analysis simple and conservative. Do not build a full shell parser in this phase.

**Step 3: Verify**

Run: `go test ./internal/permissions -run TestActionRisk`

Expected: PASS.

**Step 4: Commit**

```bash
git add internal/permissions/risk.go internal/permissions/risk_test.go
git commit -m "permissions: add action risk facts"
```

### Task 6: Add Classifier Interface And Fake Driver Tests

**Step 1: Write failing classifier tests**

Use a fake classifier. Do not call a real model in unit tests.

Cases:

- deterministic deny/rule block bypasses classifier
- low-risk command can be classifier-allowed
- medium-risk edit can be classifier-allowed only when risk facts are non-destructive and no secrets matched
- high-risk command returns ask unless classifier returns deny
- parse failure returns ask or deny based on config, never allow

Run: `go test ./internal/permissions ./internal/react -run TestAutoPermissionClassifier`

Expected: FAIL.

**Step 2: Implement classifier contract**

Create `internal/permissions/classifier.go`:

```go
type ClassifierDecision string

const (
    ClassifierAllow ClassifierDecision = "allow"
    ClassifierDeny  ClassifierDecision = "deny"
    ClassifierAsk   ClassifierDecision = "ask"
)

type ClassifierRequest struct {
    Action     Action
    Risk       RiskFacts
    Rules      []Rule
    Transcript string
}

type ClassifierResponse struct {
    Decision ClassifierDecision
    Reason   string
}

type Classifier interface {
    Classify(context.Context, ClassifierRequest) (ClassifierResponse, error)
}
```

**Step 3: Add prompt builder**

Create `internal/permissions/classifier_prompt.go` with a compact prompt that includes:

- action tool/name/detail with secrets redacted
- deterministic risk facts
- matched rules
- compact user/tool transcript from current turn only
- explicit output JSON: `{ "decision": "allow|deny|ask", "reason": "..." }`

Prompt rules:

- err on `ask` for ambiguity
- never allow classifier-immune actions
- never allow secret-matched writes
- prefer `allow` for common tests, read-only git commands, and safe local build commands
- permit balanced auto mode for normal workspace edits when deterministic facts are low/medium and scoped rules do not ask/deny

**Step 4: Wire into ApprovalGate**

Modify `internal/react/approval.go`:

- keep sandbox and deterministic deny first
- keep scoped rules before classifier
- call classifier only when policy is `auto` or when new config enables auto classifier under `unless_trusted`
- record decision source as classifier in `ApprovalUpdate`
- on classifier `ask`, prompt user
- on classifier error, prompt user in interactive mode and deny in headless mode

**Step 5: Verify**

Run: `go test ./internal/permissions ./internal/react`

Expected: PASS.

**Step 6: Commit**

```bash
git add internal/permissions internal/react
git commit -m "permissions: add action-scoped auto classifier"
```

### Task 7: Add Denial Tracking And Runtime Wiring

**Step 1: Write failing denial tests**

Cases:

- 3 consecutive denials triggers prompt fallback
- success resets consecutive count
- 20 total denials triggers prompt fallback and reset after user review

Run: `go test ./internal/permissions -run TestDenialTracking`

Expected: FAIL.

**Step 2: Implement denial tracking**

Create `internal/permissions/denials.go` with Claude-like limits:

- `MaxConsecutive = 3`
- `MaxTotal = 20`

**Step 3: Add config**

Add:

```toml
[permissions.auto]
enabled = false
posture = "balanced"
model = ""
max_consecutive_denials = 3
max_total_denials = 20
failure_behavior = "ask"
```

Allowed postures: `conservative`, `balanced`.

Do not add `aggressive` yet.

**Step 4: Wire runtime classifier provider**

Modify `internal/runtime/chat.go` where `ApprovalGate` is created:

- construct classifier only when enabled
- use configured side model or summarizer/auditor fallback
- give classifier a short timeout
- never include secret values in request logging

**Step 5: Verify**

Run: `go test ./internal/permissions ./internal/react ./internal/runtime ./internal/config`

Expected: PASS.

**Step 6: Commit**

```bash
git add internal/permissions internal/react internal/runtime internal/config
git commit -m "runtime: wire balanced auto permissions"
```

---

## Phase 4: Multi-Tier Context Compaction

**Goal:** Replace Forge's simple turn dropping with explicit context tiers and prompt-too-long recovery.

**Files:**

- Create: `internal/react/compaction_manager.go`
- Create: `internal/react/compaction_manager_test.go`
- Modify: `internal/react/compact.go`
- Modify: `internal/react/session.go`
- Modify: `internal/react/loop.go`
- Modify: `internal/hooks/types.go`
- Modify: `internal/plugins/protocol.go`
- Modify: `internal/tui/chatmodel.go`
- Modify: `internal/config/config.go`

### Task 8: Add Compaction Manager State Machine

**Step 1: Write failing tests**

Cases:

- below threshold: no compaction
- old tool result over size threshold: microcompact only
- high history pressure: summarize old turns and keep recent turns
- repeated compaction failures open circuit breaker
- user partial compact request compacts selected range

Run: `go test ./internal/react -run TestCompactionManager`

Expected: FAIL.

**Step 2: Implement manager**

Create `internal/react/compaction_manager.go` with:

```go
type CompactionMode string

const (
    CompactionNone         CompactionMode = "none"
    CompactionMicro        CompactionMode = "micro"
    CompactionSummarize    CompactionMode = "summarize"
    CompactionReactive     CompactionMode = "reactive"
    CompactionUserPartial  CompactionMode = "user_partial"
)

type CompactionDecision struct {
    Mode   CompactionMode
    Reason string
}
```

Start with estimated token counts using existing heuristics. Avoid provider-specific tokenization until later.

**Step 3: Replace direct loop compaction**

Modify `internal/react/loop.go` line where `CompactSessionHistory(r.session, 40)` is called. Route through manager.

**Step 4: Add prompt-too-long recovery hook**

When `internal/resilience/errors` classifies prompt-too-long/context overflow, run one reactive compaction attempt and retry once. Use recovery guard from `internal/resilience/recovery` to prevent loops.

**Step 5: Verify**

Run: `go test ./internal/react ./internal/resilience/...`

Expected: PASS.

**Step 6: Commit**

```bash
git add internal/react internal/resilience
git commit -m "react: add compaction manager"
```

### Task 9: Add `/compact` Controls And Hook Events

**Step 1: Write failing TUI tests**

Cases:

- `/compact` triggers full summarization mode
- `/compact recent 20` preserves recent 20 turns
- compact status appears in transcript as a compact boundary, not raw hidden state

Run: `go test ./internal/tui ./internal/react -run Test.*Compact`

Expected: FAIL for new slash behavior.

**Step 2: Add TUI command**

Modify `internal/tui/chatmodel.go` slash command handling:

- `/compact`
- `/compact recent N`
- `/compact status`

**Step 3: Add hook protocol events**

Existing hook points include `pre_compact` and `post_compact`. Ensure plugin protocol payload includes:

- mode
- reason
- dropped turn count
- summary length
- circuit breaker state

**Step 4: Verify**

Run: `go test ./internal/tui ./internal/react ./internal/plugins ./internal/hooks`

Expected: PASS.

**Step 5: Commit**

```bash
git add internal/tui internal/react internal/plugins internal/hooks
git commit -m "tui: add compact controls"
```

---

## Phase 5: Local Plugin Manifest And Lifecycle

**Goal:** Turn Forge's existing plugin runtime into a safer local manifest lifecycle before adding marketplace/network complexity.

**Files:**

- Create: `internal/plugins/manifest.go`
- Create: `internal/plugins/manifest_test.go`
- Create: `internal/plugins/cache.go`
- Create: `internal/plugins/cache_test.go`
- Create: `internal/plugins/install.go`
- Create: `internal/plugins/install_test.go`
- Modify: `internal/plugins/manager.go`
- Modify: `internal/plugins/protocol.go`
- Modify: `internal/config/config.go`
- Modify: `internal/config/validate.go`
- Modify: `cmd/forge/main.go`
- Modify: `internal/cli/cli.go`

### Task 10: Add Manifest Parser And Validation

**Step 1: Write failing manifest tests**

Cases:

- valid local manifest contributes commands, skills, agents, hooks, MCP servers
- path traversal in component path is rejected
- absolute component path is rejected
- non-ASCII official-looking marketplace/plugin name is rejected
- duplicate component names are rejected

Run: `go test ./internal/plugins -run TestManifest`

Expected: FAIL.

**Step 2: Implement manifest schema**

Create `internal/plugins/manifest.go`:

```go
type Manifest struct {
    Name        string
    Version     string
    Description string
    Commands    map[string]CommandManifest
    Agents      []AgentManifest
    Skills      []SkillManifest
    Hooks       []HookManifest
    MCPServers  map[string]MCPServerManifest
}
```

Use strict path validation:

- no absolute paths
- no `..`
- resolve symlinks inside plugin root
- ASCII-only names for now

**Step 3: Verify**

Run: `go test ./internal/plugins -run TestManifest`

Expected: PASS.

**Step 4: Commit**

```bash
git add internal/plugins/manifest.go internal/plugins/manifest_test.go
git commit -m "plugins: add local manifest validation"
```

### Task 11: Add Versioned Local Cache And CLI Install

**Step 1: Write failing install tests**

Cases:

- local path installs into versioned cache
- same plugin/version reuses cache
- changed source path with same version errors unless `--force`
- install metadata never stores env secret values

Run: `go test ./internal/plugins -run TestInstall`

Expected: FAIL.

**Step 2: Implement cache**

Use Forge config/cache path helpers. Cache layout:

```text
forge/plugins/cache/<plugin-name>/<version>/
forge/plugins/installed_plugins.json
```

Keep permissions restrictive for metadata files.

**Step 3: Add CLI commands**

Add:

- `forge plugins install <path>`
- `forge plugins list`
- `forge plugins validate <path>`
- `forge plugins remove <name>`

Wire through `cmd/forge/main.go` and `internal/cli/cli.go` following existing command style.

**Step 4: Use installed manifests at chat startup**

Modify `internal/runtime/chat.go` plugin startup to load installed manifests in addition to explicit `[plugins]` config entries.

Keep explicit config taking precedence.

**Step 5: Verify**

Run: `go test ./internal/plugins ./internal/cli ./internal/runtime ./cmd/forge`

Expected: PASS.

**Step 6: Commit**

```bash
git add internal/plugins internal/cli internal/runtime cmd/forge
git commit -m "plugins: add local install lifecycle"
```

---

## Phase 6: Targeted TUI Performance Upgrades

**Goal:** Improve long-session rendering without replacing Bubble Tea with a custom renderer.

**Files:**

- Create: `internal/tui/transcript_cache.go`
- Create: `internal/tui/transcript_cache_test.go`
- Modify: `internal/tui/chatmodel.go`
- Modify: `internal/tui/chatrecords.go`
- Modify: `internal/tui/live.go`
- Modify: `internal/tui/traceview.go`
- Modify: `internal/tui/codeblock.go`

### Task 12: Cache Rendered Message Blocks

**Step 1: Write failing cache tests**

Cases:

- unchanged message reuses rendered content across `refreshViewport`
- width/theme change invalidates cache
- appended streaming token invalidates only active message
- codeblock render cache respects language and width

Run: `go test ./internal/tui -run TestTranscriptCache`

Expected: FAIL.

**Step 2: Implement render cache**

Create `internal/tui/transcript_cache.go` with:

```go
type transcriptRenderCache struct {
    entries map[string]renderCacheEntry
}

type renderCacheEntry struct {
    Width   int
    ThemeID string
    Source  string
    Output  string
}
```

Use a stable key from message kind/header/content plus width/theme. Keep it local to TUI.

**Step 3: Wire into viewport refresh**

Modify `chatmodel.go` rendering functions so historical messages use cached render output. Streaming active message can bypass cache until finalized.

**Step 4: Verify**

Run: `go test ./internal/tui -run 'TestTranscriptCache|TestChatModel|TestViewport'`

Expected: PASS.

**Step 5: Commit**

```bash
git add internal/tui
git commit -m "tui: cache rendered transcript blocks"
```

### Task 13: Add Large Transcript Virtualization Guardrails

**Step 1: Write failing tests**

Cases:

- 1000-message transcript renders only the visible window plus overscan when not at bottom
- searching or copying transcript can still access full content
- trace view remains complete

Run: `go test ./internal/tui -run 'Test.*Virtual|Test.*Transcript'`

Expected: FAIL.

**Step 2: Implement viewport-window rendering**

Keep the full `m.messages` model. Only reduce expensive string rendering when viewport is far from bottom.

Do not change transcript persistence or trace data.

**Step 3: Add optional debug metric**

Expose render stats in `/stats` or trace only:

- rendered message count
- cache hits/misses
- viewport line count

No telemetry or external logging required.

**Step 4: Verify**

Run: `go test ./internal/tui`

Expected: PASS.

**Step 5: Commit**

```bash
git add internal/tui
git commit -m "tui: virtualize large transcript rendering"
```

---

## Final Verification

### Task 14: Full Repository Verification

**Step 1: Run focused suites**

Run:

```bash
go test ./internal/permissions ./internal/secrets ./internal/react ./internal/agent/tools ./internal/plugins ./internal/tui ./internal/config ./internal/runtime
```

Expected: PASS.

**Step 2: Run full test suite**

Run:

```bash
go test ./...
```

Expected: PASS.

**Step 3: Run repo check if available**

Run:

```bash
just check
```

Expected: PASS, or document missing tool/pre-existing failures.

**Step 4: Review changed files**

Run:

```bash
git diff --stat
git diff -- internal/permissions internal/secrets internal/react internal/agent/tools internal/plugins internal/tui internal/config internal/runtime cmd/forge
```

Expected: no unrelated rewrites, no secrets, no broad UI rewrite.

**Step 5: Commit final polish**

```bash
git add docs/plans internal cmd
git commit -m "feat: bring Claude-style safety and resilience to forge"
```

## Explicit Non-Goals

- No user-intent classifier.
- No regex mode classifier.
- No post-hoc completion enforcement retry loop.
- No full bash AST parser in this plan.
- No public plugin marketplace or network auto-update in this plan.
- No terminal renderer rewrite in this plan.
- No aggressive auto-approval posture.

## Success Criteria

- Common safe commands and edits prompt less often under balanced auto mode.
- Deterministic deny/ask policy remains enforceable without the classifier.
- Secret values are never printed into prompts, approval details, logs, or tool results.
- Prompt-too-long failures trigger one guarded recovery path instead of session failure or loops.
- Plugins can be installed locally with manifest validation and versioned cache.
- Long transcripts render measurably faster without changing Forge's TUI architecture.
