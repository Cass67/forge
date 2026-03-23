# Bubble Tea Chat UI Migration Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the raw tcell chat UI (`chatlive.go`) with a proper Bubble Tea model that renders box-style message bubbles, preserving all existing functionality.

**Architecture:** The current chat screen bypasses Bubble Tea entirely — it runs its own tcell event loop, manages its own screen, and handles rendering cell-by-cell. Every other screen in the TUI already uses Bubble Tea properly. This migration replaces the raw tcell loop with a `ChatModel` that implements `tea.Model` and renders via `View()` using lipgloss for styling. The public API (`RunChatLive`, `ChatLiveConfig`, `ChatLiveResult`) stays the same.

**Tech Stack:** Bubble Tea v1.3.10 (already in go.mod), Lipgloss v1.1.0 (already in go.mod), charmbracelet/bubbles viewport (new dep for scrollable panes)

---

## Chunk 1: Foundation — ChatModel and Message Types

### Task 1: Add bubbles/viewport dependency

**Files:**
- Modify: `go.mod`

- [ ] **Step 1: Add viewport dependency**

```bash
cd /Users/cass/git/forge
go get github.com/charmbracelet/bubbles/viewport
```

- [ ] **Step 2: Verify it resolves**

Run: `go mod tidy`
Expected: Clean exit, viewport in go.sum

- [ ] **Step 3: Commit**

```bash
git add go.mod go.sum
git commit -m "deps: add charmbracelet/bubbles viewport"
```

---

### Task 2: Define message types and chat message model

**Files:**
- Create: `internal/tui/chatmsg.go`
- Test: `internal/tui/chatmsg_test.go`

The chat display needs a structured message list instead of raw text buffers. Each message knows its type (user, agent, forge, status) and renders itself as a box.

- [ ] **Step 1: Write the failing test**

```go
// internal/tui/chatmsg_test.go
package tui

import "testing"

func TestChatMessageRenderUser(t *testing.T) {
	m := ChatMessage{
		Kind:      MsgUser,
		Header:    "You • 22:59:50",
		Content:   "hello world",
	}
	got := m.Render(60, false)
	if got == "" {
		t.Fatal("expected non-empty render")
	}
	// Should contain the header and content
	if !strings.Contains(got, "You • 22:59:50") {
		t.Fatalf("render missing header: %s", got)
	}
	if !strings.Contains(got, "hello world") {
		t.Fatalf("render missing content: %s", got)
	}
}

func TestChatMessageRenderAgent(t *testing.T) {
	m := ChatMessage{
		Kind:    MsgAgent,
		Content: "I can help with that.",
	}
	got := m.Render(60, false)
	if got == "" {
		t.Fatal("expected non-empty render")
	}
	if !strings.Contains(got, "I can help with that.") {
		t.Fatalf("render missing content: %s", got)
	}
}

func TestChatMessageRenderStatus(t *testing.T) {
	m := ChatMessage{
		Kind:    MsgStatus,
		Content: "Agent complete • 22:59:51",
	}
	got := m.Render(60, false)
	if got == "" {
		t.Fatal("expected non-empty render")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/tui/ -run TestChatMessage -v`
Expected: FAIL — types not defined

- [ ] **Step 3: Write the message types and render**

