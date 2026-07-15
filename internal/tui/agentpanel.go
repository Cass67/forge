package tui

import (
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
)

// renderAgentTaskPanel returns a compact bottom panel showing active agent tasks.
// It returns an empty string when there are no active tasks.
func (m ChatModel) renderAgentTaskPanel(theme chatTheme) string {
	active := m.activeTaskPanelItems()
	if len(active) == 0 {
		return ""
	}

	now := time.Now()
	width := max(1, m.width)
	var lines []string

	for _, task := range active {
		line := formatTaskPanelLine(task, now, width, theme)
		if line != "" {
			lines = append(lines, line)
		}
	}

	panel := strings.Join(lines, "\n")
	if panel == "" {
		return ""
	}

	// Apply panel style
	title := lipgloss.NewStyle().
		Foreground(theme.AccentPrimary).
		Bold(true).
		Render(fitCell(" Active Agents ", width))

	body := lipgloss.NewStyle().
		Width(width).
		Foreground(theme.Text).
		Render(panel)

	return lipgloss.JoinVertical(lipgloss.Left, title, body)
}

// activeTaskPanelItems returns non-terminal agent tasks sorted by creation time.
func (m ChatModel) activeTaskPanelItems() []chatAgentTaskState {
	var items []chatAgentTaskState
	for _, task := range m.agentTasks {
		if strings.TrimSpace(task.ID) == "" {
			continue
		}
		items = append(items, task)
	}
	return items
}

// formatTaskPanelLine renders a single agent task row for the bottom panel.
func formatTaskPanelLine(task chatAgentTaskState, now time.Time, width int, theme chatTheme) string {
	role := strings.TrimSpace(task.Role)
	if role == "" {
		role = "agent"
	}

	status := strings.TrimSpace(task.Status)
	if status == "" {
		status = "running"
	}

	elapsed := agentTaskElapsed(task, now)

	// Build status indicator
	indicator := ""
	var statusColor lipgloss.TerminalColor
	switch {
	case isTerminalAgentTaskStatus(status):
		if status == "completed" || status == "passed" {
			indicator = "OK"
			statusColor = theme.Success
		} else {
			indicator = "ERR"
			statusColor = theme.Error
		}
	case status == "running" || status == "active":
		indicator = "..."
		statusColor = theme.AccentPrimary
	default:
		indicator = "..."
		statusColor = theme.Warning
	}

	// Build the line
	roleStyle := lipgloss.NewStyle().Foreground(theme.AccentSecondary).Bold(true)
	statusStyle := lipgloss.NewStyle().Foreground(statusColor)
	dimStyle := lipgloss.NewStyle().Foreground(theme.TextDim)

	parts := []string{
		roleStyle.Render(role),
		statusStyle.Render(indicator),
	}
	if elapsed != "" {
		parts = append(parts, dimStyle.Render(elapsed))
	}
	if tool := strings.TrimSpace(task.LastToolName); tool != "" {
		parts = append(parts, dimStyle.Render(tool))
	}

	return lipgloss.NewStyle().Width(width).Render(strings.Join(parts, "  "))
}

func (m ChatModel) agentTaskPanelHeight() int {
	active := m.activeTaskPanelItems()
	n := len(active)
	if n == 0 {
		return 0
	}
	// Title line + tasks + separator
	return min(n, 3) + 1
}
