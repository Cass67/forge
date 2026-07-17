package tui

import (
	"fmt"
	"strings"
	"time"

	"forge/internal/secscan"

	"github.com/charmbracelet/lipgloss"
)

func (m ChatModel) renderedToolsBuf() string {
	var sb strings.Builder
	for _, sec := range m.toolsSections {
		if sec.collapsed && sec.summary != "" {
			sb.WriteString(sec.summary)
			sb.WriteByte('\n')
		} else {
			sb.WriteString(sec.buf)
		}
	}
	return sb.String()
}

func (m ChatModel) renderedAgentWorkBuf() string {
	var sb strings.Builder
	if stateBuf := strings.TrimSpace(m.renderedAgentTaskStateBuf(time.Now())); stateBuf != "" {
		sb.WriteString(stateBuf)
		sb.WriteString("\n\n")
	}
	for _, sec := range m.toolsSections {
		if strings.TrimSpace(sec.role) == "" {
			continue
		}
		if sec.collapsed && sec.summary != "" {
			sb.WriteString(sec.summary)
			sb.WriteByte('\n')
		} else {
			sb.WriteString(sec.buf)
		}
	}
	return strings.TrimRight(sb.String(), "\n")
}

func (m ChatModel) activeAgentViewItems() []chatAgentTaskState {
	items := make([]chatAgentTaskState, 0, len(m.agentTasks))
	for _, task := range m.agentTasks {
		if strings.TrimSpace(task.ID) == "" || isTerminalAgentTaskStatus(task.Status) {
			continue
		}
		items = append(items, task)
	}
	return items
}

func (m ChatModel) selectedAgentViewTask() (chatAgentTaskState, bool) {
	items := m.activeAgentViewItems()
	if len(items) == 0 {
		return chatAgentTaskState{}, false
	}
	idx := clamp(m.agentViewIndex, 0, len(items)-1)
	return items[idx], true
}

func (m ChatModel) renderedAgentViewBuf() string {
	if task, ok := m.selectedAgentViewTask(); ok {
		role := strings.TrimSpace(task.Role)
		var sb strings.Builder
		if line := formatChatAgentTaskLine(task, time.Now()); line != "" {
			sb.WriteString("Agent task\n")
			sb.WriteString(line)
			sb.WriteString("\n\n")
		}
		for _, sec := range m.toolsSections {
			if role == "" || strings.TrimSpace(sec.role) != role {
				continue
			}
			if sec.collapsed && sec.summary != "" {
				sb.WriteString(sec.summary)
				sb.WriteByte('\n')
			} else {
				sb.WriteString(sec.buf)
			}
		}
		return strings.TrimRight(sb.String(), "\n")
	}
	return m.renderedAgentWorkBuf()
}

func (m ChatModel) renderAgentView(theme chatTheme, height int) string {
	width := max(1, m.width)
	visible := m.agentViewVisibleLineCount(height)
	contentWidth := max(1, width-2)
	lines := m.agentViewWrappedLines(contentWidth)
	offset := clamp(m.toolsScroll, 0, m.agentViewMaxScrollForHeight(height))
	end := min(len(lines), offset+visible)
	bodyLines := append([]string(nil), lines[offset:end]...)
	for len(bodyLines) < visible {
		bodyLines = append(bodyLines, "")
	}
	scrollbar := scrollbarColumn(len(lines), visible, offset, visible)
	body := joinWithScrollbar(bodyLines, scrollbar, contentWidth, visible)
	title := " Agent view "
	if task, ok := m.selectedAgentViewTask(); ok {
		items := m.activeAgentViewItems()
		title = fmt.Sprintf(" Agent view %d/%d: %s ", clamp(m.agentViewIndex, 0, max(0, len(items)-1))+1, len(items), strings.TrimSpace(task.ID))
	}
	footer := lipgloss.NewStyle().Foreground(theme.TextDim).Width(width).Render("Tab cycle agents • Esc close")
	return lipgloss.NewStyle().Foreground(theme.Text).Width(width).Height(height).Render(lipgloss.JoinVertical(lipgloss.Left,
		lipgloss.NewStyle().Foreground(theme.AccentPrimary).Bold(true).Width(width).Render(fitCell(title, width)),
		body,
		footer,
	))
}

func (m ChatModel) agentViewVisibleLineCount(height int) int {
	return max(1, height-2)
}

func (m ChatModel) agentViewWrappedLines(contentWidth int) []string {
	lines := strings.Split(lipgloss.NewStyle().Width(max(1, contentWidth)).Render(strings.TrimSpace(m.renderedAgentViewBuf())), "\n")
	if len(lines) == 1 && strings.TrimSpace(lines[0]) == "" {
		return []string{"No agent work yet."}
	}
	return lines
}

