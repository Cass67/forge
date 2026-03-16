package tui

import (
    "fmt"
    "os/exec"
    "runtime"
    "strings"

    tea "github.com/charmbracelet/bubbletea"
    "github.com/charmbracelet/lipgloss"
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

var successStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("10"))

func (m DoneModel) View() string {
    var sb strings.Builder
    if m.Aborted {
        sb.WriteString("forge — session aborted\n\n")
        if m.AbortReason != "" {
            sb.WriteString(errStyle.Render("Reason: "+m.AbortReason) + "\n\n")
        }
    } else {
        sb.WriteString(successStyle.Render("forge — session complete") + "\n\n")
    }
    sb.WriteString(fmt.Sprintf("Output: %s\n\n", m.OutputDir))
    sb.WriteString("(o) open in Finder   (n) new session   (q) quit\n")
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
