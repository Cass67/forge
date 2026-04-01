# Forge Team First-Class Mode Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add `forge team` as a first-class live team mode with a shared composer, creator/auditor split-pane workflow, optional verifier checkpoints, and new-project bootstrap support.

**Architecture:** Build a new host-owned team runtime. Reuse the modern runtime stack wherever possible: approvals, tool registry, exec sessions, typed hooks, bounded memory, and Bubble Tea chat chrome.

**Tech Stack:** Go, Bubble Tea, `internal/runtime`, `internal/react`, `internal/hooks`, `internal/memory`, `internal/tui`, `internal/agent/tools`, existing CLI/runtime/TUI tests.

---

**Spec:** `docs/superpowers/specs/2026-04-01-forge-team-first-class-mode-design.md`

## File Structure

- Create: `internal/team/types.go`
  Responsibility: team roles, team phases, checkpoint states, workspace bootstrap request/response types.
- Create: `internal/team/runtime.go`
  Responsibility: creator/auditor/verifier orchestration, host-owned team turn protocol, role routing, and checkpoint progression.
- Create: `internal/team/runtime_test.go`
  Responsibility: creator/auditor/verifier loop regressions and role-routing coverage.
- Create: `internal/runtime/make.go`
  Responsibility: first-class `forge team` live runtime assembly, shared tool wiring, approvals, workspace bootstrap, and team-run entrypoints.
- Create: `internal/runtime/make_test.go`
  Responsibility: end-to-end runtime tests for repo bootstrap, team startup, and checkpoint flows.
- Create: `internal/tui/teammodel.go`
  Responsibility: split-pane creator/auditor shell built on the modern Bubble Tea runtime chrome.
- Create: `internal/tui/teammsg.go`
  Responsibility: team-mode Bubble Tea messages and event translation helpers.
- Create: `internal/tui/teammodel_test.go`
  Responsibility: split-pane rendering, shared composer behavior, checkpoint output, and approval/session-status surfaces.
- Modify: `internal/tui/chatshared.go`
  Responsibility: launch the new team-mode UI entrypoint alongside chat mode.
- Modify: `internal/tui/chatmodel.go`
  Responsibility: extract/reuse shared chrome helpers where practical.
- Modify: `internal/tui/input.go`
  Responsibility: reshape startup input for team mode and remove writer/auditor startup assumptions that no longer fit.
- Modify: `internal/tui/input_test.go`
  Responsibility: new shared-composer/team-start regressions.
- Modify: `internal/runtime/chat.go`
  Responsibility: reuse common tool/runtime assembly helpers without duplicating logic.
- Modify: `internal/react/session.go`
  Responsibility: add any team-mode session state that must be host-owned and prompt-visible.
- Modify: `internal/react/prompt.go`
  Responsibility: team-mode prompt assembly support for role-aware context, checkpoint state, and role-tagged memory when needed.
- Modify: `cmd/forge/main.go`
  Responsibility: add `forge team`, route it to the new live team mode entrypoint, and update help text.
- Modify: `cmd/forge/main_test.go`
  Responsibility: CLI behavior regressions for new `forge team`.
- Modify: `README.md`
  Responsibility: document first-class `forge team` mode once it ships.
- Modify: `ARCHITECTURE.md`
  Responsibility: describe the new team runtime and document it as a first-class product surface.

## Task 1: Introduce Team Runtime Types And Host-Owned Protocol

**Files:**
- Create: `internal/team/types.go`
- Create: `internal/team/runtime.go`
- Create: `internal/team/runtime_test.go`

- [ ] **Step 1: Write the failing team-runtime tests**

Add tests covering:
- creator acts first on a new shared directive
- auditor receives creator output and can block progression
- verifier only runs at checkpoints or explicit verification requests
- unresolved auditor blockers return control to creator

Run: `go test ./internal/team -run 'TestTeamRuntime'`
Expected: FAIL because the package and runtime do not exist yet.

- [ ] **Step 2: Add core team types**

Define explicit types for:
- roles: creator, auditor, verifier
- team phase/state
- checkpoint status
- team turn outcome
- workspace bootstrap request/response

Keep the shapes small and host-owned. Avoid turning this into a generic arbitrary-agent framework.

