package tui

import (
	"regexp"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// enhancedDiffBlock parses a raw diff body and returns a richer rendering
// with file headers, hunk headers, and +/- stats.
func enhancedDiffBlock(body string, width int, theme chatTheme) string {
	lines := strings.Split(strings.TrimRight(body, "\n"), "\n")
	if len(lines) == 0 || (len(lines) == 1 && lines[0] == "") {
		return ""
	}
	var out []string
	reHunk := regexp.MustCompile(`^@@ -(\d+),(\d+) \+(\d+),(\d+) @@(.*)`)
	for _, line := range lines {
		trim := line
		switch {
		case strings.HasPrefix(trim, "+++ ") || strings.HasPrefix(trim, "--- "):
			s := lipgloss.NewStyle().Foreground(theme.TextDim).Width(width)
			out = append(out, s.Render(trim))
		case strings.HasPrefix(trim, "@@"):
			m := reHunk.FindStringSubmatch(trim)
			hdr := trim
			if m != nil {
				hdr = "@@ -" + m[1] + "," + m[2] + " +" + m[3] + "," + m[4] + " @@" + m[5]
			}
			s := lipgloss.NewStyle().Foreground(theme.AccentSecondary).Width(width)
			out = append(out, s.Render(hdr))
		case strings.HasPrefix(trim, "+"):
			s := lipgloss.NewStyle().Foreground(theme.Success).Width(width)
			out = append(out, s.Render(trim))
		case strings.HasPrefix(trim, "-"):
			s := lipgloss.NewStyle().Foreground(theme.Error).Width(width)
			out = append(out, s.Render(trim))
		default:
			s := lipgloss.NewStyle().Foreground(theme.Text).Width(width)
			out = append(out, s.Render(trim))
		}
	}
	return strings.Join(out, "\n")
}
