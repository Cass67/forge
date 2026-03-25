package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

func renderTraceOverlayPanel(theme chatTheme, content string, width, height int) string {
	titleStyle := lipgloss.NewStyle().Foreground(theme.AccentPrimary).Bold(true)
	dimStyle := lipgloss.NewStyle().Foreground(theme.TextDim)
	textStyle := lipgloss.NewStyle().Foreground(theme.Text)

	lines := []string{
		titleStyle.Render("Debug trace"),
		dimStyle.Render("Available because forge was started with -d."),
		"",
	}
	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		lines = append(lines, dimStyle.Render("No trace captured yet."))
	} else {
		lines = append(lines, strings.Split(trimmed, "\n")...)
	}
	lines = append(lines, "", dimStyle.Render("Esc / Enter closes this overlay"))

	boxW := min(104, max(64, width-8))
	boxH := min(max(16, len(lines)+4), max(14, height-4))
	contentHeight := max(1, boxH-4)
	if len(lines) > contentHeight {
		lines = lines[:contentHeight]
	}
	body := make([]string, 0, len(lines))
	for _, line := range lines {
		body = append(body, textStyle.Width(max(1, boxW-8)).Render(line))
	}

	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(theme.BorderFocus).
		Background(theme.HeaderBG).
		Padding(1, 2).
		Width(boxW - 6).
		Height(contentHeight).
		Render(strings.Join(body, "\n"))
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, box)
}
