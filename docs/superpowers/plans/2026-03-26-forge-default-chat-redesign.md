# Forge Default Chat Redesign Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ship a default `forge` chat that behaves like a copy-friendly Codex-style terminal transcript, keeps orchestration hidden unless `-d` is enabled, and gives hidden workers the same real skill access contract as the primary assistant.

**Architecture:** Keep the existing kernel runtime and Bubble Tea shell, but split the chat surface into an append-first default mode and a managed debug mode. Introduce focused TUI primitives for surface mode, transcript records, composer behavior, and live progress, then extend the harness worker contract with a shared `internal/skills` runtime adapter so workers inherit real skills and fail closed instead of bluffing.

**Tech Stack:** Go, Bubble Tea, Lip Gloss, existing `internal/runtime`, existing `internal/harness`, existing `internal/agent`, existing `internal/skills`, Go test tooling, and a PTY test helper dependency for terminal-mode integration coverage.

---

## File Map

### Runtime and surface selection

- Modify: `internal/runtime/chat.go`
  Purpose: pass explicit default-vs-debug surface mode into the TUI, wire worker skill context into the harness manager, and keep kernel mode as the default chat runtime.
- Modify: `internal/runtime/chat_test.go`
  Purpose: lock runtime wiring for default surface mode, worker skill inheritance, and debug-only advanced view behavior.
- Modify: `internal/runtime/chat_debug.go`
  Purpose: keep `-d` as the sole entrypoint for advanced trace visibility and log the selected surface mode.
- Modify: `internal/runtime/chat_debug_test.go`
  Purpose: cover debug-mode logging, mode metadata, and fresh-file behavior.
- Create: `internal/runtime/chat_pty_test.go`
  Purpose: PTY-backed integration coverage proving default chat does not enter the alternate screen and preserves append-first transcript behavior.

### TUI surface primitives

- Create: `internal/tui/chatsurface.go`
  Purpose: define `SurfaceModeConfig` and Bubble Tea option selection for default and debug surfaces.
- Create: `internal/tui/chatsurface_test.go`
  Purpose: lock alt-screen, mouse-capture, bracketed-paste, and live-region flags per surface.
- Create: `internal/tui/chatrecords.go`
  Purpose: define the durable transcript record model and segment types used by default chat rendering.
- Create: `internal/tui/chatrecords_test.go`
  Purpose: verify transcript record creation, ordering, and code-segment handling.
- Create: `internal/tui/chatcomposer.go`
  Purpose: isolate composer behavior for `Enter`, `Shift+Enter`, draft clearing, height limits, and interrupts.
- Create: `internal/tui/chatcomposer_test.go`
  Purpose: lock composer key handling, visible height, paste behavior, and single-active-turn semantics.
- Create: `internal/tui/chatprogress.go`
  Purpose: own the one-line live progress slot for the append-first default surface.
- Create: `internal/tui/chatprogress_test.go`
  Purpose: verify replace-in-place progress semantics and safe fallback when the live region cannot be updated.
- Modify: `internal/tui/chatshared.go`
  Purpose: extend `ChatLiveConfig` with explicit surface-mode fields and remove default reliance on a managed viewport mental model.
- Modify: `internal/tui/chatlive_bubbletea.go`
  Purpose: build the Bubble Tea program from `SurfaceModeConfig` instead of always enabling alt-screen and mouse capture.
- Modify: `internal/tui/chatmodel.go`
  Purpose: consume transcript records, composer actions, and live progress state while rendering a single-column default transcript.
- Modify: `internal/tui/chatmodel_test.go`
  Purpose: cover prompt echo/clear, single-column rendering, debug-only overlays, and progress-slot behavior.
- Modify: `internal/tui/chatmsg.go`
  Purpose: flatten message styling so labels and spacing replace the current bordered cards.
- Modify: `internal/tui/chatmsg_test.go`
  Purpose: verify the new flat rendering while preserving code/content readability.
- Modify: `internal/tui/chattheme.go`
  Purpose: move the default theme to a muted graphite palette with one restrained focus accent.
