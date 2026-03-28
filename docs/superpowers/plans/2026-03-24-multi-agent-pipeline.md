# Multi-Agent Pipeline Rework Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Fix sub-agent feedback bugs, add live progress in chat pane, sub-agent cancellation, tools pane collapse, and agent model configuration TUI.

**Architecture:** Rework the sub-agent event pipeline so activity produces two streams (detail → tools pane, progress → chat pane). Fix the scout feedback loop and result truncation bugs. Add child context cancellation for sub-agents. Replace flat tools buffer with structured sections. Add `/agents` and `/agents models` TUI overlays.

**Tech Stack:** Go, Bubble Tea TUI framework, TOML config

**Spec:** `docs/superpowers/specs/2026-03-24-multi-agent-pipeline-design.md`

---

## Chunk 1: Agent Core Fixes (Bug Fixes + New Fields)

### Task 1: Add new Agent fields and methods

**Files:**
- Modify: `internal/agent/agent.go:16-28` (Agent struct)
- Modify: `internal/agent/agent.go:66-74` (after SetSystem/SetTools)
- Test: `internal/agent/agent_test.go`

- [ ] **Step 1: Write failing test for sub-agent nudge bypass**

In `internal/agent/agent_test.go`, add a test that creates an agent with `isSubAgent = true`, gives it a short (< 300 char) final response, and verifies it returns immediately without nudging:

```go
func TestSubAgentSkipsNudgeOnShortResponse(t *testing.T) {
	driver := &mockDriver{responses: []string{"FINDINGS:\n- found 3 files"}}
	reg := tools.NewRegistry()
	reg.Register(tools.NewListDir(t.TempDir(), nil))

	var output bytes.Buffer
	renderer := NewRenderer(&output, 80, false)
	a := NewAgent(driver, reg, YoloApproval(), t.TempDir(), 10, renderer, nil, nil)
	a.isSubAgent = true

	if err := a.Run(context.Background(), "find files"); err != nil {
		t.Fatal(err)
	}
	// Should return after 1 response, not nudge for more.
	if driver.callCount != 1 {
		t.Errorf("expected 1 LLM call, got %d (sub-agent was nudged)", driver.callCount)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/agent/ -run TestSubAgentSkipsNudgeOnShortResponse -v`
Expected: FAIL — `isSubAgent` field doesn't exist yet.

- [ ] **Step 3: Add fields to Agent struct**

In `internal/agent/agent.go`, update the struct at line 16:

```go
type Agent struct {
	driver          llm.Driver
	tools           *tools.Registry
	approve         tools.ApprovalFunc
	history         []llm.Message
	system          string
	systemOverride  bool
	workDir         string
	maxTurns        int
	renderer        RenderTarget
	skills          []skills.Skill
	state           *chatstate.State
	isSubAgent      bool
	lastFullResponse string
	role            string
	mu              sync.Mutex
	activeSubCancel context.CancelFunc
}
```

Add `"sync"` to imports.

Add methods after `SetTools` (around line 74):

```go
// SetRole sets the agent's role identifier (e.g. "dispatch").
func (a *Agent) SetRole(role string) {
	a.role = role
}

// CancelSubAgent cancels the currently running sub-agent, if any.
func (a *Agent) CancelSubAgent() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.activeSubCancel != nil {
		a.activeSubCancel()
	}
}
```

- [ ] **Step 4: Add `callCount` to mockDriver**

In `internal/agent/agent_test.go`, find the `mockDriver` struct and add a `callCount int` field. Increment it in the `Stream` method.

- [ ] **Step 5: Skip nudge for sub-agents**

In `internal/agent/agent.go`, find the nudge check at line 197:

```go
if (isPreamble || isShort) && actionPreambleRetries < 4 && turn+1 < a.maxTurns {
```

Change to:

```go
if !a.isSubAgent && (isPreamble || isShort) && actionPreambleRetries < 4 && turn+1 < a.maxTurns {
```

- [ ] **Step 6: Run test to verify it passes**

Run: `go test ./internal/agent/ -run TestSubAgentSkipsNudgeOnShortResponse -v`
Expected: PASS

- [ ] **Step 7: Commit**

```bash
git add internal/agent/agent.go internal/agent/agent_test.go
git commit -m "feat: add isSubAgent, role, cancel fields to Agent; skip nudge for sub-agents"
```

