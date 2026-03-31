package tui

import (
	"fmt"
	"strings"
	"time"

	"forge/internal/llm"

	tea "github.com/charmbracelet/bubbletea"
)

// PauseToggled is emitted when the user pauses/resumes.
type PauseToggled struct{}

// SnapshotRequested is emitted when the user presses s.
type SnapshotRequested struct{ At time.Time }

// phase tracks which agent is currently active.
type phase int

const (
	phaseIdle phase = iota
	phaseWriter
	phaseAuditor
	phaseSummarizer
	phasePassDone
)

type paneFocus int

const (
	focusAI1 paneFocus = iota
	focusAI2
)

// RunningModel is the Bubble Tea model for the running screen.
type RunningModel struct {
	TotalPasses  int
	TotalRounds  int
	CurrentPass  int
	CurrentRound int
	Phase        phase
	PassName     string
	WriterBuf    string
	AuditorBuf   string
	Paused       bool
	YoloMode     bool
	YoloFeed     []string
	// kept for the yolo view
	ActiveAgent    string
	AI1Model       string
	AI2Model       string
	Width          int
	Height         int
	Focus          paneFocus
	AI1Scroll      int
	AI2Scroll      int
	writerTurnGap  bool
	auditorTurnGap bool
}

func NewRunningModel(totalPasses, totalRounds int, ai1Model, ai2Model string) RunningModel {
	return RunningModel{
		TotalPasses: totalPasses,
		TotalRounds: totalRounds,
		PassName:    "starting...",
		AI1Model:    ai1Model,
		AI2Model:    ai2Model,
		Width:       120,
		Height:      36,
	}
}

func (m RunningModel) Init() tea.Cmd { return nil }

func (m RunningModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case llm.Event:
		return m.handleEvent(msg)
	case tea.WindowSizeMsg:
		m.Width = msg.Width
		m.Height = msg.Height
		m.clampScrolls()
		return m, nil
	case tea.KeyMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

func (m RunningModel) handleEvent(ev llm.Event) (tea.Model, tea.Cmd) {
	switch ev.Kind {
	case llm.EventPassStart:
		m.CurrentPass = ev.Pass
		m.PassName = llm.PassName(ev.Pass)
		m.Phase = phaseIdle

	case llm.EventRoundStart:
		m.CurrentPass = ev.Pass
		m.CurrentRound = ev.Round
		m.Phase = phaseWriter
		m.writerTurnGap = m.WriterBuf != ""
		m.auditorTurnGap = m.AuditorBuf != ""

	case llm.EventToken:
		m.ActiveAgent = ev.Agent
		switch ev.Agent {
		case "writer":
			m.Phase = phaseWriter
			m.WriterBuf = appendTurnText(m.WriterBuf, &m.writerTurnGap, ev.Text)
		case "auditor":
			m.Phase = phaseAuditor
			m.AuditorBuf = appendTurnText(m.AuditorBuf, &m.auditorTurnGap, ev.Text)
		case "summarizer":
			m.Phase = phaseSummarizer
		}
		if m.YoloMode {
			m.YoloFeed = appendYolo(m.YoloFeed, ev.Agent, ev.Text)
		}

	case llm.EventRoundEnd:
		m.CurrentPass = ev.Pass
		m.CurrentRound = ev.Round
		m.Phase = phaseSummarizer

	case llm.EventPassEnd:
		m.CurrentPass = ev.Pass
		m.Phase = phasePassDone
	}
	m.clampScrolls()
	return m, nil
}

func (m RunningModel) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case KeyToggleView:
		m.YoloMode = !m.YoloMode
	case "left", "h":
		m.Focus = focusAI1
	case "right", "l":
		m.Focus = focusAI2
	case "up", "k":
		m.scrollFocused(-1)
	case "down", "j":
		m.scrollFocused(1)
	case "pgup", "b":
		m.scrollFocused(-max(1, m.bodyHeight()/2))
	case "pgdown", "f":
		m.scrollFocused(max(1, m.bodyHeight()/2))
	case "home":
		m.setFocusedScroll(0)
	case "end":
		m.setFocusedScroll(m.maxScroll(m.focusedPaneLines()))
	case KeyPause:
		m.Paused = !m.Paused
		return m, func() tea.Msg { return PauseToggled{} }
	case KeySnapshot:
		return m, func() tea.Msg { return SnapshotRequested{At: time.Now()} }
	case KeyQuit:
		return m, tea.Quit
	}
	return m, nil
}

func (m RunningModel) View() string {
	if m.YoloMode {
		return m.yoloView()
	}
	return m.splitView()
}

