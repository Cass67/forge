# Side Effect Transaction Enforcement Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Make Forge enforce scoped write/commit/push intent in runtime state so child-agent reports, dirty worktrees, and git mutations cannot corrupt the requested artifact or commit unrelated files.

**Architecture:** Add a runtime `SideEffectIntent` contract to `react.Session`, persist it in the durable protocol, and have the ReAct loop derive/update it from user requests and delegated handoffs. Replace unsafe `git_commit` behavior with scoped git transaction tools that stage, commit, push, and verify against the active contract. Treat child-agent handoff/report text as control-plane material that cannot be written to the artifact path unless explicitly requested.

**Tech Stack:** Go, existing `internal/react` runner/session, `internal/protocol` durable items, `internal/agent/tools` registry/tools, local git commands with fixed argv, existing live acceptance local-provider harness.

---

## Principles From Surveyed Tools

- **Codex:** centralize side-effect safety in tool/runtime orchestration, not prompts; make read-only git easy and mutation explicit.
- **DeepSeek TUI:** child-agent results are self-reports; require parent verification before success claims.
- **OpenCode:** snapshots and durable session state make failures inspectable and reversible; permissions are not a sandbox.
- **CCI:** separate read-only research, mutating implementation, and adversarial verification roles; do not let child agents own parent commits by default.

## Non-Goals

- Do not build a full git porcelain replacement.
- Do not add marketplace/plugin policy changes.
- Do not implement remote PR creation in this pass.
- Do not remove shell entirely; only block shell git mutation while a scoped git transaction is active.
- Do not revert user or unrelated dirty worktree changes automatically.

## Additional Failure Covered: New Repo Outside Current Workspace

Latest reproduction:

```text
user: create a new repo in ~/git/arkanoid and build an Arkanoid-style game
tool: run_command "mkdir -p ~/git/arkanoid && cd ~/git/arkanoid && git init"
next tool: write_file "/Users/cass/git/arkanoid/index.html"
error: path "/Users/cass/git/arkanoid/index.html" escapes working directory
```

Root cause: `run_command` runs in a subprocess. Its `cd` does not change Forge's registered tool workspace. `write_file` correctly resolves all write paths against the original `workDir` and rejects absolute paths outside it. The safety boundary is correct, but Forge lacks a first-class new-project/workspace transition, so the model tried to use shell state as if it persisted.

This plan therefore also adds a workspace transaction layer: creating or switching to a user-requested external project root must be explicit runtime state, approved, and then used by write/git tools through a dynamic workspace provider instead of a stale fixed `workDir` string.

## Task 0: Add Active Workspace Root For New Repo Requests

**Files:**
- Create: `internal/react/workspace_intent.go`
- Modify: `internal/react/session.go`
- Test: `internal/react/session_test.go`
- Create: `internal/agent/tools/workdir_provider.go`
- Modify: `internal/agent/tools/write.go`
- Modify: `internal/agent/tools/command.go`
- Modify later in Task 5/6: `internal/agent/tools/git_scoped.go`
- Modify: `internal/runtime/chat.go`
- Test: `internal/runtime/chat_test.go`
- Test: `internal/agent/tools/write_test.go`

**Step 1: Write failing session tests**

Add tests near the existing session snapshot tests in `internal/react/session_test.go`:

```go
func TestSessionStoresActiveWorkspaceRootInSnapshot(t *testing.T) {
	s := NewSession()
	s.SetActiveWorkspaceRoot("/Users/cass/git/arkanoid")

	snap := s.Snapshot()
	if snap.ActiveWorkspaceRoot != "/Users/cass/git/arkanoid" {
		t.Fatalf("ActiveWorkspaceRoot = %q", snap.ActiveWorkspaceRoot)
	}
}

func TestDeriveActiveWorkspaceRootFromNewRepoRequest(t *testing.T) {
	root := deriveActiveWorkspaceRoot("create me a new repo in ~/git/arkanoid and build a game")
	if !strings.HasSuffix(root, filepath.Join("git", "arkanoid")) {
		t.Fatalf("root = %q, want ~/git/arkanoid", root)
	}
}
```

Use `t.Setenv("HOME", home)` in the derivation test so `~` expansion is deterministic.

**Step 2: Run tests to verify failure**

Run: `go test -count=1 ./internal/react -run 'ActiveWorkspaceRoot|DeriveActiveWorkspaceRoot'`

Expected: FAIL because the session field, setter, and derivation helper do not exist.

**Step 3: Add minimal active workspace state**

Create `internal/react/workspace_intent.go`:

```go
package react

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var newRepoRootPattern = regexp.MustCompile(`(?i)\b(?:new repo|new repository|create repo|create repository|make repo|make repository|set up repo|setup repo)\b.*?\b(?:in|at|under)\s+([^\s,;]+)`) // conservative, single explicit path

func deriveActiveWorkspaceRoot(text string) string {
	text = strings.TrimSpace(text)
	if text == "" || !inputSuggestsRepoSetupCommandWork(normalizeToolIntentText(text)) {
		return ""
	}
	matches := newRepoRootPattern.FindStringSubmatch(text)
	if len(matches) < 2 {
		return ""
	}
	root := strings.Trim(matches[1], "`'\".,:;()[]{}<>")
	root = expandHomePath(root)
	abs, err := filepath.Abs(root)
	if err != nil {
		return ""
	}
	clean := filepath.Clean(abs)
	if clean == string(filepath.Separator) || strings.Contains(clean, string(filepath.Separator)+".."+string(filepath.Separator)) {
		return ""
	}
	return clean
}

func expandHomePath(path string) string {
	if path == "~" || strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil && home != "" {
			return filepath.Join(home, strings.TrimPrefix(path, "~/"))
		}
	}
	return path
}
```

Modify `internal/react/session.go`:

- Add `ActiveWorkspaceRoot string` to `SessionSnapshot`.
- Add `activeWorkspaceRoot string` to `Session`.
- Include it in `Snapshot()`.
- Add:

```go
func (s *Session) SetActiveWorkspaceRoot(root string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.activeWorkspaceRoot = strings.TrimSpace(root)
}
```

In `Runner.RunWithParts`, after input recording, call `deriveActiveWorkspaceRoot(prompt)` and set it when non-empty. Do not infer roots from tool output or child reports.

**Step 4: Add dynamic workdir provider**

Create `internal/agent/tools/workdir_provider.go`:

```go
package tools

import "strings"

type WorkDirProvider func() string

func FixedWorkDirProvider(workDir string) WorkDirProvider {
	return func() string { return strings.TrimSpace(workDir) }
}

