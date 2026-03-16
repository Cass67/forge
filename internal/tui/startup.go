package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
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

func (m StartupModel) View() string {
	var sb strings.Builder
	sb.WriteString(styleBold.Render("forge") + "  " + styleDim.Render("v0.1.0") + "\n\n")
	sb.WriteString(styleDim.Render("Checking configuration...") + "\n\n")
	for _, r := range m.Results {
		if r.OK {
			sb.WriteString(styleGreen.Render("✓") + styleMid.Render(" "+r.Name) + "\n")
		} else {
			sb.WriteString(styleRed.Render("✗") + styleMid.Render(" "+r.Name) + styleDim.Render(": "+r.Detail) + "\n")
		}
	}
	if m.Failed {
		sb.WriteString("\n" + styleRed.Render("Check your API keys and try again.") + styleDim.Render("  (q) quit") + "\n")
	} else {
		sb.WriteString("\n" + styleYellow.Render("●") + styleDim.Render(" Checking...") + "\n")
	}
	return sb.String()
}
