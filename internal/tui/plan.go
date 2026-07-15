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
		color = theme.Success
	case planStepInProgress:
		icon = "▶"
		color = theme.AccentPrimary
	case planStepBlocked:
		icon = "⊘"
		color = theme.Warning
	case planStepFailed:
		icon = "✗"
		color = theme.Error
	default:
		icon = "○"
		color = theme.TextDim
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

// parsePlanStep parses a single line into a planStep.
func parsePlanStep(line string) planStep {
	m := planStepRegex.FindStringSubmatch(line)
	if len(m) >= 3 {
		status := strings.ToLower(strings.TrimSpace(m[1]))
		var s planStepStatus
		switch status {
		case planStepCompleted, "done", "finished", "complete":
			s = planStepCompleted
		case planStepInProgress, "active", "running", "doing":
			s = planStepInProgress
		case planStepBlocked, "waiting", "stuck":
			s = planStepBlocked
		case planStepFailed, "error", "errored":
			s = planStepFailed
		default:
			s = planStepPending
		}
		return planStep{Text: strings.TrimSpace(m[2]), Status: s}
	}
	text := strings.TrimSpace(line)
	// Strip common list markers for plain items
	text = regexp.MustCompile(`^\s*[-*0-9.]+\s*`).ReplaceAllString(text, "")
	return planStep{Text: text, Status: planStepPlain}
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

// computePlanProgress aggregates step statuses into stats.
func computePlanProgress(steps []planStep) planProgressStats {
	var s planProgressStats
	s.Total = len(steps)
	for _, step := range steps {
		switch step.Status {
		case planStepCompleted:
			s.Completed++
		case planStepInProgress:
			s.InProgress++
		case planStepBlocked:
			s.Blocked++
		case planStepFailed:
			s.Failed++
		}
	}
	if s.Total > 0 {
		s.Percent = (s.Completed * 100) / s.Total
	}
	return s
}

// renderPlanProgressBar renders a compact progress bar string.
func renderPlanProgressBar(stats planProgressStats, width int, theme chatTheme) string {
	if stats.Total == 0 {
		return ""
	}
	barWidth := max(8, min(30, width-20))
	filled := (stats.Percent * barWidth) / 100
	bar := strings.Repeat("━", filled) + strings.Repeat("─", barWidth-filled)

	statsStr := fmt.Sprintf("%d/%d", stats.Completed, stats.Total)
	if stats.InProgress > 0 {
		statsStr += fmt.Sprintf(" · %d active", stats.InProgress)
	}
	if stats.Blocked > 0 {
		statsStr += fmt.Sprintf(" · %d blocked", stats.Blocked)
	}
	if stats.Failed > 0 {
		statsStr += fmt.Sprintf(" · %d failed", stats.Failed)
	}

	barStyle := lipgloss.NewStyle().Foreground(theme.AccentPrimary)
	if stats.Failed > 0 || stats.Blocked > 0 {
		barStyle = lipgloss.NewStyle().Foreground(theme.Warning)
	}
	if stats.Completed == stats.Total {
		barStyle = lipgloss.NewStyle().Foreground(theme.Success)
	}

	return lipgloss.NewStyle().Foreground(theme.TextDim).Render("Progress ") +
		barStyle.Render(bar) + " " +
		lipgloss.NewStyle().Foreground(theme.Text).Render(statsStr)
}

// renderPlanStepIcon returns the styled icon for a plan step.
func renderPlanStepIcon(step planStep, theme chatTheme) string {
	var icon string
	var color lipgloss.Color
	switch step.Status {
	case planStepCompleted:
		icon = "✓"
		color = theme.Success
	case planStepInProgress:
		icon = "▶"
		color = theme.AccentPrimary
	case planStepBlocked:
		icon = "⊘"
		color = theme.Warning
	case planStepFailed:
		icon = "✗"
		color = theme.Error
	default:
		icon = "○"
		color = theme.TextDim
	}
	return lipgloss.NewStyle().Foreground(color).Render(icon)
}