```go
// internal/tui/chatmsg.go
package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// MsgKind identifies the type of chat message.
type MsgKind int

const (
	MsgUser   MsgKind = iota // User input
	MsgAgent                 // Agent response
	MsgForge                 // Forge steering input
	MsgStatus                // Status line (e.g. "Agent complete")
)

// ChatMessage is a single message in the conversation.
type ChatMessage struct {
	Kind    MsgKind
	Header  string // e.g. "You • 22:59:50" (empty for agent)
	Content string // message body (may be multi-line)
}

// borderColor returns the accent color for this message kind.
func (m ChatMessage) borderColor() lipgloss.Color {
	switch m.Kind {
	case MsgUser:
		return lipgloss.Color("#56d364")
	case MsgAgent:
		return lipgloss.Color("#58a6ff")
	case MsgForge:
		return lipgloss.Color("#d2a8ff")
	default:
		return lipgloss.Color("#484f58")
	}
}

// Render returns the styled string for this message at the given width.
// lowContrast adjusts colors for accessibility.
func (m ChatMessage) Render(width int, lowContrast bool) string {
	if width < 10 {
		width = 10
	}

	// Status messages are just dim centered text
	if m.Kind == MsgStatus {
		style := lipgloss.NewStyle().
			Foreground(lipgloss.Color("#484f58")).
			Width(width).
			Align(lipgloss.Center)
		return style.Render(m.Content)
	}

	bc := m.borderColor()
	if lowContrast && m.Kind == MsgUser {
		bc = lipgloss.Color("#7fbf7f")
	}

	boxBg := lipgloss.Color("#0d1117")
	headerBg := lipgloss.Color("#161b22")
	innerWidth := width - 2 // account for left+right border

	// Build header if present
	var headerBlock string
	if m.Header != "" {
		headerStyle := lipgloss.NewStyle().
			Background(headerBg).
			Foreground(bc).
			Bold(true).
			Width(innerWidth).
			Padding(0)
		headerBlock = headerStyle.Render(m.Header)
	}

	// Build content
	contentStyle := lipgloss.NewStyle().
		Background(boxBg).
		Foreground(lipgloss.Color("#c9d1d9")).
		Width(innerWidth).
		Padding(0)
	contentBlock := contentStyle.Render(m.Content)

	// Combine header + separator + content inside a border
	var inner string
	if headerBlock != "" {
		sepStyle := lipgloss.NewStyle().
			Background(boxBg).
			Foreground(bc).
			Width(innerWidth)
		sep := sepStyle.Render(strings.Repeat("─", innerWidth))
		inner = lipgloss.JoinVertical(lipgloss.Left, headerBlock, sep, contentBlock)
	} else {
		inner = contentBlock
	}

	// Wrap in border
	boxStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(bc).
		Background(boxBg).
		Width(width - 2) // border takes 2 chars
	return boxStyle.Render(inner)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/tui/ -run TestChatMessage -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/tui/chatmsg.go internal/tui/chatmsg_test.go
git commit -m "feat: add ChatMessage type with box rendering"
```

---

### Task 3: Define ChatModel with message list and viewport

**Files:**
- Create: `internal/tui/chatmodel.go`
- Test: `internal/tui/chatmodel_test.go`

This is the new Bubble Tea model that replaces the raw tcell `chatLiveModel`. It holds the message list, input buffer, and viewport for scrolling.

- [ ] **Step 1: Write the failing test**

```go
// internal/tui/chatmodel_test.go
package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestChatModelInit(t *testing.T) {
	m := NewChatModel(ChatLiveConfig{Model: "test-model", WorkDir: "/tmp"})
	cmd := m.Init()
	if cmd == nil {
		t.Fatal("Init should return a command for spinner/event listening")
	}
}

func TestChatModelAddMessage(t *testing.T) {
	m := NewChatModel(ChatLiveConfig{Model: "test-model", WorkDir: "/tmp"})
	m.AddMessage(ChatMessage{Kind: MsgUser, Header: "You • 12:00:00", Content: "hello"})
	if len(m.messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(m.messages))
	}
	if m.messages[0].Content != "hello" {
		t.Fatalf("message content = %q", m.messages[0].Content)
	}
}

func TestChatModelViewNotEmpty(t *testing.T) {
	m := NewChatModel(ChatLiveConfig{Model: "test-model", WorkDir: "/tmp"})
	m.width = 80
	m.height = 24
	m.AddMessage(ChatMessage{Kind: MsgUser, Header: "You • 12:00:00", Content: "hello"})
	v := m.View()
	if v == "" {
		t.Fatal("View() should not be empty")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/tui/ -run TestChatModel -v`
Expected: FAIL

- [ ] **Step 3: Write the ChatModel**