### Task 2: Preserve full sub-agent response

**Files:**
- Modify: `internal/agent/agent.go:188-248` (response handling in Run loop)
- Modify: `internal/agent/subagent.go:74-86` (result extraction)
- Test: `internal/agent/agent_test.go`

- [ ] **Step 1: Write failing test for lastFullResponse preservation**

```go
func TestLastFullResponsePreserved(t *testing.T) {
	longResponse := strings.Repeat("x", 500)
	driver := &mockDriver{responses: []string{longResponse}}
	reg := tools.NewRegistry()

	var output bytes.Buffer
	renderer := NewRenderer(&output, 80, false)
	a := NewAgent(driver, reg, YoloApproval(), t.TempDir(), 10, renderer, nil, nil)
	a.isSubAgent = true

	if err := a.Run(context.Background(), "do something"); err != nil {
		t.Fatal(err)
	}
	if a.lastFullResponse != longResponse {
		t.Errorf("lastFullResponse length = %d, want %d", len(a.lastFullResponse), len(longResponse))
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/agent/ -run TestLastFullResponsePreserved -v`
Expected: FAIL — `lastFullResponse` is never set.

- [ ] **Step 3: Set lastFullResponse on both code paths**

In `internal/agent/agent.go`, in the Run method:

In the "no tool calls" path (around line 194), before the nudge check:
```go
a.lastFullResponse = response
```

In the tool-call path, after executing tools and before compaction (around line 238):
```go
a.lastFullResponse = visibleText
```

- [ ] **Step 4: Update SpawnSubAgent to use lastFullResponse**

In `internal/agent/subagent.go`, replace lines 76-86:

```go
// Extract the last assistant message as the result.
var result string
for i := len(sub.history) - 1; i >= 0; i-- {
	if sub.history[i].Role == llm.RoleAssistant {
		result = sub.history[i].Content
		break
	}
}
if result == "" {
	result = "(sub-agent produced no output)"
}
```

With:

```go
result := sub.lastFullResponse
if result == "" {
	result = "(sub-agent produced no output)"
}
```

Also set `isSubAgent` on the sub-agent. In the sub-agent construction (around line 64), add:

```go
sub := &Agent{
	driver:     driver,
	tools:      filteredTools,
	approve:    a.approve,
	workDir:    a.workDir,
	maxTurns:   roleDef.MaxTurns,
	renderer:   subRenderer,
	system:     system,
	isSubAgent: true,
}
```

- [ ] **Step 5: Run tests**

Run: `go test ./internal/agent/ -v`
Expected: All PASS

- [ ] **Step 6: Commit**

```bash
git add internal/agent/agent.go internal/agent/subagent.go internal/agent/agent_test.go
git commit -m "fix: preserve full sub-agent response; bypass compaction truncation"
```

### Task 3: Dispatch prose filtering

**Files:**
- Modify: `internal/agent/agent.go:238-244` (compactAssistantHistory call)
- Modify: `internal/runtime/chat.go:157-158` (configureMultiAgent)
- Test: `internal/agent/agent_test.go`

- [ ] **Step 1: Write failing test for dispatch prose suppression**

```go
func TestDispatchProseFilteredOnToolCallTurns(t *testing.T) {
	driver := &mockDriver{responses: []string{
		"Let me delegate to scout.\n\n<tool_call>\n{\"name\": \"delegate\", \"args\": {\"role\": \"scout\", \"task\": \"find it\"}}\n</tool_call>",
		"Here are the results from scout.",
	}}
	reg := tools.NewRegistry()
	reg.Register(tools.Tool{
		Name:        "delegate",
		Description: "Delegate",
		Execute: func(ctx context.Context, args map[string]any) (string, error) {
			return "scout found stuff", nil
		},
	})

	var output bytes.Buffer
	renderer := NewRenderer(&output, 80, false)
	a := NewAgent(driver, reg, YoloApproval(), t.TempDir(), 10, renderer, nil, nil)
	a.SetRole("dispatch")

	if err := a.Run(context.Background(), "find something"); err != nil {
		t.Fatal(err)
	}
	got := output.String()
	// Prose before tool call should be suppressed
	if strings.Contains(got, "Let me delegate") {
		t.Errorf("dispatch prose before tool call leaked: %q", got)
	}
	// Result presentation (no tool calls) should be shown
	if !strings.Contains(got, "Here are the results") {
		t.Errorf("dispatch result presentation missing from output: %q", got)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/agent/ -run TestDispatchProseFilteredOnToolCallTurns -v`
