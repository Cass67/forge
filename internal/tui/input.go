package tui

import (
    "fmt"
    "strconv"
    "strings"

    tea "github.com/charmbracelet/bubbletea"
    "github.com/charmbracelet/lipgloss"
)

type FocusField int

const (
    FocusPrompt FocusField = iota
    FocusWriter
    FocusAuditor
    FocusRounds
    FocusLang
)

// SessionStarted is emitted when the user presses enter to start.
type SessionStarted struct {
    Prompt       string
    WriterModel  string
    AuditorModel string
    Rounds       int
    LangHint     string
    ContextFiles []string
}

// InputModel is the Bubble Tea model for the input screen.
type InputModel struct {
    Prompt        string
    WriterModels  []string
    AuditorModels []string
    WriterIdx     int
    AuditorIdx    int
    Rounds        int
    LangHint      string
    ContextFiles  []string
    Focus         FocusField
    RoundsInput   string
    RoundsErr     string
    AttachPrompt  bool
    AttachInput   string
    Preserved     bool
}

func NewInputModel(writerModels, auditorModels []string) InputModel {
    return InputModel{
        WriterModels:  writerModels,
        AuditorModels: auditorModels,
        Rounds:        3,
        RoundsInput:   "3",
        LangHint:      "auto",
        Focus:         FocusWriter,
    }
}

func ClampRounds(n int) int {
    if n < 1 {
        return 1
    }
    if n > 10 {
        return 10
    }
    return n
}

func (m InputModel) Init() tea.Cmd { return nil }

func (m InputModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
    switch msg := msg.(type) {
    case tea.KeyMsg:
        if m.AttachPrompt {
            return m.updateAttachPrompt(msg)
        }
        switch msg.String() {
        case "q":
            return m, tea.Quit
        case "tab":
            if m.Focus == FocusWriter {
                m.Focus = FocusAuditor
            } else {
                m.Focus = FocusWriter
            }
        case "left":
            if m.Focus == FocusWriter && len(m.WriterModels) > 0 {
                m.WriterIdx = (m.WriterIdx - 1 + len(m.WriterModels)) % len(m.WriterModels)
            } else if m.Focus == FocusAuditor && len(m.AuditorModels) > 0 {
                m.AuditorIdx = (m.AuditorIdx - 1 + len(m.AuditorModels)) % len(m.AuditorModels)
            }
        case "right":
            if m.Focus == FocusWriter && len(m.WriterModels) > 0 {
                m.WriterIdx = (m.WriterIdx + 1) % len(m.WriterModels)
            } else if m.Focus == FocusAuditor && len(m.AuditorModels) > 0 {
                m.AuditorIdx = (m.AuditorIdx + 1) % len(m.AuditorModels)
            }
        case "a":
            m.AttachPrompt = true
            m.AttachInput = ""
        case "enter":
            if m.Prompt == "" {
                return m, nil
            }
            return m, func() tea.Msg {
                return SessionStarted{
                    Prompt:       m.Prompt,
                    WriterModel:  m.writerModel(),
                    AuditorModel: m.auditorModel(),
                    Rounds:       m.Rounds,
                    LangHint:     m.LangHint,
                    ContextFiles: m.ContextFiles,
                }
            }
        case "backspace":
            if len(m.Prompt) > 0 {
                m.Prompt = m.Prompt[:len(m.Prompt)-1]
            }
        default:
            if len(msg.String()) == 1 {
                m.Prompt += msg.String()
            }
        }
    }
    return m, nil
}

func (m InputModel) updateAttachPrompt(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
    switch msg.String() {
    case "esc":
        m.AttachPrompt = false
    case "enter":
        path := strings.TrimSpace(m.AttachInput)
        if path != "" {
            m.ContextFiles = append(m.ContextFiles, path)
        }
        m.AttachPrompt = false
        m.AttachInput = ""
    case "backspace":
        if len(m.AttachInput) > 0 {
            m.AttachInput = m.AttachInput[:len(m.AttachInput)-1]
        }
    default:
        m.AttachInput += msg.String()
    }
    return m, nil
}

func (m InputModel) writerModel() string {
    if len(m.WriterModels) == 0 {
        return ""
    }
    return m.WriterModels[m.WriterIdx]
}

func (m InputModel) auditorModel() string {
    if len(m.AuditorModels) == 0 {
        return ""
    }
    return m.AuditorModels[m.AuditorIdx]
}

var focusedStyle = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).Padding(0, 1)

func (m InputModel) View() string {
    var sb strings.Builder
    sb.WriteString("forge v0.1.0\n\n")
    sb.WriteString("What do you want to build?\n")
    promptLen := len(m.Prompt)
    padding := 42 - promptLen
    if padding < 0 {
        padding = 0
    }
    sb.WriteString("┌" + strings.Repeat("─", 44) + "┐\n")
    sb.WriteString("│ " + m.Prompt + "_" + strings.Repeat(" ", padding) + "│\n")
    sb.WriteString("└" + strings.Repeat("─", 44) + "┘\n\n")

    sb.WriteString("Context files: (a) attach\n")
    for _, f := range m.ContextFiles {
        sb.WriteString("  📄 " + f + "\n")
    }

    writerLabel := fmt.Sprintf("Writer:  [ %-22s ]", m.writerModel())
    auditorLabel := fmt.Sprintf("Auditor: [ %-22s ]", m.auditorModel())
    if m.Focus == FocusWriter {
        writerLabel += " ←"
    } else {
        auditorLabel += " ←"
    }
    sb.WriteString("\n" + writerLabel + "\n")
    sb.WriteString(auditorLabel + "\n\n")
    sb.WriteString(fmt.Sprintf("Rounds per pass: [%s]   Language hint: [%s]\n\n", m.RoundsInput, m.LangHint))

    if m.AttachPrompt {
        sb.WriteString(fmt.Sprintf("Attach file: [%s_]\n(enter) confirm   (esc) cancel\n", m.AttachInput))
    } else {
        sb.WriteString("(enter) Start   (tab) Shift slot focus  (←/→) Cycle\n(q) Quit\n")
    }

    if m.RoundsErr != "" {
        sb.WriteString("\n⚠  " + m.RoundsErr + "\n")
    }
    return sb.String()
}

// roundsFromInput parses and validates the rounds input string.
func roundsFromInput(s string) (int, error) {
    n, err := strconv.Atoi(s)
    if err != nil || n < 1 || n > 10 {
        return 0, fmt.Errorf("rounds must be 1–10")
    }
    return n, nil
}

// keep focusedStyle used to avoid unused import warning
var _ = focusedStyle
