package tui

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"forge/internal/agent/tools"
	"forge/internal/chatstate"
	"forge/internal/llm"
	"forge/internal/skills"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type chatTickMsg time.Time
type chatApprovalMsg tools.Action

// ChatModel is the Bubble Tea model for the interactive chat screen.
type chatPaneFocus int

const (
	focusChat chatPaneFocus = iota
	focusTools
)

type ChatModel struct {
	config  ChatLiveConfig
	model   string
	workDir string

	messages []ChatMessage

	inputBuf string
	inputPos int

	width  int
	height int

	chatViewport viewport.Model
	chatContent  string
	paneFocus    chatPaneFocus
	toolsScroll  int

	toolsBuf        string
	toolsVisible    bool
	toolsWasShowing bool

	busy           bool
	viewportDirty  bool
	spinnerFrame   int
	status         string
	flash          string
	skills         []skills.Skill
	autoSkillsMode string
	state          *chatstate.State
	lowContrast    bool

	helpVisible bool
	helpTab     int
	helpScroll  int

	sessionsVisible bool
	sessionsCursor  int
	sessionsList    []chatSessionEntry

	pendingApproval *tools.Action
	inputCh         chan<- string
	responseCh      chan<- bool
}

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
		paneFocus:      focusChat,
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

func (m *ChatModel) AddMessage(msg ChatMessage) {
	m.messages = append(m.messages, msg)
	m.refreshViewport()
}

func (m *ChatModel) AppendToLastAgent(text string) {
	if len(m.messages) == 0 || m.messages[len(m.messages)-1].Kind != MsgAgent {
		stamp := time.Now().Format("15:04:05")
		m.messages = append(m.messages, ChatMessage{Kind: MsgAgent, Header: "Agent • " + stamp})
	}
	m.messages[len(m.messages)-1].Content += text
	m.viewportDirty = true
}

func (m *ChatModel) refreshViewport() {
	contentWidth := m.chatContentWidth()
	if contentWidth < 10 {
		contentWidth = 60
	}

	var blocks []string
	for _, msg := range m.messages {
		blocks = append(blocks, msg.Render(contentWidth, m.lowContrast))
	}
	content := strings.Join(blocks, "\n")
	m.chatContent = content
	m.chatViewport.SetContent(content)
	m.chatViewport.GotoBottom()
}

func (m ChatModel) chatPaneWidth() int {
	if !m.toolsVisible || m.toolsBuf == "" {
		return m.width
	}
	return max(20, m.width*7/10)
}

func (m ChatModel) chatContentWidth() int {
	paneWidth := m.chatPaneWidth()
	innerWidth := max(1, paneWidth-2)
	return max(10, innerWidth-1)
}

type chatLayoutMouseContext struct {
	chatX, chatY, chatW, chatH     int
	toolsX, toolsY, toolsW, toolsH int
	inputY                         int
	inChat, inTools                bool
	inChatScrollbar                bool
	inToolsScrollbar               bool
}

func (m ChatModel) mouseContext() chatLayoutMouseContext {
	headerH := 1
	chatPaneWidth := m.chatPaneWidth()
	chatBodyHeight := max(1, m.chatViewport.Height)
	chatH := chatBodyHeight + 2
	ctx := chatLayoutMouseContext{
		chatX:  0,
		chatY:  headerH,
		chatW:  chatPaneWidth,
		chatH:  chatH,
		inputY: headerH + chatH,
	}
	if m.toolsVisible && m.toolsBuf != "" {
		ctx.toolsX = chatPaneWidth
		ctx.toolsY = headerH
		ctx.toolsW = max(0, m.width-chatPaneWidth)
		ctx.toolsH = chatH
	}
	return ctx
}

func (m ChatModel) toolsWrappedLines() []string {
	if strings.TrimSpace(m.toolsBuf) == "" {
		return nil
	}
	toolsWidth := m.width - m.chatPaneWidth()
	toolsInnerWidth := max(1, toolsWidth-2)
	toolsContentWidth := max(1, toolsInnerWidth-1)
	wrappedTools := lipgloss.NewStyle().Width(toolsContentWidth).Render(m.toolsBuf)
	return strings.Split(wrappedTools, "\n")
}

func (m ChatModel) toolsMaxScroll() int {
	return max(0, len(m.toolsWrappedLines())-max(1, m.chatViewport.Height))
}