- Modify: `internal/tui/chattheme_test.go`
  Purpose: lock the renamed/default theme behavior and keep legacy aliases working when still needed.
- Modify: `internal/tui/chatstats.go`
  Purpose: reduce the top chrome to a slim status line in default mode and keep richer stats in debug overlays only.
- Modify: `internal/tui/traceview.go`
  Purpose: keep trace rendering strictly debug-only.
- Modify: `internal/tui/view_test.go`
  Purpose: verify the final rendered layout shape for default and debug modes.

### Worker and skill runtime

- Create: `internal/skills/runtime.go`
  Purpose: define the shared skill runtime adapter used by the primary assistant and hidden workers.
- Create: `internal/skills/runtime_test.go`
  Purpose: verify skill catalog generation, required-skill resolution, auto-skill resolution, and non-shell injection behavior.
- Modify: `internal/skills/skills.go`
  Purpose: expose stable skill descriptors and keep skill loading reusable outside the top-level chat path.
- Modify: `internal/skills/skills_test.go`
  Purpose: cover descriptor generation and runtime loading expectations.
- Modify: `internal/skills/auto.go`
  Purpose: reuse auto-skill selection through the shared runtime adapter instead of ad hoc call sites.
- Modify: `internal/skills/enforce.go`
  Purpose: let the harness and workers ask for required-skill decisions through one path.
- Modify: `internal/harness/types.go`
  Purpose: extend `WorkerTask` with typed skill context, permission profile, and deadline/cancellation metadata.
- Modify: `internal/harness/workers.go`
  Purpose: create worker agents with loaded skills and apply the shared skill runtime adapter before worker execution.
- Modify: `internal/harness/runner.go`
  Purpose: populate worker skill context, keep top-level answer ownership, and preserve fail-closed fallback behavior.
- Modify: `internal/harness/planner.go`
  Purpose: keep worker admission capability-based while carrying the metadata needed for worker skill use.
- Modify: `internal/harness/policy.go`
  Purpose: tighten surfacing/fallback rules for worker failures and noisy orchestration chatter.
- Modify: `internal/harness/contracts.go`
  Purpose: preserve the structured worker result contract while allowing worker-side skill use.
- Modify: `internal/harness/policy_test.go`
  Purpose: verify capability-based worker admission and error-surfacing policy.
- Modify: `internal/harness/runner_test.go`
  Purpose: verify top-level response ownership, worker fallback behavior, and hidden worker progress.
- Modify: `internal/harness/workers_test.go`
  Purpose: verify worker skill inheritance, required-skill failure handling, and no shell-based slash execution.
- Modify: `internal/harness/contracts_test.go`
  Purpose: ensure structured worker results stay valid after worker contract changes.

### Agent renderer and progress wording

- Modify: `internal/agent/system.go`
  Purpose: include real skill availability in worker system prompts without implying slash commands are shell binaries.
- Modify: `internal/agent/event_render.go`
  Purpose: keep worker progress and debug events separated so default chat only gets quiet progress rows.
- Modify: `internal/agent/progress.go`
  Purpose: replace role-theater strings such as `dispatching to scout` with generic human-readable progress summaries.

### CLI and test dependency updates

- Modify: `cmd/forge/main.go`
  Purpose: keep `forge` as the default chat app, make `-d` the documented advanced view switch, and remove stale copy that implies panels or visible agents are part of normal chat.
- Modify: `go.mod`
- Modify: `go.sum`
  Purpose: add the PTY test helper dependency required for terminal-mode integration tests.

## Chunk 1: Surface Mode And Transcript Primitives

### Task 1: Make default and debug chat surfaces explicit

**Files:**
- Create: `internal/tui/chatsurface.go`
- Create: `internal/tui/chatsurface_test.go`
- Modify: `internal/tui/chatshared.go`
- Modify: `internal/tui/chatlive_bubbletea.go`
- Modify: `internal/runtime/chat.go`
- Modify: `internal/runtime/chat_test.go`

