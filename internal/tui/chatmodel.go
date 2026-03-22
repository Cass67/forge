package tui

import (
	"fmt"
	"strings"
	"time"

	"forge/internal/chatstate"
	"forge/internal/llm"
	"forge/internal/skills"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type chatTickMsg time.Time

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

	inputCh chan<- string
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

	if input == "/exit" || input == "/quit" {
		return m, tea.Quit
	}

	if strings.HasPrefix(input, "/") {
		return m.handleSlashCommand(input)
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

func (m ChatModel) handleSlashCommand(input string) (tea.Model, tea.Cmd) {
	m.inputBuf = ""
	m.inputPos = 0

	switch input {
	case "/clear":
		m.messages = nil
		m.refreshViewport()
		m.flash = "conversation cleared"
	case "/help":
		m.flash = "help: /clear /exit /model /skills /theme"
	case "/theme":
		m.lowContrast = !m.lowContrast
		m.refreshViewport()
		if m.lowContrast {
			m.flash = "theme: low contrast"
		} else {
			m.flash = "theme: default"
		}
	case "/skills":
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