func currentWorkDir(provider WorkDirProvider, fallback string) string {
	if provider != nil {
		if workDir := strings.TrimSpace(provider()); workDir != "" {
			return workDir
		}
	}
	return strings.TrimSpace(fallback)
}
```

Add provider constructors instead of replacing old APIs:

```go
func NewWriteFileWithWorkDirProvider(fallbackWorkDir string, provider WorkDirProvider, approve ApprovalFunc, policies ...SecretPolicy) Tool
func NewRunCommandWithWorkDirProvider(fallbackWorkDir string, provider WorkDirProvider, timeoutSecs int, manager *ExecSessionManager, approve ApprovalFunc, policy SecretPolicy, forcePrompt ...ApprovalFunc) Tool
```

Keep `NewWriteFile` and `NewRunCommandWithSecretPolicy` delegating to the provider variants with `FixedWorkDirProvider(workDir)` so existing tests and callers keep working.

**Step 5: Write failing tool test for the Arkanoid class**

Add to `internal/agent/tools/write_test.go`:

```go
func TestWriteFileUsesActiveWorkspaceProviderForExternalNewRepo(t *testing.T) {
	base := t.TempDir()
	workspace := filepath.Join(t.TempDir(), "arkanoid")
	tool := NewWriteFileWithWorkDirProvider(base, func() string { return workspace }, func(Action) (bool, error) { return true, nil })

	_, err := tool.Execute(context.Background(), map[string]any{
		"path":    filepath.Join(workspace, "index.html"),
		"content": "game",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := mustReadFile(t, filepath.Join(workspace, "index.html")); got != "game" {
		t.Fatalf("content = %q", got)
	}
}
```

Also add a negative test that an absolute path outside the active workspace still fails with `escapes working directory`.

**Step 6: Wire runtime tools to session workspace**

Modify `internal/runtime/chat.go` in `registerTools`:

```go
workDirProvider := tools.FixedWorkDirProvider(workDir)
if session != nil {
	workDirProvider = func() string {
		snap := session.Snapshot()
		if strings.TrimSpace(snap.ActiveWorkspaceRoot) != "" {
			return snap.ActiveWorkspaceRoot
		}
		return workDir
	}
}
```

Use provider variants for mutating tools that need the active root immediately:

- `write_file`
- `edit_file`
- `apply_patch`
- `run_command`
- scoped git commit/push when added in Tasks 5/6

Leave read-only search/list/LSP on the original project root in this pass unless a test demonstrates they must follow the new root. This keeps external-workspace mutation explicit without unexpectedly changing broad read scope.

**Step 7: Add runtime registration test**

Add to `internal/runtime/chat_test.go`:

```go
func TestRegisterToolsWriteFileUsesActiveWorkspaceRoot(t *testing.T) {
	cfg, err := config.Load(filepath.Join(t.TempDir(), "forge.toml"))
	if err != nil { t.Fatal(err) }
	base := t.TempDir()
	workspace := filepath.Join(t.TempDir(), "arkanoid")
	session := reactruntime.NewSession()
	session.SetActiveWorkspaceRoot(workspace)
	reg := tools.NewRegistry()
	registerTools(reg, base, cfg, session, func(tools.Action) (bool, error) { return true, nil }, nil, nil)

	write, ok := reg.Get("write_file")
	if !ok { t.Fatal("missing write_file") }
	_, err = write.Execute(context.Background(), map[string]any{"path": filepath.Join(workspace, "index.html"), "content": "game"})
	if err != nil { t.Fatal(err) }
	if got := readTextFile(t, filepath.Join(workspace, "index.html")); got != "game" {
		t.Fatalf("content = %q", got)
	}
}
```

Adapt helper names to the existing test helpers in `chat_test.go`.

**Step 8: Run tests**

Run: `go test -count=1 ./internal/react ./internal/agent/tools ./internal/runtime -run 'ActiveWorkspaceRoot|DeriveActiveWorkspaceRoot|WriteFileUsesActiveWorkspace|RegisterToolsWriteFileUsesActiveWorkspace'`

Expected: PASS.

**Step 9: Checkpoint**

Inspect: `git diff -- internal/react internal/agent/tools internal/runtime/chat.go internal/runtime/chat_test.go`

Do not commit unless explicitly requested.

## Task 1: Add Runtime Side-Effect Intent State

**Files:**
- Create: `internal/react/side_effect_intent.go`
- Modify: `internal/react/session.go`
- Test: `internal/react/session_test.go`

**Step 1: Write failing session tests**

Add tests near the existing durable/session state tests in `internal/react/session_test.go`:

```go
func TestSessionStoresSideEffectIntentInSnapshot(t *testing.T) {
	s := NewSession()
	intent := SideEffectIntent{
		ID:            "intent-1",
		ArtifactPaths: []string{"FORGE_VS_CODEX.md"},
		AllowedPaths:  []string{"FORGE_VS_CODEX.md"},
		RequiredActions: []SideEffectAction{
			SideEffectActionWrite,
			SideEffectActionCommit,
			SideEffectActionPush,
		},
		TargetBranch: "main",
		Remote:       "origin",
	}
	s.SetSideEffectIntent(intent)

	snap := s.Snapshot()
	if snap.SideEffectIntent == nil {
		t.Fatal("missing side-effect intent")
	}
	if got := snap.SideEffectIntent.AllowedPaths; len(got) != 1 || got[0] != "FORGE_VS_CODEX.md" {
		t.Fatalf("AllowedPaths = %#v", got)
	}
}

func TestSessionClearsSideEffectIntent(t *testing.T) {
	s := NewSession()
	s.SetSideEffectIntent(SideEffectIntent{ID: "intent-1", AllowedPaths: []string{"a.md"}})
	s.ClearSideEffectIntent()
	if got := s.Snapshot().SideEffectIntent; got != nil {
		t.Fatalf("SideEffectIntent = %#v, want nil", got)
	}
}
```

**Step 2: Run tests to verify failure**

Run: `go test -count=1 ./internal/react -run 'TestSessionStoresSideEffectIntentInSnapshot|TestSessionClearsSideEffectIntent'`

Expected: FAIL because `SideEffectIntent`, `SideEffectAction`, and session accessors do not exist.

**Step 3: Add minimal types**

Create `internal/react/side_effect_intent.go`:

```go
package react

import (
	"path/filepath"
	"strings"
)

type SideEffectAction string

const (
	SideEffectActionWrite  SideEffectAction = "write"
	SideEffectActionVerify SideEffectAction = "verify"
	SideEffectActionCommit SideEffectAction = "commit"
	SideEffectActionPush   SideEffectAction = "push"
)

type SideEffectGateStatus string

const (
	SideEffectGatePending SideEffectGateStatus = "pending"
	SideEffectGatePassed  SideEffectGateStatus = "passed"
	SideEffectGateFailed  SideEffectGateStatus = "failed"
)

type SideEffectGate struct {
	Name     string               `json:"name"`
	Status   SideEffectGateStatus `json:"status"`
	Evidence string               `json:"evidence,omitempty"`
}

type SideEffectIntent struct {
	ID              string             `json:"id"`
	SourceTurn      int                `json:"source_turn,omitempty"`
	ArtifactPaths   []string           `json:"artifact_paths,omitempty"`
	AllowedPaths    []string           `json:"allowed_paths,omitempty"`
	RequiredActions []SideEffectAction `json:"required_actions,omitempty"`
	TargetBranch    string             `json:"target_branch,omitempty"`
	Remote          string             `json:"remote,omitempty"`
	Gates           []SideEffectGate   `json:"gates,omitempty"`
	IncidentMode    bool               `json:"incident_mode,omitempty"`
	Reason          string             `json:"reason,omitempty"`
}

func normalizeIntentPath(path string) string {
	path = strings.TrimSpace(path)
	path = strings.Trim(path, "`'\".,:;()[]{}<>")
	if path == "" || filepath.IsAbs(path) {
		return ""
	}
	cleaned := filepath.Clean(path)
	if cleaned == "." || strings.HasPrefix(cleaned, "..") || strings.Contains(cleaned, string(filepath.Separator)+".."+string(filepath.Separator)) {
		return ""
	}
	return filepath.ToSlash(cleaned)
}
```

**Step 4: Add state to session**

Modify `internal/react/session.go`:

- Add `SideEffectIntent *SideEffectIntent` to `SessionSnapshot`.
- Add `sideEffectIntent *SideEffectIntent` to `Session`.
- Include a deep copy in `Snapshot()`.
- Add methods:

```go
func (s *Session) SetSideEffectIntent(intent SideEffectIntent) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	copy := copySideEffectIntent(&intent)
	s.sideEffectIntent = copy
}

func (s *Session) UpdateSideEffectIntent(update func(*SideEffectIntent)) {
	if s == nil || update == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.sideEffectIntent == nil {
		s.sideEffectIntent = &SideEffectIntent{}
	}
	update(s.sideEffectIntent)
}

func (s *Session) ClearSideEffectIntent() {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sideEffectIntent = nil
}
```

Also add `copySideEffectIntent` to `side_effect_intent.go` to copy slices.

**Step 5: Run tests**

Run: `go test -count=1 ./internal/react -run 'TestSessionStoresSideEffectIntentInSnapshot|TestSessionClearsSideEffectIntent'`

Expected: PASS.

**Step 6: Checkpoint**

Inspect: `git diff -- internal/react/side_effect_intent.go internal/react/session.go internal/react/session_test.go`

Do not commit unless the maintainer explicitly requested commits for this execution.

## Task 2: Persist And Replay Side-Effect Intent

**Files:**
- Modify: `internal/protocol/items.go`
- Modify: `internal/protocol/schema.go`
- Modify: `internal/protocol/schema_test.go`
- Modify: `internal/protocol/schemas/forge_protocol.schema.json`
- Modify: `internal/sessionstore/replay.go`
- Test: `internal/sessionstore/replay_test.go`
- Test: `internal/react/session_test.go`

**Step 1: Write failing replay test**

Add to `internal/sessionstore/replay_test.go`:

```go
func TestReplayRestoresLatestSideEffectIntent(t *testing.T) {
	items := []protocol.Item{{
		Version:  protocol.CurrentItemVersion,
		ThreadID: "thread-1",
		TurnID:   "turn-1",
		Seq:      1,
		Kind:     protocol.ItemSideEffectIntent,
		SideEffectIntent: &protocol.SideEffectIntentItem{
			ID:              "intent-1",
			AllowedPaths:    []string{"FORGE_VS_CODEX.md"},
			ArtifactPaths:   []string{"FORGE_VS_CODEX.md"},
			RequiredActions: []string{"write", "commit", "push"},
			TargetBranch:    "main",
			Remote:          "origin",
		},
	}}
	replay, err := ReplayItems(items)
	if err != nil {
		t.Fatal(err)
	}
	if replay.SideEffectIntent == nil || replay.SideEffectIntent.AllowedPaths[0] != "FORGE_VS_CODEX.md" {
		t.Fatalf("SideEffectIntent = %#v", replay.SideEffectIntent)
	}
}
```

**Step 2: Run test to verify failure**

Run: `go test -count=1 ./internal/sessionstore -run TestReplayRestoresLatestSideEffectIntent`

Expected: FAIL because protocol/replay fields do not exist.

**Step 3: Add durable item shape**

Modify `internal/protocol/items.go`:

- Add `ItemSideEffectIntent ItemKind = "side_effect_intent"`.
- Add `SideEffectIntent *SideEffectIntentItem` to `Item`.
- Add:

```go
type SideEffectGateItem struct {
	Name     string `json:"name"`
	Status   string `json:"status"`
	Evidence string `json:"evidence,omitempty"`
}

type SideEffectIntentItem struct {
	ID              string               `json:"id"`
	SourceTurn      int                  `json:"source_turn,omitempty"`
	ArtifactPaths   []string             `json:"artifact_paths,omitempty"`
	AllowedPaths    []string             `json:"allowed_paths,omitempty"`
	RequiredActions []string             `json:"required_actions,omitempty"`
	TargetBranch    string               `json:"target_branch,omitempty"`
	Remote          string               `json:"remote,omitempty"`
	Gates           []SideEffectGateItem `json:"gates,omitempty"`
	IncidentMode    bool                 `json:"incident_mode,omitempty"`
	Reason          string               `json:"reason,omitempty"`
}
```

**Step 4: Persist from session**

Modify `Session.SetSideEffectIntent` and `Session.ClearSideEffectIntent` to append `protocol.ItemSideEffectIntent` records. Clearing should append an item with the same kind and empty `ID` plus `Reason: "cleared"`, then set runtime state to nil.

Add conversion helpers in `internal/react/side_effect_intent.go`:

```go
func sideEffectIntentToProtocol(intent SideEffectIntent) protocol.SideEffectIntentItem
func sideEffectIntentFromProtocol(item protocol.SideEffectIntentItem) SideEffectIntent
```

**Step 5: Replay latest intent**

Modify `internal/sessionstore/replay.go`:

- Add `SideEffectIntent *protocol.SideEffectIntentItem` to `Replay`.
- In `ReplayItems`, handle `protocol.ItemSideEffectIntent` before normal turn processing.
- If `item.SideEffectIntent.ID == ""`, clear `replay.SideEffectIntent`; otherwise copy the item as latest.

Modify `NewSessionFromItems` in `internal/react/session.go` to restore `sideEffectIntent` from replay.

**Step 6: Update schema fixture**

Modify `internal/protocol/schema.go` to include `side_effect_intent` in the `kind` enum and properties. Regenerate `internal/protocol/schemas/forge_protocol.schema.json` by running the existing schema fixture test workflow.

**Step 7: Run tests**

Run: `go test -count=1 ./internal/protocol ./internal/sessionstore ./internal/react -run 'SideEffectIntent|SchemaFixture|ReplayRestoresLatestSideEffectIntent'`

Expected: PASS.

**Step 8: Checkpoint**

Inspect: `git diff -- internal/protocol internal/sessionstore internal/react`

Do not commit unless explicitly requested.

## Task 3: Derive Intent From User Requests And Delegation State

**Files:**
- Modify: `internal/react/side_effect_intent.go`
- Modify: `internal/react/loop.go`
- Test: `internal/react/loop_test.go`

**Step 1: Write failing unit tests for intent derivation**

Add to `internal/react/loop_test.go` near post-delegation tests:

```go
func TestRunnerCreatesSideEffectIntentForWriteCommitPushRequest(t *testing.T) {
	session := NewSession()
	r := NewRunner(Config{Session: session, Driver: &scriptedDriver{responses: []string{"ok"}}})

	if err := r.Run(context.Background(), "write FORGE_VS_CODEX.md, commit it to main and push"); err != nil {
		t.Fatal(err)
	}
	intent := session.Snapshot().SideEffectIntent
	if intent == nil {
		t.Fatal("missing side-effect intent")
	}
	if !containsString(intent.AllowedPaths, "FORGE_VS_CODEX.md") {
		t.Fatalf("AllowedPaths = %#v", intent.AllowedPaths)
	}
	for _, want := range []SideEffectAction{SideEffectActionWrite, SideEffectActionCommit, SideEffectActionPush} {
		if !containsSideEffectAction(intent.RequiredActions, want) {
			t.Fatalf("RequiredActions = %#v, want %s", intent.RequiredActions, want)
		}
	}
	if intent.TargetBranch != "main" || intent.Remote != "origin" {
		t.Fatalf("target = %s/%s", intent.Remote, intent.TargetBranch)
	}
}
```

Add a focused helper test:

```go
func TestDeriveSideEffectIntentIgnoresAbsoluteAndParentPaths(t *testing.T) {
	intent := deriveSideEffectIntentFromText(1, "write /tmp/bad.md and ../bad.md and docs/good.md")
	if intent == nil || !containsString(intent.AllowedPaths, "docs/good.md") {
		t.Fatalf("intent = %#v", intent)
	}
	if containsString(intent.AllowedPaths, "/tmp/bad.md") || containsString(intent.AllowedPaths, "../bad.md") {
		t.Fatalf("unsafe allowed paths = %#v", intent.AllowedPaths)
	}
}
```

Add this helper near the existing string-slice test helpers if one does not already exist:

```go
func containsSideEffectAction(actions []SideEffectAction, want SideEffectAction) bool {
	for _, action := range actions {
		if action == want {
			return true
		}
	}
	return false
}
```

**Step 2: Run tests to verify failure**

Run: `go test -count=1 ./internal/react -run 'TestRunnerCreatesSideEffectIntentForWriteCommitPushRequest|TestDeriveSideEffectIntentIgnoresAbsoluteAndParentPaths'`

Expected: FAIL.

**Step 3: Implement derivation helpers**

In `internal/react/side_effect_intent.go`, add:

```go
func deriveSideEffectIntentFromText(turn int, text string) *SideEffectIntent {
	paths := extractMarkdownAndNamedPaths(text)
	if len(paths) == 0 {
		return nil
	}
	intent := &SideEffectIntent{
		ID:            fmt.Sprintf("intent-%d", turn),
		SourceTurn:    turn,
		ArtifactPaths: paths,
		AllowedPaths:  paths,
		Remote:        "origin",
	}
	if inputSuggestsFileWrites(normalizeToolIntentText(text)) {
		intent.RequiredActions = append(intent.RequiredActions, SideEffectActionWrite)
	}
	if inputSuggestsGitCommit(normalizeToolIntentText(text)) {
		intent.RequiredActions = append(intent.RequiredActions, SideEffectActionCommit)
	}
	if inputSuggestsGitPush(normalizeToolIntentText(text)) {
		intent.RequiredActions = append(intent.RequiredActions, SideEffectActionPush)
	}
	intent.TargetBranch = extractTargetBranch(text)
	if intent.TargetBranch == "" && containsSideEffectAction(intent.RequiredActions, SideEffectActionPush) {
		intent.TargetBranch = "main"
	}
	intent.Gates = initialGatesForActions(intent.RequiredActions)
	return intent
}
```

Keep extraction intentionally conservative:

- Accept `.md` paths from current `extractDelegationTargetPath` style.
- Accept common repo-relative paths with `/` or known extensions.
- Reject absolute paths and parent traversal.
- Do not infer all changed files; only infer explicitly named targets.

**Step 4: Set intent at turn start**

In `Runner.RunWithParts`, after `RecordInputWithParts` succeeds and before direct write handling, call:

```go
if intent := deriveSideEffectIntentFromText(turn, prompt); intent != nil {
	r.session.SetSideEffectIntent(*intent)
}
```

If a previous intent exists and the new prompt does not mention side effects, keep it. If the prompt says cancel/forget/no commit, clear or downgrade only the matching action.

**Step 5: Update pending delegation action to feed intent**

In `updatePostDelegationWorkflow`, when creating `DelegationActionWriteDoc`, also call a helper that merges target path into the active intent:

```go
r.ensureSideEffectIntentForDelegation(action, snapshot.LastInput+"\n"+result)
```

This ensures child-reported paths are not the sole source; the user request remains authoritative when available.

**Step 6: Run tests**

Run: `go test -count=1 ./internal/react -run 'SideEffectIntent|PostDelegation|RestoresActionTools'`

Expected: PASS.

**Step 7: Checkpoint**

Inspect: `git diff -- internal/react/side_effect_intent.go internal/react/loop.go internal/react/loop_test.go`

Do not commit unless explicitly requested.

## Task 4: Block Control-Plane Text From Artifact Writes

**Files:**
- Modify: `internal/react/side_effect_intent.go`
- Modify: `internal/react/loop.go`
- Test: `internal/react/loop_test.go`

**Step 1: Write failing test for child handoff report overwrite**

Add to `internal/react/loop_test.go`:

```go
func TestRunnerBlocksControlPlaneReportWriteToArtifactPath(t *testing.T) {
	reg := agenttools.NewRegistry()
	reg.Register(agenttools.Tool{Name: "write_file", Description: "write", Parameters: []agenttools.ParameterDef{
		{Name: "path", Type: "string", Required: true},
		{Name: "content", Type: "string", Required: true},
	}, AutoApprove: true, Execute: func(context.Context, map[string]any) (string, error) {
		return "wrote", nil
	}})
	session := NewSession()
	session.SetSideEffectIntent(SideEffectIntent{
		ID:            "intent-1",
		ArtifactPaths: []string{"FORGE_VS_CODEX.md"},
		AllowedPaths:  []string{"FORGE_VS_CODEX.md"},
	})
	active, cancel, err := session.BeginTurn(context.Background(), "turn-1")
	if err != nil { t.Fatal(err) }
	defer cancel()
	r := NewRunner(Config{Tools: reg, Session: session})

	err = r.executeNativeToolCalls(active.Context, active.Number, []llm.NativeToolCall{{
		ID: "write-1", Name: "write_file", ArgsJSON: `{"path":"FORGE_VS_CODEX.md","content":"I've successfully created the commit, but I have a couple of issues to report:"}`,
	}})
	if err != nil { t.Fatal(err) }

	if !sessionHistoryContains(session, "blocked", "control-plane") {
		t.Fatalf("history missing control-plane block feedback: %#v", session.Snapshot().History)
	}
}
```

**Step 2: Run test to verify failure**

Run: `go test -count=1 ./internal/react -run TestRunnerBlocksControlPlaneReportWriteToArtifactPath`

Expected: FAIL because the write is not blocked.

**Step 3: Add classifier helper**

In `internal/react/side_effect_intent.go`, add:

```go
func looksLikeControlPlaneArtifactContent(content string) bool {
	lower := strings.ToLower(strings.TrimSpace(content))
	needles := []string{
		"i've successfully created the commit",
		"i have a couple of issues to report",
		"forge_handoff",
		"remaining_actions",
		"accidental_write",
		"unresolved push",
		"child agent",
		"sub-agent",
		"tool was unavailable",
	}
	for _, needle := range needles {
		if strings.Contains(lower, needle) {
			return true
		}
	}
	return false
}
```

Keep this small and conservative. It is a safety tripwire, not a content classifier.

**Step 4: Block before tool execution**

In `beforeToolHookOutput` or the native tool execution path before `executeToolCall`, if:

- tool is `write_file`, `edit_file`, or `apply_patch`;
- target path matches `SideEffectIntent.ArtifactPaths`;
- new content looks like control-plane content;

then append a recoverable tool result with content like:

```text
blocked: refusing to write control-plane child-agent report text into requested artifact FORGE_VS_CODEX.md. Synthesize the user-facing document content instead, or ask the user if they explicitly want an incident report in this file.
```

Do not execute the file mutation.

**Step 5: Run tests**

Run: `go test -count=1 ./internal/react -run 'TestRunnerBlocksControlPlaneReportWriteToArtifactPath|Handoff|PostDelegation'`

Expected: PASS.

**Step 6: Checkpoint**

Inspect: `git diff -- internal/react/side_effect_intent.go internal/react/loop.go internal/react/loop_test.go`

Do not commit unless explicitly requested.

## Task 5: Replace Unsafe `git_commit` With Scoped Git Transaction Semantics

**Files:**
- Create: `internal/agent/tools/git_scoped.go`
- Modify: `internal/agent/tools/git.go`
- Test: `internal/agent/tools/git_test.go`
- Modify: `internal/runtime/chat.go`

**Step 1: Write failing tests for scoped commit**

Add to `internal/agent/tools/git_test.go`:

```go
func TestGitCommitScopedStagesOnlyAllowedPaths(t *testing.T) {
	dir := initGitRepo(t)
	mustWriteFile(t, filepath.Join(dir, "FORGE_VS_CODEX.md"), "doc\n")
	mustWriteFile(t, filepath.Join(dir, "unrelated.go"), "package main\n")

	scope := func() GitScope {
		return GitScope{AllowedPaths: []string{"FORGE_VS_CODEX.md"}, TargetBranch: "main"}
	}
	tool := NewGitCommitScoped(dir, func(Action) (bool, error) { return true, nil }, scope)
	result, err := tool.Execute(context.Background(), map[string]any{"message": "add comparison"})
	if err != nil { t.Fatal(err) }
	if !strings.Contains(result, "commit") {
		t.Fatalf("result = %q", result)
	}

	out := gitOut(t, dir, "show", "--name-only", "--format=", "HEAD")
	if !strings.Contains(out, "FORGE_VS_CODEX.md") || strings.Contains(out, "unrelated.go") {
		t.Fatalf("commit files = %q", out)
	}
	status := gitOut(t, dir, "status", "--porcelain")
	if !strings.Contains(status, "unrelated.go") {
		t.Fatalf("unrelated file should remain dirty, status=%q", status)
	}
}

func TestGitCommitScopedRejectsPreStagedUnrelatedFile(t *testing.T) {
	dir := initGitRepo(t)
	mustWriteFile(t, filepath.Join(dir, "FORGE_VS_CODEX.md"), "doc\n")
	mustWriteFile(t, filepath.Join(dir, "AI-1.md"), "agent report\n")
	runGit(t, dir, "add", "AI-1.md")

	scope := func() GitScope { return GitScope{AllowedPaths: []string{"FORGE_VS_CODEX.md"}} }
	tool := NewGitCommitScoped(dir, func(Action) (bool, error) { return true, nil }, scope)
	result, err := tool.Execute(context.Background(), map[string]any{"message": "add comparison"})
	if err != nil { t.Fatal(err) }
	if !strings.Contains(result, "blocked") || !strings.Contains(result, "AI-1.md") {
		t.Fatalf("result = %q", result)
	}
}
```

Add small test helpers if not already present:

```go
func mustWriteFile(t *testing.T, path, content string)
func gitOut(t *testing.T, dir string, args ...string) string
func runGit(t *testing.T, dir string, args ...string)
```

**Step 2: Run tests to verify failure**

Run: `go test -count=1 ./internal/agent/tools -run 'TestGitCommitScopedStagesOnlyAllowedPaths|TestGitCommitScopedRejectsPreStagedUnrelatedFile'`

Expected: FAIL because `NewGitCommitScoped` and `GitScope` do not exist.

**Step 3: Implement scoped git helper**

Create `internal/agent/tools/git_scoped.go` with:

```go
type GitScope struct {
	AllowedPaths  []string
	TargetBranch  string
	Remote        string
	RequireBranch bool
}

type GitScopeProvider func() GitScope

func NewGitCommitScoped(workDir string, approve ApprovalFunc, scope GitScopeProvider) Tool
```

Implementation requirements:

- Normalize allowed paths with a helper that rejects absolute paths and `..`.
- Inspect pre-existing staged files with `git diff --cached --name-only -z`.
- If staged files include anything outside the allowlist, return a blocked result and do not mutate staging.
- Stage only allowed paths with fixed argv: `git add -- <paths...>`.
- Re-read staged files with `git diff --cached --name-only -z`.
- Refuse if staged files are empty.
- Refuse if staged files are outside allowlist.
- Show approval detail with staged file list and `git diff --cached --stat`.
- Run `git commit -m <message>`.
- Verify commit file list with `git show --name-only --format= HEAD`.
- Refuse success if commit file list differs from staged allowlist.

Do not use `sh -c`. Do not use `git add -A`.

**Step 4: Make legacy `git_commit` safe**

Modify `NewGitCommit` in `internal/agent/tools/git.go` to call `NewGitCommitScoped` with a provider that returns an empty `GitScope` and return a blocked result when no scope is supplied:

```text
blocked: git_commit requires an active side-effect intent with allowed_paths; use scoped git transaction tools after the runtime captures the requested target files.
```

This intentionally breaks message-only commits. It prevents the exact `git add -A` failure class.

**Step 5: Register scoped git commit with session scope**

Modify `internal/runtime/chat.go`:

- Add a small provider that reads `session.Snapshot().SideEffectIntent` at execution time and converts it to `tools.GitScope`.
- Register `NewGitCommitScoped` with that provider instead of unsafe `NewGitCommit` when a session exists.
- Keep tool name `git_commit` initially to minimize prompt/exposure churn, but update description to say it commits only active intent allowlist paths.

**Step 6: Update existing tests**

Update existing tests that expect message-only `git_commit` to succeed:

- `internal/agent/tools/git_test.go:198-241` should use `NewGitCommitScoped` with allowed path.
- `internal/react/loop_test.go:2657-2737` should register `NewGitCommitScoped` or set session intent before calling `git_commit`.

**Step 7: Run tests**

Run: `go test -count=1 ./internal/agent/tools ./internal/react ./internal/runtime -run 'GitCommit|SideEffectIntent|ToolContracts|ToolSchemaFixture'`

Expected: PASS.

**Step 8: Checkpoint**

Inspect: `git diff -- internal/agent/tools internal/runtime/chat.go internal/react/loop_test.go`

Do not commit unless explicitly requested.

## Task 6: Add Scoped Push Verification

**Files:**
- Modify: `internal/agent/tools/git_scoped.go`
- Test: `internal/agent/tools/git_test.go`
- Modify: `internal/runtime/chat.go`
- Modify: `internal/react/loop.go`
- Test: `internal/react/loop_test.go`

**Step 1: Write failing scoped push test**

Add to `internal/agent/tools/git_test.go`:

```go
func TestGitPushScopedVerifiesRemoteContainsCommit(t *testing.T) {
	dir := initGitRepo(t)
	remote := filepath.Join(t.TempDir(), "remote.git")
	runGit(t, t.TempDir(), "init", "--bare", remote)
	runGit(t, dir, "branch", "-M", "main")
	runGit(t, dir, "remote", "add", "origin", remote)

	mustWriteFile(t, filepath.Join(dir, "FORGE_VS_CODEX.md"), "doc\n")
	scope := func() GitScope {
		return GitScope{AllowedPaths: []string{"FORGE_VS_CODEX.md"}, TargetBranch: "main", Remote: "origin", RequireBranch: true}
	}
	commit := NewGitCommitScoped(dir, func(Action) (bool, error) { return true, nil }, scope)
	if _, err := commit.Execute(context.Background(), map[string]any{"message": "add comparison"}); err != nil { t.Fatal(err) }

	push := NewGitPushScoped(dir, func(Action) (bool, error) { return true, nil }, scope)
	result, err := push.Execute(context.Background(), map[string]any{})
	if err != nil { t.Fatal(err) }
	if !strings.Contains(result, "remote contains") {
		t.Fatalf("result = %q", result)
	}
}
```

**Step 2: Run test to verify failure**

Run: `go test -count=1 ./internal/agent/tools -run TestGitPushScopedVerifiesRemoteContainsCommit`

Expected: FAIL because `NewGitPushScoped` does not exist.

**Step 3: Implement `git_push_scoped`**

In `internal/agent/tools/git_scoped.go`, add:

```go
func NewGitPushScoped(workDir string, approve ApprovalFunc, scope GitScopeProvider) Tool
```

Implementation requirements:

- Require non-empty `scope.Remote` and `scope.TargetBranch`.
- Verify current branch with `git branch --show-current` when `RequireBranch` is true.
- Verify local HEAD with `git rev-parse HEAD`.
- Ask approval with summary `git push <remote> HEAD:<targetBranch>` and detail containing current branch, HEAD, remote, target branch, and allowed paths.
- Run `git push <remote> HEAD:<targetBranch>` with fixed argv.
- Verify remote with `git ls-remote <remote> refs/heads/<targetBranch>` and require the advertised SHA equals local HEAD.
- Return result containing `remote contains <sha>` on success.

**Step 4: Register and expose scoped push**

Modify `internal/runtime/chat.go` to register `git_push` as hidden by default, backed by active `SideEffectIntent`.

Modify `gitReadToolNames` or a new `gitMutationToolNames` in `internal/react/loop.go` so `git_push` is exposed only when active intent requires push.

**Step 5: Add loop tool exposure test**

Add to `internal/react/loop_test.go`:

```go
func TestRunnerExposesGitPushOnlyForPushIntent(t *testing.T) {
	reg := agenttools.NewRegistry()
	for _, name := range []string{"git_status", "git_commit", "git_push"} {
		reg.Register(agenttools.Tool{Name: name, Description: name})
	}
	r := NewRunner(Config{Tools: reg})
	snap := SessionSnapshot{SideEffectIntent: &SideEffectIntent{RequiredActions: []SideEffectAction{SideEffectActionCommit, SideEffectActionPush}}}
	names := toolDefNames(r.selectToolDefs(snap))
	if !containsString(names, "git_push") {
		t.Fatalf("tools = %#v, want git_push", names)
	}
}
```

**Step 6: Run tests**

Run: `go test -count=1 ./internal/agent/tools ./internal/react ./internal/runtime -run 'GitPush|GitCommit|SideEffectIntent|ToolSchemaFixture'`

Expected: PASS.

**Step 7: Checkpoint**

Inspect: `git diff -- internal/agent/tools internal/runtime/chat.go internal/react/loop.go internal/react/loop_test.go`

Do not commit unless explicitly requested.

## Task 7: Block Generic Shell Git Mutation During Active Intent

**Files:**
- Modify: `internal/react/loop.go`
- Test: `internal/react/loop_test.go`
- Modify: `internal/agent/tools/command_test.go` only if command parsing helper is moved there

**Step 1: Write failing shell-block tests**

Add to `internal/react/loop_test.go`:

```go
func TestRunnerBlocksShellGitMutationDuringScopedIntent(t *testing.T) {
	r := NewRunner(Config{})
	session := NewSession()
	session.SetSideEffectIntent(SideEffectIntent{ID: "intent-1", AllowedPaths: []string{"FORGE_VS_CODEX.md"}, RequiredActions: []SideEffectAction{SideEffectActionCommit}})
	r.session = session

	for _, command := range []string{"git add .", "git add -A", "git commit -m x", "git push origin HEAD:main"} {
		t.Run(command, func(t *testing.T) {
			output := r.beforeToolHookOutput(context.Background(), "run_command", map[string]any{"command": command})
			if output.Block == nil || !strings.Contains(output.Block.Message, "scoped git transaction") {
				t.Fatalf("block for %q = %#v", command, output.Block)
			}
		})
	}
}
```

**Step 2: Run test to verify failure**

Run: `go test -count=1 ./internal/react -run TestRunnerBlocksShellGitMutationDuringScopedIntent`

Expected: FAIL.

**Step 3: Add block hook logic**

In `beforeToolGitCommitBlockHook` or a new hook registered at `hooks.PointBeforeTool`, if active `SideEffectIntent` exists and tool is `run_command`, block commands matching mutating git forms:

- `git add`
- `git commit`
- `git push`
- `git reset`
- `git checkout` with path-like args
- `git restore`
- `git clean`

Message:

```text
blocked: scoped git transaction is active. Use git_commit/git_push so Forge can enforce allowed paths, branch, remote, and verification gates.
```

Do not block read-only commands such as `git status`, `git diff`, `git log`, `git show`, or `git branch --show-current`.

**Step 4: Run tests**

Run: `go test -count=1 ./internal/react -run 'ShellGitMutation|CommitWorkflow|GitWorkflow'`

Expected: PASS.

**Step 5: Checkpoint**

Inspect: `git diff -- internal/react/loop.go internal/react/loop_test.go`

Do not commit unless explicitly requested.

## Task 8: Gate Final Success On Required Evidence

**Files:**
- Modify: `internal/react/side_effect_intent.go`
- Modify: `internal/react/loop.go`
- Test: `internal/react/loop_test.go`

**Step 1: Write failing final-synthesis gate tests**

Add to `internal/react/loop_test.go`:

```go
func TestRunnerBlocksFinalSuccessWhenSideEffectGatesPending(t *testing.T) {
	driver := &nativeSequenceDriver{steps: [][]llm.Token{{{Text: "Done, committed and pushed."}}}}
	session := NewSession()
	session.SetSideEffectIntent(SideEffectIntent{
		ID: "intent-1",
		AllowedPaths: []string{"FORGE_VS_CODEX.md"},
		RequiredActions: []SideEffectAction{SideEffectActionWrite, SideEffectActionCommit, SideEffectActionPush},
		Gates: []SideEffectGate{
			{Name: "write", Status: SideEffectGatePassed, Evidence: "write_file ok"},
			{Name: "commit", Status: SideEffectGatePending},
			{Name: "push", Status: SideEffectGatePending},
		},
	})
	r := NewRunner(Config{Driver: driver, Session: session})

	if err := r.Run(context.Background(), "continue"); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(r.LastResponse(), "committed and pushed") {
		t.Fatalf("final response = %q, should not claim unresolved gates", r.LastResponse())
	}
	if !sessionHistoryContains(session, "unresolved side-effect gates", "commit") {
		t.Fatalf("history missing gate feedback: %#v", session.Snapshot().History)
	}
}
```

**Step 2: Run test to verify failure**

Run: `go test -count=1 ./internal/react -run TestRunnerBlocksFinalSuccessWhenSideEffectGatesPending`

Expected: FAIL because final text is not gate-checked.

**Step 3: Add gate evaluator**

In `internal/react/side_effect_intent.go`, add:

```go
func unresolvedSideEffectGates(intent *SideEffectIntent) []SideEffectGate
func finalResponseClaimsSideEffectSuccess(text string) bool
func sideEffectGateFeedback(intent *SideEffectIntent) string
```

Keep `finalResponseClaimsSideEffectSuccess` conservative: match phrases like `committed`, `pushed`, `uploaded`, `remote`, `done`, `complete`, `created the commit` only when an active intent has matching required actions.

**Step 4: Use evaluator before completion**

In the `runLoop` path where assistant text is about to complete the turn, if:

- active intent has unresolved gates;
- final text claims side-effect success;

then append a runtime user/tool feedback message and continue the model loop instead of completing with that final response.

Feedback should include exact pending gates and the safe tools to call.

**Step 5: Mark gates from tool results**

In `updateWorkflowAfterToolResult`, update active intent gates:

- `write_file` success on artifact path -> pass `write` gate.
- `git_commit` success with commit hash/file list -> pass `commit` gate.
- `git_push` success with remote verification -> pass `push` gate.
- blocked/error result -> failed gate with evidence.

**Step 6: Run tests**

Run: `go test -count=1 ./internal/react -run 'SideEffectGates|GitCommit|GitPush|PostDelegation'`

Expected: PASS.

**Step 7: Checkpoint**

Inspect: `git diff -- internal/react/side_effect_intent.go internal/react/loop.go internal/react/loop_test.go`

Do not commit unless explicitly requested.

## Task 9: Restrict Child Agent Mutation By Default

**Files:**
- Modify: `internal/runtime/chat.go`
- Modify: `internal/react/tools/spawn_agent.go`
- Test: `internal/react/tools/spawn_wait_test.go`
- Test: `internal/runtime/chat_test.go`

**Step 1: Write failing child permission tests**

Add to `internal/runtime/chat_test.go` near existing child read-only tests:

```go
func TestRegisterDelegationToolsStripsGitMutationFromChildren(t *testing.T) {
	cfg, err := config.Load(filepath.Join(t.TempDir(), "forge.toml"))
	if err != nil { t.Fatal(err) }
	reg := tools.NewRegistry()
	session := reactruntime.NewSession()
	approve := func(tools.Action) (bool, error) { return true, nil }
	registerTools(reg, t.TempDir(), cfg, session, approve, nil, nil)

	// Use the helper that constructs child registries/prompts. Assert research roles
	// do not receive write_file, run_command, git_commit, or git_push.
	childReg := childRegistryForRole(reg, "repo-auditor", session.Snapshot())
	for _, forbidden := range []string{"write_file", "edit_file", "apply_patch", "run_command", "git_commit", "git_push"} {
		if _, ok := childReg.Get(forbidden); ok {
			t.Fatalf("child registry includes forbidden tool %s", forbidden)
		}
	}
}
```

Adapt the exact helper name to the current registration code. If no helper exists, extract one from `registerReactDelegationTools` first.

**Step 2: Run test to verify failure**

Run: `go test -count=1 ./internal/runtime -run TestRegisterDelegationToolsStripsGitMutationFromChildren`

Expected: FAIL until child registry behavior is made explicit/testable.

**Step 3: Extract child tool policy helper**

In `internal/runtime/chat.go`, extract child registry selection into a testable function:

```go
func childRegistryForRole(base *tools.Registry, role string, snap reactruntime.SessionSnapshot) *tools.Registry
```

Rules:

- Research/audit/explore/review roles: read-only tools plus `git_status`, `git_diff`, `git_log`, `git_branch_state`; no write, run, commit, push.
- Implementer roles: write tools only if active `SideEffectIntent.AllowedPaths` is non-empty; still no commit/push.
- Commit/push remains parent-owned unless a future explicit child-owned contract is added.

**Step 4: Strengthen child prompt**

In `internal/react/tools/spawn_agent.go`, update `AgentHandoffInstructions` usage or child prompt text to state:

```text
Child agents must not commit or push. Return findings and proposed artifact content. Parent/orchestrator owns write, commit, push, and verification gates.
```

**Step 5: Run tests**

Run: `go test -count=1 ./internal/runtime ./internal/react/tools -run 'Child|Spawn|Delegation|GitMutation'`

Expected: PASS.

**Step 6: Checkpoint**

Inspect: `git diff -- internal/runtime/chat.go internal/runtime/chat_test.go internal/react/tools/spawn_agent.go internal/react/tools/spawn_wait_test.go`

Do not commit unless explicitly requested.

## Task 10: Add Dirty-Worktree Live Acceptance Regression

**Files:**
- Modify: `cmd/forge/live_acceptance_test.go`

**Step 1: Add failing live acceptance test**

Add a new test:

```go
func TestLiveAcceptanceScopedDocCommitPushDirtyWorktreeWithLocalProvider(t *testing.T) {
	server := newLiveAcceptanceMock(t)
	defer server.Close()
	bin := buildForgeBinary(t)
	workDir := initLiveAcceptanceFixture(t)
	initLiveAcceptanceBareRemote(t, workDir)
	writeTextFile(t, filepath.Join(workDir, "AI-1.md"), "unrelated child scratch\n")
	writeTextFile(t, filepath.Join(workDir, "internal", "react", "loop.go"), "package react\n// unrelated dirty change\n")
	configHome := writeLiveAcceptanceConfig(t, server.URL())

	output, _ := runForgeConsole(t, bin, configHome, workDir, strings.Join([]string{
		`LIVE_SCOPED_DOC_COMMIT_PUSH_CHECK: write FORGE_VS_CODEX.md, commit only that file to main, and push origin main. If a child reports accidental extra files or unresolved push, resolve safely without overwriting the doc with the report.`,
		`/quit`,
	}, "\n")+"\n")

	if strings.Contains(readTextFile(t, filepath.Join(workDir, "FORGE_VS_CODEX.md")), "I've successfully created the commit") {
		t.Fatal("artifact was overwritten with control-plane report")
	}
	commitFiles := gitOutput(t, workDir, "show", "--name-only", "--format=", "HEAD")
	if strings.TrimSpace(commitFiles) != "FORGE_VS_CODEX.md" {
		t.Fatalf("commit files = %q", commitFiles)
	}
	status := gitOutput(t, workDir, "status", "--porcelain")
	if !strings.Contains(status, "AI-1.md") {
		t.Fatalf("unrelated dirty file was not preserved, status=%q", status)
	}
	if !strings.Contains(output, "SCOPED_DOC_PUSH_VERIFIED") {
		t.Fatalf("console output missing verified completion:\n%s", output)
	}
	server.AssertScopedDocCommitPush(t)
}
```

Use existing helper style in this file. Add helpers only if missing: `initLiveAcceptanceBareRemote`, `gitOutput`, `writeTextFile`.

**Step 2: Script local provider sequence**

Extend `liveAcceptanceMock` with flags:

```go
scopedDocStarted bool
scopedDocWrite bool
scopedDocCommit bool
scopedDocPush bool
scopedDocNoShellGitMutation bool
```

Add `ServeHTTP` branches that force the failure path:

1. Parent writes `FORGE_VS_CODEX.md` with real doc content.
2. Provider attempts to write control-plane text to the same path; runtime must block it.
3. Provider attempts `run_command` with `git add -A`; runtime must block it.
4. Provider calls `git_commit`.
5. Provider calls `git_push`.
6. Provider answers `SCOPED_DOC_PUSH_VERIFIED` only after tool results show commit and push gates passed.

**Step 3: Run test to verify failure before implementation is complete**

Run: `go test -count=1 ./cmd/forge -run TestLiveAcceptanceScopedDocCommitPushDirtyWorktreeWithLocalProvider`

Expected before earlier tasks: FAIL due to unsafe commit behavior or missing scoped tools. Expected after earlier tasks: PASS.

**Step 4: Run existing live acceptance subset**

Run: `go test -count=1 ./cmd/forge -run 'TestLiveAcceptance(DelegatedAuditWritesReport|ComparisonReposWritesMarkup|ScopedDocCommitPush)'`

Expected: PASS.

**Step 5: Checkpoint**

Inspect: `git diff -- cmd/forge/live_acceptance_test.go`

Do not commit unless explicitly requested.

## Task 11: Documentation Closeout

**Files:**
- Modify: `docs/reliability-security-roadmap.md`
- Create or Modify: `docs/plans/2026-05-18-side-effect-transaction-enforcement-closeout.md` only if maintainers want a closeout note
- Modify: `latest-failure-tool-enforcement-survey.md` only if implementation details materially changed the plan

**Step 1: Update roadmap**

Add a new section after the 2026-05-17 Agent Handoff Safety section:

```markdown
## 2026-05-18 Side-Effect Transaction Kernel

Forge now binds scoped write/commit/push requests to runtime side-effect intent contracts. Artifact paths, allowed commit paths, required git actions, target branch, remote, and verification gates are runtime state. Child reports and handoffs are control-plane data and cannot overwrite requested artifacts. Git mutation flows use scoped tools that stage only allowed paths, verify commit file lists, verify remote refs after push, and block final success until required gates pass.
```

Only mark as complete after Task 10 passes.

**Step 2: Add verification evidence**

Record the exact commands run and results in the closeout note or roadmap subsection:

- `go test -count=1 ./internal/agent/tools ./internal/react ./internal/runtime ./internal/sessionstore ./internal/protocol`
- `go test -count=1 ./cmd/forge -run 'TestLiveAcceptance(DelegatedAuditWritesReport|ComparisonReposWritesMarkup|ScopedDocCommitPush)'`
- `go test -count=1 ./...`
- `just build`
- `git diff --check`

**Step 3: Run doc check**

Run: `git diff --check`

Expected: PASS.

**Step 4: Checkpoint**

Inspect: `git diff -- docs/reliability-security-roadmap.md docs/plans latest-failure-tool-enforcement-survey.md`

Do not commit unless explicitly requested.

## Task 12: Final Verification

**Files:**
- No code changes.

**Step 1: Run focused package suite**

Run: `go test -count=1 ./internal/agent/tools ./internal/react ./internal/runtime ./internal/sessionstore ./internal/protocol`

Expected: PASS.

**Step 2: Run live regression suite**

Run: `go test -count=1 ./cmd/forge -run 'TestLiveAcceptance(DelegatedAuditWritesReport|ComparisonReposWritesMarkup|ScopedDocCommitPush|StatusAndCancellation|MultipleAgentsStatus)'`

Expected: PASS.

**Step 3: Run full suite**

Run: `go test -count=1 ./...`

Expected: PASS.

**Step 4: Build**

Run: `just build`

Expected: PASS.

**Step 5: Diff hygiene**

Run: `git diff --check`

Expected: PASS.

**Step 6: Final state inspection**

Run: `git status --short --branch`

Expected: Only intended files are modified/untracked. No generated `cmd/forge/output` artifacts remain unless intentionally documented.

## Acceptance Criteria

- `git_commit` no longer stages all changes with `git add -A`.
- Scoped commit refuses unrelated pre-staged files.
- Scoped commit creates a commit containing only active intent allowlist paths.
- Scoped push verifies the remote branch contains the local commit.
- Shell git mutation is blocked while scoped git intent is active.
- Child handoff/control-plane text cannot overwrite the requested artifact path.
- Child agents do not receive commit/push tools by default.
- Final success claims are blocked while write/commit/push gates are unresolved.
- Dirty-worktree live acceptance test reproduces and prevents the latest failure.
- Existing delegated write, comparison markup, status/cancel, secret, compaction, and git merge tests still pass.

## Execution Notes

- Preserve unrelated user dirty work. Never revert files outside the task scope.
- Do not use `git add -A`, `git add .`, or shell git mutation in tests except when explicitly testing that Forge blocks it.
- Do not add broad prompt-only fixes as substitutes for runtime gates.
- Keep old `git_status`, `git_diff`, and `git_log` read-only paths auto-approved.
- If a gate cannot be verified because the remote is unavailable, the correct result is unresolved/failed gate feedback, not a success claim.
