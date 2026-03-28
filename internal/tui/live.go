package tui

import (
	"fmt"
	"strings"

	"forge/internal/llm"
	"forge/internal/output"
	"forge/internal/session"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/reflow/wordwrap"
)

type LiveConfig struct {
	WriterModel  string
	AuditorModel string
	Gate         *session.TurnGate
}

type LiveResult struct {
	Aborted           bool
	FeedbackRequested bool
	FeedbackPass      int
	FeedbackPassName  string
	OutputDir         string
	Err               error
}

type liveEventsClosedMsg struct{}

type liveModel struct {
	writerModel    string
	auditorModel   string
	width          int
	height         int
	currentPass    int
	totalPasses    int
	currentRound   int
	totalRounds    int
	passName       string
	phase          string
	writerBuf      string
	auditorBuf     string
	writerScroll   int
	auditorScroll  int
	focusRight     bool
	manualMode     bool
	waitingAdvance bool
	waitingAgent   string
	writerTurnGap  bool
	auditorTurnGap bool
	gate           *session.TurnGate
	outputDir      string
	result         LiveResult
}

func RunLive(events <-chan llm.Event, totalPasses, totalRounds int, cfg LiveConfig, outputDir string) LiveResult {
	model := liveModel{
		writerModel:  cfg.WriterModel,
		auditorModel: cfg.AuditorModel,
		manualMode:   cfg.Gate != nil && cfg.Gate.Enabled(),
		totalPasses:  totalPasses,
		totalRounds:  totalRounds,
		passName:     "starting",
		phase:        "starting",
		gate:         cfg.Gate,
		outputDir:    outputDir,
		result:       LiveResult{OutputDir: outputDir},
	}

	p := tea.NewProgram(model, tea.WithAltScreen())
	go func() {
		for ev := range events {
			p.Send(ev)
		}
		p.Send(liveEventsClosedMsg{})
	}()

	finalModel, _ := p.Run()
	return finalModel.(liveModel).result
}

func (m liveModel) Init() tea.Cmd {
	return nil
}