```go
// internal/tui/chatmodel.go
package tui

import (
	"strings"
	"time"

	"forge/internal/agent/tools"
	"forge/internal/chatstate"
	"forge/internal/skills"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// chatTickMsg is sent periodically for spinner animation.
type chatTickMsg time.Time

// ChatModel is the Bubble Tea model for the interactive chat screen.
type ChatModel struct {
	// Config
	config  ChatLiveConfig
	model   string
	workDir string

	// Messages
	messages []ChatMessage

	// Agent streaming state
	agentStreaming bool   // true while agent is generating
	streamBuf     string // partial content of current agent message

	// Input
	inputBuf string
	inputPos int

	// Layout
	width  int
	height int

	// Viewport for scrollable message area
	chatViewport viewport.Model

	// Tools pane
	toolsBuf     string
	toolsVisible bool

	// State
	busy           bool
	status         string
	flash          string
	skills         []skills.Skill
	autoSkillsMode string
	state          *chatstate.State
	lowContrast    bool

	// Channels (set externally before running)
	inputCh    chan<- string
	approvalCh <-chan tools.Action
	responseCh chan<- bool
}

// NewChatModel creates a new ChatModel from config.
func NewChatModel(cfg ChatLiveConfig) ChatModel {
	vp := viewport.New(80, 20)
	vp.SetContent("")

	state := cfg.State
	if state == nil {
		state = chatstate.New()
	}

	return ChatModel{
		config:         cfg,
		model:          cfg.Model,
		workDir:        cfg.WorkDir,
		chatViewport:   vp,
		status:         "ready",
		skills:         cfg.Skills,
		autoSkillsMode: cfg.AutoSkillsMode,
		state:          state,
		toolsVisible:   true,
	}
}

func (m ChatModel) Init() tea.Cmd {
	return tea.Batch(
		m.chatViewport.Init(),
		tickCmd(),
	)
}

func tickCmd() tea.Cmd {
	return tea.Tick(120*time.Millisecond, func(t time.Time) tea.Msg {
		return chatTickMsg(t)
	})
}

// AddMessage appends a message and refreshes the viewport.
func (m *ChatModel) AddMessage(msg ChatMessage) {
	m.messages = append(m.messages, msg)
	m.refreshViewport()
}

// AppendToLastAgent appends streaming text to the last agent message.
func (m *ChatModel) AppendToLastAgent(text string) {
	if len(m.messages) == 0 || m.messages[len(m.messages)-1].Kind != MsgAgent {
		m.messages = append(m.messages, ChatMessage{Kind: MsgAgent})
	}
	m.messages[len(m.messages)-1].Content += text
	m.refreshViewport()
}

// refreshViewport re-renders all messages into the viewport content.
func (m *ChatModel) refreshViewport() {
	contentWidth := m.chatPaneWidth()
	if contentWidth < 10 {
		contentWidth = 60
	}

	var blocks []string
	for _, msg := range m.messages {
		blocks = append(blocks, msg.Render(contentWidth, m.lowContrast))
	}
	content := strings.Join(blocks, "\n")
	m.chatViewport.SetContent(content)
	m.chatViewport.GotoBottom()
}

func (m ChatModel) chatPaneWidth() int {
	if !m.toolsVisible {
		return m.width
	}
	return max(20, m.width*7/10)
}

func (m ChatModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		headerH := 1
		inputH := 4
		bodyH := max(3, m.height-headerH-inputH)
		m.chatViewport.Width = m.chatPaneWidth()
		m.chatViewport.Height = bodyH
		m.refreshViewport()
		return m, nil

	case chatTickMsg:
		return m, tickCmd()

	case tea.KeyMsg:
		return m.handleKey(msg)
	}

	// Update viewport (handles scroll etc.)
	var cmd tea.Cmd
	m.chatViewport, cmd = m.chatViewport.Update(msg)
	return m, cmd
}

func (m ChatModel) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c":
		return m, tea.Quit
	case "enter":
		return m.submitInput()
	case "backspace":
		if len(m.inputBuf) > 0 && m.inputPos > 0 {
			runes := []rune(m.inputBuf)
			m.inputBuf = string(append(runes[:m.inputPos-1], runes[m.inputPos:]...))
			m.inputPos--
		}
	case "left":
		if m.inputPos > 0 {
			m.inputPos--
		}
	case "right":
		if m.inputPos < len([]rune(m.inputBuf)) {
			m.inputPos++
		}
	default:
		if len(msg.String()) == 1 {
			runes := []rune(m.inputBuf)
			newRunes := make([]rune, 0, len(runes)+1)
			newRunes = append(newRunes, runes[:m.inputPos]...)
			newRunes = append(newRunes, []rune(msg.String())...)
			newRunes = append(newRunes, runes[m.inputPos:]...)
			m.inputBuf = string(newRunes)
			m.inputPos++
		}
	}
	return m, nil
}

func (m ChatModel) submitInput() (tea.Model, tea.Cmd) {
	input := strings.TrimSpace(m.inputBuf)
	if input == "" {
		return m, nil
	}

	// Add user message
	stamp := time.Now().Format("15:04:05")
	m.AddMessage(ChatMessage{
		Kind:    MsgUser,
		Header:  "You • " + stamp,
		Content: input,
	})

	m.inputBuf = ""
	m.inputPos = 0
	m.busy = true
	m.status = "running"

	// Send to agent
	if m.inputCh != nil {
		m.inputCh <- input
	}

	return m, nil
}

func (m ChatModel) View() string {
	if m.width == 0 || m.height == 0 {
		return "Initializing..."
	}

	// Header
	headerStyle := lipgloss.NewStyle().
		Background(lipgloss.Color("#161b22")).
		Foreground(lipgloss.Color("#c9d1d9")).
		Width(m.width).
		Bold(true)
	header := headerStyle.Render("forge • " + m.model + " • " + m.workDir)

	// Chat viewport
	chatPane := m.chatViewport.View()

	// Input area
	inputStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("#30363d")).
		Background(lipgloss.Color("#0d1117")).
		Foreground(lipgloss.Color("#c9d1d9")).
		Width(m.width - 4)

	inputContent := m.inputBuf
	if inputContent == "" {
		inputContent = "Type a message..."
	}
	inputBox := inputStyle.Render(inputContent)

	// Status bar
	statusStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#484f58")).
		Width(m.width)
	statusBar := statusStyle.Render(m.status)

	return lipgloss.JoinVertical(lipgloss.Left,
		header,
		chatPane,
		inputBox,
		statusBar,
	)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/tui/ -run TestChatModel -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/tui/chatmodel.go internal/tui/chatmodel_test.go
git commit -m "feat: add ChatModel with Bubble Tea message rendering"
```

