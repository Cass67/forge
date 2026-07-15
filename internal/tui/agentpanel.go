package tui

import (
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
)

// renderAgentTaskPanel returns a compact bottom panel showing active agent tasks.
// Each task is rendered as a compact bordered card.
func (m ChatModel) renderAgentTaskPanel(theme chatTheme) string {
	active := m.activeTaskPanelItems()
	if len(active) == 0 {
		return ""
	}

	now := time.Now()
	width := max(1, m.width)

	// Render each task as a bordered card
	var cards []string
	n := len(active)
	if n > 3 {
		n = 3
	}
	show := active[:n]

	for _, task := range show {
		card := formatTaskPanelCard(task, now, width, theme)
		if card != "" {
			cards = append(cards, card)
		}
	}

	if len(cards) == 0 {
		return ""
	}

	body := strings.Join(cards, "\n")

	// Title
	title := lipgloss.NewStyle().
		Foreground(theme.AccentPrimary).
		Bold(true).
		Render(fitCell(" Active Agents ", width))

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

// formatTaskPanelCard renders a single agent task as a compact bordered card.
func formatTaskPanelCard(task chatAgentTaskState, now time.Time, width int, theme chatTheme) string {
	role := strings.TrimSpace(task.Role)
	if role == "" {
		role = "agent"
	}

	status := strings.TrimSpace(task.Status)
	if status == "" {
		status = "running"
	}

	elapsed := agentTaskElapsed(task, now)

	// Status icon and color
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

	borderColor := statusColor
	if isTerminalAgentTaskStatus(status) {
		borderColor = theme.Border
	}

	width = max(10, width)

	roleStyle := lipgloss.NewStyle().Foreground(theme.AccentSecondary).Bold(true)
	iconStyle := lipgloss.NewStyle().Foreground(statusColor)
	dimStyle := lipgloss.NewStyle().Foreground(theme.TextDim)

	// Build first content line: icon + role + tool
	var firstParts []string
	firstParts = append(firstParts, iconStyle.Render(icon))
	firstParts = append(firstParts, roleStyle.Render(role))
	if tool := strings.TrimSpace(task.LastToolName); tool != "" {
		firstParts = append(firstParts, dimStyle.Render("·"))
		firstParts = append(firstParts, dimStyle.Render(tool))
	}
	firstLine := strings.Join(firstParts, " ")

	// Build second content line (optional): elapsed + summary
	var secondParts []string
	if elapsed != "" {
		secondParts = append(secondParts, dimStyle.Render(elapsed))
	}
	if summary := strings.TrimSpace(task.Result); summary != "" {
		if len(summary) > 40 {
			summary = summary[:40] + "…"
		}
		secondParts = append(secondParts, dimStyle.Render(summary))
	}
	secondLine := strings.Join(secondParts, "  ")

	// Combine card body
	var bodyLines []string
	bodyLines = append(bodyLines, firstLine)
	if secondLine != "" {
		bodyLines = append(bodyLines, "  "+secondLine)
	}
	body := strings.Join(bodyLines, "\n")

	// Card width: subtract 2 for border left+right, 2 for padding
	cardInnerWidth := width - 4
	if cardInnerWidth < 4 {
		cardInnerWidth = 4
	}

	// Card border: use the status-appropriate border color, thin normal border
	card := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(borderColor).
		Padding(0, 1).
		Width(cardInnerWidth).
		Render(body)

	return card
}

// agentTaskPanelHeight returns the height needed for the agent task panel.
func (m ChatModel) agentTaskPanelHeight() int {
	active := m.activeTaskPanelItems()
	n := len(active)
	if n == 0 {
		return 0
	}
	if n > 3 {
		n = 3
	}
	// Title line + n cards (each card = 2 border lines + 1-2 content lines)
	// Estimate each card at 3 lines (border-top, content, border-bottom)
	// For tasks with results/summary, add an extra content line
	extra := 0
	for _, task := range active[:n] {
		if strings.TrimSpace(task.Result) != "" {
			extra++
		}
	}
	return 1 + n*3 + extra
}
