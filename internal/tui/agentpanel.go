package tui

import (
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
)

// renderAgentTaskPanel returns a compact bottom panel showing active agent tasks,
// one line per task.
func (m ChatModel) renderAgentTaskPanel(theme chatTheme) string {
	active := m.activeTaskPanelItems()
	if len(active) == 0 {
		return ""
	}

	now := time.Now()
	width := max(1, m.width)

	var rows []string
	n := len(active)
	if n > 3 {
		n = 3
	}
	for _, task := range active[:n] {
		if row := formatTaskPanelRow(task, now, width, theme); row != "" {
			rows = append(rows, row)
		}
	}
	if len(rows) == 0 {
		return ""
	}

	title := lipgloss.NewStyle().
		Foreground(theme.TextDim).
		Bold(true).
		Render(fitCell(" agents ", width))

	return lipgloss.JoinVertical(lipgloss.Left, append([]string{title}, rows...)...)
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

// formatTaskPanelRow renders a single agent task as one compact line.
func formatTaskPanelRow(task chatAgentTaskState, now time.Time, width int, theme chatTheme) string {
	role := strings.TrimSpace(task.Role)
	if role == "" {
		role = "agent"
	}

	status := strings.TrimSpace(task.Status)
	if status == "" {
		status = "running"
	}

	var icon string
	var statusColor lipgloss.TerminalColor
	switch {
	case isTerminalAgentTaskStatus(status):
		if status == "completed" || status == "passed" {
			icon = "✓"
			statusColor = theme.TaskCompleted
		} else {
			icon = "✗"
			statusColor = theme.Error
		}
	case status == "running" || status == "active":
		icon = "⟳"
		statusColor = theme.TaskActive
	case status == "blocked" || status == "waiting":
		icon = "⊘"
		statusColor = theme.TaskBlocked
	case status == "pending":
		icon = "○"
		statusColor = theme.TaskPending
	default:
		icon = "⟳"
		statusColor = theme.TaskActive
	}

	iconStyle := lipgloss.NewStyle().Foreground(statusColor)
	roleStyle := lipgloss.NewStyle().Foreground(theme.AccentSecondary).Bold(true)
	dimStyle := lipgloss.NewStyle().Foreground(theme.TextDim)

	parts := []string{"  " + iconStyle.Render(icon), roleStyle.Render(role)}
	if tool := strings.TrimSpace(task.LastToolName); tool != "" {
		parts = append(parts, dimStyle.Render("· "+tool))
	}
	if elapsed := agentTaskElapsed(task, now); elapsed != "" {
		parts = append(parts, dimStyle.Render(elapsed))
	}
	if summary := strings.TrimSpace(task.Result); summary != "" {
		summary = truncateRightEllipsis(summary, max(1, width-len(role)-20))
		parts = append(parts, dimStyle.Render(summary))
	}
	return strings.Join(parts, " ")
}

// agentTaskPanelHeight returns the height needed for the agent task panel.
func (m ChatModel) agentTaskPanelHeight() int {
	n := len(m.activeTaskPanelItems())
	if n == 0 {
		return 0
	}
	if n > 3 {
		n = 3
	}
	// Title line + one row per task
	return 1 + n
}
