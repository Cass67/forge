package tui

import (
    "fmt"
    "strings"

    tea "github.com/charmbracelet/bubbletea"
    "github.com/charmbracelet/lipgloss"
)

// CheckResult is sent as a Bubble Tea message when a startup check completes.
type CheckResult struct {
    Name   string
    OK     bool
    Detail string
}

// StartupModel is the Bubble Tea model for the startup check screen.
type StartupModel struct {
    Results []CheckResult
    Done    bool
    Failed  bool
}

func NewStartupModel() StartupModel {
    return StartupModel{}
}

func (m StartupModel) Init() tea.Cmd { return nil }

func (m StartupModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
    switch msg := msg.(type) {
    case CheckResult:
        m.Results = append(m.Results, msg)
        if !msg.OK {
            m.Failed = true
        }
        return m, nil
    case tea.KeyMsg:
        if isQuit(msg) {
            return m, tea.Quit
        }
    }
    return m, nil
}

var (
    okStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("10"))
    errStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("9"))
    waitStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("11"))
)

func (m StartupModel) View() string {
    var sb strings.Builder
    sb.WriteString("forge v0.1.0\n\nChecking configuration...\n\n")
    for _, r := range m.Results {
        if r.OK {
            sb.WriteString(okStyle.Render("✓ "+r.Name+"\n"))
        } else {
            sb.WriteString(errStyle.Render(fmt.Sprintf("✗ %s: %s\n", r.Name, r.Detail)))
        }
    }
    if m.Failed {
        sb.WriteString(errStyle.Render("\nCheck your API keys and try again.  (q) quit\n"))
    } else {
        sb.WriteString(waitStyle.Render("\n● Checking...\n"))
    }
    return sb.String()
}