- [ ] **Step 3: Implement the team runtime protocol**

Implement a bounded protocol that:
- accepts one shared user directive
- routes first action to creator
- routes critique to auditor
- routes verifier only at checkpoints
- records unresolved blockers
- exposes next-step ownership explicitly

- [ ] **Step 4: Verify the slice**

Run: `go test ./internal/team -run 'TestTeamRuntime'`
Expected: PASS

- [ ] **Step 5: Commit the task**

```bash
git add internal/team/types.go internal/team/runtime.go internal/team/runtime_test.go
git commit -m "team: add live make runtime protocol"
```

## Task 2: Build The First-Class Make Runtime And Workspace Bootstrap

**Files:**
- Create: `internal/runtime/make.go`
- Create: `internal/runtime/make_test.go`
- Modify: `internal/runtime/chat.go`
- Modify: `cmd/forge/main.go`
- Modify: `cmd/forge/main_test.go`

- [ ] **Step 1: Write the failing runtime/CLI tests**

Add tests covering:
- `forge team` launches the new live team mode by default
- a bootstrap request can create a target directory and optionally initialize git
- the new runtime reuses approvals and shared tool assembly

Run: `go test ./cmd/forge ./internal/runtime -run 'Test(Make|Team)'`
Expected: FAIL because `forge team` does not exist yet and team startup/runtime support is missing.

- [ ] **Step 2: Extract reusable runtime wiring**

Factor common helpers from chat runtime where needed for:
- approvals
- tool registry
- preview runtime
- exec-session manager
- MCP integration

Do this carefully to avoid regressing chat behavior.

- [ ] **Step 3: Implement the new make runtime**

Create a live `forge team` runtime that:
- creates a shared session/runtime context
- supports existing-repo mode
- supports new workspace bootstrap
- uses the team runtime protocol from `internal/team`
- emits structured events for creator, auditor, verifier, checkpoint, and bootstrap activity

- [ ] **Step 4: Switch CLI dispatch**

Update `cmd/forge/main.go` so `forge team` launches the new runtime.

- [ ] **Step 5: Verify the slice**

Run: `go test ./cmd/forge ./internal/runtime -run 'Test(Make|Team)'`
Expected: PASS

- [ ] **Step 6: Commit the task**

```bash
git add internal/runtime/make.go internal/runtime/make_test.go internal/runtime/chat.go cmd/forge/main.go cmd/forge/main_test.go
git commit -m "runtime: add first-class team mode"
```

## Task 3: Add Team-Aware Session And Prompt Context

**Files:**
- Modify: `internal/react/session.go`
- Modify: `internal/react/prompt.go`
- Modify: `internal/team/runtime.go`
- Test: `internal/react/prompt_test.go`
- Test: `internal/team/runtime_test.go`

- [ ] **Step 1: Write the failing session/prompt tests**

Add tests covering:
- team-mode checkpoint state appears in prompt context
- role-aware progress/blocker context is visible without duplicating noise
- role-tagged retained memory can be surfaced compactly

Run: `go test ./internal/react ./internal/team -run 'Test(BuildMessages|TeamRuntime).*Make'`
Expected: FAIL because prompt/session do not understand team-mode state yet.

- [ ] **Step 2: Add bounded team-mode session state**

Extend the session model only where needed for:
- team phase
- active role
- unresolved auditor blocker
- checkpoint/verifier state

Keep the state host-owned and prompt-friendly.

- [ ] **Step 3: Update prompt assembly**

Update `internal/react/prompt.go` so the creator/auditor/verifier each receive compact, relevant team state without turning the prompt into transcript spam.

- [ ] **Step 4: Verify the slice**

Run: `go test ./internal/react ./internal/team -run 'Test(BuildMessages|TeamRuntime).*Make'`
Expected: PASS

- [ ] **Step 5: Commit the task**

```bash
git add internal/react/session.go internal/react/prompt.go internal/react/prompt_test.go internal/team/runtime.go internal/team/runtime_test.go
git commit -m "react: add team mode prompt state"
```

## Task 4: Build The Modern Split-Pane Team UI

