package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"forge/internal/agent/tools"
	"forge/internal/auth"
	"forge/internal/chatgptauth"
	"forge/internal/chatstate"
	"forge/internal/copilot"
	"forge/internal/llm"
	"forge/internal/skills"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type chatTickMsg time.Time
type chatApprovalMsg tools.Action
type providerAuthStartedMsg struct {
	providerID string
	verifyURL  string
	userCode   string
	flow       any
}
type providerAuthSucceededMsg struct {
	providerID string
	session    *chatgptauth.Session
	token      string
}
type providerAuthFailedMsg struct {
	providerID string
	err        error
}
type chatSlashCompletionState struct {
	baseInput string
	matches   []string
	index     int
}

var (
	startChatGPTDeviceAuth = func(ctx context.Context) (*chatgptauth.DeviceFlow, error) {
		return chatgptauth.StartDeviceAuth(ctx)
	}
	waitChatGPTDeviceAuth = func(ctx context.Context, flow *chatgptauth.DeviceFlow) (chatgptauth.Session, error) {
		return flow.Wait(ctx)
	}
	startCopilotDeviceAuth = func(ctx context.Context, clientID string) (*copilot.DeviceCode, error) {
		return copilot.RequestDeviceCode(ctx, clientID)
	}
	waitCopilotDeviceAuth = func(ctx context.Context, clientID string, dc *copilot.DeviceCode) (string, error) {
		return copilot.PollForToken(ctx, clientID, dc)
	}
)

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
	copyFn  func(string) error

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
	lastExpandable  string
	lastToolResult  string
	lastCodeBlock   string

	busy           bool
	viewportDirty  bool
	spinnerFrame   int
	status         string
	flash          string
	statsDuration  time.Duration
	statsUsage     llm.Usage
	sessionUsage   llm.Usage
	skills         []skills.Skill
	autoSkillsMode string
	state          *chatstate.State
	lowContrast    bool

	helpVisible bool
	helpTab     int
	helpScroll  int

	statsVisible bool

	searchVisible bool
	searchQuery   string
	searchPos     int
	searchPane    chatPaneFocus
	searchMatches []int
	searchCurrent int

	modelsVisible  bool
	modelsCursor   int
	modelsList     []string
	modelsFiltered []string
	modelsQuery    string
	modelsQueryPos int

	providersVisible     bool
	providersCursor      int
	providersList        []ProviderOption
	providerPromptingKey bool
	providerKeyInput     string
	providerKeyPos       int
	providerStatus       string
	providerAuthURL      string
	providerAuthCode     string
	providerAuthWaiting  bool
	providerAuthProvider string
	providerAuthCancel   context.CancelFunc

	sessionsVisible  bool
	sessionsCursor   int
	sessionsList     []chatSessionEntry
	sessionRenaming  bool
	sessionRenameBuf string
	sessionRenamePos int

	contextFiles  []string
	filesVisible  bool
	filesCursor   int
	filesList     []string
	filesFiltered []string
	filesQuery    string
	filesPos      int

	pendingApproval *tools.Action
	inputCh         chan<- string
	responseCh      chan<- bool
	slashComplete   chatSlashCompletionState
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
		copyFn:         copyToClipboard,
		chatViewport:   vp,
		status:         "ready",
		skills:         cfg.Skills,
		autoSkillsMode: cfg.AutoSkillsMode,
		state:          state,
		toolsVisible:   true,
		paneFocus:      focusChat,
		modelsList:     uniqueStringsPreserveOrder(cfg.AvailableModels),
		modelsFiltered: uniqueStringsPreserveOrder(cfg.AvailableModels),
		providersList:  append([]ProviderOption(nil), cfg.Providers...),
		contextFiles:   append([]string(nil), cfg.ContextFiles...),
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
	m.lastCodeBlock = latestFencedCodeBlock(m.messages[len(m.messages)-1].Content)
	m.viewportDirty = true
}

func (m *ChatModel) refreshViewport() {
	contentWidth := m.chatContentWidth()
	if contentWidth < 10 {
		contentWidth = 60
	}

	var blocks []string
	for _, msg := range m.messages {
		// Skip agent/forge boxes with no content — they render as blank space
		// (created before first token arrives; if error occurs, they stay empty)
		if (msg.Kind == MsgAgent || msg.Kind == MsgForge) && strings.TrimSpace(msg.Content) == "" {
			continue
		}
		blocks = append(blocks, msg.Render(contentWidth, m.lowContrast))
	}
	content := strings.Join(blocks, "\n")
	m.chatContent = content
	m.chatViewport.SetContent(content)
	m.chatViewport.GotoBottom()
}

func (m ChatModel) chatPaneWidth() int {
	if !m.toolsVisible {
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
	if m.toolsVisible {
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
	if m.helpVisible {
		if msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonLeft {
			m.helpVisible = false
		}
		return m, nil
	}
	if m.statsVisible {
		if msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonLeft {
			m.statsVisible = false
		}
		return m, nil
	}
	if m.searchVisible {
		if msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonLeft {
			m.searchVisible = false
		}
		return m, nil
	}
	if m.filesVisible {
		return m.handleFilesMouse(msg)
	}
	if m.modelsVisible {
		return m.handleModelsMouse(msg)
	}
	if m.providersVisible {
		return m.handleProvidersMouse(msg)
	}
	if m.sessionsVisible {
		return m.handleSessionsMouse(msg)
	}

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

func (m ChatModel) modelsOverlayLayout() (x0, y0, boxW, boxH, listY, contentHeight, start int) {
	boxW = min(96, max(56, m.width-6))
	boxH = min(28, max(14, m.height-4))
	contentHeight = max(1, boxH-8)
	x0 = max(0, (m.width-boxW)/2)
	y0 = max(0, (m.height-boxH)/2)
	listY = y0 + 5
	if m.modelsCursor >= contentHeight {
		start = m.modelsCursor - contentHeight + 1
	}
	return
}

func (m ChatModel) providersOverlayLayout() (x0, y0, boxW, boxH, listY, contentHeight, start int) {
	boxW = min(96, max(64, m.width-6))
	boxH = min(30, max(14, m.height-4))
	contentHeight = max(1, boxH-9)
	x0 = max(0, (m.width-boxW)/2)
	y0 = max(0, (m.height-boxH)/2)
	listY = y0 + 5
	if m.providersCursor >= contentHeight {
		start = m.providersCursor - contentHeight + 1
	}
	return
}

func (m ChatModel) sessionsOverlayLayout() (x0, y0, boxW, boxH, listY, contentHeight, start int) {
	boxW = min(88, max(56, m.width-6))
	boxH = min(28, max(12, m.height-4))
	contentHeight = max(1, boxH-6)
	x0 = max(0, (m.width-boxW)/2)
	y0 = max(0, (m.height-boxH)/2)
	listY = y0 + 4
	if m.sessionsCursor >= contentHeight {
		start = m.sessionsCursor - contentHeight + 1
	}
	return
}

func (m ChatModel) filesOverlayLayout() (x0, y0, boxW, boxH, listY, contentHeight, start int) {
	boxW = min(72, max(42, m.width-6))
	boxH = min(24, max(12, m.height-4))
	contentHeight = max(1, boxH-8)
	x0 = max(0, (m.width-boxW)/2)
	y0 = max(0, (m.height-boxH)/2)
	listY = y0 + 5
	if m.filesCursor >= contentHeight {
		start = m.filesCursor - contentHeight + 1
	}
	return
}

func (m ChatModel) handleModelsMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	x0, y0, boxW, boxH, listY, contentHeight, start := m.modelsOverlayLayout()
	if tea.MouseEvent(msg).IsWheel() {
		switch msg.Button {
		case tea.MouseButtonWheelUp:
			if m.modelsCursor > 0 {
				m.modelsCursor--
			}
		case tea.MouseButtonWheelDown:
			if m.modelsCursor < len(m.modelsFiltered)-1 {
				m.modelsCursor++
			}
		}
		return m, nil
	}
	if msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonLeft {
		if msg.X >= x0 && msg.X < x0+boxW && msg.Y >= y0 && msg.Y < y0+boxH {
			if msg.Y >= listY && msg.Y < listY+contentHeight {
				idx := start + (msg.Y - listY)
				if idx >= 0 && idx < len(m.modelsFiltered) {
					m.modelsCursor = idx
					m.pickModel(idx)
				}
			}
			return m, nil
		}
		m.modelsVisible = false
	}
	return m, nil
}

func (m ChatModel) handleProvidersMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	x0, y0, boxW, boxH, listY, contentHeight, start := m.providersOverlayLayout()
	if tea.MouseEvent(msg).IsWheel() {
		switch msg.Button {
		case tea.MouseButtonWheelUp:
			if m.providersCursor > 0 {
				m.providersCursor--
			}
		case tea.MouseButtonWheelDown:
			if m.providersCursor < len(m.providersList)-1 {
				m.providersCursor++
			}
		}
		return m, nil
	}
	if msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonLeft {
		if msg.X >= x0 && msg.X < x0+boxW && msg.Y >= y0 && msg.Y < y0+boxH {
			if msg.Y >= listY && msg.Y < listY+contentHeight {
				idx := start + (msg.Y - listY)
				if idx >= 0 && idx < len(m.providersList) {
					m.providersCursor = idx
					return m.activateProviderSelection()
				}
			}
			return m, nil
		}
		m.providersVisible = false
	}
	return m, nil
}

func (m ChatModel) handleSessionsMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	if m.sessionRenaming {
		if msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonLeft {
			m.sessionRenaming = false
		}
		return m, nil
	}
	x0, y0, boxW, boxH, listY, contentHeight, start := m.sessionsOverlayLayout()
	if tea.MouseEvent(msg).IsWheel() {
		switch msg.Button {
		case tea.MouseButtonWheelUp:
			if m.sessionsCursor > 0 {
				m.sessionsCursor--
			}
		case tea.MouseButtonWheelDown:
			if m.sessionsCursor < len(m.sessionsList)-1 {
				m.sessionsCursor++
			}
		}
		return m, nil
	}
	if msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonLeft {
		if msg.X >= x0 && msg.X < x0+boxW && msg.Y >= y0 && msg.Y < y0+boxH {
			if msg.Y >= listY && msg.Y < listY+contentHeight {
				idx := start + (msg.Y - listY)
				if idx >= 0 && idx < len(m.sessionsList) {
					m.sessionsCursor = idx
					m.restorePickedSession(idx)
				}
			}
			return m, nil
		}
		m.sessionsVisible = false
	}
	return m, nil
}