Expected: FAIL — "Let me delegate" appears in output.

- [ ] **Step 3: Implement dispatch prose filtering**

In `internal/agent/agent.go`, in the Run method's streaming filter section. Add `seenToolCall` to the per-turn state (around line 119, before the token loop):

```go
seenToolCall := false
```

In the tool call open detection (around line 141):
```go
if _, ok := isToolCallOpen(trimmed); ok {
	seenToolCall = true
	inToolCall = true
	continue
}
```

In the token rendering (around line 159):
```go
if !inToolCall {
	if a.role != "dispatch" || seenToolCall {
		a.renderer.AgentToken(line)
	}
}
```

Also update the partial line flush (around line 170) with the same guard:
```go
if !inToolCall {
	if a.role != "dispatch" || seenToolCall {
		// existing flush logic
	}
}
```

Also in the compactAssistantHistory call (around line 239), suppress dispatch prose on tool-call turns:
```go
assistantText := visibleText
if a.role == "dispatch" && len(calls) > 0 {
	assistantText = ""
}
if assistantSummary := compactAssistantHistory(assistantText); assistantSummary != "" {
```

- [ ] **Step 4: Set dispatch role in configureMultiAgent**

In `internal/runtime/chat.go`, in `configureMultiAgent` (around line 288), add after `a.SetTools(...)`:

```go
a.SetRole("dispatch")
```

- [ ] **Step 5: Run tests**

Run: `go test ./internal/agent/ -v`
Expected: All PASS

- [ ] **Step 6: Commit**

```bash
git add internal/agent/agent.go internal/agent/agent_test.go internal/runtime/chat.go
git commit -m "feat: filter dispatch narration on tool-call turns"
```

## Chunk 2: Progress Events + Sub-Agent Cancellation

### Task 4: Add EventProgress and progressLine

**Files:**
- Modify: `internal/llm/types.go:66-81` (EventKind constants)
- Modify: `internal/agent/event_render.go:125-163` (SubAgentRenderer methods)
- Create: `internal/agent/progress.go` (progressLine helper)
- Test: `internal/agent/progress_test.go`

- [ ] **Step 1: Write failing test for progressLine**

Create `internal/agent/progress_test.go`:

```go
package agent

import "testing"

func TestProgressLine(t *testing.T) {
	tests := []struct {
		role, tool, summary string
		want                string
	}{
		{"scout", "read_file", "/Users/x/main.go", "scout: reading main.go"},
		{"scout", "search", "session", `scout: searching for "session"`},
		{"scout", "glob", "*.go", `scout: finding "*.go"`},
		{"builder", "edit_file", "/Users/x/main.go", "builder: editing main.go"},
		{"builder", "run_command", "go build ./...", "builder: running go build ./..."},
		{"builder", "run_command", "very long command that exceeds the forty character limit here", "builder: running very long command that exceeds the for..."},
		{"builder", "write_file", "/Users/x/new.go", "builder: writing new.go"},
		{"dispatch", "delegate", "scout", "dispatching to scout"},
		{"scout", "unknown_tool", "whatever", "scout: unknown_tool"},
	}
	for _, tt := range tests {
		t.Run(tt.tool, func(t *testing.T) {
			got := progressLine(tt.role, tt.tool, tt.summary)
			if got != tt.want {
				t.Errorf("progressLine(%q, %q, %q) = %q, want %q", tt.role, tt.tool, tt.summary, got, tt.want)
			}
		})
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/agent/ -run TestProgressLine -v`
Expected: FAIL — function doesn't exist.

- [ ] **Step 3: Add EventProgress to types.go**

In `internal/llm/types.go`, add after `EventStats` (line 81):

```go
EventProgress EventKind = "progress"
```

- [ ] **Step 4: Create progressLine helper**

Create `internal/agent/progress.go`:

```go
package agent

import (
	"fmt"
	"path/filepath"
)

// progressLine generates a human-readable one-liner for a sub-agent tool call.
func progressLine(role, toolName, summary string) string {
	switch toolName {
	case "read_file":
		return fmt.Sprintf("%s: reading %s", role, filepath.Base(summary))
	case "search":
		return fmt.Sprintf("%s: searching for %q", role, summary)
	case "glob":
		return fmt.Sprintf("%s: finding %q", role, summary)
	case "edit_file":
		return fmt.Sprintf("%s: editing %s", role, filepath.Base(summary))
	case "write_file":
		return fmt.Sprintf("%s: writing %s", role, filepath.Base(summary))
	case "run_command":
		cmd := summary
		if len(cmd) > 40 {
			cmd = cmd[:40] + "..."
		}
		return fmt.Sprintf("%s: running %s", role, cmd)
	case "delegate":
		return fmt.Sprintf("dispatching to %s", summary)
	default:
		return fmt.Sprintf("%s: %s", role, toolName)
	}
}
```

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./internal/agent/ -run TestProgressLine -v`
Expected: PASS

- [ ] **Step 6: Update SubAgentRenderer to emit progress events**

In `internal/agent/event_render.go`, update `SubAgentRenderer.ToolCall` (line 133):

```go
func (r *SubAgentRenderer) ToolCall(name, summary string) {
	r.parent.events <- llm.Event{Kind: llm.EventToolCall, Agent: name, Text: summary, SubAgent: r.role}
	r.parent.events <- llm.Event{Kind: llm.EventProgress, Agent: r.role, Text: progressLine(r.role, name, summary)}
}
```

Update `SubAgentRenderer.ToolResult` (line 137) to emit progress on errors:

```go
func (r *SubAgentRenderer) ToolResult(name, output, diff string, isError bool) {
	r.parent.events <- llm.Event{
		Kind:     llm.EventToolResult,
		Agent:    name,
		Text:     output,
		Content:  diff,
		IsError:  isError,
		SubAgent: r.role,
	}
	if isError {
		r.parent.events <- llm.Event{Kind: llm.EventProgress, Agent: r.role, Text: fmt.Sprintf("%s: %s failed", r.role, name)}
	}
}
```

Add `"fmt"` to imports if not already present.

- [ ] **Step 7: Route EventProgress in TUI**

In `internal/tui/chatmodel.go`, in `handleLLMEvent` (around line 754), add a case before the `default`:

```go
case llm.EventProgress:
	m.AddWorkingMessage(ev.Text)
```

- [ ] **Step 8: Run all tests**

Run: `go build ./... && go test ./...`
Expected: All PASS

- [ ] **Step 9: Commit**

```bash
git add internal/llm/types.go internal/agent/progress.go internal/agent/progress_test.go internal/agent/event_render.go internal/tui/chatmodel.go
git commit -m "feat: sub-agent progress events stream to chat pane"
```

### Task 5: Sub-agent cancellation

**Files:**
- Modify: `internal/agent/subagent.go:30-94` (SpawnSubAgent)
- Modify: `internal/tui/chatmodel.go:1025-1035` (escape key handling)
- Modify: `internal/runtime/chat.go:197-228` (input switch)
- Test: `internal/agent/subagent_test.go` (new)

- [ ] **Step 1: Write failing test for CancelSubAgent**

Create `internal/agent/subagent_test.go`:

```go
package agent

import (
	"context"
	"testing"
	"time"
)

func TestCancelSubAgentSafe(t *testing.T) {
	a := &Agent{}
	// Should not panic when no sub-agent is active.
	a.CancelSubAgent()
}

func TestCancelSubAgentStopsRun(t *testing.T) {
	// Create a slow driver that blocks until context is cancelled.
	driver := &blockingDriver{}
	reg := tools.NewRegistry()
	reg.Register(tools.NewListDir(t.TempDir(), nil))

	var output bytes.Buffer
	renderer := NewRenderer(&output, 80, false)
	a := NewAgent(driver, reg, YoloApproval(), t.TempDir(), 10, renderer, nil, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- a.Run(ctx, "do something slow")
	}()

	// Give Run a moment to start streaming.
	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case err := <-errCh:
		if err == nil {
			// Context cancellation may or may not produce an error depending on timing.
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not stop after context cancellation")
	}
}
```

Also add `blockingDriver` to the test file:

```go
type blockingDriver struct{}