func (m ChatModel) handleMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	ctx := m.mouseContext()
	x, y := msg.X, msg.Y
	ctx.inChat = x >= ctx.chatX && x < ctx.chatX+ctx.chatW && y >= ctx.chatY && y < ctx.chatY+ctx.chatH
	ctx.inTools = ctx.toolsW > 0 && x >= ctx.toolsX && x < ctx.toolsX+ctx.toolsW && y >= ctx.toolsY && y < ctx.toolsY+ctx.toolsH
	chatScrollbarX := ctx.chatX + max(1, ctx.chatW-2)
	toolsScrollbarX := ctx.toolsX + max(1, ctx.toolsW-2)
	ctx.inChatScrollbar = ctx.inChat && x == chatScrollbarX && y > ctx.chatY && y < ctx.chatY+ctx.chatH-1
	ctx.inToolsScrollbar = ctx.inTools && x == toolsScrollbarX && y > ctx.toolsY && y < ctx.toolsY+ctx.toolsH-1

	if tea.MouseEvent(msg).IsWheel() {
		switch msg.Button {
		case tea.MouseButtonWheelUp:
			if ctx.inTools {
				m.paneFocus = focusTools
				m.toolsScroll = max(0, m.toolsScroll-3)
				return m, nil
			}
			m.paneFocus = focusChat
			m.chatViewport.ScrollUp(3)
			return m, nil
		case tea.MouseButtonWheelDown:
			if ctx.inTools {
				m.paneFocus = focusTools
				m.toolsScroll = min(m.toolsMaxScroll(), m.toolsScroll+3)
				return m, nil
			}
			m.paneFocus = focusChat
			m.chatViewport.ScrollDown(3)
			return m, nil
		}
	}

	if msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonLeft {
		switch {
		case ctx.inChatScrollbar:
			m.paneFocus = focusChat
			total := len(strings.Split(m.chatContent, "\n"))
			visible := max(1, m.chatViewport.Height)
			thumbTop, thumbH := scrollbarThumb(ctx.chatY+2, max(1, visible-2), total, visible, m.chatViewport.YOffset)
			switch {
			case y == ctx.chatY+1:
				m.chatViewport.ScrollUp(1)
			case y == ctx.chatY+ctx.chatH-2:
				m.chatViewport.ScrollDown(1)
			case y >= thumbTop && y < thumbTop+thumbH:
				// thumb click focuses pane; drag can be added later
			case y < thumbTop:
				m.chatViewport.ScrollUp(max(1, visible-1))
			default:
				m.chatViewport.ScrollDown(max(1, visible-1))
			}
			return m, nil
		case ctx.inToolsScrollbar:
			m.paneFocus = focusTools
			total := len(m.toolsWrappedLines())
			visible := max(1, m.chatViewport.Height)
			thumbTop, thumbH := scrollbarThumb(ctx.toolsY+2, max(1, visible-2), total, visible, m.toolsScroll)
			switch {
			case y == ctx.toolsY+1:
				m.toolsScroll = max(0, m.toolsScroll-1)
			case y == ctx.toolsY+ctx.toolsH-2:
				m.toolsScroll = min(m.toolsMaxScroll(), m.toolsScroll+1)
			case y >= thumbTop && y < thumbTop+thumbH:
				// thumb click focuses pane; drag can be added later
			case y < thumbTop:
				m.toolsScroll = max(0, m.toolsScroll-(visible-1))
			default:
				m.toolsScroll = min(m.toolsMaxScroll(), m.toolsScroll+(visible-1))
			}
			return m, nil
		case ctx.inTools:
			m.paneFocus = focusTools
			return m, nil
		case ctx.inChat:
			m.paneFocus = focusChat
			return m, nil
		}
	}

	var cmd tea.Cmd
	if m.paneFocus != focusTools {
		m.chatViewport, cmd = m.chatViewport.Update(msg)
	}
	return m, cmd
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
		if m.busy {
			m.spinnerFrame = (m.spinnerFrame + 1) % 8
		}
		// Detect tools pane appearing/disappearing and resize chat viewport
		toolsShowing := m.toolsVisible && m.toolsBuf != ""
		if toolsShowing != m.toolsWasShowing {
			m.toolsWasShowing = toolsShowing
			m.chatViewport.Width = m.chatPaneWidth()
			m.viewportDirty = true
		}
		if m.viewportDirty {
			m.refreshViewport()
			m.viewportDirty = false
		}
		return m, tickCmd()

	case llm.Event:
		return m.handleLLMEvent(msg)

	case chatApprovalMsg:
		action := tools.Action(msg)
		m.pendingApproval = &action
		return m, nil

	case tea.KeyMsg:
		return m.handleKey(msg)

	case tea.MouseMsg:
		return m.handleMouse(msg)
	}

	var cmd tea.Cmd
	m.chatViewport, cmd = m.chatViewport.Update(msg)
	return m, cmd
}