func (m ChatModel) handleFilesMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	x0, y0, boxW, boxH, listY, contentHeight, start := m.filesOverlayLayout()
	if tea.MouseEvent(msg).IsWheel() {
		switch msg.Button {
		case tea.MouseButtonWheelUp:
			if m.filesCursor > 0 {
				m.filesCursor--
			}
		case tea.MouseButtonWheelDown:
			if m.filesCursor < len(m.filesFiltered)-1 {
				m.filesCursor++
			}
		}
		return m, nil
	}
	if msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonLeft {
		if msg.X >= x0 && msg.X < x0+boxW && msg.Y >= y0 && msg.Y < y0+boxH {
			if msg.Y >= listY && msg.Y < listY+contentHeight {
				idx := start + (msg.Y - listY)
				if idx >= 0 && idx < len(m.filesFiltered) {
					m.filesCursor = idx
					m.replaceActiveAtToken(m.filesFiltered[idx])
				}
			}
			return m, nil
		}
		m.filesVisible = false
	}
	return m, nil
}

func (m ChatModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		headerH := 1
		inputH := 4
		bodyH := max(3, m.height-headerH-inputH)
		m.chatViewport.Width = m.chatContentWidth()
		m.chatViewport.Height = bodyH
		m.refreshViewport()
		return m, nil

	case chatTickMsg:
		if m.busy {
			m.spinnerFrame = (m.spinnerFrame + 1) % 8
		}
		// Detect tools pane appearing/disappearing and resize chat viewport
		toolsShowing := m.toolsVisible
		if toolsShowing != m.toolsWasShowing {
			m.toolsWasShowing = toolsShowing
			m.chatViewport.Width = m.chatContentWidth()
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

	case providerAuthStartedMsg:
		return m.handleProviderAuthStarted(msg)

	case providerAuthSucceededMsg:
		return m.handleProviderAuthSucceeded(msg)

	case providerAuthFailedMsg:
		return m.handleProviderAuthFailed(msg)

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
		if ev.Content != "" {
			m.lastToolResult = ev.Content
		} else if ev.Text != "" {
			m.lastToolResult = ev.Text
		}
		if ev.IsError {
			m.toolsBuf += fmt.Sprintf("  status: ✗ %s\n", ev.Text)
		} else if ev.Content != "" {
			truncated := truncate(ev.Content, 200)
			if truncated != ev.Content {
				m.lastExpandable = ev.Content
				truncated += "\n  ... (/expand)"
			}
			m.toolsBuf += fmt.Sprintf("  status: ✓\n  %s\n", truncated)
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
		errMsg := eventErrorMessage(ev)
		m.toolsBuf += fmt.Sprintf("  ✗ %s\n", errMsg)
		m.flash = "error: " + errMsg
		m.AddMessage(ChatMessage{
			Kind:    MsgStatus,
			Content: "Error: " + errMsg,
		})
	case llm.EventStats:
		m.statsDuration = ev.Duration
		m.statsUsage = ev.Usage
		m.sessionUsage.InputTokens += ev.Usage.InputTokens
		m.sessionUsage.OutputTokens += ev.Usage.OutputTokens
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

func latestFencedCodeBlock(content string) string {
	parts := strings.Split(content, "```")
	if len(parts) < 3 {
		return ""
	}
	for i := len(parts) - 2; i >= 1; i -= 2 {
		block := strings.TrimLeft(parts[i], "\n")
		if newline := strings.Index(block, "\n"); newline >= 0 {
			block = block[newline+1:]
		}
		block = strings.TrimSpace(block)
		if block != "" {
			return block
		}
	}
	return ""
}

func (m ChatModel) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.helpVisible {
		return m.handleHelpKey(msg)
	}
	if m.statsVisible {
		return m.handleStatsKey(msg)
	}
	if m.searchVisible {
		return m.handleSearchKey(msg)
	}
	if m.filesVisible {
		return m.handleFilePickerKey(msg)
	}
	if m.modelsVisible {
		return m.handleModelsKey(msg)
	}
	if m.providersVisible {
		return m.handleProvidersKey(msg)
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
	case tea.KeyCtrlF:
		m.openSearchOverlay("")
		return m, nil
	case tea.KeyEscape:
		if m.busy && m.inputCh != nil {
			ch := m.inputCh
			m.flash = "canceling..."
			return m, func() tea.Msg {
				ch <- "__cancel_turn__"
				return nil
			}
		}
		m.resetSlashCompletion()
		return m, nil
	case tea.KeyEnter:
		m.flash = ""
		m.resetSlashCompletion()
		return m.submitInput()
	case tea.KeyBackspace:
		m.resetSlashCompletion()
		if len(m.inputBuf) > 0 && m.inputPos > 0 {
			runes := []rune(m.inputBuf)
			m.inputBuf = string(append(runes[:m.inputPos-1], runes[m.inputPos:]...))
			m.inputPos--
		}
	case tea.KeyLeft:
		m.resetSlashCompletion()
		if m.inputPos > 0 {
			m.inputPos--
		}
	case tea.KeyRight:
		m.resetSlashCompletion()
		if m.inputPos < len([]rune(m.inputBuf)) {
			m.inputPos++
		}
	case tea.KeyHome:
		m.resetSlashCompletion()
		m.inputPos = 0
	case tea.KeyEnd:
		m.resetSlashCompletion()
		m.inputPos = len([]rune(m.inputBuf))
	case tea.KeyPgUp:
		m.resetSlashCompletion()
		m.chatViewport.HalfPageUp()
	case tea.KeyPgDown:
		m.resetSlashCompletion()
		m.chatViewport.HalfPageDown()
	case tea.KeyF1:
		m.helpVisible = true
		m.helpTab = 0
		m.helpScroll = 0
		m.flash = ""
		return m, nil
	case tea.KeyTab:
		if m.completeSlashCommand() {
			return m, nil
		}
		m.toolsVisible = !m.toolsVisible
		m.chatViewport.Width = m.chatContentWidth()
		m.refreshViewport()
	case tea.KeySpace:
		m.flash = ""
		m.resetSlashCompletion()
		runes := []rune(m.inputBuf)
		newRunes := make([]rune, 0, len(runes)+1)
		newRunes = append(newRunes, runes[:m.inputPos]...)
		newRunes = append(newRunes, ' ')
		newRunes = append(newRunes, runes[m.inputPos:]...)
		m.inputBuf = string(newRunes)
		m.inputPos++
	case tea.KeyRunes:
		m.flash = ""
		if len(msg.Runes) == 1 {
			switch msg.Runes[0] {
			case 'n':
				if m.searchNext(1) {
					return m, nil
				}
			case 'N':
				if m.searchNext(-1) {
					return m, nil
				}
			}
		}
		m.resetSlashCompletion()
		for _, r := range msg.Runes {
			runes := []rune(m.inputBuf)
			newRunes := make([]rune, 0, len(runes)+1)
			newRunes = append(newRunes, runes[:m.inputPos]...)
			newRunes = append(newRunes, r)
			newRunes = append(newRunes, runes[m.inputPos:]...)
			m.inputBuf = string(newRunes)
			m.inputPos++
			if r == '@' {
				m.openFilePicker("")
			}
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

func (m *ChatModel) resetSlashCompletion() {
	m.slashComplete = chatSlashCompletionState{}
}

func (m *ChatModel) completeSlashCommand() bool {
	if strings.Contains(m.inputBuf, "\n") {
		m.resetSlashCompletion()
		return false
	}
	input := strings.TrimSpace(m.inputBuf)
	if !strings.HasPrefix(input, "/") {
		m.resetSlashCompletion()
		return false
	}
	if m.slashComplete.baseInput != "" && len(m.slashComplete.matches) > 1 && input == m.inputBuf {
		for _, match := range m.slashComplete.matches {
			if input == match {
				m.slashComplete.index = (m.slashComplete.index + 1) % len(m.slashComplete.matches)
				m.inputBuf = m.slashComplete.matches[m.slashComplete.index]
				m.inputPos = len([]rune(m.inputBuf))
				m.flash = strings.Join(m.slashComplete.matches, "  ")
				return true
			}
		}
	}
	matches := m.matchingSlashCommands(input)
	if len(matches) == 0 {
		m.resetSlashCompletion()
		m.flash = fmt.Sprintf("no command matches %s", input)
		return true
	}
	if len(matches) == 1 {
		m.resetSlashCompletion()
		m.inputBuf = matches[0]
		m.inputPos = len([]rune(m.inputBuf))
		m.flash = matches[0]
		return true
	}
	prefix := longestCommonPrefix(matches)
	m.slashComplete = chatSlashCompletionState{baseInput: input, matches: matches, index: 0}
	if len([]rune(prefix)) > len([]rune(input)) {
		m.inputBuf = prefix
		m.inputPos = len([]rune(m.inputBuf))
		m.flash = strings.Join(matches, "  ")
		return true
	}
	m.inputBuf = matches[0]
	m.inputPos = len([]rune(m.inputBuf))
	m.flash = strings.Join(matches, "  ")
	return true
}

func (m ChatModel) matchingSlashCommands(input string) []string {
	matches := make([]string, 0)
	for _, cmd := range builtinCommands {
		if strings.HasPrefix(cmd, input) {
			matches = append(matches, cmd)
		}
	}
	return matches
}

func longestCommonPrefix(values []string) string {
	if len(values) == 0 {
		return ""
	}
	prefix := values[0]
	for _, value := range values[1:] {
		for !strings.HasPrefix(value, prefix) && prefix != "" {
			prefix = prefix[:len(prefix)-1]
		}
		if prefix == "" {
			return ""
		}
	}
	return prefix
}

func uniqueStringsPreserveOrder(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	out := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func resolveModelName(models []string, input string) string {
	input = strings.TrimSpace(input)
	if input == "" {
		return ""
	}
	var idx int
	if _, err := fmt.Sscanf(input, "%d", &idx); err == nil && idx >= 1 && idx <= len(models) {
		return models[idx-1]
	}
	for _, model := range models {
		if strings.EqualFold(model, input) {
			return model
		}
	}
	for _, model := range models {
		if strings.Contains(strings.ToLower(model), strings.ToLower(input)) {
			return model
		}
	}
	return ""
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

var builtinCommands = []string{
	"/clear", "/clear all", "/clear agent", "/clear tools",
	"/help", "/stats",
	"/theme", "/theme low", "/theme default",
	"/tools", "/toggle tools", "/toggle tools on", "/toggle tools off",
	"/models", "/model", "/provider",
	"/skills", "/auto-skills", "/sessions", "/save", "/restore",
	"/find", "/copy agent", "/copy tools", "/copy code", "/copy result", "/expand",
	"/exit", "/quit",
}

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
	case input == "/clear" || input == "/clear all":
		m.messages = nil
		m.toolsBuf = ""
		m.refreshViewport()
		m.flash = "conversation cleared"
	case input == "/clear agent":
		m.messages = nil
		m.refreshViewport()
		m.flash = "conversation cleared"
	case input == "/clear tools":
		m.toolsBuf = ""
		m.flash = "tools pane cleared"
	case input == "/help":
		m.helpVisible = true
		m.helpTab = 0
		m.helpScroll = 0
		m.flash = "help opened"
	case input == "/stats":
		hasStats := m.statsDuration > 0 ||
			m.statsUsage.InputTokens > 0 ||
			m.statsUsage.OutputTokens > 0 ||
			m.sessionUsage.InputTokens > 0 ||
			m.sessionUsage.OutputTokens > 0
		if !hasStats {
			m.flash = "no stats yet"
			break
		}
		m.statsVisible = true
		m.flash = "stats opened"
	case input == "/theme":
		m.lowContrast = !m.lowContrast
		m.refreshViewport()
		if m.lowContrast {
			m.flash = "theme: low contrast"
		} else {
			m.flash = "theme: default"
		}
	case input == "/theme low":
		m.lowContrast = true
		m.refreshViewport()
		m.flash = "theme: low contrast"
	case input == "/theme default":
		m.lowContrast = false
		m.refreshViewport()
		m.flash = "theme: default"
	case input == "/sessions":
		_ = m.saveSession("last-session")
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
	case input == "/tools" || input == "/toggle tools":
		m.toolsVisible = !m.toolsVisible
		m.refreshViewport()
		if m.toolsVisible {
			m.flash = "tools pane: visible"
		} else {
			m.flash = "tools pane: hidden"
		}
	case input == "/toggle tools on":
		m.toolsVisible = true
		m.refreshViewport()
		m.flash = "tools pane: visible"
	case input == "/toggle tools off":
		m.toolsVisible = false
		m.refreshViewport()
		m.flash = "tools pane: hidden"
	case input == "/provider":
		m.openProviderPicker()
		m.flash = "providers opened"
	case input == "/models" || input == "/model":
		m.openModelPicker()
		m.flash = "models opened"
	case strings.HasPrefix(input, "/model "):
		arg := strings.TrimSpace(strings.TrimPrefix(input, "/model "))
		if arg != "" && m.config.SwitchModel != nil {
			models := m.modelsList
			if len(models) == 0 {
				models = append([]string(nil), m.config.AvailableModels...)
			}
			resolved := resolveModelName(models, arg)
			if resolved == "" {
				m.flash = fmt.Sprintf("unknown model %q — try /models", arg)
				break
			}
			newModel, err := m.config.SwitchModel(resolved)
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
	case input == "/auto-skills":
		mode := skills.NormalizeAutoMode(m.autoSkillsMode)
		if mode == "" {
			mode = skills.AutoSkillsSuggest
		}
		m.flash = fmt.Sprintf("auto-skills: %s", mode)
	case strings.HasPrefix(input, "/auto-skills "):
		mode := skills.NormalizeAutoMode(strings.TrimSpace(strings.TrimPrefix(input, "/auto-skills ")))
		if mode == "" {
			m.flash = "auto-skills must be one of: off, suggest, auto"
			break
		}
		m.autoSkillsMode = mode
		m.flash = fmt.Sprintf("auto-skills: %s", mode)
	case input == "/find":
		m.openSearchOverlay("")
		m.flash = "search opened"
	case strings.HasPrefix(input, "/find "):
		query := strings.TrimSpace(strings.TrimPrefix(input, "/find "))
		m.openSearchOverlay(query)
	case input == "/copy agent":
		if err := m.copyFn(m.chatContent); err != nil {
			m.flash = fmt.Sprintf("copy failed: %v", err)
		} else {
			m.flash = "agent copied"
		}
	case input == "/copy tools":
		if err := m.copyFn(m.toolsBuf); err != nil {
			m.flash = fmt.Sprintf("copy failed: %v", err)
		} else {
			m.flash = "tools copied"
		}
	case input == "/copy code":
		if strings.TrimSpace(m.lastCodeBlock) == "" {
			m.flash = "copy failed: no code block yet"
		} else if err := m.copyFn(m.lastCodeBlock); err != nil {
			m.flash = fmt.Sprintf("copy failed: %v", err)
		} else {
			m.flash = "code copied"
		}
	case input == "/copy result":
		if strings.TrimSpace(m.lastToolResult) == "" {
			m.flash = "copy failed: no tool result yet"
		} else if err := m.copyFn(m.lastToolResult); err != nil {
			m.flash = fmt.Sprintf("copy failed: %v", err)
		} else {
			m.flash = "result copied"
		}
	case input == "/expand":
		if strings.TrimSpace(m.lastExpandable) == "" {
			m.flash = "nothing to expand"
		} else {
			m.AddMessage(ChatMessage{Kind: MsgStatus, Content: m.lastExpandable})
			m.lastExpandable = ""
			m.flash = "expanded"
		}
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
			"  /find              open search for current pane",
			"  /find <query>      search current pane with initial query",
			"  /models            list available models",
			"  /model             list available models",
			"  /model <name>      switch to a model",
			"  /provider          open provider picker",
			"  /auto-skills       show auto-skills mode",
			"  /auto-skills <m>   set off, suggest, or auto",
			"",
			"Session state:",
			"  /sessions          open saved sessions picker",
			"  /save [name]       save the current session",
			"  /restore [name]    restore a saved session",
			"  /stats             show latest turn and session token usage",
			"  /skills            list loaded skills",
			"",
			"Layout and display:",
			"  /tools             show / hide tools pane",
			"  /toggle tools      show / hide tools pane",
			"  /theme             toggle low-contrast theme",
			"  /expand            expand last truncated result",
			"",
			"Export and cleanup:",
			"  /copy agent        copy agent pane",
			"  /copy tools        copy tools pane",
			"  /copy code         copy latest code block",
			"  /copy result       copy latest tool result",
			"  /clear             clear conversation and tools pane",
			"  /clear all         same as /clear",
			"  /clear agent       clear agent pane",
			"  /clear tools       clear tools pane",
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
			"  Ctrl-F             open search for current pane",
			"  n / N              next / previous search hit",
			"",
			"Turn control:",
			"  Esc                cancel current run",
		}
	}
}

func (m *ChatModel) openSearchOverlay(query string) {
	m.searchPane = m.paneFocus
	m.searchVisible = true
	m.searchQuery = query
	m.searchPos = len([]rune(query))
	m.updateSearchMatches(false)
}

func (m *ChatModel) searchTarget() ([]string, *int, int, string) {
	switch m.searchPane {
	case focusTools:
		lines := m.toolsWrappedLines()
		if len(lines) == 0 {
			lines = []string{""}
		}
		return lines, &m.toolsScroll, max(1, m.chatViewport.Height), "tools"
	default:
		lines := strings.Split(m.chatContent, "\n")
		if len(lines) == 0 {
			lines = []string{""}
		}
		return lines, &m.chatViewport.YOffset, max(1, m.chatViewport.Height), "agent"
	}
}

func (m *ChatModel) updateSearchMatches(jump bool) {
	query := strings.TrimSpace(strings.ToLower(m.searchQuery))
	m.searchMatches = nil
	m.searchCurrent = -1
	if query == "" {
		return
	}
	lines, scroll, visible, paneName := m.searchTarget()
	for idx, line := range lines {
		if strings.Contains(strings.ToLower(line), query) {
			m.searchMatches = append(m.searchMatches, idx)
		}
	}
	if len(m.searchMatches) == 0 {
		m.flash = fmt.Sprintf("no matches for %q", m.searchQuery)
		return
	}
	m.searchCurrent = 0
	line := m.searchMatches[0]
	*scroll = clamp(line-(visible/2), 0, max(0, len(lines)-visible))
	m.flash = fmt.Sprintf("%d match(es) in %s pane", len(m.searchMatches), paneName)
	if jump && len(m.searchMatches) > 1 {
		m.searchNext(1)
	}
}

func (m *ChatModel) openFilePicker(query string) {
	list, err := discoverContextFiles(m.workDir)
	if err != nil {
		m.flash = fmt.Sprintf("file picker failed: %v", err)
		return
	}
	m.filesVisible = true
	m.filesList = list
	m.filesQuery = query
	m.filesPos = len([]rune(query))
	m.filesCursor = 0
	m.updateFilePickerMatches()
}

func (m *ChatModel) updateFilePickerMatches() {
	query := strings.TrimSpace(strings.ToLower(m.filesQuery))
	filtered := make([]string, 0, len(m.filesList))
	for _, path := range m.filesList {
		if query == "" || strings.Contains(strings.ToLower(path), query) {
			filtered = append(filtered, path)
		}
	}
	m.filesFiltered = filtered
	if len(filtered) == 0 {
		m.filesCursor = 0
		return
	}
	m.filesCursor = clamp(m.filesCursor, 0, len(filtered)-1)
}

func (m *ChatModel) replaceActiveAtToken(path string) {
	runes := []rune(m.inputBuf)
	if m.inputPos < 0 || m.inputPos > len(runes) {
		return
	}
	start := m.inputPos
	for start > 0 {
		r := runes[start-1]
		if r == '@' {
			start--
			break
		}
		if r == ' ' || r == '\t' || r == '\n' {
			return
		}
		start--
	}
	if start < 0 || start >= len(runes) || runes[start] != '@' {
		return
	}
	end := m.inputPos
	for end < len(runes) {
		r := runes[end]
		if r == ' ' || r == '\t' || r == '\n' {
			break
		}
		end++
	}
	repl := "@" + path + " "
	prefix := string(runes[:start])
	m.inputBuf = prefix + repl + string(runes[end:])
	m.inputPos = len([]rune(prefix + repl))
	already := false
	for _, existing := range m.contextFiles {
		if existing == path {
			already = true
			break
		}
	}
	if !already {
		m.contextFiles = append(m.contextFiles, path)
		sort.Strings(m.contextFiles)
	}
	m.filesVisible = false
	m.flash = fmt.Sprintf("added context %s", path)
}

func (m ChatModel) handleFilePickerKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEscape:
		m.filesVisible = false
	case tea.KeyEnter:
		if rel := m.resolveExplicitContextPath(strings.TrimSpace(m.filesQuery)); rel != "" {
			m.replaceActiveAtToken(rel)
			return m, nil
		}
		if m.filesCursor >= 0 && m.filesCursor < len(m.filesFiltered) {
			m.replaceActiveAtToken(m.filesFiltered[m.filesCursor])
		}
	case tea.KeyUp:
		if m.filesCursor > 0 {
			m.filesCursor--
		}
	case tea.KeyDown:
		if m.filesCursor < len(m.filesFiltered)-1 {
			m.filesCursor++
		}
	case tea.KeyLeft:
		if m.filesPos > 0 {
			m.filesPos--
		}
	case tea.KeyRight:
		if m.filesPos < len([]rune(m.filesQuery)) {
			m.filesPos++
		}
	case tea.KeyBackspace:
		if m.filesPos > 0 {
			runes := []rune(m.filesQuery)
			m.filesQuery = string(append(runes[:m.filesPos-1], runes[m.filesPos:]...))
			m.filesPos--
			m.updateFilePickerMatches()
		}
	case tea.KeyDelete:
		runes := []rune(m.filesQuery)
		if m.filesPos < len(runes) {
			m.filesQuery = string(append(runes[:m.filesPos], runes[m.filesPos+1:]...))
			m.updateFilePickerMatches()
		}
	case tea.KeyCtrlA:
		m.filesPos = 0
	case tea.KeyCtrlE:
		m.filesPos = len([]rune(m.filesQuery))
	case tea.KeyCtrlU:
		m.filesQuery = ""
		m.filesPos = 0
		m.updateFilePickerMatches()
	case tea.KeyRunes:
		if len(msg.Runes) == 1 && (msg.Runes[0] == 'q' || msg.Runes[0] == 'Q') {
			m.filesVisible = false
			return m, nil
		}
		for _, r := range msg.Runes {
			runes := []rune(m.filesQuery)
			newRunes := make([]rune, 0, len(runes)+1)
			newRunes = append(newRunes, runes[:m.filesPos]...)
			newRunes = append(newRunes, r)
			newRunes = append(newRunes, runes[m.filesPos:]...)
			m.filesQuery = string(newRunes)
			m.filesPos++
		}
		m.updateFilePickerMatches()
	}
	return m, nil
}

func (m ChatModel) resolveExplicitContextPath(query string) string {
	if query == "" {
		return ""
	}
	resolved, err := tools.ResolvePath(m.workDir, query)
	if err != nil {
		return ""
	}
	if _, err := os.Stat(resolved); err != nil {
		return ""
	}
	resolvedWorkDir, err := filepath.EvalSymlinks(m.workDir)
	if err != nil {
		resolvedWorkDir = filepath.Clean(m.workDir)
	}
	rel, err := filepath.Rel(resolvedWorkDir, resolved)
	if err != nil {
		return ""
	}
	return filepath.ToSlash(rel)
}

func discoverContextFiles(workDir string) ([]string, error) {
	var files []string
	walkErr := filepath.WalkDir(workDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		name := d.Name()
		if d.IsDir() {
			switch name {
			case ".git", "node_modules", "vendor":
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasPrefix(name, ".") && strings.HasSuffix(name, ".swp") {
			return nil
		}
		rel, relErr := filepath.Rel(workDir, path)
		if relErr != nil {
			return nil
		}
		files = append(files, filepath.ToSlash(rel))
		return nil
	})
	if walkErr != nil {
		return nil, walkErr
	}
	sort.Strings(files)
	return files, nil
}

func (m *ChatModel) searchNext(delta int) bool {
	if len(m.searchMatches) == 0 {
		return false
	}
	lines, scroll, visible, _ := m.searchTarget()
	if m.searchCurrent < 0 {
		m.searchCurrent = 0
	} else {
		m.searchCurrent = (m.searchCurrent + delta + len(m.searchMatches)) % len(m.searchMatches)
	}
	line := m.searchMatches[m.searchCurrent]
	*scroll = clamp(line-(visible/2), 0, max(0, len(lines)-visible))
	m.flash = fmt.Sprintf("match %d/%d for %q", m.searchCurrent+1, len(m.searchMatches), m.searchQuery)
	return true
}

func (m ChatModel) handleSearchKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEscape:
		m.searchVisible = false
	case tea.KeyEnter:
		m.updateSearchMatches(true)
		m.searchVisible = false
	case tea.KeyLeft:
		if m.searchPos > 0 {
			m.searchPos--
		}
	case tea.KeyRight:
		if m.searchPos < len([]rune(m.searchQuery)) {
			m.searchPos++
		}
	case tea.KeyBackspace:
		if m.searchPos > 0 {
			runes := []rune(m.searchQuery)
			m.searchQuery = string(append(runes[:m.searchPos-1], runes[m.searchPos:]...))
			m.searchPos--
			m.updateSearchMatches(false)
		}
	case tea.KeyDelete:
		runes := []rune(m.searchQuery)
		if m.searchPos < len(runes) {
			m.searchQuery = string(append(runes[:m.searchPos], runes[m.searchPos+1:]...))
			m.updateSearchMatches(false)
		}
	case tea.KeyCtrlA:
		m.searchPos = 0
	case tea.KeyCtrlE:
		m.searchPos = len([]rune(m.searchQuery))
	case tea.KeyCtrlU:
		m.searchQuery = ""
		m.searchPos = 0
		m.updateSearchMatches(false)
	case tea.KeyRunes:
		for _, r := range msg.Runes {
			runes := []rune(m.searchQuery)
			newRunes := make([]rune, 0, len(runes)+1)
			newRunes = append(newRunes, runes[:m.searchPos]...)
			newRunes = append(newRunes, r)
			newRunes = append(newRunes, runes[m.searchPos:]...)
			m.searchQuery = string(newRunes)
			m.searchPos++
		}
		m.updateSearchMatches(false)
	}
	return m, nil
}

func (m ChatModel) handleStatsKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEscape, tea.KeyEnter:
		m.statsVisible = false
	case tea.KeyRunes:
		if len(msg.Runes) == 1 && (msg.Runes[0] == 'q' || msg.Runes[0] == 'Q') {
			m.statsVisible = false
		}
	}
	return m, nil
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
		m.sessionRenaming = false
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
	m.sessionRenaming = false
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
	if m.sessionRenaming {
		return m.handleSessionRenameKey(msg)
	}
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
		m.restorePickedSession(m.sessionsCursor)
	case tea.KeyRunes:
		if len(msg.Runes) == 1 {
			r := msg.Runes[0]
			if r >= '1' && r <= '9' {
				idx := int(r - '1')
				if idx < len(m.sessionsList) {
					m.sessionsCursor = idx
					m.restorePickedSession(idx)
				}
				return m, nil
			}
			switch r {
			case 'd', 'D':
				m.deletePickedSession(m.sessionsCursor)
			case 'r', 'R':
				m.beginRenamePickedSession(m.sessionsCursor)
			}
		}
	}
	return m, nil
}

