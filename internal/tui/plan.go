package tui

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// PlanStep represents a single step in a plan checklist.
type PlanStep struct {
	Title  string
	Status string
	Notes  string
}

// PlanStepStatus constants for well-known states.
const (
	planStepCompleted  = "completed"
	planStepInProgress = "in_progress"
	planStepPending    = "pending"
	planStepBlocked    = "blocked"
	planStepFailed     = "failed"
)

// planStepRegex matches lines like "- [completed] Do thing" or "- [in_progress] Do thing".
var planStepRegex = regexp.MustCompile(`(?m)^\s*[-*0-9.]+\s*\[([a-zA-Z_]+)\]\s*(.*)$`)

// parsePlanSteps extracts structured steps from plan content.
func parsePlanSteps(content string) []PlanStep {
	matches := planStepRegex.FindAllStringSubmatch(content, -1)
	steps := make([]PlanStep, 0, len(matches))
	for _, m := range matches {
		if len(m) < 3 {
			continue
		}
		status := strings.TrimSpace(m[1])
		title := strings.TrimSpace(m[2])
		// Trim any trailing notes after the title
		if idx := strings.Index(title, "  "); idx > 0 {
			title = title[:idx]
		}
		steps = append(steps, PlanStep{Title: title, Status: status})
	}
	return steps
}

// renderPlanContent renders structured plan content with progress indicators,
// checkbox-style icons, and a progress bar.
func renderPlanContent(content string, width int, theme chatTheme) string {
	if width < 10 {
		width = 10
	}
	steps := parsePlanSteps(content)
	if len(steps) == 0 {
		return renderMessageContent(content, width, theme)
	}

	var out strings.Builder

	// Preserve leading prose before the first step
	firstMatch := 0
	if m := planStepRegex.FindStringIndex(content); m != nil {
		firstMatch = m[0]
	}
	if leading := strings.TrimSpace(content[:firstMatch]); leading != "" {
		out.WriteString(renderMessageContent(leading, width, theme))
		out.WriteString("\n\n")
	}

	// Render progress bar + stats
	progress := formatPlanProgressBar(steps, width, theme)
	if progress != "" {
		out.WriteString(progress)
		out.WriteString("\n\n")
	}

	// Render each step with icon and color
	for _, step := range steps {
		line := formatPlanStepLine(step, width, theme)
		if line != "" {
			out.WriteString(line)
			out.WriteString("\n")
		}
	}

	// Append any trailing prose after the last step
	lastMatchEnd := 0
	for _, m := range planStepRegex.FindAllStringIndex(content, -1) {
		lastMatchEnd = m[1]
	}
	if trailing := strings.TrimSpace(content[lastMatchEnd:]); trailing != "" {
		out.WriteString("\n")
		out.WriteString(renderMessageContent(trailing, width, theme))
	}

	return strings.TrimSpace(out.String())
}

// formatPlanProgressBar creates a compact progress bar for the plan.
func formatPlanProgressBar(steps []PlanStep, width int, theme chatTheme) string {
	if len(steps) == 0 {
		return ""
	}
	completed := 0
	failed := 0
	blocked := 0
	inProgress := 0
	for _, s := range steps {
		switch s.Status {
		case planStepCompleted:
			completed++
		case planStepFailed:
			failed++
		case planStepBlocked:
			blocked++
		case planStepInProgress:
			inProgress++
		}
	}

	total := len(steps)
	done := completed
	pct := 0.0
	if total > 0 {
		pct = float64(done) / float64(total)
	}

	barWidth := max(8, min(30, width-20))
	filled := int(pct * float64(barWidth))
	if filled > barWidth {
		filled = barWidth
	}
	bar := strings.Repeat("━", filled) + strings.Repeat("─", barWidth-filled)

	stats := fmt.Sprintf("%d/%d", done, total)
	if failed > 0 {
		stats += fmt.Sprintf(" · %d failed", failed)
	}
	if blocked > 0 {
		stats += fmt.Sprintf(" · %d blocked", blocked)
	}
	if inProgress > 0 {
		stats += fmt.Sprintf(" · %d active", inProgress)
	}

	barStyle := lipgloss.NewStyle().Foreground(theme.AccentPrimary)
	if failed > 0 || blocked > 0 {
		barStyle = lipgloss.NewStyle().Foreground(theme.Warning)
	}
	if done == total {
		barStyle = lipgloss.NewStyle().Foreground(theme.Success)
	}

	return lipgloss.NewStyle().
		Foreground(theme.TextDim).
		Render("Progress ") +
		barStyle.Render(bar) + " " +
		lipgloss.NewStyle().Foreground(theme.Text).Render(stats)
}