func (m ChatModel) handleLLMEvent(ev llm.Event) (tea.Model, tea.Cmd) {
	switch ev.Kind {
	case llm.EventToken:
		m.AppendToLastAgent(ev.Text)
	case llm.EventToolCall:
		if m.toolsBuf != "" && !strings.HasSuffix(m.toolsBuf, "\n\n") {
			m.toolsBuf += "\n"
		}
		m.toolsBuf += "────────────────────────\n"
		m.toolsBuf += fmt.Sprintf("● %s\n", ev.Agent)
		m.toolsBuf += fmt.Sprintf("  %s\n", ev.Text)
	case llm.EventToolResult:
		if ev.IsError {
			m.toolsBuf += fmt.Sprintf("  status: ✗ %s\n", ev.Text)
		} else if ev.Content != "" {
			m.toolsBuf += fmt.Sprintf("  status: ✓\n  %s\n", truncate(ev.Content, 200))
		} else {
			m.toolsBuf += fmt.Sprintf("  status: ✓ %s\n", truncate(ev.Text, 200))
		}
	case llm.EventRoundStart:
		m.toolsBuf += fmt.Sprintf("\n── round %d ──\n", ev.Round)
	case llm.EventDone:
		m.busy = false
		m.status = "ready"
		stamp := time.Now().Format("15:04:05")
		m.AddMessage(ChatMessage{
			Kind:    MsgStatus,
			Content: "Agent complete • " + stamp,
		})
		if strings.TrimSpace(m.toolsBuf) != "" {
			m.toolsBuf += fmt.Sprintf("status: complete • %s\n", stamp)
		}
	case llm.EventError:
		m.busy = false
		m.status = "error"
		errMsg := "unknown error"
		if ev.Err != nil {
			errMsg = ev.Err.Error()
		}
		m.toolsBuf += fmt.Sprintf("  ✗ %s\n", errMsg)
		m.AddMessage(ChatMessage{
			Kind:    MsgStatus,
			Content: "Error: " + errMsg,
		})
	case llm.EventStats:
		if ev.Duration > 0 {
			m.toolsBuf += fmt.Sprintf("  %.1fs", ev.Duration.Seconds())
			if ev.Usage.InputTokens > 0 {
				m.toolsBuf += fmt.Sprintf(" • %d in / %d out", ev.Usage.InputTokens, ev.Usage.OutputTokens)
			}
			m.toolsBuf += "\n"
		}
	}
	return m, nil
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "…"
}

func (m ChatModel) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.helpVisible {
		return m.handleHelpKey(msg)
	}
	if m.sessionsVisible {
		return m.handleSessionsKey(msg)
	}

	// Handle approval mode
	if m.pendingApproval != nil {
		switch {
		case msg.Type == tea.KeyRunes && string(msg.Runes) == "y":
			m.pendingApproval = nil
			if m.responseCh != nil {
				ch := m.responseCh
				return m, func() tea.Msg {
					ch <- true
					return nil
				}
			}
		case msg.Type == tea.KeyRunes && string(msg.Runes) == "n":
			m.pendingApproval = nil
			if m.responseCh != nil {
				ch := m.responseCh
				return m, func() tea.Msg {
					ch <- false
					return nil
				}
			}
		case msg.Type == tea.KeyCtrlC:
			return m, tea.Quit
		}
		return m, nil
	}

	switch msg.Type {
	case tea.KeyCtrlC:
		return m, tea.Quit
	case tea.KeyEscape:
		if m.busy && m.inputCh != nil {
			ch := m.inputCh
			m.flash = "canceling..."
			return m, func() tea.Msg {
				ch <- "__cancel_turn__"
				return nil
			}
		}
		return m, nil
	case tea.KeyEnter:
		m.flash = ""
		return m.submitInput()
	case tea.KeyBackspace:
		if len(m.inputBuf) > 0 && m.inputPos > 0 {
			runes := []rune(m.inputBuf)
			m.inputBuf = string(append(runes[:m.inputPos-1], runes[m.inputPos:]...))
			m.inputPos--
		}
	case tea.KeyLeft:
		if m.inputPos > 0 {
			m.inputPos--
		}
	case tea.KeyRight:
		if m.inputPos < len([]rune(m.inputBuf)) {
			m.inputPos++
		}
	case tea.KeyHome:
		m.inputPos = 0
	case tea.KeyEnd:
		m.inputPos = len([]rune(m.inputBuf))
	case tea.KeyPgUp:
		m.chatViewport.HalfViewUp()
	case tea.KeyPgDown:
		m.chatViewport.HalfViewDown()
	case tea.KeyF1:
		m.helpVisible = true
		m.helpTab = 0
		m.helpScroll = 0
		m.flash = ""
		return m, nil
	case tea.KeyTab:
		m.toolsVisible = !m.toolsVisible
		m.chatViewport.Width = m.chatPaneWidth()
		m.refreshViewport()
	case tea.KeySpace:
		m.flash = ""
		runes := []rune(m.inputBuf)
		newRunes := make([]rune, 0, len(runes)+1)
		newRunes = append(newRunes, runes[:m.inputPos]...)
		newRunes = append(newRunes, ' ')
		newRunes = append(newRunes, runes[m.inputPos:]...)
		m.inputBuf = string(newRunes)
		m.inputPos++
	case tea.KeyRunes:
		m.flash = ""
		for _, r := range msg.Runes {
			runes := []rune(m.inputBuf)
			newRunes := make([]rune, 0, len(runes)+1)
			newRunes = append(newRunes, runes[:m.inputPos]...)
			newRunes = append(newRunes, r)
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

	if input == "/exit" || input == "/quit" {
		return m, tea.Quit
	}

	if strings.HasPrefix(input, "/") {
		cmd := strings.TrimPrefix(input, "/")
		// Built-in commands first, then skill activation
		if m.isBuiltinCommand(input) {
			return m.handleSlashCommand(input)
		}
		if s, ok := skills.Get(m.skills, cmd); ok {
			return m.submitSkillInput(s, fmt.Sprintf("/%s", s.Name), skills.SkillMessage(s))
		}
		return m.handleSlashCommand(input) // falls through to "unknown command"
	}

	// Auto-skill detection
	if !m.busy {
		switch m.autoSkillsMode {
		case skills.AutoSkillsAuto:
			if s, ok := skills.DetectAuto(m.skills, input); ok {
				return m.submitSkillInput(s, input, skills.SkillMessageWithUserInput(s, input))
			}
		case "", skills.AutoSkillsSuggest:
			if s, ok := skills.DetectAuto(m.skills, input); ok {
				m.flash = fmt.Sprintf("suggested skill: /%s", s.Name)
			}
		}
	}

	// Required skill check
	requiredSkill := skills.RequiredForInput(input)
	if requiredSkill != "" && !m.state.SkillActivated(requiredSkill) && skills.NormalizeAutoMode(m.autoSkillsMode) != skills.AutoSkillsSuggest {
		if _, ok := skills.Get(m.skills, requiredSkill); ok {
			m.flash = fmt.Sprintf("required skill: /%s", requiredSkill)
			return m, nil
		}
	}

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
		ch := m.inputCh
		return m, func() tea.Msg {
			ch <- input
			return nil
		}
	}

	return m, nil
}