---

## Chunk 2: Event Integration and Input Handling

### Task 4: Wire LLM events into ChatModel

**Files:**
- Modify: `internal/tui/chatmodel.go`
- Test: `internal/tui/chatmodel_test.go`

The ChatModel needs to receive LLM events (tokens, tool calls, completion) and update the message list.

- [ ] **Step 1: Write the failing test**

```go
func TestChatModelHandlesTokenEvent(t *testing.T) {
	m := NewChatModel(ChatLiveConfig{Model: "test", WorkDir: "/tmp"})
	m.width = 80
	m.height = 24

	// Simulate token event
	ev := llm.Event{Kind: llm.EventToken, Text: "Hello "}
	updated, _ := m.Update(ev)
	m = updated.(ChatModel)

	ev2 := llm.Event{Kind: llm.EventToken, Text: "world"}
	updated, _ = m.Update(ev2)
	m = updated.(ChatModel)

	if len(m.messages) != 1 {
		t.Fatalf("expected 1 agent message, got %d", len(m.messages))
	}
	if m.messages[0].Content != "Hello world" {
		t.Fatalf("content = %q, want %q", m.messages[0].Content, "Hello world")
	}
}

func TestChatModelHandlesDoneEvent(t *testing.T) {
	m := NewChatModel(ChatLiveConfig{Model: "test", WorkDir: "/tmp"})
	m.width = 80
	m.height = 24
	m.busy = true

	ev := llm.Event{Kind: llm.EventDone}
	updated, _ := m.Update(ev)
	m = updated.(ChatModel)

	if m.busy {
		t.Fatal("expected busy=false after done event")
	}
	// Should have a status message
	found := false
	for _, msg := range m.messages {
		if msg.Kind == MsgStatus {
			found = true
		}
	}
	if !found {
		t.Fatal("expected status message after done")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/tui/ -run "TestChatModelHandles" -v`
Expected: FAIL

- [ ] **Step 3: Add event handling to Update**

Add the following case to `ChatModel.Update()`:

```go
case llm.Event:
	return m.handleLLMEvent(msg)
```

And the handler method:

```go
func (m ChatModel) handleLLMEvent(ev llm.Event) (tea.Model, tea.Cmd) {
	switch ev.Kind {
	case llm.EventToken:
		m.AppendToLastAgent(ev.Text)
	case llm.EventDone:
		m.busy = false
		m.status = "ready"
		stamp := time.Now().Format("15:04:05")
		m.AddMessage(ChatMessage{
			Kind:    MsgStatus,
			Content: "Agent complete • " + stamp,
		})
	case llm.EventError:
		m.busy = false
		m.status = "error"
		errMsg := "unknown error"
		if ev.Err != nil {
			errMsg = ev.Err.Error()
		}
		m.AddMessage(ChatMessage{
			Kind:    MsgStatus,
			Content: "Error: " + errMsg,
		})
	}
	return m, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/tui/ -run "TestChatModelHandles" -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/tui/chatmodel.go internal/tui/chatmodel_test.go
git commit -m "feat: wire LLM events into ChatModel"
```