func (m *ChatModel) restorePickedSession(idx int) {
	if idx < 0 || idx >= len(m.sessionsList) {
		return
	}
	name := m.sessionsList[idx].name
	if err := m.restoreSession(name); err != nil {
		m.flash = fmt.Sprintf("restore failed: %v", err)
		return
	}
	m.sessionsVisible = false
	m.flash = fmt.Sprintf("session restored: %s", name)
}

func (m *ChatModel) beginRenamePickedSession(idx int) {
	if idx < 0 || idx >= len(m.sessionsList) {
		return
	}
	name := m.sessionsList[idx].name
	if name == "last-session" {
		m.flash = "cannot rename last-session"
		return
	}
	m.sessionRenaming = true
	m.sessionRenameBuf = name
	m.sessionRenamePos = len([]rune(name))
}

func (m ChatModel) handleSessionRenameKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEscape:
		m.sessionRenaming = false
	case tea.KeyEnter:
		m.commitRenamePickedSession()
	case tea.KeyLeft:
		if m.sessionRenamePos > 0 {
			m.sessionRenamePos--
		}
	case tea.KeyRight:
		if m.sessionRenamePos < len([]rune(m.sessionRenameBuf)) {
			m.sessionRenamePos++
		}
	case tea.KeyBackspace:
		if m.sessionRenamePos > 0 {
			runes := []rune(m.sessionRenameBuf)
			m.sessionRenameBuf = string(append(runes[:m.sessionRenamePos-1], runes[m.sessionRenamePos:]...))
			m.sessionRenamePos--
		}
	case tea.KeyDelete:
		runes := []rune(m.sessionRenameBuf)
		if m.sessionRenamePos < len(runes) {
			m.sessionRenameBuf = string(append(runes[:m.sessionRenamePos], runes[m.sessionRenamePos+1:]...))
		}
	case tea.KeyCtrlA:
		m.sessionRenamePos = 0
	case tea.KeyCtrlE:
		m.sessionRenamePos = len([]rune(m.sessionRenameBuf))
	case tea.KeyCtrlU:
		m.sessionRenameBuf = ""
		m.sessionRenamePos = 0
	case tea.KeyRunes:
		for _, r := range msg.Runes {
			runes := []rune(m.sessionRenameBuf)
			newRunes := make([]rune, 0, len(runes)+1)
			newRunes = append(newRunes, runes[:m.sessionRenamePos]...)
			newRunes = append(newRunes, r)
			newRunes = append(newRunes, runes[m.sessionRenamePos:]...)
			m.sessionRenameBuf = string(newRunes)
			m.sessionRenamePos++
		}
	}
	return m, nil
}

