package tui

import (
	"os/exec"
	"runtime"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// NewSessionRequested is emitted when the user presses n.
type NewSessionRequested struct{}

// DoneModel is the Bubble Tea model for both done and abort screens.
type DoneModel struct {
	OutputDir   string
	Aborted     bool
	AbortReason string
}

func NewDoneModel(outputDir string, aborted bool, reason string) DoneModel {
	return DoneModel{OutputDir: outputDir, Aborted: aborted, AbortReason: reason}
}

func (m DoneModel) Init() tea.Cmd { return nil }

func (m DoneModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	msg2, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	switch msg2.String() {
	case KeyQuit:
		return m, tea.Quit
	case KeyOpen:
		return m, openDir(m.OutputDir)
	case KeyNewSession:
		return m, func() tea.Msg { return NewSessionRequested{} }
	}
	return m, nil
}

func (m DoneModel) View() string {
	var sb strings.Builder
	sb.WriteString(styleBold.Render("forge") + "  " + styleDim.Render("v0.1.0") + "\n\n")
	if m.Aborted {
		sb.WriteString(styleRed.Render("✗") + styleBright.Render(" Session aborted") + "\n")
		if m.AbortReason != "" {
			sb.WriteString(styleDim.Render("Reason: ") + styleRed.Render(m.AbortReason) + "\n")
		}
		sb.WriteString("\n" + styleDim.Render("Press (n) to return to setup with the same prompt so you can choose different models.") + "\n")
	} else {
		sb.WriteString(styleGreen.Render("✓") + styleBright.Render(" Session complete") + "\n")
	}
	sb.WriteString("\n" + styleDim.Render("Output  ") + styleMid.Render(m.OutputDir) + "\n")
	sb.WriteString("\n" + styleDim.Render("o  open in Finder   n  new session   q  quit") + "\n")
	return sb.String()
}

func openDir(path string) tea.Cmd {
	return func() tea.Msg {
		var cmd *exec.Cmd
		switch runtime.GOOS {
		case "darwin":
			cmd = exec.Command("open", path)
		default:
			cmd = exec.Command("xdg-open", path)
		}
		cmd.Start()
		return nil
	}
}
