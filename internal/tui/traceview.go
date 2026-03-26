package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

type traceStyledLine struct {
	text  string
	style lipgloss.Style
}

func renderTraceOverlayPanel(theme chatTheme, content, debugLogPath string, width, height int) string {
	titleStyle := lipgloss.NewStyle().Foreground(theme.AccentPrimary).Bold(true)
	dimStyle := lipgloss.NewStyle().Foreground(theme.TextDim)
	textStyle := lipgloss.NewStyle().Foreground(theme.Text)

	lines := []string{
		titleStyle.Render("Debug trace"),
		dimStyle.Render("Available only because forge was started with -d."),
	}
	if path := strings.TrimSpace(debugLogPath); path != "" {
		lines = append(lines, dimStyle.Render("Log: "+path))
	}
	lines = append(lines, "")
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
		Background(theme.AppBG).
		Padding(1, 2).
		Width(boxW - 6).
		Height(contentHeight).
		Render(strings.Join(body, "\n"))
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, box)
}

func renderTraceDockPanel(theme chatTheme, content, debugLogPath string, width, height int) string {
	if width <= 0 || height <= 0 {
		return ""
	}
	titleStyle := lipgloss.NewStyle().Foreground(theme.AccentPrimary).Bold(true)
	dimStyle := lipgloss.NewStyle().Foreground(theme.TextDim)
	textStyle := lipgloss.NewStyle().Foreground(theme.Text)
	contentWidth := max(1, width-6)
	contentHeight := max(1, height-1)

	lines := make([]traceStyledLine, 0, contentHeight)
	lines = appendTraceStyledLine(lines, titleStyle, "Debug trace", contentWidth)
	lines = appendTraceStyledLine(lines, dimStyle, "Visible because forge was started with -d.", contentWidth)
	if path := strings.TrimSpace(debugLogPath); path != "" {
		lines = appendTraceStyledLine(lines, dimStyle, "Log: "+path, contentWidth)
	}
	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		lines = appendTraceStyledLine(lines, dimStyle, "No trace captured yet.", contentWidth)
	} else {
		traceLines := wrapTraceText(trimmed, contentWidth)
		if len(traceLines) > contentHeight-len(lines) {
			traceLines = traceLines[len(traceLines)-(contentHeight-len(lines)):]
		}
		for _, line := range traceLines {
			lines = append(lines, traceStyledLine{text: line, style: textStyle})
		}
	}
	if len(lines) > contentHeight {
		lines = lines[:contentHeight]
	}
	body := make([]string, 0, len(lines))
	for _, line := range lines {
		body = append(body, line.style.Width(contentWidth).Render(line.text))
	}

	return lipgloss.NewStyle().
		BorderTop(true).
		BorderStyle(lipgloss.NormalBorder()).
		BorderForeground(theme.Border).
		Background(theme.AppBG).
		Padding(0, 1).
		Width(width - 2).
		Height(contentHeight).
		Render(strings.Join(body, "\n"))
}

func appendTraceStyledLine(lines []traceStyledLine, style lipgloss.Style, text string, width int) []traceStyledLine {
	for _, line := range wrapTraceText(text, width) {
		lines = append(lines, traceStyledLine{text: line, style: style})
	}
	return lines
}

func wrapTraceText(text string, width int) []string {
	width = max(1, width)
	text = strings.TrimRight(text, "\n")
	if text == "" {
		return []string{""}
	}

	out := make([]string, 0, 4)
	for _, raw := range strings.Split(text, "\n") {
		runes := []rune(raw)
		if len(runes) == 0 {
			out = append(out, "")
			continue
		}
		for len(runes) > width {
			out = append(out, string(runes[:width]))
			runes = runes[width:]
		}
		out = append(out, string(runes))
	}
	return out
}