func (m RunningModel) statusLine() string {
	passLabel := fmt.Sprintf("Pass %d/%d: %s", m.CurrentPass, m.TotalPasses, m.PassName)
	roundLabel := fmt.Sprintf("round %d/%d", m.CurrentRound, m.TotalRounds)

	var activity string
	switch m.Phase {
	case phaseIdle:
		activity = "starting..."
	case phaseWriter:
		activity = "* writer"
	case phaseAuditor:
		activity = "* auditor"
	case phaseSummarizer:
		activity = "summarizing"
	case phasePassDone:
		activity = "pass done"
	}

	if m.Paused {
		activity = "paused"
	}

	return passLabel + "  " + roundLabel + "  " + activity
}

func (m RunningModel) splitView() string {
	width := max(20, m.Width)
	height := max(3, m.Height-1)
	status := fitCell(m.statusLine(), width)
	bodyHeight := m.bodyHeight()
	leftWidth, rightWidth := m.paneWidths()
	writerPane := m.renderPane("AI-1", m.ai1Subheader(), m.writerContent(), leftWidth, bodyHeight, m.AI1Scroll, m.Focus == focusAI1)
	auditorPane := m.renderPane("AI-2", m.ai2Subheader(), m.auditorContent(), rightWidth, bodyHeight, m.AI2Scroll, m.Focus == focusAI2)

	lines := make([]string, 0, height)
	lines = append(lines, status)
	lines = append(lines, strings.Split(joinColumns(writerPane, auditorPane, leftWidth, rightWidth), "\n")...)
	for len(lines) < height {
		lines = append(lines, strings.Repeat(" ", width))
	}
	if len(lines) > height {
		lines = lines[:height]
	}
	return paintRows(lines, width)
}

func (m RunningModel) yoloView() string {
	var sb strings.Builder
	sb.WriteString(m.statusLine() + "     [v] split view\n\n")
	for _, line := range m.YoloFeed {
		sb.WriteString(line + "\n")
	}
	sb.WriteString("\n[v] split  [p] pause  [s] snapshot  [q] quit\n")
	return sb.String()
}

func appendYolo(feed []string, agent, text string) []string {
	prefix := "WRITER: "
	if agent == "auditor" {
		prefix = "AUDITOR: "
	}
	if len(feed) > 0 && strings.HasPrefix(feed[len(feed)-1], prefix) {
		feed[len(feed)-1] += text
		return feed
	}
	return append(feed, prefix+text)
}

func appendTurnText(buf string, needGap *bool, text string) string {
	if text == "" {
		return buf
	}
	if *needGap {
		buf += "\n\n"
		*needGap = false
	}
	return buf + text
}

func (m RunningModel) writerContent() string {
	if m.WriterBuf != "" {
		return m.WriterBuf
	}
	switch m.Phase {
	case phaseWriter:
		return "…"
	case phaseAuditor, phaseSummarizer, phasePassDone:
		return "(done)"
	default:
		return "(waiting)"
	}
}

func (m RunningModel) auditorContent() string {
	if m.AuditorBuf != "" {
		return m.AuditorBuf
	}
	switch m.Phase {
	case phaseWriter, phaseIdle:
		return "(waiting for AI-1)"
	case phaseAuditor:
		return "…"
	case phaseSummarizer, phasePassDone:
		return "(done)"
	default:
		return "(waiting)"
	}
}

func (m RunningModel) ai1Subheader() string {
	if m.AI1Model == "" {
		return "writer"
	}
	return "writer • " + m.AI1Model
}

func (m RunningModel) ai2Subheader() string {
	if m.AI2Model == "" {
		return "auditor"
	}
	return "auditor • " + m.AI2Model
}

func (m RunningModel) bodyHeight() int {
	if m.Height <= 0 {
		return 12
	}
	reserved := 2
	body := m.Height - reserved
	if body < 3 {
		body = 3
	}
	return body
}

func (m RunningModel) paneWidth() int {
	left, _ := m.paneWidths()
	return left
}

func (m RunningModel) paneWidths() (int, int) {
	width := m.Width
	if width <= 0 {
		width = 80
	}
	if width < 20 {
		width = 20
	}
	leftWidth := (width - 1) / 2
	rightWidth := width - 1 - leftWidth
	if leftWidth < 8 {
		leftWidth = 8
	}
	if rightWidth < 8 {
		rightWidth = 8
	}
	return leftWidth, rightWidth
}

func wrapContent(content string, width int) []string {
	if width < 1 {
		return []string{content}
	}
	var lines []string
	for _, rawLine := range strings.Split(content, "\n") {
		lines = append(lines, wrapLine(rawLine, width)...)
	}
	if len(lines) == 0 {
		return []string{""}
	}
	return lines
}

