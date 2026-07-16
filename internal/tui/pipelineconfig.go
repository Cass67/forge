package tui

import (
	"fmt"
	"strconv"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// openPipelineConfig opens the pipeline configuration dialog.
func (m *ChatModel) openPipelineConfig(prompt string) {
	m.ensureModelListLoaded()

	// Load saved defaults
	writerModel := m.model
	auditorModel := m.model
	rounds := 5
	if m.config.LoadPipelineDefaults != nil {
		if w, a, r := m.config.LoadPipelineDefaults(); w != "" || a != "" || r > 0 {
			if w != "" {
				writerModel = w
			}
			if a != "" {
				auditorModel = a
			}
			if r > 0 {
				rounds = r
			}
		}
	}

	m.pipelineConfigVisible = true
	m.pipelineConfigFocus = 0
	m.pipelineConfigWriterModel = writerModel
	m.pipelineConfigAuditorModel = auditorModel
	m.pipelineConfigRounds = rounds

	m.pipelineConfigWriterCursor = indexOf(m.modelsList, writerModel)
	if m.pipelineConfigWriterCursor < 0 {
		m.pipelineConfigWriterCursor = 0
	}
	m.pipelineConfigAuditorCursor = indexOf(m.modelsList, auditorModel)
	if m.pipelineConfigAuditorCursor < 0 {
		m.pipelineConfigAuditorCursor = 0
	}
}

// handlePipelineConfigKey handles keyboard input while the pipeline config dialog is open.
func (m ChatModel) handlePipelineConfigKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if !m.pipelineConfigVisible {
		return m, nil
	}

	switch msg.Type {
	case tea.KeyEscape:
		m.pipelineConfigVisible = false
		return m, nil

	case tea.KeyEnter:
		// Save models and rounds, close dialog, set pending for the next submitted text
		writerModel := m.pipelineConfigWriterModel
		auditorModel := m.pipelineConfigAuditorModel
		rounds := m.pipelineConfigRounds
		if m.config.SavePipelineDefaults != nil {
			m.config.SavePipelineDefaults(writerModel, auditorModel, rounds)
		}
		m.pipelinePendingWriter = writerModel
		m.pipelinePendingAuditor = auditorModel
		m.pipelinePendingRounds = rounds
		m.pipelineConfigVisible = false
		m.flash = fmt.Sprintf("pipeline ready — writer: %s  auditor: %s  rounds: %d — type a prompt and press Enter",
			writerModel, auditorModel, rounds)
		return m, nil

	case tea.KeyTab:
		m.pipelineConfigFocus = (m.pipelineConfigFocus + 1) % 3
		return m, nil

	case tea.KeyShiftTab:
		m.pipelineConfigFocus = (m.pipelineConfigFocus + 2) % 3
		return m, nil

	case tea.KeyUp:
		switch m.pipelineConfigFocus {
		case 0:
			if m.pipelineConfigWriterCursor > 0 {
				m.pipelineConfigWriterCursor--
				m.pipelineConfigWriterModel = m.modelsList[m.pipelineConfigWriterCursor]
			}
		case 1:
			if m.pipelineConfigAuditorCursor > 0 {
				m.pipelineConfigAuditorCursor--
				m.pipelineConfigAuditorModel = m.modelsList[m.pipelineConfigAuditorCursor]
			}
		case 2:
			if m.pipelineConfigRounds > 1 {
				m.pipelineConfigRounds--
			}
		}
		return m, nil

	case tea.KeyDown:
		switch m.pipelineConfigFocus {
		case 0:
			if m.pipelineConfigWriterCursor < len(m.modelsList)-1 {
				m.pipelineConfigWriterCursor++
				m.pipelineConfigWriterModel = m.modelsList[m.pipelineConfigWriterCursor]
			}
		case 1:
			if m.pipelineConfigAuditorCursor < len(m.modelsList)-1 {
				m.pipelineConfigAuditorCursor++
				m.pipelineConfigAuditorModel = m.modelsList[m.pipelineConfigAuditorCursor]
			}
		case 2:
			if m.pipelineConfigRounds < 10 {
				m.pipelineConfigRounds++
			}
		}
		return m, nil

	case tea.KeyRunes:
		if m.pipelineConfigFocus == 2 {
			// Typing on the rounds field — parse digits
			for _, r := range msg.Runes {
				if r >= '0' && r <= '9' {
					val, _ := strconv.Atoi(string(r))
					m.pipelineConfigRounds = val
				}
			}
		}
		return m, nil
	}

	return m, nil
}