func (m ChatModel) submitSkillInput(s skills.Skill, turnLabel, msg string) (tea.Model, tea.Cmd) {
	if m.state != nil {
		m.state.ActivateSkill(s.Name)
	}
	stamp := time.Now().Format("15:04:05")
	m.AddMessage(ChatMessage{
		Kind:    MsgForge,
		Header:  "Forge • " + stamp,
		Content: turnLabel,
	})

	m.inputBuf = ""
	m.inputPos = 0
	m.flash = fmt.Sprintf("skill: %s", s.Name)
	m.busy = true
	m.status = "running"

	if m.inputCh != nil {
		ch := m.inputCh
		return m, func() tea.Msg {
			ch <- msg
			return nil
		}
	}
	return m, nil
}

var builtinCommands = []string{"/clear", "/help", "/theme", "/tools", "/models", "/model", "/skills", "/exit", "/quit"}

func (m ChatModel) isBuiltinCommand(input string) bool {
	for _, cmd := range builtinCommands {
		if input == cmd || strings.HasPrefix(input, cmd+" ") {
			return true
		}
	}
	return false
}

func (m ChatModel) handleSlashCommand(input string) (tea.Model, tea.Cmd) {
	m.inputBuf = ""
	m.inputPos = 0

	switch {
	case input == "/clear":
		m.messages = nil
		m.toolsBuf = ""
		m.refreshViewport()
		m.flash = "conversation cleared"
	case input == "/help":
		m.helpVisible = true
		m.helpTab = 0
		m.helpScroll = 0
		m.flash = "help opened"
	case input == "/theme":
		m.lowContrast = !m.lowContrast
		m.refreshViewport()
		if m.lowContrast {
			m.flash = "theme: low contrast"
		} else {
			m.flash = "theme: default"
		}
	case input == "/sessions":
		if ok := m.refreshSessionsPicker(true); ok {
			m.sessionsVisible = true
			m.flash = "sessions opened"
		}
	case input == "/save":
		name, err := defaultChatSessionName()
		if err != nil {
			m.flash = fmt.Sprintf("save failed: %v", err)
			break
		}
		if err := m.saveSession(name); err != nil {
			m.flash = fmt.Sprintf("save failed: %v", err)
		} else {
			m.flash = fmt.Sprintf("session saved: %s", name)
		}
	case strings.HasPrefix(input, "/save "):
		name := sanitizeChatSessionName(strings.TrimSpace(strings.TrimPrefix(input, "/save ")))
		if name == "" {
			m.flash = "save failed: missing session name"
			break
		}
		if err := m.saveSession(name); err != nil {
			m.flash = fmt.Sprintf("save failed: %v", err)
		} else {
			m.flash = fmt.Sprintf("session saved: %s", name)
		}
	case input == "/restore":
		name, err := latestChatSessionName()
		if err != nil {
			m.flash = fmt.Sprintf("restore failed: %v", err)
			break
		}
		if err := m.restoreSession(name); err != nil {
			m.flash = fmt.Sprintf("restore failed: %v", err)
		} else {
			m.flash = fmt.Sprintf("session restored: %s", name)
		}
	case strings.HasPrefix(input, "/restore "):
		name := sanitizeChatSessionName(strings.TrimSpace(strings.TrimPrefix(input, "/restore ")))
		if name == "" {
			m.flash = "restore failed: missing session name"
			break
		}
		if err := m.restoreSession(name); err != nil {
			m.flash = fmt.Sprintf("restore failed: %v", err)
		} else {
			m.flash = fmt.Sprintf("session restored: %s", name)
		}
	case input == "/tools":
		m.toolsVisible = !m.toolsVisible
		m.refreshViewport()
		if m.toolsVisible {
			m.flash = "tools pane: visible"
		} else {
			m.flash = "tools pane: hidden"
		}
	case input == "/models" || input == "/model":
		var sb strings.Builder
		sb.WriteString("Models:\n")
		for _, name := range m.config.AvailableModels {
			marker := "  "
			if name == m.model {
				marker = "● "
			}
			sb.WriteString(marker + name + "\n")
		}
		sb.WriteString("\nUse /model <name> to switch")
		m.AddMessage(ChatMessage{Kind: MsgStatus, Content: sb.String()})
	case strings.HasPrefix(input, "/model "):
		arg := strings.TrimSpace(strings.TrimPrefix(input, "/model "))
		if arg != "" && m.config.SwitchModel != nil {
			newModel, err := m.config.SwitchModel(arg)
			if err != nil {
				m.flash = fmt.Sprintf("error: %v", err)
			} else {
				m.model = newModel
				m.flash = fmt.Sprintf("switched to %s", newModel)
			}
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
		m.AddMessage(ChatMessage{Kind: MsgStatus, Content: sb.String()})
	default:
		m.flash = "unknown command: " + input
	}
	return m, nil
}

func (m ChatModel) helpTabs() []string {
	return []string{"Keys", "Chat Commands", "CLI Skills"}
}

func (m ChatModel) helpLines() []string {
	switch m.helpTab {
	case 1:
		return []string{
			"Chat commands",
			"",
			"Discovery and navigation:",
			"  /help              open this help overlay",
			"  /models            list available models",
			"  /model             list available models",
			"  /model <name>      switch to a model",
			"",
			"Session state:",
			"  /sessions          open saved sessions picker",
			"  /save [name]       save the current session",
			"  /restore [name]    restore a saved session",
			"  /skills            list loaded skills",
			"",
			"Layout and display:",
			"  /tools             show / hide tools pane",
			"  /theme             toggle low-contrast theme",
			"",
			"Cleanup:",
			"  /clear             clear conversation and tools pane",
			"  /exit              leave live mode",
			"  /quit              leave live mode",
		}
	case 2:
		return []string{
			"CLI skills",
			"",
			"In chat:",
			"  /skills            list available skills and activation names",
			"  /<skill>           activate a loaded skill by name",
			"                     example: /skills, then /tdd",
			"",
			"Skill locations:",
			"  project: ./.forge/skills/",
			"  global:  ~/.config/forge/skills/",
			"",
			"CLI help:",
			"  forge help",
			"  forge help skills",
			"",
			"Inspect skills:",
			"  forge skills list",
			"  forge skills dir",
			"  forge skills status",
			"  forge skills search <query>",
			"  forge skills remove <name>",
			"",
			"Install/update skills:",
			"  forge skills install [--scope global|project] <source>",
			"  forge skills install [--scope global|project] --git <repo-url> [--subdir <path>]",
			"  forge skills install [--scope global|project] superpowers [skill-name ...]",
			"  forge skills update superpowers [--scope global|project]",
		}
	default:
		return []string{
			"Keyboard shortcuts",
			"",
			"Help navigation:",
			"  F1                 open help",
			"  ← / →              switch help tab",
			"  ↑ / ↓              scroll help",
			"  PgUp / PgDn        page help",
			"  Home / End         jump to top / bottom",
			"  Esc / Enter / ?    close help",
			"",
			"Prompt editing:",
			"  Enter              send current prompt",
			"  ← / →              move prompt cursor",
			"  Home / End         jump to start / end",
			"  Backspace          delete previous character",
			"",
			"Pane navigation:",
			"  PgUp / PgDn        scroll conversation",
			"  Tab                toggle tools pane",
			"  Mouse wheel        scroll conversation",
			"",
			"Turn control:",
			"  Esc                cancel current run",
		}
	}
}

func (m *ChatModel) refreshSessionsPicker(resetCursor bool) bool {
	sessions, err := listChatSessions()
	if err != nil {
		m.flash = fmt.Sprintf("sessions failed: %v", err)
		return false
	}
	if len(sessions) == 0 {
		m.sessionsList = nil
		m.sessionsVisible = false
		m.sessionsCursor = 0
		m.flash = "no saved sessions"
		return false
	}
	currentName := ""
	if !resetCursor && m.sessionsCursor >= 0 && m.sessionsCursor < len(m.sessionsList) {
		currentName = m.sessionsList[m.sessionsCursor].name
	}
	m.sessionsList = sessions
	m.sessionsVisible = true
	if resetCursor {
		m.sessionsCursor = 0
		return true
	}
	for i, entry := range sessions {
		if entry.name == currentName {
			m.sessionsCursor = i
			return true
		}
	}
	m.sessionsCursor = clamp(m.sessionsCursor, 0, len(sessions)-1)
	return true
}

func (m ChatModel) handleSessionsKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEscape:
		m.sessionsVisible = false
	case tea.KeyUp:
		if m.sessionsCursor > 0 {
			m.sessionsCursor--
		}
	case tea.KeyDown:
		if m.sessionsCursor < len(m.sessionsList)-1 {
			m.sessionsCursor++
		}
	case tea.KeyEnter:
		if len(m.sessionsList) > 0 {
			name := m.sessionsList[m.sessionsCursor].name
			if err := m.restoreSession(name); err != nil {
				m.flash = fmt.Sprintf("restore failed: %v", err)
			} else {
				m.sessionsVisible = false
				m.flash = fmt.Sprintf("session restored: %s", name)
			}
		}
	case tea.KeyRunes:
		if len(msg.Runes) == 1 {
			r := msg.Runes[0]
			if r >= '1' && r <= '9' {
				idx := int(r - '1')
				if idx < len(m.sessionsList) {
					m.sessionsCursor = idx
					name := m.sessionsList[idx].name
					if err := m.restoreSession(name); err != nil {
						m.flash = fmt.Sprintf("restore failed: %v", err)
					} else {
						m.sessionsVisible = false
						m.flash = fmt.Sprintf("session restored: %s", name)
					}
				}
			}
		}
	}
	return m, nil
}