func (m liveModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil
	case tea.KeyMsg:
		switch msg.Type {
		case tea.KeyLeft:
			m.focusRight = false
		case tea.KeyRight:
			m.focusRight = true
		case tea.KeyUp:
			m.scrollFocused(-1)
		case tea.KeyDown:
			m.scrollFocused(1)
		case tea.KeyPgUp:
			m.scrollFocused(-(m.bodyHeight() / 2))
		case tea.KeyPgDown:
			m.scrollFocused(m.bodyHeight() / 2)
		case tea.KeyHome:
			m.setFocusedScroll(0)
		case tea.KeyEnd:
			m.setFocusedScroll(m.focusedMaxScroll())
		case tea.KeyEnter:
			if m.gate != nil && m.waitingAdvance {
				m.gate.Advance()
				m.waitingAdvance = false
				m.waitingAgent = ""
			}
		case tea.KeyEsc:
			m.result = LiveResult{Aborted: true, OutputDir: m.outputDir}
			return m, tea.Quit
		case tea.KeyRunes:
			switch string(msg.Runes) {
			case "q":
				m.result = LiveResult{Aborted: true, OutputDir: m.outputDir}
				return m, tea.Quit
			case "m":
				if m.gate != nil {
					m.manualMode = m.gate.Toggle()
					if !m.manualMode {
						m.waitingAdvance = false
						m.waitingAgent = ""
					}
				}
			case " ", "c":
				if m.gate != nil && m.waitingAdvance {
					m.gate.Advance()
					m.waitingAdvance = false
					m.waitingAgent = ""
				}
			}
		}
		return m, nil
	case llm.Event:
		switch msg.Kind {
		case llm.EventPassStart:
			m.currentPass = msg.Pass
			if msg.PassName != "" {
				m.passName = msg.PassName
			} else {
				m.passName = llm.PassName(msg.Pass)
			}
			m.phase = "starting"
		case llm.EventRoundStart:
			m.currentPass = msg.Pass
			m.currentRound = msg.Round
			m.phase = "writer"
			m.writerTurnGap = m.writerBuf != ""
			m.auditorTurnGap = m.auditorBuf != ""
		case llm.EventToken:
			switch msg.Agent {
			case "writer":
				m.phase = "writer"
				m.writerBuf = appendTurnText(m.writerBuf, &m.writerTurnGap, msg.Text)
				m.writerScroll = m.writerMaxScroll()
			case "auditor":
				m.phase = "auditor"
				m.auditorBuf = appendTurnText(m.auditorBuf, &m.auditorTurnGap, msg.Text)
				m.auditorScroll = m.auditorMaxScroll()
			case "summarizer":
				m.phase = "summarizing"
			}
		case llm.EventRoundEnd:
			m.currentPass = msg.Pass
			m.currentRound = msg.Round
			m.phase = "summarizing"
		case llm.EventAgentDone:
			m.waitingAdvance = m.manualMode
			m.waitingAgent = msg.Agent
		case llm.EventPassEnd:
			m.currentPass = msg.Pass
			m.phase = "pass done"
		case llm.EventFeedbackRequest:
			m.result = LiveResult{
				FeedbackRequested: true,
				FeedbackPass:      msg.Pass,
				FeedbackPassName:  msg.PassName,
				OutputDir:         m.outputDir,
			}
			return m, tea.Quit
		case llm.EventDone:
			m.result = LiveResult{OutputDir: m.outputDir}
			return m, tea.Quit
		case llm.EventAbort, llm.EventError:
			m.result = LiveResult{Aborted: true, OutputDir: m.outputDir, Err: fmt.Errorf("%s", eventErrorMessage(msg))}
			return m, tea.Quit
		}
		return m, nil
	case liveEventsClosedMsg:
		return m, tea.Quit
	}
	return m, nil
}

func (m liveModel) View() string {
	if m.width == 0 || m.height == 0 {
		return "Initializing..."
	}

	header := lipgloss.NewStyle().
		Background(lipgloss.Color("#000000")).
		Foreground(lipgloss.Color("#b1bac4")).
		Width(m.width).
		Render(m.statusLine())

	leftWidth := max(20, (m.width-1)/2)
	rightWidth := max(20, m.width-leftWidth)
	bodyHeight := m.bodyHeight()
	leftPane := m.renderPane("A", m.writerModel, m.writerBuf, leftWidth, bodyHeight, m.writerScroll, !m.focusRight)
	rightPane := m.renderPane("B", m.auditorModel, m.auditorBuf, rightWidth, bodyHeight, m.auditorScroll, m.focusRight)
	body := lipgloss.JoinHorizontal(lipgloss.Top, leftPane, rightPane)

	footer := lipgloss.NewStyle().
		Background(lipgloss.Color("#161b22")).
		Foreground(lipgloss.Color("#8b949e")).
		Width(m.width).
		Render(m.helpLine())

	return lipgloss.JoinVertical(lipgloss.Left, header, body, footer)
}

func (m liveModel) renderPane(label, modelName, content string, width, height, scroll int, focused bool) string {
	theme := defaultSemanticTheme()
	titleColor := lipgloss.Color("#8b949e")
	borderColor := lipgloss.Color("#30363d")
	if focused {
		titleColor = lipgloss.Color("#56d364")
		borderColor = lipgloss.Color("#56d364")
	}

	title := lipgloss.NewStyle().Foreground(titleColor).Bold(true).Render(label + " " + modelName)
	bodyWidth := max(1, width-4)
	lines := wrapPlain(foldForDisplay(content), bodyWidth)
	if len(lines) == 0 {
		lines = []string{""}
	}
	scroll = clamp(scroll, 0, max(0, len(lines)-height))
	end := min(len(lines), scroll+height)
	visible := append([]string(nil), lines[scroll:end]...)
	for i, line := range visible {
		visible[i] = RenderSemanticPlain(line, profileStatus, theme)
	}
	scrollbar := scrollbarColumn(len(lines), height, scroll, height)
	contentBody := joinWithScrollbar(visible, scrollbar, bodyWidth, height)

	inner := lipgloss.JoinVertical(
		lipgloss.Left,
		title,
		lipgloss.NewStyle().Width(max(1, width-2)).Height(height).Render(contentBody),
	)
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(borderColor).
		Background(lipgloss.Color("#0d1117")).
		Foreground(lipgloss.Color("#f0f6fc")).
		Width(max(1, width-2)).
		Height(max(1, height+1)).
		Render(inner)
}