- [ ] **Step 1: Write failing surface-mode tests**

Cover:
- default chat returns a surface config with `UseAltScreen=false`, `EnableMouseCapture=false`, `EnableBracketedPaste=true`, and `EnableLiveRegion=true`
- debug chat returns a surface config that selects the managed debug surface while still enabling bracketed paste and live-region support
- `programOptionsForSurfaceMode(mode SurfaceModeConfig) []tea.ProgramOption` builds Bubble Tea program options from that config instead of hardcoding them in `chatlive_bubbletea.go`
- `internal/runtime/chat.go` passes debug state through the new surface config rather than assuming a single TUI mode

Example test target:

```go
func TestDefaultSurfaceModeDisablesAltScreen(t *testing.T) {
	cfg := ChatLiveConfig{DebugEnabled: false}
	mode := cfg.SurfaceMode()
	if mode.UseAltScreen || mode.EnableMouseCapture {
		t.Fatalf("default mode = %#v", mode)
	}
	if !mode.EnableBracketedPaste || !mode.EnableLiveRegion {
		t.Fatalf("default mode missing required flags: %#v", mode)
	}
}
```

- [ ] **Step 2: Run the focused runtime/TUI tests and verify they fail**

Run: `go test ./internal/tui ./internal/runtime -run 'Test(DefaultSurfaceMode|DebugSurfaceMode|RunChatLiveUsesSurfaceMode)' -count=1`

Expected: FAIL because `SurfaceMode()` and the option-selection helper do not exist yet.

- [ ] **Step 3: Implement the surface-mode split**

Implement:
- `SurfaceModeConfig` in `internal/tui/chatsurface.go`
- `func (cfg ChatLiveConfig) SurfaceMode() SurfaceModeConfig`
- `func programOptionsForSurfaceMode(mode SurfaceModeConfig) []tea.ProgramOption`
- runtime wiring in `internal/runtime/chat.go` that sets the mode from `setup.debugRec != nil`

Example structure:

```go
type SurfaceModeConfig struct {
	UseAltScreen        bool
	EnableMouseCapture  bool
	EnableBracketedPaste bool
	EnableLiveRegion    bool
}
```

- [ ] **Step 4: Run the focused runtime/TUI tests and verify they pass**

Run: `go test ./internal/tui ./internal/runtime -run 'Test(DefaultSurfaceMode|DebugSurfaceMode|RunChatLiveUsesSurfaceMode)' -count=1`

Expected: PASS

- [ ] **Step 5: Commit the surface-mode split**

```bash
git add internal/tui/chatsurface.go internal/tui/chatsurface_test.go internal/tui/chatshared.go internal/tui/chatlive_bubbletea.go internal/runtime/chat.go internal/runtime/chat_test.go
git commit -m "feat: split forge chat into default and debug surfaces"
```

### Task 2: Add transcript records and the live progress slot

**Files:**
- Create: `internal/tui/chatrecords.go`
- Create: `internal/tui/chatrecords_test.go`
- Create: `internal/tui/chatprogress.go`
- Create: `internal/tui/chatprogress_test.go`
- Modify: `internal/tui/chatmodel.go`
- Modify: `internal/tui/chatmodel_test.go`

- [ ] **Step 1: Write failing tests for transcript records and live progress**

Cover:
- durable transcript records distinguish `user`, `assistant`, `error`, and `system`
- assistant records hold text segments and code segments instead of one giant string blob
- `ProgressUpdate` replaces the current live progress row rather than appending a new durable transcript row
- when a turn completes, the live progress slot clears or collapses into one durable dim note

Example test target:

```go
func TestLiveProgressReplacesPreviousMessage(t *testing.T) {
	slot := LiveProgressState{}
	slot = slot.Apply(ProgressUpdate{TurnID: 3, ReplaceKey: "active", Message: "reviewing the repo"})
	slot = slot.Apply(ProgressUpdate{TurnID: 3, ReplaceKey: "active", Message: "checking tests"})
	if slot.Message != "checking tests" {
		t.Fatalf("slot = %#v", slot)
	}
}
```