// formatPlanStepLine renders a single step with an icon and color.
func formatPlanStepLine(step PlanStep, width int, theme chatTheme) string {
	status := normalizePlanStepStatus(step.Status)
	var icon string
	var color lipgloss.Color

	switch status {
	case planStepCompleted:
		icon = "✓"
		color = theme.TaskCompleted
	case planStepInProgress:
		icon = "▶"
		color = theme.TaskActive
	case planStepBlocked:
		icon = "⊘"
		color = theme.TaskBlocked
	case planStepFailed:
		icon = "✗"
		color = theme.Error
	default:
		icon = "○"
		color = theme.TaskPending
	}

	iconStyled := lipgloss.NewStyle().Foreground(color).Render(icon)
	titleStyled := lipgloss.NewStyle().Foreground(theme.Text).Render(step.Title)
	line := iconStyled + " " + titleStyled

	// If step has notes, append them dimmed
	if step.Notes != "" {
		notes := lipgloss.NewStyle().Foreground(theme.TextDim).Render("  " + step.Notes)
		line += "\n" + notes
	}

	return lipgloss.NewStyle().Width(width).Render(line)
}

// planStepStatus is the normalized status of a plan step.
type planStepStatus string

const (
	planStepPlain planStepStatus = "plain"
)

// planStep is the internal representation of a parsed plan line.
type planStep struct {
	Text   string
	Status planStepStatus
}

// stickyPlan returns the parsed plan content for the sticky plan,
// or empty string if no plan is active.
func (m ChatModel) stickyPlan() string {
	return strings.TrimSpace(m.stickyPlanContent)
}

// stickyPlanHeight returns the rendered height of the sticky plan,
// or 0 if no plan is active.
func (m ChatModel) stickyPlanHeight() int {
	if m.stickyPlan() == "" {
		return 0
	}
	// Collapsed plan: accent header + 1 line for each step + progress bar
	steps := parsePlanSteps(m.stickyPlanContent)
	n := len(steps)
	if n == 0 {
		return 0
	}
	// 1 for header, 1 for progress bar, n for steps + trailing blank line
	return min(n, 5) + 3
}

// renderStickyPlan renders the pinned plan above the chat viewport.
func (m ChatModel) renderStickyPlan(theme chatTheme) string {
	content := m.stickyPlan()
	if content == "" {
		return ""
	}
	width := max(10, m.width)

	header := lipgloss.NewStyle().
		Foreground(theme.Warning).
		Bold(true).
		Render(fitCell(" Plan ", width))

	body := renderPlanContent(content, width, theme)

	// Limit the sticky plan height so it doesn't consume the whole screen
	lines := strings.Split(body, "\n")
	maxLines := 7
	if len(lines) > maxLines {
		lines = lines[:maxLines]
		lines = append(lines, lipgloss.NewStyle().
			Foreground(theme.TextDim).
			Italic(true).
			Render("… more steps — scroll in chat"))
	}

	panel := header + "\n" + strings.Join(lines, "\n")

	return lipgloss.NewStyle().
		Width(width).
		Render(panel)
}

// currentPlanProgress returns the progress stats for the current plan,
// or nil if no plan is active.
func (m ChatModel) currentPlanProgress() *planProgressStats {
	for i := len(m.messages) - 1; i >= 0; i-- {
		if m.messages[i].Kind != MsgPlan {
			continue
		}
		content := strings.TrimSpace(m.messages[i].Content)
		if content == "" {
			return nil
		}
		steps := parsePlanSteps(content)
		if len(steps) == 0 {
			return nil
		}

		total := len(steps)
		completed := 0
		inProgress := 0
		blocked := 0
		failed := 0
		for _, s := range steps {
			switch s.Status {
			case planStepCompleted:
				completed++
			case planStepInProgress:
				inProgress++
			case planStepBlocked:
				blocked++
			case planStepFailed:
				failed++
			}
		}

		pct := 0
		if total > 0 {
			pct = (completed * 100) / total
		}

		return &planProgressStats{
			Total:      total,
			Completed:  completed,
			InProgress: inProgress,
			Blocked:    blocked,
			Failed:     failed,
			Percent:    pct,
		}
	}
	return nil
}

// renderPlanProgressBarCompact returns a short progress string for the stats footer.
// Examples: "plan 3/7 ████░░░" or "plan 3/7 · 2 active"
func renderPlanProgressBarCompact(stats planProgressStats, theme chatTheme) string {
	if stats.Total == 0 {
		return ""
	}

	barWidth := 7
	filled := (stats.Percent * barWidth) / 100
	bar := strings.Repeat("━", filled) + strings.Repeat("─", barWidth-filled)

	statsStr := fmt.Sprintf("%d/%d", stats.Completed, stats.Total)
	label := "plan " + statsStr

	if stats.InProgress > 0 {
		label += fmt.Sprintf(" · %d active", stats.InProgress)
	}
	if stats.Blocked > 0 {
		label += fmt.Sprintf(" · %d blocked", stats.Blocked)
	}

	barStyle := lipgloss.NewStyle().Foreground(theme.AccentPrimary)
	if stats.Failed > 0 || stats.Blocked > 0 {
		barStyle = lipgloss.NewStyle().Foreground(theme.Warning)
	}
	if stats.Completed == stats.Total {
		barStyle = lipgloss.NewStyle().Foreground(theme.Success)
	}

	return label + " " + barStyle.Render(bar)
}

// planProgressStats holds aggregated progress for a plan.
type planProgressStats struct {
	Total      int
	Completed  int
	InProgress int
	Blocked    int
	Failed     int
	Percent    int
}