**Files:**
- Create: `internal/tui/teammodel.go`
- Create: `internal/tui/teammsg.go`
- Create: `internal/tui/teammodel_test.go`
- Modify: `internal/tui/chatshared.go`
- Modify: `internal/tui/chatmodel.go`
- Modify: `internal/tui/input.go`
- Modify: `internal/tui/input_test.go`

- [ ] **Step 1: Write the failing TUI tests**

Add tests covering:
- one shared composer feeds the whole team
- creator and auditor panes render independently
- verifier output appears as checkpoint/status output rather than permanent third pane
- approvals and command-session status remain visible in team mode
- startup input reflects team-mode launch instead of the old writer/auditor setup screen

Run: `go test ./internal/tui -run 'Test(TeamModel|Input).*Make'`
Expected: FAIL because the split-pane team UI does not exist yet.

- [ ] **Step 2: Add team-mode Bubble Tea model**

Build a dedicated `TeamModel` that:
- reuses modern shell/chrome where practical
- renders creator and auditor panes
- keeps one shared composer
- handles checkpoint/verifier output
- surfaces approvals, nudges, and command-session state

- [ ] **Step 3: Integrate the UI entrypoint**

Update `chatshared.go` and startup flow so `forge team` launches the new team-mode UI.

- [ ] **Step 4: Verify the slice**

Run: `go test ./internal/tui -run 'Test(TeamModel|Input).*Make'`
Expected: PASS

- [ ] **Step 5: Commit the task**

```bash
git add internal/tui/teammodel.go internal/tui/teammsg.go internal/tui/teammodel_test.go internal/tui/chatshared.go internal/tui/chatmodel.go internal/tui/input.go internal/tui/input_test.go
git commit -m "tui: add split-pane team UI"
```

## Task 5: Wire Verifier Checkpoints And Team-Safe Tool Shaping

**Files:**
- Modify: `internal/team/runtime.go`
- Modify: `internal/runtime/make.go`
- Test: `internal/team/runtime_test.go`
- Test: `internal/runtime/make_test.go`

- [ ] **Step 1: Write the failing checkpoint tests**

Add tests covering:
- verifier triggers when creator+a auditor claim readiness
- verifier failure returns control to creator with evidence
- role-specific tool shaping prevents auditor from acting like unrestricted creator

Run: `go test ./internal/team ./internal/runtime -run 'Test(Verifier|Checkpoint|Role).*Make'`
Expected: FAIL because checkpoints and role shaping are not finished yet.

- [ ] **Step 2: Implement checkpoint verifier flow**

Add explicit checkpoint transitions and verifier invocation rules.

- [ ] **Step 3: Enforce light role-based tool shaping**

Restrict the auditor/verifier to review/verification-oriented tool access while keeping the creator implementation-oriented.

- [ ] **Step 4: Verify the slice**

Run: `go test ./internal/team ./internal/runtime -run 'Test(Verifier|Checkpoint|Role).*Make'`
Expected: PASS

- [ ] **Step 5: Commit the task**

```bash
git add internal/team/runtime.go internal/runtime/make.go internal/team/runtime_test.go internal/runtime/make_test.go
git commit -m "team: add verifier checkpoints and tool shaping"
```

## Task 6: Documentation, Migration Notes, And Final Verification

**Files:**
- Modify: `README.md`
- Modify: `ARCHITECTURE.md`
- Modify: any implementation files above only if verification reveals a real issue

- [ ] **Step 1: Update docs**

Document:
- `forge team` as first-class live team mode
- creator/auditor split-pane behavior
- verifier checkpoints
- bootstrap flow for new repos/apps

- [ ] **Step 2: Run targeted verification**

Run: `go test ./cmd/forge ./internal/runtime ./internal/react ./internal/team ./internal/tui`
Expected: PASS

- [ ] **Step 3: Run full repo verification**

Run: `go test ./...`
Expected: PASS

- [ ] **Step 4: Run repo check command**

Run: `just check`
Expected: PASS

- [ ] **Step 5: Inspect the feature diff**

Run: `git diff --stat $(git merge-base HEAD main)..HEAD`
Expected: Team-mode runtime, TUI, prompt/session, docs, and CLI changes only.

- [ ] **Step 6: Commit any final verification-driven fixes**

```bash
git add <changed-files>
git commit -m "make: polish first-class team mode"
```

Only do this if verification reveals a real issue that requires a code change.