- [ ] **Step 2: Run the focused TUI tests and verify they fail**

Run: `go test ./internal/tui -run 'Test(LiveProgress|TranscriptRecord)' -count=1`

Expected: FAIL because the record and progress primitives do not exist yet.

- [ ] **Step 3: Implement the transcript record and live-progress primitives**

Implement:
- `TranscriptRecord`, `Segment`, and record-kind constants in `internal/tui/chatrecords.go`
- `LiveProgressState` plus `Apply`, `Finalize`, and `Reset` helpers in `internal/tui/chatprogress.go`
- the collapsed progress note as `TranscriptRecord{Kind: RecordSystem, ...}` returned by `LiveProgressState.Finalize(...)`
- minimal `chatmodel.go` wiring that uses the new record model internally while leaving rendering behavior unchanged for now

Example structure:

```go
type TranscriptRecord struct {
	ID       string
	TurnID   int
	Kind     RecordKind
	Label    string
	Segments []Segment
	Final    bool
}

type ProgressUpdate struct {
	TurnID     int
	ReplaceKey string
	Message    string
}
```

- [ ] **Step 4: Run the focused TUI tests and verify they pass**

Run: `go test ./internal/tui -run 'Test(LiveProgress|TranscriptRecord)' -count=1`

Expected: PASS

- [ ] **Step 5: Commit the transcript primitives**

```bash
git add internal/tui/chatrecords.go internal/tui/chatrecords_test.go internal/tui/chatprogress.go internal/tui/chatprogress_test.go internal/tui/chatmodel.go internal/tui/chatmodel_test.go
git commit -m "feat: add transcript records and live progress state"
```

## Chunk 2: Transcript-First Default UI

### Task 3: Isolate composer behavior and flatten transcript message styling

**Files:**
- Create: `internal/tui/chatcomposer.go`
- Create: `internal/tui/chatcomposer_test.go`
- Modify: `internal/tui/chatmsg.go`
- Modify: `internal/tui/chatmsg_test.go`
- Modify: `internal/tui/chattheme.go`
- Modify: `internal/tui/chattheme_test.go`
- Modify: `internal/tui/chatmodel.go`
- Modify: `internal/tui/chatmodel_test.go`

- [ ] **Step 1: Write failing tests for composer rules and flat message styling**

Cover:
- `Enter` submits a turn
- `Shift+Enter` inserts a newline
- bracketed paste inserts literal multi-line text and does not submit
- `Ctrl+C` clears the draft when idle and requests turn cancel when busy
- `Ctrl+D` or EOF exits only when the composer is empty and no turn is running
- the composer renders at 3 visible lines when short, grows to 5 visible lines as content wraps, then scrolls internally without increasing view height
- rendered user and assistant messages no longer contain the left border/card treatment
- the default theme uses a graphite background, muted text, and one cool focus accent

Example composer test:

```go
func TestComposerEnterSubmitsAndClears(t *testing.T) {
	c := NewChatComposer()
	c.InsertString("review this repo")
	action := c.HandleKey(tea.KeyMsg{Type: tea.KeyEnter})
	if action.SubmitText != "review this repo" {
		t.Fatalf("action = %#v", action)
	}
	if c.Text() != "" {
		t.Fatalf("composer should clear after submit, got %q", c.Text())
	}
}
```

- [ ] **Step 2: Run the focused TUI tests and verify they fail**

Run: `go test ./internal/tui -run 'Test(ChatComposer|ChatMessage|ChatTheme)' -count=1`

Expected: FAIL because the composer helper does not exist and the message renderer still emits bordered cards.

- [ ] **Step 3: Implement the composer helper and flat transcript styling**

Implement:
- `ChatComposer` in `internal/tui/chatcomposer.go`
- new message rendering in `chatmsg.go` that relies on labels/spacing instead of a left border
- theme updates in `chattheme.go`
- `chatmodel.go` integration so submission echoes into the transcript in the same update cycle that clears the composer

