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
		dimStyle.Render("Available only because forge was started with -d."),
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

func renderTraceDockPanel(theme chatTheme, content string, width, height int) string {
	if width <= 0 || height <= 0 {
		return ""
	}
	titleStyle := lipgloss.NewStyle().Foreground(theme.AccentPrimary).Bold(true)
	dimStyle := lipgloss.NewStyle().Foreground(theme.TextDim)
	textStyle := lipgloss.NewStyle().Foreground(theme.Text)

	lines := []string{
		titleStyle.Render("Debug trace"),
		dimStyle.Render("Visible because forge was started with -d."),
	}
	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		lines = append(lines, dimStyle.Render("No trace captured yet."))
	} else {
		traceLines := strings.Split(trimmed, "\n")
		if len(traceLines) > height-2 {
			traceLines = traceLines[len(traceLines)-(height-2):]
		}
		lines = append(lines, traceLines...)
	}
	if len(lines) > height {
		lines = lines[:height]
	}
	body := make([]string, 0, len(lines))
	for _, line := range lines {
		body = append(body, textStyle.Width(max(1, width-6)).Render(line))
	}

	return lipgloss.NewStyle().
		BorderTop(true).
		BorderStyle(lipgloss.NormalBorder()).
		BorderForeground(theme.Border).
		Background(theme.HeaderBG).
		Padding(0, 1).
		Width(width - 2).
		Height(height).
		Render(strings.Join(body, "\n"))
}
