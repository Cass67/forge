package history

import (
	"fmt"
	"strings"
)

// FormatList renders a table of session summaries for terminal output.
func FormatList(sessions []SessionSummary) string {
	if len(sessions) == 0 {
		return "No sessions found.\n"
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("%-22s %-10s %-25s %s\n", "ID", "STATUS", "MODELS", "PROMPT"))
	sb.WriteString(strings.Repeat("─", 90) + "\n")
	for _, s := range sessions {
		models := s.Writer
		if s.Auditor != "" && s.Auditor != s.Writer {
			models += "/" + s.Auditor
		}
		if len(models) > 25 {
			models = models[:22] + "..."
		}
		prompt := s.Prompt
		if len(prompt) > 40 {
			prompt = prompt[:37] + "..."
		}
		status := s.Status
		if status == "" {
			status = "?"
		}
		sb.WriteString(fmt.Sprintf("%-22s %-10s %-25s %s\n", s.ID, status, models, prompt))
	}
	return sb.String()
}

// FormatDetail renders full session details for terminal output.
func FormatDetail(d *SessionDetail) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Session: %s\n", d.Meta.ID))
	sb.WriteString(fmt.Sprintf("Dir:     %s\n", d.Dir))
	sb.WriteString(fmt.Sprintf("Status:  %s\n", d.Meta.Status))
	if d.Meta.AbortReason != "" {
		sb.WriteString(fmt.Sprintf("Reason:  %s\n", d.Meta.AbortReason))
	}
	sb.WriteString(fmt.Sprintf("Writer:  %s\n", d.Meta.Writer))
	sb.WriteString(fmt.Sprintf("Auditor: %s\n", d.Meta.Auditor))
	sb.WriteString(fmt.Sprintf("Rounds:  %d per pass\n", d.Meta.RoundsPerPass))
	if !d.Meta.StartedAt.IsZero() {
		sb.WriteString(fmt.Sprintf("Started: %s\n", d.Meta.StartedAt.Format("2006-01-02 15:04:05")))
	}
	if d.Meta.CompletedAt != nil {
		sb.WriteString(fmt.Sprintf("Ended:   %s\n", d.Meta.CompletedAt.Format("2006-01-02 15:04:05")))
	}

	sb.WriteString(fmt.Sprintf("\nPrompt:\n  %s\n", d.Meta.Prompt))

	if len(d.Meta.Passes) > 0 {
		sb.WriteString("\nPasses:\n")
		for _, p := range d.Meta.Passes {
			sb.WriteString(fmt.Sprintf("  %-15s %d rounds  %s\n", p.Name, p.RoundsCompleted, p.Status))
		}
	}

	if len(d.CodeFiles) > 0 {
		sb.WriteString("\nCode files:\n")
		for _, f := range d.CodeFiles {
			sb.WriteString(fmt.Sprintf("  %-40s %s\n", f.Name, formatSize(f.Size)))
		}
	}

	if len(d.Artifacts) > 0 {
		sb.WriteString("\nArtifacts:\n")
		for _, f := range d.Artifacts {
			sb.WriteString(fmt.Sprintf("  %-40s %s\n", f.Name, formatSize(f.Size)))
		}
	}

	return sb.String()
}

func formatSize(bytes int64) string {
	if bytes < 1024 {
		return fmt.Sprintf("%d B", bytes)
	}
	return fmt.Sprintf("%.1f KB", float64(bytes)/1024)
}