Example structure:

```go
type ComposerAction struct {
	SubmitText string
	CancelTurn bool
	Exit       bool
}
```

- [ ] **Step 4: Run the focused TUI tests and verify they pass**

Run: `go test ./internal/tui -run 'Test(ChatComposer|ChatMessage|ChatTheme)' -count=1`

Expected: PASS

- [ ] **Step 5: Commit the composer and message rewrite**

```bash
git add internal/tui/chatcomposer.go internal/tui/chatcomposer_test.go internal/tui/chatmsg.go internal/tui/chatmsg_test.go internal/tui/chattheme.go internal/tui/chattheme_test.go internal/tui/chatmodel.go internal/tui/chatmodel_test.go
git commit -m "feat: flatten forge transcript and composer"
```

### Task 4: Remove the default tools pane and finish the single-column chat layout

**Files:**
- Modify: `internal/tui/chatmodel.go`
- Modify: `internal/tui/chatmodel_test.go`
- Modify: `internal/tui/chatstats.go`
- Modify: `internal/tui/traceview.go`
- Modify: `internal/tui/view_test.go`

- [ ] **Step 1: Write failing layout tests**

Cover:
- default chat view renders a single transcript column with the composer at the bottom
- default chat does not render the tools pane, trace overlay, or multi-panel chrome
- the live progress slot sits directly above the composer
- debug mode still exposes trace rendering and richer overlays
- the prompt text is echoed into the transcript immediately and does not linger in the composer after submit

Example layout assertion:

```go
func TestDefaultChatViewOmitsToolsPane(t *testing.T) {
	m := NewChatModel(ChatLiveConfig{Model: "test", WorkDir: "/tmp", DebugEnabled: false})
	m.width, m.height = 100, 30
	view := m.View()
	if strings.Contains(view, "Tools") || strings.Contains(view, "Debug trace") {
		t.Fatalf("default view leaked debug chrome: %s", view)
	}
}
```

- [ ] **Step 2: Run the focused TUI tests and verify they fail**

Run: `go test ./internal/tui -run 'Test(DefaultChatView|DebugChatView|PromptEcho|ProgressSlot)' -count=1`

Expected: FAIL because the current chat model still assumes pane-oriented layout and trace/tool surfaces.

- [ ] **Step 3: Implement the single-column default layout**

Implement:
- default-mode `View()` path in `chatmodel.go` that renders one transcript column plus the live region
- slim top status line from `chatstats.go`
- debug-only overlay path in `traceview.go`
- removal of the default tools-pane branch from normal chat rendering while preserving debug access paths

Keep:
- code block rendering
- `/help`, `/skills`, `/auto-skills`, and existing slash-command handling

- [ ] **Step 4: Run the focused TUI tests and verify they pass**

Run: `go test ./internal/tui -run 'Test(DefaultChatView|DebugChatView|PromptEcho|ProgressSlot)' -count=1`

Expected: PASS

- [ ] **Step 5: Commit the default-layout cutover**

```bash
git add internal/tui/chatmodel.go internal/tui/chatmodel_test.go internal/tui/chatstats.go internal/tui/traceview.go internal/tui/view_test.go
git commit -m "feat: ship single-column default forge chat layout"
```

## Chunk 3: Worker Skill Runtime And Contracts

### Task 5: Add a shared skill runtime adapter

**Files:**
- Create: `internal/skills/runtime.go`
- Create: `internal/skills/runtime_test.go`
- Modify: `internal/skills/skills.go`
- Modify: `internal/skills/skills_test.go`
- Modify: `internal/skills/auto.go`
- Modify: `internal/skills/enforce.go`

- [ ] **Step 1: Write failing tests for the shared skill runtime**

Cover:
- the runtime exposes a stable descriptor list with skill name, description, and source path
- required-skill resolution and auto-skill resolution both flow through the same adapter
- the adapter returns skill documents, not shell command strings
- applying a skill produces the exact injected history payload the agent expects

