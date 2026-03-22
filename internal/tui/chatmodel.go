package tui

import (
	"fmt"
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

	toolsBuf     string
	toolsVisible bool

	busy           bool
	status         string
	flash          string
	skills         []skills.Skill
	autoSkillsMode string
	state          *chatstate.State
	lowContrast    bool

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
		m.messages = append(m.messages, ChatMessage{Kind: MsgAgent})
	}
	m.messages[len(m.messages)-1].Content += text
	m.refreshViewport()
}

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

	case llm.Event:
		return m.handleLLMEvent(msg)

	case chatApprovalMsg:
		action := tools.Action(msg)
		m.pendingApproval = &action
		return m, nil

	case tea.KeyMsg:
		return m.handleKey(msg)
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
		m.toolsBuf += fmt.Sprintf("● %s %s\n", ev.Text, ev.Content)
	case llm.EventToolResult:
		m.toolsBuf += fmt.Sprintf("  → %s\n", truncate(ev.Text, 120))
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

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "…"
}

func (m ChatModel) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Handle approval mode
	if m.pendingApproval != nil {
		switch {
		case msg.Type == tea.KeyRunes && string(msg.Runes) == "y":
			if m.responseCh != nil {
				m.responseCh <- true
			}
			m.pendingApproval = nil
		case msg.Type == tea.KeyRunes && string(msg.Runes) == "n":
			if m.responseCh != nil {
				m.responseCh <- false
			}
			m.pendingApproval = nil
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
			m.inputCh <- "__cancel_turn__"
			m.flash = "canceling..."
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
		// Check for skill activation before built-in commands
		cmd := strings.TrimPrefix(input, "/")
		if s, ok := skills.Get(m.skills, cmd); ok {
			return m.submitSkillInput(s, fmt.Sprintf("/%s", s.Name), skills.SkillMessage(s))
		}
		return m.handleSlashCommand(input)
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
		m.inputCh <- input
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
		m.inputCh <- msg
	}
	return m, nil
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
		m.flash = "commands: /clear /exit /model <name> /models /skills /theme /tools"
	case input == "/theme":
		m.lowContrast = !m.lowContrast
		m.refreshViewport()
		if m.lowContrast {
			m.flash = "theme: low contrast"
		} else {
			m.flash = "theme: default"
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
		m.flash = sb.String()
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
		m.flash = sb.String()
	default:
		m.flash = "unknown command: " + input
	}
	return m, nil
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
	header := headerStyle.Render("forge • " + m.model + " • " + m.workDir)

	chatPane := m.chatViewport.View()

	// Side-by-side with tools pane if visible
	if m.toolsVisible && m.toolsBuf != "" {
		toolsWidth := m.width - m.chatPaneWidth()
		toolsStyle := lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("#30363d")).
			Background(lipgloss.Color("#0d1117")).
			Foreground(lipgloss.Color("#8b949e")).
			Width(toolsWidth - 4).
			Height(m.chatViewport.Height - 2)
		toolsPane := toolsStyle.Render(m.toolsBuf)
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

	// Status bar with flash message
	statusText := m.status
	if m.flash != "" {
		statusText = m.flash
	}
	statusStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#484f58")).
		Width(m.width)
	statusBar := statusStyle.Render(statusText)

	return lipgloss.JoinVertical(lipgloss.Left,
		header,
		chatPane,
		inputBox,
		statusBar,
	)
}