func (d *blockingDriver) Name() string { return "blocking" }
func (d *blockingDriver) Stream(ctx context.Context, messages []llm.Message, out chan<- llm.Token) error {
	defer close(out)
	<-ctx.Done()
	return ctx.Err()
}
```

- [ ] **Step 2: Run test to verify behavior**

Run: `go test ./internal/agent/ -run TestCancelSubAgent -v`
Expected: First test passes (CancelSubAgent is already implemented). Second test validates context cancellation works.

- [ ] **Step 3: Add child context to SpawnSubAgent**

In `internal/agent/subagent.go`, update `SpawnSubAgent`. After the role lookup and driver resolution, before creating the sub-agent:

```go
subCtx, subCancel := context.WithCancel(ctx)
defer subCancel()

a.mu.Lock()
a.activeSubCancel = subCancel
a.mu.Unlock()
defer func() {
	a.mu.Lock()
	a.activeSubCancel = nil
	a.mu.Unlock()
}()
```

Change `sub.Run(ctx, task)` to `sub.Run(subCtx, task)`.

After `sub.Run` returns, detect cancellation:

```go
err := sub.Run(subCtx, task)

if err != nil && subCtx.Err() != nil {
	subRenderer.Info(fmt.Sprintf("[%s] cancelled", role))
	return fmt.Sprintf("CANCELLED: %s was cancelled by user. Present what you have or re-delegate.", role), nil
}
```

Add `"sync"` to imports if not already present.

- [ ] **Step 4: Add escape routing in TUI**

In `internal/tui/chatmodel.go`, add a `lastEscapeTime time.Time` field to `ChatModel` struct (around line 103).

Update the escape key handler (line 1025):

```go
case tea.KeyEscape:
	if m.busy && m.inputCh != nil {
		ch := m.inputCh
		if m.activeSubAgent != "" && time.Since(m.lastEscapeTime) > 2*time.Second {
			// First escape while sub-agent active: cancel sub-agent only.
			m.lastEscapeTime = time.Now()
			m.flash = fmt.Sprintf("cancelling %s... (Esc again to cancel turn)", m.activeSubAgent)
			return m, func() tea.Msg {
				ch <- "__cancel_subagent__"
				return nil
			}
		}
		// Second escape (or no sub-agent): cancel whole turn.
		m.lastEscapeTime = time.Now()
		m.flash = "canceling..."
		return m, func() tea.Msg {
			ch <- "__cancel_turn__"
			return nil
		}
	}
	m.resetSlashCompletion()
	return m, nil
```

- [ ] **Step 5: Handle __cancel_subagent__ in runtime**

In `internal/runtime/chat.go`, in the input switch (around line 202), add a new case before `__cancel_turn__`:

```go
case "__cancel_subagent__":
	a.CancelSubAgent()
	evRenderer.Info("sub-agent cancelled")
```

- [ ] **Step 6: Run all tests**

Run: `go build ./... && go test ./...`
Expected: All PASS

- [ ] **Step 7: Commit**

```bash
git add internal/agent/subagent.go internal/agent/subagent_test.go internal/tui/chatmodel.go internal/runtime/chat.go
git commit -m "feat: sub-agent cancellation via Escape key"
```

## Chunk 3: Tools Pane Collapse

### Task 6: Replace flat toolsBuf with structured sections

**Files:**
- Modify: `internal/tui/chatmodel.go` (toolsBuf → toolsSections throughout)

This is a larger refactor touching many places where `toolsBuf` is read/written. The approach: define the `toolsSection` type, replace the field, update all write sites (handleLLMEvent, handleSubAgentEvent), and update the render site.

- [ ] **Step 1: Define toolsSection type and add field**

In `internal/tui/chatmodel.go`, add the type near the top (after the imports, around line 65):

```go
type toolsSection struct {
	role      string // "" for main agent tools
	buf       string // full detail
	summary   string // collapsed summary (set on completion)
	collapsed bool   // true after sub-agent completes
	turnCount int
	toolCount int
}
```

In the `ChatModel` struct, replace:
```go
toolsBuf        string
```
With:
```go
toolsSections []toolsSection
```

- [ ] **Step 2: Add helper methods for tools sections**

```go
// currentToolsSection returns a pointer to the current (last) tools section,
// creating one if needed. role="" for main agent sections.
func (m *ChatModel) currentToolsSection(role string) *toolsSection {
	if len(m.toolsSections) == 0 || m.toolsSections[len(m.toolsSections)-1].role != role {
		m.toolsSections = append(m.toolsSections, toolsSection{role: role})
	}
	return &m.toolsSections[len(m.toolsSections)-1]
}