Example runtime test:

```go
func TestRuntimeReturnsInjectableSkillDocument(t *testing.T) {
	rt := NewRuntime([]Skill{{Name: "brainstorming", Description: "plan first", Body: "Do not implement yet."}})
	skill, ok := rt.ResolveRequired("design the chat ui")
	if !ok || skill.Name != "brainstorming" {
		t.Fatalf("skill = %#v ok=%v", skill, ok)
	}
	msg := rt.InjectableMessage(skill)
	if !strings.HasPrefix(msg, "[Skill: brainstorming]") {
		t.Fatalf("msg = %q", msg)
	}
}
```

- [ ] **Step 2: Run the focused skills tests and verify they fail**

Run: `go test ./internal/skills -run 'Test(Runtime|RequiredForInput|DetectAuto)' -count=1`

Expected: FAIL because the runtime adapter does not exist yet.

- [ ] **Step 3: Implement the shared skill runtime adapter**

Implement:
- `Runtime`, `Descriptor`, `ResolveRequired`, `ResolveAuto`, `LoadByName`, `InjectableMessage`, and `RecordSkillUse` in `internal/skills/runtime.go`
- thin wrappers in `auto.go` and `enforce.go` so all call sites use the same logic
- descriptor generation in `skills.go`

Example structure:

```go
type Descriptor struct {
	Name        string
	Description string
	Source      string
}

type Runtime struct {
	loaded []Skill
}
```

- [ ] **Step 4: Run the focused skills tests and verify they pass**

Run: `go test ./internal/skills -run 'Test(Runtime|RequiredForInput|DetectAuto)' -count=1`

Expected: PASS

- [ ] **Step 5: Commit the skill runtime adapter**

```bash
git add internal/skills/runtime.go internal/skills/runtime_test.go internal/skills/skills.go internal/skills/skills_test.go internal/skills/auto.go internal/skills/enforce.go
git commit -m "feat: add shared skill runtime for forge and workers"
```

### Task 6: Pass real skill context into hidden workers and keep failures structured

**Files:**
- Modify: `internal/harness/types.go`
- Modify: `internal/harness/workers.go`
- Modify: `internal/harness/runner.go`
- Modify: `internal/harness/planner.go`
- Modify: `internal/harness/policy.go`
- Modify: `internal/harness/contracts.go`
- Modify: `internal/harness/runner_test.go`
- Modify: `internal/harness/workers_test.go`
- Modify: `internal/harness/policy_test.go`
- Modify: `internal/harness/contracts_test.go`
- Modify: `internal/runtime/chat.go`
- Modify: `internal/agent/system.go`

- [ ] **Step 1: Write failing tests for worker skill inheritance and structured fallback**

Cover:
- worker tasks carry a `SkillContext`, permission profile, and deadline/cancellation metadata
- hidden workers are created with loaded skills instead of `nil`
- a worker can auto-apply or require a skill through the shared runtime adapter before `Run`
- missing required skill access yields a structured blocked observation and a local fallback path
- final user-visible text still comes from top-level Forge rather than raw worker prose

Example worker test:

```go
func TestWorkersUseInjectedSkillContext(t *testing.T) {
	task := WorkerTask{
		Kind:      WorkerEditor,
		Objective: "implement a cleanup helper",
		SkillContext: WorkerSkillContext{
			AutoMode: "auto",
			Loaded: []skills.Skill{{
				Name: "test-driven-development",
				Body: "Write a failing test before implementation.",
			}},
		},
	}
	obs, err := mgr.Execute(context.Background(), task)
	if err != nil {
		t.Fatal(err)
	}
	if obs.Status != ObservationComplete {
		t.Fatalf("obs = %#v", obs)
	}
	if stub.lastInjectedSkill != "test-driven-development" {
		t.Fatalf("lastInjectedSkill = %q", stub.lastInjectedSkill)
	}
}
```

- [ ] **Step 2: Run the focused harness/runtime tests and verify they fail**

