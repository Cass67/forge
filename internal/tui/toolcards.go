// package tui — tool card rendering for agent tool calls and file changes panel
package tui

import (
	"fmt"
	"strings"

	"forge/internal/llm"

	"github.com/charmbracelet/lipgloss"
)

// trackToolCall records a tool call from an LLM event for display in the tool cards panel.
func (m *ChatModel) trackToolCall(ev llm.Event) {
	toolName := strings.TrimSpace(ev.Agent)
	if toolName == "" {
		return
	}

	entry := toolCallEntry{
		ToolName: toolName,
		Target:   extractToolCallTarget(toolName, ev.Text),
		Status:   "done",
	}

	// Keep last 5 tool calls
	m.recentToolCalls = append(m.recentToolCalls, entry)
	if len(m.recentToolCalls) > 5 {
		m.recentToolCalls = m.recentToolCalls[len(m.recentToolCalls)-5:]
	}

	// Track file changes from write/edit/apply tools
	trackFileChanges(&m.fileChanges, toolName, ev.Text)
}

// extractToolCallTarget creates a brief one-line target/summary from the tool result.
func extractToolCallTarget(toolName, text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	// Try to extract first meaningful line
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "{") || strings.HasPrefix(line, "[") {
			continue
		}
		// Truncate to reasonable length
		if len(line) > 60 {
			line = line[:60] + "…"
		}
		return line
	}
	// Fallback: show indicator of result type
	if strings.HasPrefix(text, "{") || strings.HasPrefix(text, "[") {
		if len(text) > 50 {
			return text[:50] + "…"
		}
		return text
	}
	return ""
}

// trackFileChanges identifies file modifications from tool calls.
func trackFileChanges(tracker *fileChangesTracker, toolName, resultText string) {
	switch toolName {
	case "write_file":
		path := extractFilePathFromResult(resultText)
		if path != "" {
			tracker.markModified(path)
		}
	case "edit_file":
		path := extractFilePathFromResult(resultText)
		if path != "" {
			tracker.markModified(path)
		}
	case "apply_patch":
		path := extractFilePathFromResult(resultText)
		if path != "" {
			tracker.markModified(path)
		}
	}
}

// extractFilePathFromResult tries to find a file path in the tool result text.
func extractFilePathFromResult(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	// Try JSON patterns
	if strings.HasPrefix(text, "{") {
		for _, line := range strings.Split(text, "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, `"path":`) {
				parts := strings.SplitN(line, ":", 2)
				if len(parts) == 2 {
					val := strings.Trim(strings.TrimSpace(parts[1]), `" `)
					if val != "" && val != "null" && !strings.HasPrefix(val, "{") {
						return val
					}
				}
			}
		}
	}
	return ""
}

// renderToolCardsPanel renders recent tool calls as compact single-line rows.
// Hidden by default; toggled with Ctrl+T or /tools.
func (m ChatModel) renderToolCardsPanel(theme chatTheme) string {
	if !m.toolPanelsVisible || len(m.recentToolCalls) == 0 {
		return ""
	}

	width := max(1, m.width)
	title := lipgloss.NewStyle().
		Foreground(theme.TextDim).
		Bold(true).
		Render(fitCell(" tools ", width))

	toolStyle := lipgloss.NewStyle().Foreground(theme.AccentSecondary)
	dimStyle := lipgloss.NewStyle().Foreground(theme.TextDim)

	var rows []string
	n := len(m.recentToolCalls)
	if n > 3 {
		n = 3
	}
	for i := len(m.recentToolCalls) - n; i < len(m.recentToolCalls); i++ {
		call := m.recentToolCalls[i]
		icon, iconColor := "●", theme.TaskActive
		switch call.Status {
		case "done":
			icon, iconColor = "✓", theme.TaskCompleted
		case "error":
			icon, iconColor = "✗", theme.Error
		}
		line := "  " + lipgloss.NewStyle().Foreground(iconColor).Render(icon) +
			" " + toolStyle.Render(call.ToolName)
		if call.Target != "" {
			target := truncateRightEllipsis(call.Target, max(1, width-len(call.ToolName)-8))
			line += "  " + dimStyle.Render(target)
		}
		rows = append(rows, line)
	}

	return lipgloss.JoinVertical(lipgloss.Left, append([]string{title}, rows...)...)
}

// renderFileChangesPanel renders a compact summary of file changes.
func (m ChatModel) renderFileChangesPanel(theme chatTheme) string {
	if !m.toolPanelsVisible {
		return ""
	}
	changes := m.fileChanges
	if changes.Total() == 0 {
		return ""
	}

	width := max(1, m.width)
	title := lipgloss.NewStyle().
		Foreground(theme.TextDim).
		Bold(true).
		Render(fitCell(" changed files ", width))

	modified := lipgloss.NewStyle().
		Foreground(theme.Success).
		Render(fmt.Sprintf("  M %d", len(changes.Modified)))
	added := lipgloss.NewStyle().
		Foreground(theme.AccentSecondary).
		Render(fmt.Sprintf("  A %d", len(changes.Added)))
	deleted := lipgloss.NewStyle().
		Foreground(theme.Error).
		Render(fmt.Sprintf("  D %d", len(changes.Deleted)))

	stats := lipgloss.JoinHorizontal(lipgloss.Left, modified, added, deleted)

	// Show up to 3 file paths
	var fileList string
	count := 0
	for _, path := range changes.Paths() {
		if count >= 3 {
			break
		}
		dimStyle := lipgloss.NewStyle().Foreground(theme.TextDim)
		fileList += "\n  " + dimStyle.Render(path)
		count++
	}

	if fileList != "" {
		stats += fileList
	}

	body := lipgloss.NewStyle().
		Width(width).
		Render(stats)

	return lipgloss.JoinVertical(lipgloss.Left, title, body)
}

// toolCardsPanelHeight returns the height needed for the tool cards panel.
func (m ChatModel) toolCardsPanelHeight() int {
	if !m.toolPanelsVisible || len(m.recentToolCalls) == 0 {
		return 0
	}
	n := len(m.recentToolCalls)
	if n > 3 {
		n = 3
	}
	// Title + one row per call
	return 1 + n
}

// fileChangesPanelHeight returns the height needed for the file changes panel.
func (m ChatModel) fileChangesPanelHeight() int {
	if !m.toolPanelsVisible {
		return 0
	}
	changes := m.fileChanges
	if changes.Total() == 0 {
		return 0
	}
	h := 2 // title + stats
	if len(changes.Modified)+len(changes.Added)+len(changes.Deleted) > 0 {
		n := len(changes.Paths())
		if n > 3 {
			n = 3
		}
		h += n // one per file path
	}
	return h
}