---

### Task 5: Add slash command handling

**Files:**
- Modify: `internal/tui/chatmodel.go`
- Test: `internal/tui/chatmodel_test.go`

Slash commands (`/help`, `/clear`, `/model`, `/skills`, etc.) need to work in the new model. Port the essential commands from `chatlive_commands.go`.

- [ ] **Step 1: Write the failing test**

```go
func TestChatModelSlashClear(t *testing.T) {
	m := NewChatModel(ChatLiveConfig{Model: "test", WorkDir: "/tmp"})
	m.width = 80
	m.height = 24
	m.AddMessage(ChatMessage{Kind: MsgUser, Header: "You", Content: "hello"})
	m.AddMessage(ChatMessage{Kind: MsgAgent, Content: "hi"})

	m.inputBuf = "/clear"
	m.inputPos = 6
	updated, _ := m.submitInput()
	m = updated.(ChatModel)

	if len(m.messages) != 0 {
		t.Fatalf("expected 0 messages after /clear, got %d", len(m.messages))
	}
}

func TestChatModelSlashExit(t *testing.T) {
	m := NewChatModel(ChatLiveConfig{Model: "test", WorkDir: "/tmp"})
	m.inputBuf = "/exit"
	m.inputPos = 5
	_, cmd := m.submitInput()
	if cmd == nil {
		t.Fatal("expected quit command from /exit")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/tui/ -run "TestChatModelSlash" -v`
Expected: FAIL

- [ ] **Step 3: Add slash command dispatch to submitInput**

Update `submitInput()` to check for `/` prefix and dispatch:

```go
func (m ChatModel) submitInput() (tea.Model, tea.Cmd) {
	input := strings.TrimSpace(m.inputBuf)
	if input == "" {
		return m, nil
	}

	if input == "/exit" || input == "/quit" {
		return m, tea.Quit
	}

	if strings.HasPrefix(input, "/") {
		return m.handleSlashCommand(input)
	}

	// Regular message
	stamp := time.Now().Format("15:04:05")
	m.AddMessage(ChatMessage{
		Kind:    MsgUser,
		Header:  "You • " + stamp,
		Content: input,
	})
	m.inputBuf = ""
	m.inputPos = 0
	m.busy = true
	m.status = "running"

	if m.inputCh != nil {
		m.inputCh <- input
	}
	return m, nil
}

func (m ChatModel) handleSlashCommand(input string) (tea.Model, tea.Cmd) {
	m.inputBuf = ""
	m.inputPos = 0

	switch {
	case input == "/clear":
		m.messages = nil
		m.refreshViewport()
		m.flash = "conversation cleared"
	case input == "/help":
		m.flash = "help: /clear /exit /model /skills /theme"
	case input == "/theme":
		m.lowContrast = !m.lowContrast
		m.refreshViewport()
		if m.lowContrast {
			m.flash = "theme: low contrast"
		} else {
			m.flash = "theme: default"
		}
	case input == "/skills":
		var sb strings.Builder
		sb.WriteString("Skills:\n")
		for _, s := range m.skills {
			marker := "○"
			if m.state != nil && m.state.SkillActivated(s.Name) {
				marker = "●"
			}
			sb.WriteString("  " + marker + " /" + s.Name + " — " + s.Description + "\n")
		}
		m.flash = sb.String()
	default:
		m.flash = "unknown command: " + input
	}
	return m, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/tui/ -run "TestChatModelSlash" -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/tui/chatmodel.go internal/tui/chatmodel_test.go
git commit -m "feat: add slash command handling to ChatModel"
```

---

### Task 6: Add tool approval flow

**Files:**
- Modify: `internal/tui/chatmodel.go`
- Test: `internal/tui/chatmodel_test.go`

When the agent wants to run a tool, an approval request arrives on `approvalCh`. The ChatModel needs to display the request and let the user approve/deny.

- [ ] **Step 1: Write the failing test**