Run: `go test ./internal/harness ./internal/runtime -run 'Test(Workers.*Skill|Runner.*Worker|Policy.*Fallback)' -count=1`

Expected: FAIL because worker tasks do not yet carry skill context and workers are still built with `nil` skills.

- [ ] **Step 3: Implement the typed worker context plumbing**

Implement:
- `WorkerSkillContext` and the expanded `WorkerTask` fields in `internal/harness/types.go`
- worker-agent creation in `internal/harness/workers.go` with loaded skills and fresh state
- runtime wiring in `internal/runtime/chat.go` so worker tasks receive loaded skills, auto-skill mode, and deadline/cancellation metadata

Example `WorkerTask` target:

```go
type WorkerTask struct {
	Kind              WorkerKind
	Objective         string
	Context           string
	TopicKey          string
	StopCondition     string
	SkillContext      WorkerSkillContext
	PermissionProfile []string
	Deadline          time.Time
}
```

- [ ] **Step 4: Implement worker-side skill injection and fail-closed behavior**

Implement:
- pre-run skill resolution/injection through `internal/skills.Runtime`
- `RecordSkillUse(...)` calls so debug mode can surface worker skill metadata later
- fail-closed handling that returns structured blocked observations instead of shelling out or bluffing
- worker system prompt wording in `internal/agent/system.go` so skills are described as runtime instructions, not binaries
- an explicit guard/test path proving no slash-command shelling or “I can use skills but…” prose reaches default-mode output

- [ ] **Step 5: Run the focused harness/runtime tests and verify they pass**

Run: `go test ./internal/harness ./internal/runtime -run 'Test(Workers.*Skill|Runner.*Worker|Policy.*Fallback)' -count=1`

Expected: PASS

- [ ] **Step 6: Commit worker skill inheritance**

```bash
git add internal/harness/types.go internal/harness/workers.go internal/harness/runner.go internal/harness/planner.go internal/harness/policy.go internal/harness/contracts.go internal/harness/runner_test.go internal/harness/workers_test.go internal/harness/policy_test.go internal/harness/contracts_test.go internal/runtime/chat.go internal/agent/system.go
git commit -m "feat: give hidden workers real skill inheritance"
```

## Chunk 4: Runtime Polish, PTY Coverage, And Cutover Verification

### Task 7: Make progress and fallback messaging transcript-safe

**Files:**
- Modify: `internal/agent/progress.go`
- Modify: `internal/agent/event_render.go`
- Modify: `internal/harness/policy.go`
- Modify: `internal/harness/runner.go`
- Modify: `internal/harness/runner_test.go`
- Modify: `internal/tui/chatmodel.go`
- Modify: `internal/tui/chatmodel_test.go`

- [ ] **Step 1: Write failing tests for progress wording and fallback surfacing**

Cover:
- worker progress lines become generic user-facing phrases such as `reviewing the repo`, `checking tests`, or `editing main.go`
- progress lines never say `dispatching to scout`, `reader worker complete`, or similar harness chatter in default chat
- recoverable worker failures stay internal
- unrecoverable worker failures surface as user-impact language rather than role names

Example progress test:

```go
func TestProgressLineOmitsWorkerTheater(t *testing.T) {
	got := progressLine("reader", "delegate", "scout")
	if strings.Contains(strings.ToLower(got), "dispatch") || strings.Contains(strings.ToLower(got), "scout") {
		t.Fatalf("got %q", got)
	}
}
```

- [ ] **Step 2: Run the focused agent/harness/TUI tests and verify they fail**

Run: `go test ./internal/agent ./internal/harness ./internal/tui -run 'Test(Progress|Fallback|InlineWorking)' -count=1`

Expected: FAIL because the current progress formatter still emits role-heavy phrasing.

- [ ] **Step 3: Implement transcript-safe progress and fallback wording**

Implement:
- generic progress phrasing in `internal/agent/progress.go`
- event emission in `internal/agent/event_render.go` that keeps debug-only detail out of default chat
- policy/runner fallback summaries that describe user impact instead of harness internals
- `chatmodel.go` handling that keeps one quiet progress row above the composer