func wrapLine(line string, width int) []string {
	if width < 1 {
		return []string{line}
	}
	runes := []rune(line)
	if len(runes) == 0 {
		return []string{""}
	}
	var out []string
	for len(runes) > width {
		out = append(out, string(runes[:width]))
		runes = runes[width:]
	}
	out = append(out, string(runes))
	return out
}

func joinColumns(left, right []string, leftWidth, rightWidth int) string {
	_ = leftWidth
	_ = rightWidth
	leftLines := append([]string(nil), left...)
	rightLines := append([]string(nil), right...)
	totalLines := max(len(leftLines), len(rightLines))
	for len(leftLines) < totalLines {
		leftLines = append(leftLines, "")
	}
	for len(rightLines) < totalLines {
		rightLines = append(rightLines, "")
	}
	out := make([]string, 0, totalLines)
	for i := 0; i < totalLines; i++ {
		out = append(out, leftLines[i]+"|"+rightLines[i])
	}
	return strings.Join(out, "\n")
}

func fitSingleLine(s string, width int) string {
	return fitCell(s, width)
}

func fitCell(s string, width int) string {
	if width <= 0 {
		return ""
	}
	runes := []rune(s)
	if len(runes) > width {
		runes = runes[:width]
	}
	if len(runes) < width {
		return string(runes) + strings.Repeat(" ", width-len(runes))
	}
	return string(runes)
}

func (m RunningModel) renderPane(title, subtitle, content string, width, height, scroll int, focused bool) []string {
	theme := defaultSemanticTheme()
	lines := make([]string, 0, max(1, height))
	label := title
	if focused {
		label = "[" + title + "]"
	}
	lines = append(lines, fitCell(label, width))
	if height == 1 {
		return lines
	}
	lines = append(lines, fitCell(subtitle, width))
	if height == 2 {
		return lines
	}
	bodyHeight := height - 2
	bodyLines := wrapContent(content, width)
	scroll = clamp(scroll, 0, m.maxScroll(bodyLines))
	end := min(len(bodyLines), scroll+bodyHeight)
	visible := bodyLines[scroll:end]
	for _, line := range visible {
		styled := RenderSemanticPlain(line, profileStatus, theme)
		lines = append(lines, padStyledWidth(styled, width))
	}
	for len(lines) < height {
		lines = append(lines, strings.Repeat(" ", width))
	}
	return lines
}

func (m RunningModel) focusedPaneLines() []string {
	if m.Focus == focusAI2 {
		return wrapContent(m.auditorContent(), m.rightPaneWidth())
	}
	return wrapContent(m.writerContent(), m.leftPaneWidth())
}

func (m RunningModel) leftPaneWidth() int {
	left, _ := m.paneWidths()
	return left
}

func (m RunningModel) rightPaneWidth() int {
	_, right := m.paneWidths()
	return right
}

func (m RunningModel) maxScroll(lines []string) int {
	maxScroll := len(lines) - max(1, m.bodyHeight()-2)
	if maxScroll < 0 {
		return 0
	}
	return maxScroll
}

func (m *RunningModel) scrollFocused(delta int) {
	if m.Focus == focusAI2 {
		m.AI2Scroll = clamp(m.AI2Scroll+delta, 0, m.maxScroll(wrapContent(m.auditorContent(), m.rightPaneWidth())))
		return
	}
	m.AI1Scroll = clamp(m.AI1Scroll+delta, 0, m.maxScroll(wrapContent(m.writerContent(), m.leftPaneWidth())))
}

func (m *RunningModel) setFocusedScroll(pos int) {
	if m.Focus == focusAI2 {
		m.AI2Scroll = clamp(pos, 0, m.maxScroll(wrapContent(m.auditorContent(), m.rightPaneWidth())))
		return
	}
	m.AI1Scroll = clamp(pos, 0, m.maxScroll(wrapContent(m.writerContent(), m.leftPaneWidth())))
}

func (m *RunningModel) clampScrolls() {
	m.AI1Scroll = clamp(m.AI1Scroll, 0, m.maxScroll(wrapContent(m.writerContent(), m.leftPaneWidth())))
	m.AI2Scroll = clamp(m.AI2Scroll, 0, m.maxScroll(wrapContent(m.auditorContent(), m.rightPaneWidth())))
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func clamp(v, low, high int) int {
	if v < low {
		return low
	}
	if v > high {
		return high
	}
	return v
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func paintRows(lines []string, width int) string {
	var b strings.Builder
	b.WriteString("\x1b[2J\x1b[H")
	for i, line := range lines {
		if i > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(padStyledWidth(line, width))
	}
	return b.String()
}