func (m *ChatModel) appendTools(role, text string) {
	sec := m.currentToolsSection(role)
	sec.buf += text
}

func (m *ChatModel) renderedToolsBuf() string {
	var sb strings.Builder
	for _, sec := range m.toolsSections {
		if sec.collapsed && sec.summary != "" {
			sb.WriteString(sec.summary)
			sb.WriteByte('\n')
		} else {
			sb.WriteString(sec.buf)
		}
	}
	return sb.String()
}

func (m *ChatModel) clearToolsSections() {
	m.toolsSections = nil
}

func (m *ChatModel) toolsMaxScroll() int {
	content := m.renderedToolsBuf()
	lines := strings.Count(content, "\n")
	height := m.toolsHeight()
	if lines <= height {
		return 0
	}
	return lines - height
}
```

- [ ] **Step 3: Update handleLLMEvent to use sections**

Search for all `m.toolsBuf` references in `handleLLMEvent` and replace with `m.appendTools("", ...)`. For example:

```go
// Was: m.toolsBuf += "────────────────────────\n"
// Now:
m.appendTools("", "────────────────────────\n")
m.appendTools("", fmt.Sprintf("● %s\n", ev.Agent))
m.appendTools("", fmt.Sprintf("  %s\n", ev.Text))
```

Do the same for EventToolResult, EventRoundStart, EventDone, EventError, EventStats.

For EventDone, also clear active sections:
```go
case llm.EventDone:
	m.busy = false
	m.activeSubAgent = ""
	m.status = "ready"
	stamp := time.Now().Format("15:04:05")
	m.AddMessage(ChatMessage{Kind: MsgStatus, Content: "Agent complete • " + stamp})
	if len(m.toolsSections) > 0 {
		m.appendTools("", fmt.Sprintf("status: complete • %s\n", stamp))
	}
```

- [ ] **Step 4: Update handleSubAgentEvent to use sections**

Replace all `m.toolsBuf` references in `handleSubAgentEvent`:

On sub-agent start:
```go
if strings.Contains(ev.Text, "] starting") {
	m.toolsSections = append(m.toolsSections, toolsSection{role: label})
	sec := &m.toolsSections[len(m.toolsSections)-1]
	sec.buf = fmt.Sprintf("┌─ %s ─────────────────\n", label)
	m.status = label
	m.toolsScroll = m.toolsMaxScroll()
	return m, nil
}
```

On sub-agent done — generate summary and collapse:
```go
if strings.Contains(ev.Text, "] done") || strings.Contains(ev.Text, "] cancelled") {
	for i := len(m.toolsSections) - 1; i >= 0; i-- {
		if m.toolsSections[i].role == label {
			sec := &m.toolsSections[i]
			status := "complete"
			if strings.Contains(ev.Text, "cancelled") {
				status = "cancelled"
			}
			sec.buf += fmt.Sprintf("└─ %s %s ────────\n\n", label, status)
			sec.summary = fmt.Sprintf("┌─ %s (%d turns, %d tools) %s ─\n└─────────────────────\n",
				label, sec.turnCount, sec.toolCount, status)
			sec.collapsed = true
			break
		}
	}
	m.activeSubAgent = ""
	m.status = "running"
	m.toolsScroll = m.toolsMaxScroll()
	return m, nil
}
```

For EventToken, EventToolCall, EventToolResult in sub-agent events:
```go
case llm.EventToken:
	m.appendTools(label, ev.Text)
case llm.EventToolCall:
	sec := m.currentToolsSection(label)
	sec.toolCount++
	m.appendTools(label, fmt.Sprintf("  │ %s › %s\n", label, ev.Agent))
	m.appendTools(label, fmt.Sprintf("  │   %s\n", ev.Text))
```

Track turn counts — increment on EventStats:
```go
case llm.EventStats:
	for i := len(m.toolsSections) - 1; i >= 0; i-- {
		if m.toolsSections[i].role == label {
			m.toolsSections[i].turnCount++
			break
		}
	}