- [ ] **Step 4: Run the focused agent/harness/TUI tests and verify they pass**

Run: `go test ./internal/agent ./internal/harness ./internal/tui -run 'Test(Progress|Fallback|InlineWorking)' -count=1`

Expected: PASS

- [ ] **Step 5: Commit the transcript-safe progress wording**

```bash
git add internal/agent/progress.go internal/agent/event_render.go internal/harness/policy.go internal/harness/runner.go internal/harness/runner_test.go internal/tui/chatmodel.go internal/tui/chatmodel_test.go
git commit -m "feat: keep forge progress and fallbacks transcript-safe"
```

### Task 8: Add PTY integration tests, update help text, and run the full verification suite

**Files:**
- Modify: `go.mod`
- Modify: `go.sum`
- Create: `internal/runtime/chat_pty_test.go`
- Modify: `internal/runtime/chat.go`
- Modify: `internal/runtime/chat_test.go`
- Modify: `internal/runtime/chat_debug.go`
- Modify: `internal/runtime/chat_debug_test.go`
- Modify: `cmd/forge/main.go`
- Modify: `internal/tui/chatmodel.go`
- Modify: `internal/tui/chatmodel_test.go`

- [ ] **Step 1: Write failing PTY and help-text tests**

Cover:
- a PTY-backed test proves default chat output does not emit alternate-screen control sequences
- a PTY-backed test proves default chat transcript output remains append-first
- debug mode may still use the managed surface
- `forge -d` help text describes advanced view/debug behavior
- chat help copy no longer references a default tools panel or visible worker flow

Example PTY test target:

```go
func TestDefaultChatDoesNotEnterAltScreen(t *testing.T) {
	// Start `forge` in a PTY with github.com/creack/pty.
	// Wait for the prompt, write "inspect this repo\n", then write the quit path once the turn completes.
	// Capture all PTY output and assert:
	// 1. output never contains \x1b[?1049h
	// 2. the echoed user prompt remains present after the model response arrives
	// 3. progress updates do not erase the durable transcript line for the user turn
}
```

- [ ] **Step 2: Run the focused runtime/CLI tests and verify they fail**

Run: `go test ./internal/runtime ./cmd/forge -run 'Test(DefaultChatDoesNotEnterAltScreen|PrintChatHelp|EnableChatDebug)' -count=1`

Expected: FAIL because the PTY test helper is not wired and the help/debug copy still reflects the current surface assumptions.

- [ ] **Step 3: Implement PTY coverage and final polish**

Implement:
- the PTY test helper dependency in `go.mod` / `go.sum`
- `internal/runtime/chat_pty_test.go` using `github.com/creack/pty`
- a deterministic PTY protocol:
  - start `forge` with a fixed model/test driver
  - wait for the ready prompt
  - send one user prompt plus newline
  - wait for a fixed fake-driver response sentinel such as `FORGE_TEST_RESPONSE_DONE`
  - send `/quit` or EOF to terminate cleanly
- one PTY assertion for prompt echo persistence and one PTY assertion for bracketed-paste non-submission
- help text updates in `cmd/forge/main.go` and matching chat help copy in the runtime/TUI paths

- [ ] **Step 4: Run the full verification suite**

Run:
- `go test ./internal/skills ./internal/harness ./internal/tui ./internal/runtime ./cmd/forge -count=1`
- `go test ./... -count=1`

Expected:
- all targeted package tests PASS
- full repo test suite PASS, or any unrelated pre-existing failure is documented before handoff

- [ ] **Step 5: Commit the final redesign pass**

```bash
git add go.mod go.sum internal/runtime/chat_pty_test.go internal/runtime/chat.go internal/runtime/chat_test.go internal/runtime/chat_debug.go internal/runtime/chat_debug_test.go cmd/forge/main.go internal/tui/chatmodel.go internal/tui/chatmodel_test.go
git commit -m "feat: finalize forge default chat redesign"
```