// renderPipelineConfigOverlay renders the pipeline configuration dialog.
func (m ChatModel) renderPipelineConfigOverlay() string {
	theme := m.theme()
	boxW := min(80, max(50, m.width-6))

	titleStyle := lipgloss.NewStyle().Foreground(theme.AccentPrimary).Bold(true)
	labelStyle := lipgloss.NewStyle().Foreground(theme.TextDim).Bold(true)
	selectedStyle := lipgloss.NewStyle().Foreground(theme.HeaderFG).Background(theme.AccentPrimary)
	textStyle := lipgloss.NewStyle().Foreground(theme.Text)
	dimStyle := lipgloss.NewStyle().Foreground(theme.TextDim)
	borderFocus := func(focused bool) lipgloss.Style {
		if focused {
			return lipgloss.NewStyle().Foreground(theme.AccentPrimary)
		}
		return lipgloss.NewStyle().Foreground(theme.TextDim)
	}

	var lines []string
	lines = append(lines, titleStyle.Render("Pipeline Config"))
	lines = append(lines, "")

	// Writer model field
	writerFocus := m.pipelineConfigFocus == 0
	writerSelected := textStyle.Render(m.modelOptionLabel(m.pipelineConfigWriterModel))
	if writerFocus {
		writerSelected = selectedStyle.Render("> " + m.modelOptionLabel(m.pipelineConfigWriterModel))
	}
	lines = append(lines, labelStyle.Render(borderFocus(writerFocus).Render("Writer model:")))
	lines = append(lines, "  "+writerSelected)

	if writerFocus {
		lines = append(lines, "")
		start := max(0, m.pipelineConfigWriterCursor-2)
		end := min(len(m.modelsList), start+5)
		if end-start < 5 {
			start = max(0, len(m.modelsList)-5)
			end = len(m.modelsList)
		}
		for i := start; i < end; i++ {
			label := m.modelOptionLabel(m.modelsList[i])
			if i == m.pipelineConfigWriterCursor {
				lines = append(lines, selectedStyle.Render("  > "+label))
			} else {
				lines = append(lines, dimStyle.Render("    "+label))
			}
		}
	}

	lines = append(lines, "")

	// Auditor model field
	auditorFocus := m.pipelineConfigFocus == 1
	auditorSelected := textStyle.Render(m.modelOptionLabel(m.pipelineConfigAuditorModel))
	if auditorFocus {
		auditorSelected = selectedStyle.Render("> " + m.modelOptionLabel(m.pipelineConfigAuditorModel))
	}
	lines = append(lines, labelStyle.Render(borderFocus(auditorFocus).Render("Auditor model:")))
	lines = append(lines, "  "+auditorSelected)

	if auditorFocus {
		lines = append(lines, "")
		start := max(0, m.pipelineConfigAuditorCursor-2)
		end := min(len(m.modelsList), start+5)
		if end-start < 5 {
			start = max(0, len(m.modelsList)-5)
			end = len(m.modelsList)
		}
		for i := start; i < end; i++ {
			label := m.modelOptionLabel(m.modelsList[i])
			if i == m.pipelineConfigAuditorCursor {
				lines = append(lines, selectedStyle.Render("  > "+label))
			} else {
				lines = append(lines, dimStyle.Render("    "+label))
			}
		}
	}

	lines = append(lines, "")

	// Rounds field
	roundsFocus := m.pipelineConfigFocus == 2
	roundsText := fmt.Sprintf("%d", m.pipelineConfigRounds)
	if roundsFocus {
		roundsText = selectedStyle.Render(fmt.Sprintf("%d  (up/down or type a number)", m.pipelineConfigRounds))
	}
	lines = append(lines, labelStyle.Render(borderFocus(roundsFocus).Render("Rounds per pass:")))
	lines = append(lines, "  "+roundsText)

	lines = append(lines, "")
	lines = append(lines, dimStyle.Render("up/down select  tab switch field  enter confirm  esc cancel"))

	inner := lipgloss.JoinVertical(lipgloss.Left, lines...)
	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(theme.BorderFocus).
		Background(theme.HeaderBG).
		Padding(1, 2).
		Width(boxW - 6).
		Render(inner)

	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, box)
}

// indexOf returns the index of s in slice, or -1.
func indexOf(slice []string, s string) int {
	for i, v := range slice {
		if v == s {
			return i
		}
	}
	return -1
}