func (m ChatModel) handleHelpKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	totalTabs := len(m.helpTabs())
	pageSize := max(1, m.height-10)
	maxScroll := max(0, len(m.helpLines())-pageSize)

	switch msg.Type {
	case tea.KeyEscape, tea.KeyEnter:
		m.helpVisible = false
	case tea.KeyLeft:
		m.helpTab = (m.helpTab + totalTabs - 1) % totalTabs
		m.helpScroll = 0
	case tea.KeyRight:
		m.helpTab = (m.helpTab + 1) % totalTabs
		m.helpScroll = 0
	case tea.KeyUp:
		m.helpScroll = max(0, m.helpScroll-1)
	case tea.KeyDown:
		m.helpScroll = min(maxScroll, m.helpScroll+1)
	case tea.KeyPgUp:
		m.helpScroll = max(0, m.helpScroll-pageSize)
	case tea.KeyPgDown:
		m.helpScroll = min(maxScroll, m.helpScroll+pageSize)
	case tea.KeyHome:
		m.helpScroll = 0
	case tea.KeyEnd:
		m.helpScroll = maxScroll
	case tea.KeyRunes:
		if len(msg.Runes) == 1 {
			switch msg.Runes[0] {
			case '?', 'q', 'Q':
				m.helpVisible = false
			case '[', 'h', 'H':
				m.helpTab = (m.helpTab + totalTabs - 1) % totalTabs
				m.helpScroll = 0
			case ']', 'l', 'L':
				m.helpTab = (m.helpTab + 1) % totalTabs
				m.helpScroll = 0
			case 'j', 'J':
				m.helpScroll = min(maxScroll, m.helpScroll+1)
			case 'k', 'K':
				m.helpScroll = max(0, m.helpScroll-1)
			case 'g':
				m.helpScroll = 0
			case 'G':
				m.helpScroll = maxScroll
			}
		}
	}
	return m, nil
}