```go
func TestChatModelApprovalFlow(t *testing.T) {
	m := NewChatModel(ChatLiveConfig{Model: "test", WorkDir: "/tmp"})
	m.width = 80
	m.height = 24

	// Simulate approval request
	m.pendingApproval = &tools.Action{Tool: "write_file", Input: `{"path":"test.go"}`}

	v := m.View()
	if !strings.Contains(v, "write_file") {
		t.Fatalf("view should show pending approval, got: %s", v)
	}

	// Approve
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
	m = updated.(ChatModel)
	if m.pendingApproval != nil {
		t.Fatal("approval should be cleared after y")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/tui/ -run "TestChatModelApproval" -v`
Expected: FAIL

- [ ] **Step 3: Add approval handling**

Add `pendingApproval *tools.Action` field to `ChatModel` and handle it in `Update` and `View`.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/tui/ -run "TestChatModelApproval" -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/tui/chatmodel.go internal/tui/chatmodel_test.go
git commit -m "feat: add tool approval flow to ChatModel"
```

---

## Chunk 3: Tools Pane and Split Layout

### Task 7: Add tools pane with split view

**Files:**
- Modify: `internal/tui/chatmodel.go`
- Test: `internal/tui/chatmodel_test.go`

The chat needs a split view: left pane for conversation, right pane for tool calls/results.

- [ ] **Step 1: Write the failing test**

```go
func TestChatModelToolsPaneVisible(t *testing.T) {
	m := NewChatModel(ChatLiveConfig{Model: "test", WorkDir: "/tmp"})
	m.width = 120
	m.height = 24
	m.toolsVisible = true
	m.toolsBuf = "● read_file {\"path\":\"main.go\"}\nstatus: ok\n"

	v := m.View()
	if !strings.Contains(v, "read_file") {
		t.Fatal("tools pane should show tool calls")
	}
}