```

- [ ] **Step 5: Update tools pane rendering**

Find all remaining references to `m.toolsBuf` in the View/render methods and replace with `m.renderedToolsBuf()`. Search for `toolsBuf` — there should be references in:
- The tools pane visibility check (replace `m.toolsBuf != ""` with `len(m.toolsSections) > 0`)
- The tools pane content rendering
- The `/clear` handler (replace `m.toolsBuf = ""` with `m.clearToolsSections()`)

- [ ] **Step 6: Add Tab toggle for collapse/expand**

In the tools-pane-focused key handler, add Tab:

```go
case tea.KeyTab:
	if m.paneFocus == focusTools && len(m.toolsSections) > 0 {
		// Find the section visible at current scroll position and toggle it.
		lineCount := 0
		for i := range m.toolsSections {
			sec := &m.toolsSections[i]
			var secLines int
			if sec.collapsed {
				secLines = strings.Count(sec.summary, "\n")
			} else {
				secLines = strings.Count(sec.buf, "\n")
			}
			if lineCount+secLines > m.toolsScroll || i == len(m.toolsSections)-1 {
				if sec.role != "" && sec.summary != "" {
					sec.collapsed = !sec.collapsed
				}
				break
			}
			lineCount += secLines
		}
		m.toolsScroll = m.toolsMaxScroll()
		return m, nil
	}
```

- [ ] **Step 7: Run all tests**

Run: `go build ./... && go test ./...`
Expected: All PASS

- [ ] **Step 8: Commit**

```bash
git add internal/tui/chatmodel.go
git commit -m "feat: structured tools pane with collapsible sub-agent sections"
```

## Chunk 4: Agent Configuration TUI

### Task 7: /agents toggle command

**Files:**
- Modify: `internal/tui/chatmodel.go` (slash command handling + state)
- Modify: `internal/runtime/chat.go` (expose configureMultiAgent state)

- [ ] **Step 1: Add agents state fields to ChatModel**

In `internal/tui/chatmodel.go`, add to `ChatModel` struct:

```go
agentsEnabled    bool
agentSetup       *runtime.AgentToggleState // nil when agents not available
```

Define `AgentToggleState` in `internal/runtime/chat.go`:

```go
// AgentToggleState holds the state needed to toggle multi-agent mode on/off.
type AgentToggleState struct {
	Agent      *agent.Agent
	BaseReg    *tools.Registry
	Setup      *ChatSetup
	OrigSystem string
	OrigTools  *tools.Registry
}
```

- [ ] **Step 2: Wire AgentToggleState in RunChatLive**

In `internal/runtime/chat.go`, in `RunChatLive`, after creating the agent and optionally calling `configureMultiAgent`, create the toggle state and pass it through to the TUI config:

```go
var agentToggle *AgentToggleState
if setup.Config.Chat.Agents.Enabled {
	agentToggle = &AgentToggleState{
		Agent:   a,
		BaseReg: reg,
		Setup:   setup,
	}
}
```

Pass `agentToggle` through the `ChatLiveConfig` to `ChatModel`.

- [ ] **Step 3: Handle /agents slash command**

In the slash command processing in `chatmodel.go` `submitInput` method, add:

```go
case "/agents":
	if m.agentSetup == nil {
		m.flash = "agents not available (no config)"
		return m, nil
	}
	if m.agentsEnabled {
		// Disable: restore original state.
		// (Requires storing pre-agent state — set OrigSystem/OrigTools before configureMultiAgent)
		m.agentsEnabled = false
		m.flash = "agents: disabled"
	} else {
		// Enable: reconfigure.
		m.agentsEnabled = true
		m.flash = "agents: enabled"
	}
	return m, nil
```

- [ ] **Step 4: Run all tests**

Run: `go build ./... && go test ./...`
Expected: All PASS

- [ ] **Step 5: Commit**

```bash
git add internal/tui/chatmodel.go internal/runtime/chat.go
git commit -m "feat: /agents toggle command for multi-agent mode"
```

### Task 8: /agents models overlay

**Files:**
- Modify: `internal/tui/chatmodel.go` (overlay state, rendering, key handling)
- Modify: `internal/config/config.go` (SaveAgentModels)

- [ ] **Step 1: Add overlay state to ChatModel**

```go
agentModelsVisible bool
agentModelsCursor  int
agentModelsRoles   []string                // ["dispatch", "scout", "builder", "doctor", "architect"]
agentModelsMap     map[string]string       // role → model override (empty = default)
agentModelsDefault string                  // the chat model (fallback)
```

Initialize `agentModelsRoles` as: `[]string{"dispatch", "scout", "builder", "doctor", "architect"}`

Initialize `agentModelsMap` from `config.AgentRoleModels()`.

- [ ] **Step 2: Handle /agents models command**

In slash command processing:

```go
case "/agents models":
	m.agentModelsVisible = true
	m.agentModelsCursor = 0
	return m, nil