func (m *ChatModel) commitRenamePickedSession() {
	if m.sessionsCursor < 0 || m.sessionsCursor >= len(m.sessionsList) {
		m.sessionRenaming = false
		return
	}
	oldName := m.sessionsList[m.sessionsCursor].name
	newName := sanitizeChatSessionName(m.sessionRenameBuf)
	if newName == "" {
		m.flash = "rename failed: missing session name"
		return
	}
	if oldName == newName {
		m.sessionRenaming = false
		return
	}
	if err := renameChatSession(oldName, newName); err != nil {
		m.flash = fmt.Sprintf("rename failed: %v", err)
		return
	}
	m.flash = fmt.Sprintf("session renamed: %s -> %s", oldName, newName)
	if !m.refreshSessionsPicker(false) {
		return
	}
	for i, entry := range m.sessionsList {
		if entry.name == newName {
			m.sessionsCursor = i
			break
		}
	}
}

func (m *ChatModel) deletePickedSession(idx int) {
	if idx < 0 || idx >= len(m.sessionsList) {
		return
	}
	name := m.sessionsList[idx].name
	if name == "last-session" {
		m.flash = "cannot delete last-session"
		return
	}
	if err := deleteChatSession(name); err != nil {
		m.flash = fmt.Sprintf("delete failed: %v", err)
		return
	}
	m.flash = fmt.Sprintf("session deleted: %s", name)
	if !m.refreshSessionsPicker(false) {
		return
	}
	m.sessionsCursor = clamp(idx, 0, len(m.sessionsList)-1)
}

