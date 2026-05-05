package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// SessionStarted is emitted when the user presses enter to start.
type SessionStarted struct {
	Prompt       string
	WriterModel  string
	AuditorModel string
	Rounds       int
	LangHint     string
	ContextFiles []string
	Interactive  bool
}

// InputModel is the Bubble Tea model for the input screen.
type InputModel struct {
	Prompt          string
	WriterModels    []string
	AuditorModels   []string
	WriterIdx       int
	AuditorIdx      int
	ModelFocus      int // 0 = writer selected, 1 = auditor selected
	Rounds          int
	LangHint        string
	ContextFiles    []string
	RoundsInput     string
	RoundsErr       string
	Preserved       bool
	Interactive     bool
	Width           int
	cursorPos       int // cursor position within Prompt (runes)
	discardMouseCSI bool
}

func NewInputModel(writerModels, auditorModels []string, defaultWriter, defaultAuditor string) InputModel {
	m := InputModel{
		WriterModels:  writerModels,
		AuditorModels: auditorModels,
		Rounds:        3,
		RoundsInput:   "3",
		LangHint:      "auto",
	}
	if defaultWriter != "" {
		if idx := indexOf(writerModels, defaultWriter); idx >= 0 {
			m.WriterIdx = idx
		}
	}
	if defaultAuditor != "" {
		if idx := indexOf(auditorModels, defaultAuditor); idx >= 0 {
			m.AuditorIdx = idx
		}
	}
	return m
}

func indexOf(list []string, target string) int {
	for i, v := range list {
		if v == target {
			return i
		}
	}
	return -1
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
	if sz, ok := msg.(tea.WindowSizeMsg); ok {
		m.Width = sz.Width
		return m, nil
	}
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	if m.discardMouseCSI {
		if isMouseTrackingFragment(key) {
			m.discardMouseCSI = false
			return m, nil
		}
		m.discardMouseCSI = false
	}
	if startsMouseTrackingSequence(key) {
		m.discardMouseCSI = true
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

	case "home":
		m.cursorPos = 0

	case "end":
		m.cursorPos = len([]rune(m.Prompt))

	case "enter":
		if m.Prompt != "" {
			return m, func() tea.Msg {
				return SessionStarted{
					Prompt:       m.Prompt,
					WriterModel:  m.writerModel(),
					AuditorModel: m.auditorModel(),
					Rounds:       m.Rounds,
					LangHint:     m.LangHint,
					ContextFiles: m.ContextFiles,
					Interactive:  m.Interactive,
				}
			}
		}

	case "backspace":
		if len(m.Prompt) > 0 {
			m.Prompt = m.Prompt[:len(m.Prompt)-1]
		}

	default:
		ch := key.String()
		if ch == "ctrl+t" {
			m.Interactive = !m.Interactive
		} else if len(ch) == 1 {
			m.Prompt += ch
		}
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

func (m InputModel) View() string {
	var sb strings.Builder

	// Header
	sb.WriteString(styleBold.Render("forge") + "  " + styleDim.Render("v0.1.0") + "\n\n")

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

	// Interactive mode
	interLabel := "off"
	if m.Interactive {
		interLabel = "on"
	}
	sb.WriteString("  " + styleDim.Render("Interactive ") + styleMid.Render(interLabel) + styleDim.Render("  ^T toggle") + "\n")

	// Context files
	for _, f := range m.ContextFiles {
		sb.WriteString("  " + styleDim.Render("File     ") + styleMid.Render(f) + "\n")
	}

	// Prompt box
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
	sb.WriteString(styleDimGreen.Render("task") + "\n")
	sb.WriteString(styleDim.Render("┌"+dashes+"┐") + "\n")

	runes := []rune(m.Prompt)
	cursorPos := clamp(m.cursorPos, 0, len(runes))
	if len(runes) == 0 {
		// Empty prompt: show cursor block.
		cursorChar := styleCursor.Render(" ")
		pad := strings.Repeat(" ", innerWidth-1)
		sb.WriteString(styleDim.Render("│") + " " + cursorChar + styleDim.Render(pad) + " " + styleDim.Render("│") + "\n")
	} else {
		// Build lines wrapping runes at innerWidth, inserting cursor.
		lines := wordWrapRunes(runes, innerWidth)
		cursorLine := 0
		count := 0
		for i, line := range lines {
			lineLen := len([]rune(line))
			if cursorPos <= count+lineLen {
				cursorLine = i
				break
			}
			count += lineLen
		}
		for i, line := range lines {
			lineRunes := []rune(line)
			if i == cursorLine {
				col := clamp(cursorPos-count, 0, len(lineRunes))
				if col == len(lineRunes) {
					cursorChar := styleCursor.Render(" ")
					pad := strings.Repeat(" ", innerWidth-len(lineRunes)-1)
					sb.WriteString(styleDim.Render("│") + " " + styleBright.Render(string(lineRunes)) + cursorChar + pad + " " + styleDim.Render("│") + "\n")
				} else {
					before := styleBright.Render(string(lineRunes[:col]))
					atCursor := styleCursor.Render(string(lineRunes[col]))
					after := styleBright.Render(string(lineRunes[col+1:]))
					visible := before + atCursor + after
					padding := innerWidth - len(lineRunes)
					if padding > 0 {
						visible += strings.Repeat(" ", padding)
					}
					sb.WriteString(styleDim.Render("│") + " " + visible + " " + styleDim.Render("│") + "\n")
				}
			} else {
				pad := strings.Repeat(" ", innerWidth-len(lineRunes))
				sb.WriteString(styleDim.Render("│") + " " + styleBright.Render(string(lineRunes)) + pad + " " + styleDim.Render("│") + "\n")
			}
			count += len(lineRunes)
		}
	}
	sb.WriteString(styleDim.Render("└"+dashes+"┘") + "\n")

	// Keybind hint
	if m.Prompt != "" {
		sb.WriteString(styleDimGreen.Render("↵") + styleDim.Render(" Start  ·  ") + styleDim.Render("^C Quit") + "\n")
	} else {
		sb.WriteString(styleDim.Render("type your task...") + "\n")
	}

	if m.RoundsErr != "" {
		sb.WriteString("\n" + styleRed.Render("⚠  "+m.RoundsErr) + "\n")
	}
	return sb.String()
}

// wordWrapRunes splits a rune slice into lines of at most width runes,
// breaking at word boundaries when possible.
func wordWrapRunes(runes []rune, width int) []string {
	width = max(1, width)
	var lines []string
	for len(runes) > 0 {
		if len(runes) <= width {
			lines = append(lines, string(runes))
			break
		}
		chunk := runes[:width]
		breakAt := -1
		for i := len(chunk) - 1; i >= 0; i-- {
			if chunk[i] == ' ' {
				breakAt = i
				break
			}
		}
		if breakAt > 0 {
			lines = append(lines, string(runes[:breakAt]))
			runes = runes[breakAt+1:]
		} else {
			lines = append(lines, string(runes[:width]))
			runes = runes[width:]
		}
	}
	return lines
}
