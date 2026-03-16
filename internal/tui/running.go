package tui

import (
    "fmt"
    "strings"
    "time"

    tea "github.com/charmbracelet/bubbletea"
    "github.com/charmbracelet/lipgloss"
    "forge/internal/llm"
)

// PauseToggled is emitted when the user pauses/resumes.
type PauseToggled struct{}

// SnapshotRequested is emitted when the user presses s.
type SnapshotRequested struct{ At time.Time }

// RunningModel is the Bubble Tea model for the running screen.
type RunningModel struct {
    TotalPasses  int
    TotalRounds  int
    CurrentPass  int
    CurrentRound int
    YoloMode     bool
    Paused       bool
    WriterBuf    string
    AuditorBuf   string
    YoloFeed     []string
    ActiveAgent  string
}

func NewRunningModel(totalPasses, totalRounds int) RunningModel {
    return RunningModel{TotalPasses: totalPasses, TotalRounds: totalRounds}
}

func (m RunningModel) Init() tea.Cmd { return nil }

func (m RunningModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
    switch msg := msg.(type) {
    case llm.Event:
        return m.handleEvent(msg)
    case tea.KeyMsg:
        return m.handleKey(msg)
    }
    return m, nil
}

func (m RunningModel) handleEvent(ev llm.Event) (tea.Model, tea.Cmd) {
    switch ev.Kind {
    case llm.EventToken:
        if ev.Agent == "writer" {
            m.WriterBuf += ev.Text
            m.ActiveAgent = "writer"
        } else {
            m.AuditorBuf += ev.Text
            m.ActiveAgent = "auditor"
        }
        if m.YoloMode {
            m.YoloFeed = appendYolo(m.YoloFeed, ev.Agent, ev.Text)
        }
    case llm.EventRoundEnd:
        m.CurrentPass = ev.Pass
        m.CurrentRound = ev.Round
        m.WriterBuf = ""
        m.AuditorBuf = ""
        m.ActiveAgent = ""
    case llm.EventPassEnd:
        m.CurrentPass = ev.Pass
    }
    return m, nil
}

func (m RunningModel) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
    switch msg.String() {
    case KeyToggleView:
        m.YoloMode = !m.YoloMode
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

var (
    paneStyle    = lipgloss.NewStyle().Border(lipgloss.NormalBorder()).Padding(0, 1)
    headerStyle  = lipgloss.NewStyle().Bold(true)
    writerColor  = lipgloss.NewStyle().Foreground(lipgloss.Color("12"))
    auditorColor = lipgloss.NewStyle().Foreground(lipgloss.Color("11"))
)

func (m RunningModel) View() string {
    if m.YoloMode {
        return m.yoloView()
    }
    return m.splitView()
}

func (m RunningModel) splitView() string {
    passStr := fmt.Sprintf("PASS %d/%d  round %d/%d", m.CurrentPass, m.TotalPasses, m.CurrentRound, m.TotalRounds)
    writerPane := paneStyle.Render(headerStyle.Render(passStr) + "\n\n" + m.WriterBuf)
    auditHeader := fmt.Sprintf("AUDIT  round %d/%d", m.CurrentRound, m.TotalRounds)
    auditorContent := m.AuditorBuf
    if m.ActiveAgent == "writer" {
        auditorContent = "(waiting for writer)"
    }
    auditorPane := paneStyle.Render(headerStyle.Render(auditHeader) + "\n\n" + auditorContent)
    return lipgloss.JoinHorizontal(lipgloss.Top, writerPane, auditorPane) +
        "\n[v] yolo  [p] pause  [s] snapshot  [q] quit\n"
}

func (m RunningModel) yoloView() string {
    var sb strings.Builder
    sb.WriteString(fmt.Sprintf("PASS %d/%d: round %d/%d     [v] split view\n\n",
        m.CurrentPass, m.TotalPasses, m.CurrentRound, m.TotalRounds))
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

// keep colors used to avoid unused variable warning
var _, _ = writerColor, auditorColor