func (m *ChatModel) openModelPicker() {
	m.ensureModelListLoaded()
	m.modelsQuery = ""
	m.modelsQueryPos = 0
	m.updateModelFilter()
	m.modelsVisible = true
	m.modelsCursor = 0
}

func (m *ChatModel) updateModelFilter() {
	query := strings.TrimSpace(strings.ToLower(m.modelsQuery))
	m.modelsFiltered = m.modelsFiltered[:0]
	for _, name := range m.modelsList {
		if query == "" || strings.Contains(strings.ToLower(name), query) {
			m.modelsFiltered = append(m.modelsFiltered, name)
		}
	}
	if len(m.modelsFiltered) == 0 {
		m.modelsCursor = 0
		return
	}
	m.modelsCursor = clamp(m.modelsCursor, 0, len(m.modelsFiltered)-1)
}

func (m ChatModel) handleModelsKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEscape:
		m.modelsVisible = false
	case tea.KeyUp:
		if m.modelsCursor > 0 {
			m.modelsCursor--
		}
	case tea.KeyDown:
		if m.modelsCursor < len(m.modelsFiltered)-1 {
			m.modelsCursor++
		}
	case tea.KeyLeft:
		if m.modelsQueryPos > 0 {
			m.modelsQueryPos--
		}
	case tea.KeyRight:
		if m.modelsQueryPos < len([]rune(m.modelsQuery)) {
			m.modelsQueryPos++
		}
	case tea.KeyBackspace:
		if m.modelsQueryPos > 0 {
			runes := []rune(m.modelsQuery)
			m.modelsQuery = string(append(runes[:m.modelsQueryPos-1], runes[m.modelsQueryPos:]...))
			m.modelsQueryPos--
			m.updateModelFilter()
		}
	case tea.KeyDelete:
		runes := []rune(m.modelsQuery)
		if m.modelsQueryPos < len(runes) {
			m.modelsQuery = string(append(runes[:m.modelsQueryPos], runes[m.modelsQueryPos+1:]...))
			m.updateModelFilter()
		}
	case tea.KeyCtrlA:
		m.modelsQueryPos = 0
	case tea.KeyCtrlE:
		m.modelsQueryPos = len([]rune(m.modelsQuery))
	case tea.KeyCtrlU:
		m.modelsQuery = ""
		m.modelsQueryPos = 0
		m.updateModelFilter()
	case tea.KeyEnter:
		m.pickModel(m.modelsCursor)
	case tea.KeyRunes:
		if len(msg.Runes) == 1 && strings.TrimSpace(m.modelsQuery) == "" {
			r := msg.Runes[0]
			if r >= '1' && r <= '9' {
				idx := int(r - '1')
				if idx < len(m.modelsFiltered) {
					m.modelsCursor = idx
					m.pickModel(idx)
				}
				return m, nil
			}
		}
		for _, r := range msg.Runes {
			runes := []rune(m.modelsQuery)
			newRunes := make([]rune, 0, len(runes)+1)
			newRunes = append(newRunes, runes[:m.modelsQueryPos]...)
			newRunes = append(newRunes, r)
			newRunes = append(newRunes, runes[m.modelsQueryPos:]...)
			m.modelsQuery = string(newRunes)
			m.modelsQueryPos++
		}
		m.updateModelFilter()
	}
	return m, nil
}