func (m ChatModel) agentViewMaxScroll() int {
	return m.agentViewMaxScrollForHeight(m.chatViewport.Height)
}

func (m ChatModel) agentViewMaxScrollForHeight(height int) int {
	contentWidth := max(1, m.width-2)
	lines := m.agentViewWrappedLines(contentWidth)
	return max(0, len(lines)-m.agentViewVisibleLineCount(height))
}

func (m *ChatModel) cycleAgentView(delta int) {
	items := m.activeAgentViewItems()
	if len(items) == 0 {
		m.agentViewIndex = 0
		return
	}
	m.agentViewIndex = (m.agentViewIndex + delta) % len(items)
	if m.agentViewIndex < 0 {
		m.agentViewIndex += len(items)
	}
	m.toolsScroll = 0
}

func (m ChatModel) renderedAgentTaskStateBuf(now time.Time) string {
	if len(m.agentTasks) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("Agent tasks\n")
	for _, task := range m.agentTasks {
		if strings.TrimSpace(task.ID) == "" {
			continue
		}
		line := formatChatAgentTaskLine(task, now)
		if strings.TrimSpace(line) != "" {
			b.WriteString(line)
			b.WriteByte('\n')
		}
	}
	return strings.TrimSpace(b.String())
}

func formatChatAgentTaskLine(task chatAgentTaskState, now time.Time) string {
	id := strings.TrimSpace(task.ID)
	if id == "" {
		return ""
	}
	role := strings.TrimSpace(task.Role)
	if role == "" {
		role = "unknown-role"
	}
	status := strings.TrimSpace(task.Status)
	if status == "" {
		status = "running"
	}
	parts := []string{fmt.Sprintf("- %s (%s): %s", id, role, status)}
	if at := agentTaskDisplayTime(task); !at.IsZero() {
		parts = append(parts, "last "+at.Format("15:04:05"))
	}
	if elapsed := agentTaskElapsed(task, now); elapsed != "" {
		parts = append(parts, "elapsed "+elapsed)
	}
	if tool := strings.TrimSpace(task.LastToolName); tool != "" {
		activity := tool
		if len(task.RecentActivity) > 0 {
			last := task.RecentActivity[len(task.RecentActivity)-1]
			if summary := strings.TrimSpace(last.Summary); summary != "" {
				activity += " " + redactChatAgentTaskText(summary)
			}
		}
		parts = append(parts, activity)
	}
	if isTerminalAgentTaskStatus(status) {
		if result := strings.TrimSpace(task.Result); result != "" {
			parts = append(parts, "result: "+truncate(redactChatAgentTaskText(result), 120))
		}
		if errText := strings.TrimSpace(task.Error); errText != "" {
			parts = append(parts, "error: "+truncate(redactChatAgentTaskText(errText), 120))
		}
	}
	return strings.Join(parts, "; ")
}

func redactChatAgentTaskText(text string) string {
	if text == "" {
		return ""
	}
	scanner := secscan.NewDefaultScanner()
	return secscan.Redact(text, scanner.Scan(text))
}

func agentTaskDisplayTime(task chatAgentTaskState) time.Time {
	for _, candidate := range []time.Time{task.LastActivityAt, task.CompletedAt, task.StartedAt, task.CreatedAt} {
		if !candidate.IsZero() {
			return candidate
		}
	}
	return time.Time{}
}

func agentTaskElapsed(task chatAgentTaskState, now time.Time) string {
	start := task.StartedAt
	if start.IsZero() {
		start = task.CreatedAt
	}
	if start.IsZero() {
		return ""
	}
	end := task.CompletedAt
	if end.IsZero() {
		end = task.LastActivityAt
	}
	if end.IsZero() {
		end = now
	}
	if end.Before(start) {
		return ""
	}
	return end.Sub(start).Round(time.Second).String()
}

func isTerminalAgentTaskStatus(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "completed", "failed", "killed", "not_found":
		return true
	default:
		return false
	}
}

func (m *ChatModel) clearToolsSections() {
	m.toolsSections = nil
}

func (m ChatModel) toolsWrappedLines() []string {
	rendered := m.renderedAgentWorkBuf()
	if strings.TrimSpace(rendered) == "" {
		return nil
	}
	toolsWidth := m.width - m.chatPaneWidth()
	toolsInnerWidth := max(1, toolsWidth-2)
	toolsContentWidth := max(1, toolsInnerWidth-1)
	wrappedTools := lipgloss.NewStyle().Width(toolsContentWidth).Render(rendered)
	return strings.Split(wrappedTools, "\n")
}