func (m *liveModel) statusLine() string {
	mode := "manual:off"
	if m.manualMode {
		mode = "manual:on"
	}
	wait := ""
	if m.waitingAdvance {
		wait = "  waiting:" + m.waitingAgent
	}
	return fmt.Sprintf("pass %d/%d %s  round %d/%d  %s  %s%s", m.currentPass, m.totalPasses, m.passName, m.currentRound, m.totalRounds, m.phase, mode, wait)
}

func (m *liveModel) bodyHeight() int {
	if m.height <= 2 {
		return 3
	}
	return max(3, m.height-4)
}

func (m *liveModel) helpLine() string {
	if m.waitingAdvance {
		return "left/right focus  up/down scroll  enter/space continue  q quit"
	}
	return "left/right focus  up/down scroll  q quit"
}

func (m *liveModel) scrollFocused(delta int) {
	if m.focusRight {
		m.auditorScroll = clamp(m.auditorScroll+delta, 0, m.auditorMaxScroll())
		return
	}
	m.writerScroll = clamp(m.writerScroll+delta, 0, m.writerMaxScroll())
}

func (m *liveModel) setFocusedScroll(pos int) {
	if m.focusRight {
		m.auditorScroll = clamp(pos, 0, m.auditorMaxScroll())
		return
	}
	m.writerScroll = clamp(pos, 0, m.writerMaxScroll())
}

func (m *liveModel) focusedMaxScroll() int {
	if m.focusRight {
		return m.auditorMaxScroll()
	}
	return m.writerMaxScroll()
}

func (m *liveModel) writerMaxScroll() int {
	return max(0, len(wrapPlain(foldForDisplay(m.writerBuf), max(1, max(20, (m.width-1)/2)-4)))-m.bodyHeight())
}

func (m *liveModel) auditorMaxScroll() int {
	return max(0, len(wrapPlain(foldForDisplay(m.auditorBuf), max(1, max(20, m.width-max(20, (m.width-1)/2))-4)))-m.bodyHeight())
}

func wrapPlain(s string, width int) []string {
	if width < 1 {
		return []string{""}
	}
	wrapped := wordwrap.String(s, width)
	return strings.Split(wrapped, "\n")
}

func foldForDisplay(text string) string {
	blocks := output.ParseCodeBlocks(text)
	if len(blocks) == 0 {
		return text
	}

	lines := strings.Split(text, "\n")
	var out []string
	i := 0
	for i < len(lines) {
		line := lines[i]
		if !strings.HasPrefix(line, "```") {
			out = append(out, line)
			i++
			continue
		}

		header := strings.TrimPrefix(line, "```")
		colon := strings.Index(header, ":")
		if colon < 0 {
			out = append(out, line)
			i++
			continue
		}

		filename := header[colon+1:]
		i++
		codeLines := 0
		for i < len(lines) && lines[i] != "```" {
			codeLines++
			i++
		}
		if i < len(lines) && lines[i] == "```" {
			i++
		}
		if filename == "" {
			filename = "unnamed"
		}
		out = append(out, fmt.Sprintf("[code: %s %d lines]", filename, codeLines))
	}

	return strings.TrimSpace(strings.Join(out, "\n"))
}