func (m *ChatModel) ensureModelListLoaded() {
	if len(m.modelsList) > 0 {
		m.modelsList = uniqueStringsPreserveOrder(m.modelsList)
		return
	}
	if len(m.config.AvailableModels) > 0 {
		m.modelsList = uniqueStringsPreserveOrder(m.config.AvailableModels)
		return
	}
	m.refreshModelList()
}

func (m *ChatModel) refreshModelList() {
	var models []string
	if m.config.RefreshModels != nil {
		models = m.config.RefreshModels()
	} else {
		models = m.config.AvailableModels
	}
	m.modelsList = uniqueStringsPreserveOrder(models)
	m.updateModelFilter()
}

func (m *ChatModel) pickModel(idx int) {
	if idx < 0 || idx >= len(m.modelsFiltered) {
		return
	}
	picked := m.modelsFiltered[idx]
	if m.config.SwitchModel != nil {
		newModel, err := m.config.SwitchModel(picked)
		if err != nil {
			m.flash = fmt.Sprintf("error: %v", err)
			return
		}
		m.model = newModel
		m.flash = fmt.Sprintf("switched to %s", newModel)
	}
	m.modelsVisible = false
}

func (m *ChatModel) openProviderPicker() {
	if m.config.RefreshProviders != nil {
		m.providersList = append([]ProviderOption(nil), m.config.RefreshProviders()...)
	} else {
		m.providersList = append([]ProviderOption(nil), m.config.Providers...)
	}
	m.providersVisible = true
	m.providerPromptingKey = false
	m.providerStatus = ""
	m.providerAuthURL = ""
	m.providerAuthCode = ""
	m.providerAuthWaiting = false
	m.providerAuthProvider = ""
	m.providersCursor = 0
}

func (m ChatModel) handleProvidersKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.providerAuthWaiting {
		switch msg.Type {
		case tea.KeyEscape:
			if m.providerAuthCancel != nil {
				m.providerAuthCancel()
				m.providerAuthCancel = nil
			}
			m.providerAuthWaiting = false
			m.providerAuthURL = ""
			m.providerAuthCode = ""
			m.providerAuthProvider = ""
			m.providerStatus = "sign-in canceled"
		}
		return m, nil
	}
	if m.providerPromptingKey {
		switch msg.Type {
		case tea.KeyEscape:
			m.providerPromptingKey = false
			m.providerStatus = ""
		case tea.KeyEnter:
			return m.saveProviderKey()
		case tea.KeyLeft:
			if m.providerKeyPos > 0 {
				m.providerKeyPos--
			}
		case tea.KeyRight:
			if m.providerKeyPos < len([]rune(m.providerKeyInput)) {
				m.providerKeyPos++
			}
		case tea.KeyBackspace:
			if m.providerKeyPos > 0 {
				runes := []rune(m.providerKeyInput)
				m.providerKeyInput = string(append(runes[:m.providerKeyPos-1], runes[m.providerKeyPos:]...))
				m.providerKeyPos--
			}
		case tea.KeyDelete:
			runes := []rune(m.providerKeyInput)
			if m.providerKeyPos < len(runes) {
				m.providerKeyInput = string(append(runes[:m.providerKeyPos], runes[m.providerKeyPos+1:]...))
			}
		case tea.KeyRunes:
			for _, r := range msg.Runes {
				runes := []rune(m.providerKeyInput)
				newRunes := make([]rune, 0, len(runes)+1)
				newRunes = append(newRunes, runes[:m.providerKeyPos]...)
				newRunes = append(newRunes, r)
				newRunes = append(newRunes, runes[m.providerKeyPos:]...)
				m.providerKeyInput = string(newRunes)
				m.providerKeyPos++
			}
		}
		return m, nil
	}

	switch msg.Type {
	case tea.KeyEscape:
		m.providersVisible = false
	case tea.KeyUp:
		if m.providersCursor > 0 {
			m.providersCursor--
		}
	case tea.KeyDown:
		if m.providersCursor < len(m.providersList)-1 {
			m.providersCursor++
		}
	case tea.KeyEnter:
		return m.activateProviderSelection()
	case tea.KeyRunes:
		if len(msg.Runes) == 1 {
			switch msg.Runes[0] {
			case 'd', 'D':
				return m.deleteProviderCredential()
			}
		}
	}
	return m, nil
}

func (m ChatModel) activateProviderSelection() (tea.Model, tea.Cmd) {
	if m.providersCursor < 0 || m.providersCursor >= len(m.providersList) {
		return m, nil
	}
	provider := m.providersList[m.providersCursor]
	if providerNeedsInteractiveLogin(provider) {
		return m.startProviderLogin(provider)
	}
	if providerUsesAPIKey(provider.ID) {
		m.providerPromptingKey = true
		m.providerKeyInput = ""
		m.providerKeyPos = 0
		m.providerStatus = "enter API key"
		return m, nil
	}
	if provider.DefaultModel != "" && m.config.SwitchModel != nil {
		newModel, err := m.config.SwitchModel(provider.DefaultModel)
		if err != nil {
			m.flash = fmt.Sprintf("error: %v", err)
		} else {
			m.model = newModel
			m.flash = fmt.Sprintf("switched to %s", newModel)
		}
	}
	m.providersVisible = false
	return m, nil
}

func providerNeedsInteractiveLogin(provider ProviderOption) bool {
	id := strings.ToLower(strings.TrimSpace(provider.ID))
	if id != "chatgpt" && id != "copilot" {
		return false
	}
	status := strings.ToLower(strings.TrimSpace(provider.Status))
	return strings.Contains(status, "sign in")
}

func (m ChatModel) startProviderLogin(provider ProviderOption) (tea.Model, tea.Cmd) {
	switch strings.ToLower(strings.TrimSpace(provider.ID)) {
	case "chatgpt":
		m.providerStatus = "requesting ChatGPT device code..."
		return m, func() tea.Msg {
			flow, err := startChatGPTDeviceAuth(context.Background())
			if err != nil {
				return providerAuthFailedMsg{providerID: provider.ID, err: err}
			}
			return providerAuthStartedMsg{
				providerID: provider.ID,
				verifyURL:  flow.VerificationURL(),
				userCode:   flow.UserCode(),
				flow:       flow,
			}
		}
	case "copilot":
		if strings.TrimSpace(m.config.CopilotClientID) == "" {
			m.providerStatus = "missing Copilot client id"
			return m, nil
		}
		m.providerStatus = "requesting Copilot device code..."
		clientID := m.config.CopilotClientID
		return m, func() tea.Msg {
			dc, err := startCopilotDeviceAuth(context.Background(), clientID)
			if err != nil {
				return providerAuthFailedMsg{providerID: provider.ID, err: err}
			}
			return providerAuthStartedMsg{
				providerID: provider.ID,
				verifyURL:  dc.VerificationURI,
				userCode:   dc.UserCode,
				flow:       dc,
			}
		}
	default:
		return m, nil
	}
}