func scrollbarColumn(totalLines, visibleLines, scroll, height int) []string {
	if height <= 0 {
		return nil
	}
	col := make([]string, height)
	for i := range col {
		col[i] = "│"
	}
	if totalLines <= visibleLines || visibleLines <= 0 {
		return col
	}
	thumbH := max(1, visibleLines*height/max(1, totalLines))
	if thumbH > height {
		thumbH = height
	}
	maxScroll := max(0, totalLines-visibleLines)
	thumbTop := 0
	if height > thumbH && maxScroll > 0 {
		thumbTop = (scroll * (height - thumbH)) / maxScroll
	}
	for i := 0; i < thumbH && thumbTop+i < height; i++ {
		col[thumbTop+i] = "█"
	}
	return col
}

func joinWithScrollbar(lines []string, scrollbar []string, width, height int) string {
	bodyStyle := lipgloss.NewStyle().Width(max(1, width)).Height(max(1, height))
	if len(lines) == 0 {
		lines = []string{""}
	}
	content := bodyStyle.Render(strings.Join(lines, "\n"))
	contentLines := strings.Split(content, "\n")
	if len(contentLines) < height {
		for len(contentLines) < height {
			contentLines = append(contentLines, strings.Repeat(" ", max(1, width)))
		}
	}
	joined := make([]string, 0, height)
	for i := 0; i < height; i++ {
		sb := "│"
		if i < len(scrollbar) {
			sb = scrollbar[i]
		}
		line := ""
		if i < len(contentLines) {
			line = contentLines[i]
		}
		joined = append(joined, line+sb)
	}
	return strings.Join(joined, "\n")
}

