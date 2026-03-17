package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// FixStarted is emitted when the user submits the fix description.
type FixStarted struct {
	Issue        string
	WriterModel  string
	AuditorModel string
}

// FixModel is the Bubble Tea model for the fix-session screen.
type FixModel struct {
	SourceOutputDir string
	Issue           string
	WriterModels    []string
	AuditorModels   []string
	WriterIdx       int
	AuditorIdx      int
	ModelFocus      int // 0 = writer, 1 = auditor
	Width           int
}

func NewFixModel(sourceOutputDir string, last SessionStarted, writerModels, auditorModels []string) FixModel {
	m := FixModel{
		SourceOutputDir: sourceOutputDir,
		WriterModels:    writerModels,
		AuditorModels:   auditorModels,
	}
	if idx := indexOf(writerModels, last.WriterModel); idx >= 0 {
		m.WriterIdx = idx
	}
	if idx := indexOf(auditorModels, last.AuditorModel); idx >= 0 {
		m.AuditorIdx = idx
	}
	return m
}

func (m FixModel) Init() tea.Cmd { return nil }

func (m FixModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if sz, ok := msg.(tea.WindowSizeMsg); ok {
		m.Width = sz.Width
		return m, nil
	}
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	switch key.String() {
	case "ctrl+c":
		return m, tea.Quit
	case "tab":
		m.ModelFocus = 1 - m.ModelFocus
	case "left":
		if m.ModelFocus == 0 && len(m.WriterModels) > 0 {
			m.WriterIdx = (m.WriterIdx - 1 + len(m.WriterModels)) % len(m.WriterModels)
		} else if m.ModelFocus == 1 && len(m.AuditorModels) > 0 {
			m.AuditorIdx = (m.AuditorIdx - 1 + len(m.AuditorModels)) % len(m.AuditorModels)
		}
	case "right":
		if m.ModelFocus == 0 && len(m.WriterModels) > 0 {
			m.WriterIdx = (m.WriterIdx + 1) % len(m.WriterModels)
		} else if m.ModelFocus == 1 && len(m.AuditorModels) > 0 {
			m.AuditorIdx = (m.AuditorIdx + 1) % len(m.AuditorModels)
		}
	case "enter":
		if strings.TrimSpace(m.Issue) != "" {
			issue := m.Issue
			writer := m.writerModel()
			auditor := m.auditorModel()
			return m, func() tea.Msg {
				return FixStarted{Issue: issue, WriterModel: writer, AuditorModel: auditor}
			}
		}
	case "backspace":
		if len(m.Issue) > 0 {
			m.Issue = m.Issue[:len(m.Issue)-1]
		}
	default:
		if len(key.String()) == 1 {
			m.Issue += key.String()
		}
	}
	return m, nil
}

func (m FixModel) writerModel() string {
	if len(m.WriterModels) == 0 {
		return ""
	}
	return m.WriterModels[m.WriterIdx]
}

func (m FixModel) auditorModel() string {
	if len(m.AuditorModels) == 0 {
		return ""
	}
	return m.AuditorModels[m.AuditorIdx]
}

func (m FixModel) View() string {
	var sb strings.Builder
	sb.WriteString(styleBold.Render("forge") + "  " + styleDim.Render("fix session") + "\n\n")

	// Show the output dir being fixed
	sb.WriteString(styleDim.Render("Source  ") + styleMid.Render(m.SourceOutputDir) + "\n\n")

	// Model rows
	for i, label := range []string{"Writer  ", "Auditor "} {
		var modelName string
		if i == 0 {
			modelName = m.writerModel()
		} else {
			modelName = m.auditorModel()
		}
		if m.ModelFocus == i {
			sb.WriteString(styleGreen.Render("▶") + " " + styleDim.Render(label) + " " + styleBright.Render(modelName))
			sb.WriteString(styleDim.Render("  ← tab · ← → cycle"))
		} else {
			sb.WriteString("  " + styleDim.Render(label) + " " + styleMid.Render(modelName))
		}
		sb.WriteString("\n")
	}

	// Issue box
	boxWidth := m.Width - 4
	if boxWidth < 40 {
		boxWidth = 40
	}
	if boxWidth > 100 {
		boxWidth = 100
	}
	innerWidth := boxWidth
	dashes := strings.Repeat("─", boxWidth+2)

	sb.WriteString("\n")
	sb.WriteString(styleDimGreen.Render("what went wrong?") + "\n")
	sb.WriteString(styleDim.Render("┌"+dashes+"┐") + "\n")

	remaining := append([]rune(m.Issue), '_')
	for len(remaining) > 0 {
		var chunk []rune
		if len(remaining) >= innerWidth {
			chunk = remaining[:innerWidth]
			remaining = remaining[innerWidth:]
		} else {
			chunk = remaining
			remaining = nil
		}
		pad := strings.Repeat(" ", innerWidth-len(chunk))
		sb.WriteString(styleDim.Render("│") + " " + styleBright.Render(string(chunk)) + pad + " " + styleDim.Render("│") + "\n")
	}
	sb.WriteString(styleDim.Render("└"+dashes+"┘") + "\n")

	if strings.TrimSpace(m.Issue) != "" {
		sb.WriteString(styleDimGreen.Render("↵") + styleDim.Render(" Fix  ·  ") + styleDim.Render("^C Quit") + "\n")
	} else {
		sb.WriteString(styleDim.Render("describe the issue...") + "\n")
	}
	return sb.String()
}