func TestChatModelToolsPaneToggle(t *testing.T) {
	m := NewChatModel(ChatLiveConfig{Model: "test", WorkDir: "/tmp"})
	m.width = 120
	m.height = 24
	m.toolsVisible = true

	m.inputBuf = "/toggle tools"
	m.inputPos = len("/toggle tools")
	updated, _ := m.submitInput()
	m = updated.(ChatModel)

	if m.toolsVisible {
		t.Fatal("tools pane should be hidden after toggle")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/tui/ -run "TestChatModelTools" -v`
Expected: FAIL

- [ ] **Step 3: Add tools viewport and split rendering**

Add a second `viewport.Model` for tools pane. Update `View()` to render side-by-side using `lipgloss.JoinHorizontal`. Add `/toggle tools` command.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/tui/ -run "TestChatModelTools" -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/tui/chatmodel.go internal/tui/chatmodel_test.go
git commit -m "feat: add tools pane with split view to ChatModel"
```

---

## Chunk 4: Wire into Runtime and Replace Old Code

### Task 8: Create RunChatLiveBubbleTea entry point

**Files:**
- Create: `internal/tui/chatlive_bubbletea.go`
- Test: `internal/tui/chatlive_bubbletea_test.go`

Bridge function that creates a `ChatModel`, starts the Bubble Tea program, and returns a `ChatLiveResult` — same signature as the old `RunChatLive`.

- [ ] **Step 1: Write the failing test**

```go
func TestRunChatLiveBubbleTeaReturnsResult(t *testing.T) {
	// This is an integration-level test — just verify the function exists
	// and accepts the right parameters
	_ = RunChatLiveBubbleTea
}
```

- [ ] **Step 2: Write the bridge function**

```go
// internal/tui/chatlive_bubbletea.go
package tui

import (
	"forge/internal/llm"

	tea "github.com/charmbracelet/bubbletea"
)

// RunChatLiveBubbleTea runs the chat interface using Bubble Tea.
// It has the same signature as RunChatLive for drop-in replacement.
func RunChatLiveBubbleTea(events <-chan llm.Event, cfg ChatLiveConfig, inputCh chan<- string, doneCh chan struct{}) ChatLiveResult {
	m := NewChatModel(cfg)
	m.inputCh = inputCh

	// Listen for LLM events as tea.Msg
	eventCmd := func() tea.Msg {
		ev, ok := <-events
		if !ok {
			return llm.Event{Kind: llm.EventDone}
		}
		return ev
	}

	// Listen for approval requests
	var approvalCmd tea.Cmd
	if cfg.ApprovalCh != nil {
		approvalCmd = func() tea.Msg {
			action, ok := <-cfg.ApprovalCh
			if !ok {
				return nil
			}
			return chatApprovalMsg(action)
		}
	}

	p := tea.NewProgram(m,
		tea.WithAltScreen(),
		tea.WithMouseCellMotion(),
	)

	// Feed events into the program
	go func() {
		for {
			msg := eventCmd()
			p.Send(msg)
			if ev, ok := msg.(llm.Event); ok && (ev.Kind == llm.EventDone || ev.Kind == llm.EventAbort) {
				break
			}
		}
	}()

	// Feed approval requests
	if approvalCmd != nil {
		go func() {
			for action := range cfg.ApprovalCh {
				p.Send(chatApprovalMsg(action))
			}
		}()
	}

	finalModel, _ := p.Run()
	fm := finalModel.(ChatModel)

	close(doneCh)

	return ChatLiveResult{
		Aborted: fm.status == "aborted",
	}
}

type chatApprovalMsg tools.Action
```

- [ ] **Step 3: Commit**

```bash
git add internal/tui/chatlive_bubbletea.go internal/tui/chatlive_bubbletea_test.go
git commit -m "feat: add RunChatLiveBubbleTea bridge function"
```

---

### Task 9: Switch runtime to use new ChatModel

**Files:**
- Modify: `internal/runtime/chat.go`

- [ ] **Step 1: Update RunChatLive in runtime to call RunChatLiveBubbleTea**

In `internal/runtime/chat.go`, change the call from `tui.RunChatLive(...)` to `tui.RunChatLiveBubbleTea(...)`.

- [ ] **Step 2: Build and verify**

Run: `go build ./...`
Expected: Clean build

- [ ] **Step 3: Manual smoke test**

Run: `go run ./cmd/forge chat --live`
Expected: Chat UI appears with box-style messages, input works, agent responds

- [ ] **Step 4: Commit**

```bash
git add internal/runtime/chat.go
git commit -m "feat: switch runtime to Bubble Tea chat UI"
```

---

### Task 10: Port remaining features

**Files:**
- Modify: `internal/tui/chatmodel.go`

Port these features from the old `chatLiveModel` one at a time:

- [ ] **Step 1: Session save/restore** — Port `autoSaveSession` and `/save`/`/restore` commands
- [ ] **Step 2: Model picker** — Port `/models` overlay
- [ ] **Step 3: Search** — Port `/find` search with highlighting
- [ ] **Step 4: File picker** — Port `@` file completion
- [ ] **Step 5: Help overlay** — Port `/help` multi-tab overlay
- [ ] **Step 6: Skill activation** — Port skill commands and auto-activation
- [ ] **Step 7: Code syntax highlighting** — Port Chroma-based highlighting for code blocks in messages
- [ ] **Step 8: Mouse support** — Port scroll, selection, divider drag
- [ ] **Step 9: Copilot quota display** — Port quota fetching and display

Each of these should be a separate commit.

---

### Task 11: Remove old tcell chat code

**Files:**
- Delete: `internal/tui/chatlive.go`
- Delete: `internal/tui/chatlive_commands.go`
- Delete: `internal/tui/chatlive_mouse.go`
- Delete: `internal/tui/chatlive_overlays.go`
- Delete: `internal/tui/chatlive_render.go`
- Delete: `internal/tui/chatlive_render_overlays.go`
- Delete: `internal/tui/live.go`
- Delete associated test files

- [ ] **Step 1: Remove old files**

```bash
git rm internal/tui/chatlive.go internal/tui/chatlive_commands.go \
  internal/tui/chatlive_mouse.go internal/tui/chatlive_overlays.go \
  internal/tui/chatlive_render.go internal/tui/chatlive_render_overlays.go \
  internal/tui/live.go
git rm internal/tui/chatlive_test.go internal/tui/chatlive_skills_test.go \
  internal/tui/chatlive_render_skills_test.go internal/tui/chatlive_mouse_test.go
```

- [ ] **Step 2: Remove tcell dependency from go.mod**

```bash
go mod tidy
```

- [ ] **Step 3: Build and test**

Run: `go build ./... && go test ./...`
Expected: All pass

- [ ] **Step 4: Commit**

```bash
git add -A
git commit -m "refactor: remove old tcell chat UI code"
```