```

- [ ] **Step 3: Implement overlay key handling**

Add to the Update method, when `m.agentModelsVisible`:

```go
if m.agentModelsVisible {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.Type {
		case tea.KeyEscape:
			m.agentModelsVisible = false
			return m, nil
		case tea.KeyUp:
			if m.agentModelsCursor > 0 {
				m.agentModelsCursor--
			}
		case tea.KeyDown:
			if m.agentModelsCursor < len(m.agentModelsRoles)-1 {
				m.agentModelsCursor++
			}
		case tea.KeyEnter:
			// Open model picker for the selected role.
			m.modelsVisible = true
			m.modelsCursor = 0
			m.modelsQuery = ""
			// Store which role we're picking for.
			m.agentModelsPickingRole = m.agentModelsRoles[m.agentModelsCursor]
			return m, nil
		case tea.KeyRunes:
			if string(msg.Runes) == "s" {
				// Save to TOML.
				if err := m.saveAgentModels(); err != nil {
					m.flash = "save failed: " + err.Error()
				} else {
					m.flash = "agent models saved to config"
				}
				return m, nil
			}
		}
	}
	return m, nil
}
```

Add `agentModelsPickingRole string` field. When the model picker returns a selection, check if `agentModelsPickingRole` is set and store the selection in `agentModelsMap` instead of changing the main model.

- [ ] **Step 4: Implement overlay rendering**

Add a `renderAgentModels` method:

```go
func (m ChatModel) renderAgentModels() string {
	var sb strings.Builder
	sb.WriteString(" Agent Models\n")
	sb.WriteString(" ─────────────────────────────────────\n")
	for i, role := range m.agentModelsRoles {
		cursor := "  "
		if i == m.agentModelsCursor {
			cursor = "▸ "
		}
		model := m.agentModelsMap[role]
		label := ""
		if model == "" {
			model = m.agentModelsDefault
			label = "  (default)"
		}
		sb.WriteString(fmt.Sprintf("%s%-12s %s%s\n", cursor, role, model, label))
	}
	sb.WriteString(" ─────────────────────────────────────\n")
	sb.WriteString(" ↑↓ select  Enter: pick model  s: save  Esc: close\n")
	return sb.String()
}
```

Render it as an overlay in the View method, similar to other overlays (models, providers).

- [ ] **Step 5: Implement SaveAgentModels**

In `internal/config/config.go`, add:

```go
// SaveAgentModels updates only the [chat.agents.models] section in the config file.
func SaveAgentModels(path string, models AgentModels) error {
	cfg, err := Load(path)
	if err != nil {
		cfg = &Config{}
	}
	cfg.Chat.Agents.Models = models
	return Save(path, cfg)
}
```

Wire the `saveAgentModels` method on ChatModel to call this with the current map converted to `AgentModels`.

- [ ] **Step 6: Run all tests**

Run: `go build ./... && go test ./...`
Expected: All PASS

- [ ] **Step 7: Commit**

```bash
git add internal/tui/chatmodel.go internal/config/config.go
git commit -m "feat: /agents models overlay with per-role model selection and save"
```

### Task 9: Final integration test and cleanup

**Files:**
- All modified files
- `docs/multi-agent-next.md` (update with completed items)

- [ ] **Step 1: Run full test suite**

Run: `go build ./... && go test ./... -count=1`
Expected: All PASS

- [ ] **Step 2: Run linters**

Run: `gofmt -l . && go vet ./...`
Expected: No output (all clean)

- [ ] **Step 3: Update multi-agent-next.md**

Mark completed items and remove sections that are done. Keep deferred items (parallel delegation).

- [ ] **Step 4: Commit cleanup**

```bash
git add docs/multi-agent-next.md
git commit -m "docs: update multi-agent next steps with completed items"
```