func (m ChatModel) renderSessionsOverlay() string {
	boxW := min(88, max(56, m.width-6))
	boxH := min(28, max(12, m.height-4))
	contentHeight := max(1, boxH-6)

	titleStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#56d364")).Bold(true)
	selectedStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#ffffff")).Background(lipgloss.Color("#1f6feb")).Bold(true)
	textStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#c9d1d9"))
	dimStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#8b949e"))

	lines := make([]string, 0, min(len(m.sessionsList), contentHeight))
	start := 0
	if m.sessionsCursor >= contentHeight {
		start = m.sessionsCursor - contentHeight + 1
	}
	for i := 0; i < contentHeight && start+i < len(m.sessionsList); i++ {
		idx := start + i
		entry := m.sessionsList[idx]
		line := fmt.Sprintf("%d. %s  (%s)", idx+1, entry.name, formatSessionTimestamp(entry.modTime))
		if idx == m.sessionsCursor {
			lines = append(lines, selectedStyle.Render(line))
		} else {
			lines = append(lines, textStyle.Render(line))
		}
	}
	footer := dimStyle.Render("↑/↓ select • Enter restore • 1-9 quick restore • Esc close")
	inner := lipgloss.JoinVertical(lipgloss.Left,
		titleStyle.Render("Sessions"),
		"",
		lipgloss.NewStyle().Width(boxW-6).Height(contentHeight).Render(strings.Join(lines, "\n")),
		"",
		footer,
	)
	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("#58a6ff")).
		Background(lipgloss.Color("#161b22")).
		Padding(1, 2).
		Width(boxW - 6).
		Height(boxH - 4).
		Render(inner)
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, box)
}

func (m ChatModel) renderHelpOverlay() string {
	boxW := min(108, max(72, m.width-6))
	boxH := min(32, max(20, m.height-4))
	contentHeight := max(1, boxH-7)
	lines := m.helpLines()
	maxScroll := max(0, len(lines)-contentHeight)
	if m.helpScroll > maxScroll {
		m.helpScroll = maxScroll
	}

	titleStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#56d364")).Bold(true)
	activeTabStyle := lipgloss.NewStyle().Background(lipgloss.Color("#1f6feb")).Foreground(lipgloss.Color("#ffffff")).Bold(true).Padding(0, 1)
	inactiveTabStyle := lipgloss.NewStyle().Background(lipgloss.Color("#22272e")).Foreground(lipgloss.Color("#8b949e")).Padding(0, 1)
	textStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#c9d1d9"))
	dimStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#8b949e"))

	tabs := m.helpTabs()
	renderedTabs := make([]string, 0, len(tabs))
	for i, tab := range tabs {
		if i == m.helpTab {
			renderedTabs = append(renderedTabs, activeTabStyle.Render(tab))
		} else {
			renderedTabs = append(renderedTabs, inactiveTabStyle.Render(tab))
		}
	}

	visible := make([]string, 0, contentHeight)
	for i := 0; i < contentHeight; i++ {
		idx := m.helpScroll + i
		if idx >= len(lines) {
			break
		}
		visible = append(visible, textStyle.Render(lines[idx]))
	}
	content := strings.Join(visible, "\n")
	footer := fmt.Sprintf("Tab %d/%d • lines %d-%d/%d • ←/→ switch tabs • ↑/↓ scroll • Esc closes", m.helpTab+1, len(tabs), min(len(lines), m.helpScroll+1), min(len(lines), m.helpScroll+contentHeight), len(lines))

	inner := lipgloss.JoinVertical(lipgloss.Left,
		titleStyle.Render("Help"),
		strings.Join(renderedTabs, " "),
		"",
		lipgloss.NewStyle().Width(boxW-6).Height(contentHeight).Render(content),
		"",
		dimStyle.Render(footer),
	)

	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("#58a6ff")).
		Background(lipgloss.Color("#161b22")).
		Padding(1, 2).
		Width(boxW - 6).
		Height(boxH - 4).
		Render(inner)

	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, box)
}

func (m ChatModel) snapshot() chatSessionSnapshot {
	return chatSessionSnapshot{
		SavedAt:      time.Now(),
		Model:        m.model,
		WorkDir:      m.workDir,
		AgentBuf:     m.chatContent,
		ToolsBuf:     m.toolsBuf,
		InputBuf:     m.inputBuf,
		InputPos:     m.inputPos,
		ToolsVisible: boolPtr(m.toolsVisible),
	}
}

func (m *ChatModel) applySnapshot(s chatSessionSnapshot) {
	m.model = s.Model
	m.workDir = s.WorkDir
	m.chatContent = s.AgentBuf
	m.toolsBuf = s.ToolsBuf
	m.inputBuf = s.InputBuf
	m.inputPos = s.InputPos
	toolsVisible := true
	if s.ToolsVisible != nil {
		toolsVisible = *s.ToolsVisible
	}
	m.toolsVisible = toolsVisible
	m.messages = nil
	if strings.TrimSpace(s.AgentBuf) != "" {
		m.messages = append(m.messages, ChatMessage{Kind: MsgStatus, Content: s.AgentBuf})
	}
	m.chatViewport.SetContent(m.chatContent)
}