func (m ChatModel) saveProviderKey() (tea.Model, tea.Cmd) {
	if m.providersCursor < 0 || m.providersCursor >= len(m.providersList) {
		return m, nil
	}
	key := strings.TrimSpace(m.providerKeyInput)
	if key == "" {
		m.providerStatus = "missing API key"
		return m, nil
	}
	tokens, err := auth.Load()
	if err != nil {
		m.providerStatus = fmt.Sprintf("save failed: %v", err)
		return m, nil
	}
	setProviderToken(tokens, m.providersList[m.providersCursor].ID, key)
	if err := auth.Save(tokens); err != nil {
		m.providerStatus = fmt.Sprintf("save failed: %v", err)
		return m, nil
	}
	m.providerPromptingKey = false
	m.providerStatus = "saved"
	if m.config.RefreshProviders != nil {
		m.providersList = append([]ProviderOption(nil), m.config.RefreshProviders()...)
	}
	m.refreshModelList()
	provider := m.providersList[m.providersCursor]
	if provider.DefaultModel != "" && m.config.SwitchModel != nil {
		if newModel, err := m.config.SwitchModel(provider.DefaultModel); err == nil {
			m.model = newModel
			m.flash = fmt.Sprintf("saved key and switched to %s", newModel)
		} else {
			m.flash = "saved key"
		}
	} else {
		m.flash = "saved key"
	}
	return m, nil
}

func (m ChatModel) handleProviderAuthStarted(msg providerAuthStartedMsg) (tea.Model, tea.Cmd) {
	ctx, cancel := context.WithCancel(context.Background())
	m.providerAuthCancel = cancel
	m.providerAuthWaiting = true
	m.providerAuthProvider = msg.providerID
	m.providerAuthURL = msg.verifyURL
	m.providerAuthCode = msg.userCode
	m.providerStatus = fmt.Sprintf("visit %s and enter %s", msg.verifyURL, msg.userCode)

	switch flow := msg.flow.(type) {
	case *chatgptauth.DeviceFlow:
		return m, func() tea.Msg {
			session, err := waitChatGPTDeviceAuth(ctx, flow)
			if err != nil {
				return providerAuthFailedMsg{providerID: msg.providerID, err: err}
			}
			return providerAuthSucceededMsg{providerID: msg.providerID, session: &session}
		}
	case *copilot.DeviceCode:
		clientID := m.config.CopilotClientID
		return m, func() tea.Msg {
			token, err := waitCopilotDeviceAuth(ctx, clientID, flow)
			if err != nil {
				return providerAuthFailedMsg{providerID: msg.providerID, err: err}
			}
			return providerAuthSucceededMsg{providerID: msg.providerID, token: token}
		}
	default:
		return m, nil
	}
}

func (m ChatModel) handleProviderAuthSucceeded(msg providerAuthSucceededMsg) (tea.Model, tea.Cmd) {
	if m.providerAuthCancel != nil {
		m.providerAuthCancel()
		m.providerAuthCancel = nil
	}
	tokens, err := auth.Load()
	if err != nil {
		m.providerAuthWaiting = false
		m.providerStatus = fmt.Sprintf("save failed: %v", err)
		return m, nil
	}
	switch strings.ToLower(strings.TrimSpace(msg.providerID)) {
	case "chatgpt":
		if msg.session == nil {
			m.providerAuthWaiting = false
			m.providerStatus = "save failed: missing ChatGPT session"
			return m, nil
		}
		tokens = chatgptauth.StoreSession(tokens, *msg.session)
	case "copilot":
		tokens.CopilotToken = strings.TrimSpace(msg.token)
	}
	if err := auth.Save(tokens); err != nil {
		m.providerAuthWaiting = false
		m.providerStatus = fmt.Sprintf("save failed: %v", err)
		return m, nil
	}
	m.providerAuthWaiting = false
	m.providerAuthURL = ""
	m.providerAuthCode = ""
	m.providerAuthProvider = ""
	m.providerStatus = "authenticated"
	if m.config.RefreshProviders != nil {
		m.providersList = append([]ProviderOption(nil), m.config.RefreshProviders()...)
	}
	m.refreshModelList()
	if m.providersCursor >= 0 && m.providersCursor < len(m.providersList) {
		provider := m.providersList[m.providersCursor]
		if provider.DefaultModel != "" && m.config.SwitchModel != nil {
			if newModel, err := m.config.SwitchModel(provider.DefaultModel); err == nil {
				m.model = newModel
				m.flash = fmt.Sprintf("authenticated and switched to %s", newModel)
			} else {
				m.flash = "authenticated"
			}
		} else {
			m.flash = "authenticated"
		}
	} else {
		m.flash = "authenticated"
	}
	return m, nil
}

func (m ChatModel) handleProviderAuthFailed(msg providerAuthFailedMsg) (tea.Model, tea.Cmd) {
	if m.providerAuthCancel != nil {
		m.providerAuthCancel = nil
	}
	m.providerAuthWaiting = false
	m.providerAuthURL = ""
	m.providerAuthCode = ""
	m.providerAuthProvider = ""
	if msg.err != nil {
		m.providerStatus = fmt.Sprintf("sign-in failed: %v", msg.err)
		m.flash = fmt.Sprintf("sign-in failed: %v", msg.err)
	} else {
		m.providerStatus = "sign-in failed"
		m.flash = "sign-in failed"
	}
	return m, nil
}

