# Agent Work Panel Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Add a user-toggleable `/panel` side pane for delegated agent work, with auto-open respected unless the user hides it.

**Architecture:** Keep the old raw tools buffer hidden by rendering the side panel only for tool sections with a sub-agent role. Add a small user preference flag to distinguish "currently visible" from "user explicitly hid it", then route `/panel` commands and sub-agent events through that state.

**Tech Stack:** Go, Bubble Tea TUI, Lip Gloss rendering, existing `ChatModel` slash command and sub-agent event flow.

---

### Task 1: Add `/panel` Command Tests

**Files:**
- Modify: `internal/tui/chatmodel_test.go:1749-1809`
- Test: `internal/tui/chatmodel_test.go`

**Step 1: Write failing tests**

Add tests that prove `/panel` is separate from the removed `/tools` command:

```go
func TestChatModelSlashPanelArmsAutoOpen(t *testing.T) {
	m := NewChatModel(ChatLiveConfig{Model: "test", WorkDir: "/tmp"})
	m.width = 120
	m.height = 24

	m.inputBuf = "/panel"
	m.inputPos = len(m.inputBuf)
	updated, _ := m.submitInput()
	m = updated.(ChatModel)

	if m.toolsVisible {
		t.Fatal("panel should not render until agent work exists")
	}
	if m.agentPanelHiddenByUser {
		t.Fatal("/panel should allow future auto-open")
	}
	if !strings.Contains(m.flash, "panel will open when agent work starts") {
		t.Fatalf("flash = %q", m.flash)
	}
}

func TestChatModelSlashPanelOffSuppressesSubAgentAutoOpen(t *testing.T) {
	m := NewChatModel(ChatLiveConfig{Model: "test", WorkDir: "/tmp"})
	m.width = 120
	m.height = 24

	m.inputBuf = "/panel off"
	m.inputPos = len(m.inputBuf)
	updated, _ := m.submitInput()
	m = updated.(ChatModel)

	updated, _ = m.Update(llm.Event{Kind: llm.EventToolCall, Agent: "runtime", Text: "[repo doc editor] starting", SubAgent: "repo doc editor"})
	m = updated.(ChatModel)

	if m.toolsVisible {
		t.Fatal("panel should stay hidden after /panel off")
	}
}

func TestChatModelSlashPanelOnShowsExistingAgentWork(t *testing.T) {
	m := NewChatModel(ChatLiveConfig{Model: "test", WorkDir: "/tmp"})
	m.width = 120
	m.height = 24
	m.agentPanelHiddenByUser = true
	m.appendTools("repo doc editor", "read_file docs/file.md\n")

	m.inputBuf = "/panel on"
	m.inputPos = len(m.inputBuf)
	updated, _ := m.submitInput()
	m = updated.(ChatModel)

	if !m.toolsVisible {
		t.Fatal("panel should show existing agent work after /panel on")
	}
	if m.agentPanelHiddenByUser {
		t.Fatal("/panel on should clear explicit hide")
	}
}
```

**Step 2: Run tests to verify failure**

Run: `go test ./internal/tui/ -run 'TestChatModelSlashPanel|TestChatModelOpensSidePanelForSubAgentWork|TestChatModelToolsPaneToggleShowsRemovedMessage' -v -timeout 60s`

Expected: FAIL because `/panel` is unknown and `agentPanelHiddenByUser` does not exist.

### Task 2: Implement Panel State And Slash Commands

**Files:**
- Modify: `internal/tui/chatmodel.go:216-217`
- Modify: `internal/tui/chatmodel.go:2540-2548`
- Modify: `internal/tui/chatmodel.go:2662-2670`
- Test: `internal/tui/chatmodel_test.go`

**Step 1: Add minimal state**

Add one field to `ChatModel` near `toolsVisible`:

```go
agentPanelHiddenByUser bool
```

**Step 2: Add command completion entries**

Add the panel commands to `builtinCommands`:

```go
"/panel", "/panel on", "/panel off", "/toggle panel",
```

Keep `/tools` entries unchanged for compatibility with existing tests.

**Step 3: Add slash command handling**

Add cases before `/tools`:

```go
case input == "/panel" || input == "/toggle panel":
	m.agentPanelHiddenByUser = false
	if m.hasAgentWorkPaneContent() {
		m.toolsVisible = !m.toolsVisible
		if m.toolsVisible {
			m.flash = "panel opened"
		} else {
			m.agentPanelHiddenByUser = true
			m.flash = "panel closed"
		}
	} else {
		m.toolsVisible = false
		m.flash = "panel will open when agent work starts"
	}
case input == "/panel on":
	m.agentPanelHiddenByUser = false
	if m.hasAgentWorkPaneContent() {
		m.toolsVisible = true
		m.flash = "panel opened"
	} else {
		m.toolsVisible = false
		m.flash = "panel will open when agent work starts"
	}
case input == "/panel off":
	m.toolsVisible = false
	m.agentPanelHiddenByUser = true
	m.flash = "panel closed"
```

**Step 4: Run tests to verify pass**

Run: `go test ./internal/tui/ -run 'TestChatModelSlashPanel|TestChatModelToolsPaneToggleShowsRemovedMessage|TestChatModelSlashToggleTools' -v -timeout 60s`

Expected: PASS.

### Task 3: Gate Auto-Open On User Preference

**Files:**
- Modify: `internal/tui/chatmodel.go:956-968`
- Modify: `internal/tui/chatmodel.go:1756-1764`
- Test: `internal/tui/chatmodel_test.go`

**Step 1: Split content detection from visibility**

Rename or extract the content check so rendering and slash commands can ask different questions:

```go
func (m ChatModel) hasAgentWorkPane() bool {
	return m.toolsVisible && m.hasAgentWorkPaneContent()
}

func (m ChatModel) hasAgentWorkPaneContent() bool {
	for _, sec := range m.toolsSections {
		if strings.TrimSpace(sec.role) == "" {
			continue
		}
		if strings.TrimSpace(sec.buf) != "" || strings.TrimSpace(sec.summary) != "" {
			return true
		}
	}
	return false
}
```

**Step 2: Respect `/panel off` during sub-agent events**

Update `handleSubAgentEvent` auto-open logic:

```go
if strings.TrimSpace(label) != "" && !m.toolsVisible && !m.agentPanelHiddenByUser {
	m.toolsVisible = true
	m.toolsWasShowing = true
	m.resizeChatViewport()
	m.viewportDirty = true
}
```

**Step 3: Run focused tests**

Run: `go test ./internal/tui/ -run 'TestChatModelSlashPanel|TestChatModelOpensSidePanelForSubAgentWork|TestChatModelViewKeepsHiddenToolBufferOutOfSingleColumnShell|TestDefaultChatViewSingleColumnLayout' -v -timeout 60s`

Expected: PASS.

### Task 4: Final Verification

**Files:**
- Verify: `internal/tui/chatmodel.go`
- Verify: `internal/tui/chatmodel_test.go`
- Verify: `internal/tui/view_test.go`
- Verify: `docs/plans/2026-05-07-agent-work-panel-design.md`
- Verify: `docs/plans/2026-05-07-agent-work-panel.md`

**Step 1: Format changed Go files**

Run: `gofmt -w internal/tui/chatmodel.go internal/tui/chatmodel_test.go internal/tui/view_test.go`

Expected: no output.

**Step 2: Run related suites**

Run: `go test ./internal/tui/... ./internal/runtime/... ./internal/agent/... ./internal/react/... -timeout 120s`

Expected: PASS.

**Step 3: Build Forge**

Run: `go build -o ./bin/forge ./cmd/forge/`

Expected: PASS with no output.

**Step 4: Do not commit unless requested**

This repository explicitly says not to commit unless the user asks. Stop after verification and summarize the changed files and test results.