func (m *ChatModel) saveSession(name string) error {
	path, err := chatSessionFile(name)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(m.snapshot(), "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func (m *ChatModel) restoreSession(name string) error {
	path, err := chatSessionFile(name)
	if err != nil {
		return err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var s chatSessionSnapshot
	if err := json.Unmarshal(data, &s); err != nil {
		return err
	}
	m.applySnapshot(s)
	return nil
}

func (m ChatModel) View() string {
	if m.width == 0 || m.height == 0 {
		return "Initializing..."
	}

	headerStyle := lipgloss.NewStyle().
		Background(lipgloss.Color("#161b22")).
		Foreground(lipgloss.Color("#c9d1d9")).
		Width(m.width).
		Bold(true)
	headerText := "forge • " + m.model + " • " + m.workDir
	if m.state != nil {
		active := m.state.ActiveSkills()
		if len(active) > 0 {
			headerText += " • skills: " + strings.Join(active, ", ")
		}
	}
	header := headerStyle.Render(headerText)

	chatPaneWidth := m.chatPaneWidth()
	chatBodyHeight := max(1, m.chatViewport.Height)
	chatInnerWidth := max(1, chatPaneWidth-2)
	chatContentWidth := max(1, chatInnerWidth-1)
	chatView := m.chatViewport.View()
	chatLines := strings.Split(chatView, "\n")
	chatTotalLines := len(strings.Split(m.chatContent, "\n"))
	chatScrollbar := scrollbarColumn(chatTotalLines, m.chatViewport.Height, m.chatViewport.YOffset, chatBodyHeight)
	chatBody := joinWithScrollbar(chatLines, chatScrollbar, chatContentWidth, chatBodyHeight)
	chatPane := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("#30363d")).
		Background(lipgloss.Color("#0d1117")).
		Foreground(lipgloss.Color("#c9d1d9")).
		Width(chatInnerWidth).
		Height(chatBodyHeight).
		Render(chatBody)

	// Side-by-side with tools pane if visible and has content
	if m.toolsVisible && m.toolsBuf != "" {
		toolsWidth := m.width - chatPaneWidth
		toolsInnerWidth := max(1, toolsWidth-2)
		toolsContentWidth := max(1, toolsInnerWidth-1)
		wrappedTools := lipgloss.NewStyle().Width(toolsContentWidth).Render(m.toolsBuf)
		toolLines := strings.Split(wrappedTools, "\n")
		toolOffset := min(m.toolsScroll, max(0, len(toolLines)-chatBodyHeight))
		visibleToolLines := toolLines
		if len(visibleToolLines) > chatBodyHeight {
			visibleToolLines = visibleToolLines[toolOffset:]
		}
		toolScrollbar := scrollbarColumn(len(toolLines), chatBodyHeight, toolOffset, chatBodyHeight)
		toolsBody := joinWithScrollbar(visibleToolLines, toolScrollbar, toolsContentWidth, chatBodyHeight)
		toolsStyle := lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("#30363d")).
			Background(lipgloss.Color("#0d1117")).
			Foreground(lipgloss.Color("#8b949e")).
			Width(toolsInnerWidth).
			Height(max(1, m.chatViewport.Height-2))
		toolsPane := toolsStyle.Render(toolsBody)
		chatPane = lipgloss.JoinHorizontal(lipgloss.Top, chatPane, toolsPane)
	}

	inputStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("#30363d")).
		Background(lipgloss.Color("#0d1117")).
		Foreground(lipgloss.Color("#c9d1d9")).
		Width(m.width - 4)

	var inputBox string
	if m.pendingApproval != nil {
		approvalStyle := lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("#d29922")).
			Background(lipgloss.Color("#161b22")).
			Foreground(lipgloss.Color("#c9d1d9")).
			Width(m.width - 4)
		approvalText := fmt.Sprintf("Tool: %s\n%s\n\n[y]es / [n]o", m.pendingApproval.Tool, m.pendingApproval.Summary)
		inputBox = approvalStyle.Render(approvalText)
	} else {
		inputContent := m.inputBuf
		if inputContent == "" {
			inputContent = "Type a message..."
		}
		inputBox = inputStyle.Render(inputContent)
	}

	// Status bar with spinner and flash
	var statusText string
	if m.flash != "" {
		statusText = m.flash
	} else if m.busy {
		spinnerChars := []rune("⠋⠙⠹⠸⠼⠴⠦⠧")
		statusText = fmt.Sprintf("%c running...", spinnerChars[m.spinnerFrame])
	} else {
		statusText = "ready • /help for commands"
	}
	statusStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#484f58")).
		Width(m.width)
	statusBar := statusStyle.Render(statusText)

	base := lipgloss.JoinVertical(lipgloss.Left,
		header,
		chatPane,
		inputBox,
		statusBar,
	)
	if m.helpVisible {
		return m.renderHelpOverlay()
	}
	if m.sessionsVisible {
		return m.renderSessionsOverlay()
	}
	return base
}