func (m ChatModel) deleteProviderCredential() (tea.Model, tea.Cmd) {
	if m.providersCursor < 0 || m.providersCursor >= len(m.providersList) {
		return m, nil
	}
	provider := m.providersList[m.providersCursor]
	tokens, err := auth.Load()
	if err != nil {
		m.providerStatus = fmt.Sprintf("delete failed: %v", err)
		return m, nil
	}
	if !providerHasStoredCredential(tokens, provider.ID) {
		m.providerStatus = "no stored credential"
		return m, nil
	}
	clearProviderToken(tokens, provider.ID)
	if err := auth.Save(tokens); err != nil {
		m.providerStatus = fmt.Sprintf("delete failed: %v", err)
		return m, nil
	}
	if m.config.RefreshProviders != nil {
		m.providersList = append([]ProviderOption(nil), m.config.RefreshProviders()...)
	}
	m.refreshModelList()
	m.providerStatus = "deleted"
	m.flash = fmt.Sprintf("deleted %s key", provider.Label)
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

func (m ChatModel) renderStatsOverlay() string {
	boxW := min(76, max(46, m.width-10))
	boxH := 12

	titleStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#56d364")).Bold(true)
	textStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#c9d1d9"))
	dimStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#8b949e"))

	duration := "n/a"
	if m.statsDuration > 0 {
		duration = fmt.Sprintf("%.1fs", m.statsDuration.Seconds())
	}
	lines := []string{
		"Duration: " + duration,
		fmt.Sprintf("Latest turn input:   %d", m.statsUsage.InputTokens),
		fmt.Sprintf("Latest turn output:  %d", m.statsUsage.OutputTokens),
		fmt.Sprintf("Session input:       %d", m.sessionUsage.InputTokens),
		fmt.Sprintf("Session output:      %d", m.sessionUsage.OutputTokens),
		fmt.Sprintf("Session total:       %d", m.sessionUsage.InputTokens+m.sessionUsage.OutputTokens),
	}
	innerLines := make([]string, 0, len(lines))
	for _, line := range lines {
		innerLines = append(innerLines, textStyle.Render(line))
	}
	inner := lipgloss.JoinVertical(lipgloss.Left,
		titleStyle.Render("Latest turn stats"),
		"",
		strings.Join(innerLines, "\n"),
		"",
		dimStyle.Render("Esc / Enter closes this overlay"),
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

func (m ChatModel) renderSearchOverlay() string {
	boxW := min(72, max(42, m.width-10))
	boxH := 7

	titleStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#56d364")).Bold(true)
	textStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#c9d1d9"))
	dimStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#8b949e"))
	inputStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#c9d1d9")).Background(lipgloss.Color("#0d1117"))

	paneName := "agent"
	if m.searchPane == focusTools {
		paneName = "tools"
	}
	status := fmt.Sprintf("%d matches", len(m.searchMatches))
	if len(m.searchMatches) == 1 {
		status = "1 match"
	}
	if len(m.searchMatches) == 0 && strings.TrimSpace(m.searchQuery) != "" {
		status = "No matches"
	}
	inner := lipgloss.JoinVertical(lipgloss.Left,
		titleStyle.Render("Search"),
		dimStyle.Render("Pane: "+paneName),
		inputStyle.Render("Query: "+m.searchQuery),
		textStyle.Render(status),
		dimStyle.Render("Enter jump • Esc close"),
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

func (m ChatModel) renderFilesOverlay() string {
	boxW := min(72, max(42, m.width-6))
	boxH := min(24, max(12, m.height-4))
	contentHeight := max(1, boxH-8)

	titleStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#56d364")).Bold(true)
	selectedStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#ffffff")).Background(lipgloss.Color("#1f6feb")).Bold(true)
	textStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#c9d1d9"))
	dimStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#8b949e"))
	inputStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#c9d1d9")).Background(lipgloss.Color("#0d1117"))

	lines := make([]string, 0, min(len(m.filesFiltered), contentHeight))
	start := 0
	if m.filesCursor >= contentHeight {
		start = m.filesCursor - contentHeight + 1
	}
	for i := 0; i < contentHeight && start+i < len(m.filesFiltered); i++ {
		idx := start + i
		line := m.filesFiltered[idx]
		if idx == m.filesCursor {
			lines = append(lines, selectedStyle.Render(line))
		} else {
			lines = append(lines, textStyle.Render(line))
		}
	}
	if len(lines) == 0 {
		lines = append(lines, dimStyle.Render("No matching files"))
	}
	inner := lipgloss.JoinVertical(lipgloss.Left,
		titleStyle.Render("Add context file (@...)"),
		inputStyle.Render("Query: "+m.filesQuery),
		"",
		lipgloss.NewStyle().Width(boxW-6).Height(contentHeight).Render(strings.Join(lines, "\n")),
		"",
		dimStyle.Render("Type to filter • Enter insert • Esc close"),
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

func (m ChatModel) renderSessionRenameOverlay() string {
	boxW := min(64, max(38, m.width-10))
	boxH := 7
	titleStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#56d364")).Bold(true)
	textStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#c9d1d9"))
	dimStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#8b949e"))
	inner := lipgloss.JoinVertical(lipgloss.Left,
		titleStyle.Render("Rename session"),
		textStyle.Render("name> "+m.sessionRenameBuf),
		"",
		dimStyle.Render("Enter save • Esc cancel"),
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
	footer := dimStyle.Render("Enter restore • r rename • d delete • 1-9 quick restore • Esc close")
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
	if m.sessionRenaming {
		return m.renderSessionRenameOverlay()
	}
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

func (m ChatModel) renderModelsOverlay() string {
	boxW := min(96, max(56, m.width-6))
	boxH := min(28, max(14, m.height-4))
	contentHeight := max(1, boxH-8)

	titleStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#56d364")).Bold(true)
	selectedStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#ffffff")).Background(lipgloss.Color("#1f6feb")).Bold(true)
	textStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#c9d1d9"))
	dimStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#8b949e"))
	inputStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#c9d1d9")).Background(lipgloss.Color("#0d1117"))

	lines := make([]string, 0, min(len(m.modelsFiltered), contentHeight))
	start := 0
	if m.modelsCursor >= contentHeight {
		start = m.modelsCursor - contentHeight + 1
	}
	for i := 0; i < contentHeight && start+i < len(m.modelsFiltered); i++ {
		idx := start + i
		line := fmt.Sprintf("%d. %s", idx+1, m.modelsFiltered[idx])
		if idx == m.modelsCursor {
			lines = append(lines, selectedStyle.Render(line))
		} else {
			lines = append(lines, textStyle.Render(line))
		}
	}
	queryLine := inputStyle.Render("Query: " + m.modelsQuery)
	if len(lines) == 0 {
		lines = append(lines, dimStyle.Render("No matching models"))
	}
	rangeText := "0 models"
	if len(m.modelsFiltered) > 0 {
		rangeText = fmt.Sprintf("%d-%d/%d", start+1, min(len(m.modelsFiltered), start+len(lines)), len(m.modelsFiltered))
	}
	footer := dimStyle.Render("Type to filter • ↑/↓ select • Enter choose • 1-9 quick select • " + rangeText + " • Esc close")
	inner := lipgloss.JoinVertical(lipgloss.Left,
		titleStyle.Render("Models"),
		queryLine,
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

func (m ChatModel) renderProvidersOverlay() string {
	boxW := min(96, max(64, m.width-6))
	boxH := min(30, max(14, m.height-4))
	contentHeight := max(1, boxH-9)

	titleStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#56d364")).Bold(true)
	selectedStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#ffffff")).Background(lipgloss.Color("#1f6feb")).Bold(true)
	textStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#c9d1d9"))
	dimStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#8b949e"))

	lines := make([]string, 0, min(len(m.providersList), contentHeight))
	start := 0
	if m.providersCursor >= contentHeight {
		start = m.providersCursor - contentHeight + 1
	}
	for i := 0; i < contentHeight && start+i < len(m.providersList); i++ {
		idx := start + i
		provider := m.providersList[idx]
		line := provider.Label
		if provider.Status != "" {
			line += " — " + provider.Status
		}
		if provider.DefaultModel != "" {
			line += " (" + provider.DefaultModel + ")"
		}
		if idx == m.providersCursor {
			lines = append(lines, selectedStyle.Render(line))
		} else {
			lines = append(lines, textStyle.Render(line))
		}
	}

	keyLine := ""
	if m.providerPromptingKey {
		keyLine = "API key: " + strings.Repeat("*", len([]rune(m.providerKeyInput)))
	}
	footerText := "↑/↓ select • Enter configure/select • d delete credential • Esc close"
	if m.providerPromptingKey {
		footerText = "Enter save key • Esc cancel"
	} else if m.providerAuthWaiting {
		footerText = "Complete sign-in in browser • Esc cancel"
	}
	authLines := []string{}
	if m.providerAuthWaiting {
		authLines = append(authLines,
			textStyle.Render("Visit: "+m.providerAuthURL),
			textStyle.Render("Code:  "+m.providerAuthCode),
		)
	}
	inner := lipgloss.JoinVertical(lipgloss.Left,
		titleStyle.Render("Providers"),
		dimStyle.Render("Select a provider. API-key providers prompt for a key; ChatGPT/Copilot can sign in here."),
		"",
		lipgloss.NewStyle().Width(boxW-6).Height(contentHeight).Render(strings.Join(lines, "\n")),
		"",
		strings.Join(authLines, "\n"),
		textStyle.Render(keyLine),
		dimStyle.Render(m.providerStatus),
		dimStyle.Render(footerText),
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
		ContextFiles: append([]string(nil), m.contextFiles...),
		SessionUsage: m.sessionUsage,
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
	m.contextFiles = append([]string(nil), s.ContextFiles...)
	m.sessionUsage = s.SessionUsage
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
	if strings.TrimSpace(m.chatContent) == "" {
		empty := []string{
			"Forge is ready.",
			"",
			"Type a coding task or use /help.",
			"Common commands: /provider, /models, /sessions, /find, /toggle tools.",
		}
		chatLines = empty
		chatTotalLines = len(empty)
	}
	chatScrollbar := scrollbarColumn(chatTotalLines, m.chatViewport.Height, m.chatViewport.YOffset, chatBodyHeight)
	chatBody := joinWithScrollbar(chatLines, chatScrollbar, chatContentWidth, chatBodyHeight)
	chatBorder := lipgloss.Color("#30363d")
	if m.paneFocus == focusChat {
		chatBorder = lipgloss.Color("#58a6ff")
	}
	chatPane := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(chatBorder).
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
		toolsBorder := lipgloss.Color("#30363d")
		if m.paneFocus == focusTools {
			toolsBorder = lipgloss.Color("#58a6ff")
		}
		toolsStyle := lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(toolsBorder).
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
			inputContent = "forge> Type a message..."
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
	if m.statsVisible {
		return m.renderStatsOverlay()
	}
	if m.searchVisible {
		return m.renderSearchOverlay()
	}
	if m.filesVisible {
		return m.renderFilesOverlay()
	}
	if m.modelsVisible {
		return m.renderModelsOverlay()
	}
	if m.providersVisible {
		return m.renderProvidersOverlay()
	}
	if m.sessionsVisible {
		return m.renderSessionsOverlay()
	}
	return base
}
