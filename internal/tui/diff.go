package tui

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// compactDiffForDisplay trims a diff to its interesting parts for inline transcript
// display: headers and changed lines with up to 2 context lines around each change,
// capped at maxLines total with a trailing "… N more lines" marker.
func compactDiffForDisplay(diff string, maxLines int) string {
	lines := strings.Split(strings.TrimRight(diff, "\n"), "\n")
	keep := make([]bool, len(lines))
	isChange := func(l string) bool {
		return (strings.HasPrefix(l, "+") || strings.HasPrefix(l, "-")) &&
			!strings.HasPrefix(l, "+++") && !strings.HasPrefix(l, "---")
	}
	for i, l := range lines {
		if strings.HasPrefix(l, "---") || strings.HasPrefix(l, "+++") ||
			strings.HasPrefix(l, "@@") || strings.HasPrefix(l, "diff --git") {
			keep[i] = true
			continue
		}
		if isChange(l) {
			for j := max(0, i-2); j <= min(len(lines)-1, i+2); j++ {
				keep[j] = true
			}
		}
	}
	var out []string
	skipped := 0
	gap := false
	for i, l := range lines {
		if !keep[i] {
			gap = true
			continue
		}
		if gap && len(out) > 0 {
			out = append(out, "@@")
			gap = false
		}
		if len(out) >= maxLines {
			skipped = len(lines) - i
			break
		}
		out = append(out, l)
	}
	if skipped > 0 {
		out = append(out, fmt.Sprintf("… %d more lines", skipped))
	}
	return strings.Join(out, "\n")
}

// sideBySideMinWidth is the minimum terminal width for the two-column diff
// view; below it the unified single-column rendering is used instead.
const sideBySideMinWidth = 100

// enhancedDiffBlock parses a raw diff body and returns a richer rendering
// with line numbers and +/- styling. Wide terminals
// get a side-by-side two-column view; narrow ones the unified view.
func enhancedDiffBlock(body string, width int, theme chatTheme) string {
	if width >= sideBySideMinWidth {
		if out := sideBySideDiffBlock(body, width, theme); out != "" {
			return out
		}
	}
	return unifiedDiffBlock(body, width, theme)
}

// Hoisted out of the render path: these were recompiled on every diff block
// and, for the ANSI strip, on every line of one.
var (
	reHunk    = regexp.MustCompile(`^@@ -(\d+),(\d+) \+(\d+),(\d+) @@(.*)`)
	reANSISGR = regexp.MustCompile(`\x1b\[[0-9;]*m`)
)

func unifiedDiffBlock(body string, width int, theme chatTheme) string {
	lines := strings.Split(strings.TrimRight(body, "\n"), "\n")
	if len(lines) == 0 || (len(lines) == 1 && lines[0] == "") {
		return ""
	}
	var out []string

	// Track old/new line numbers across hunks
	oldLnum := 0
	newLnum := 0

	for _, line := range lines {
		trim := strings.TrimRight(line, "\n\r")

		switch {
		case strings.HasPrefix(trim, "+++ ") || strings.HasPrefix(trim, "--- "):
			s := lipgloss.NewStyle().Foreground(theme.TextDim).Width(width)
			out = append(out, s.Render(trim))

		case strings.HasPrefix(trim, "@@"):
			m := reHunk.FindStringSubmatch(trim)
			if m != nil {
				oldLnum = parseIntOrZero(m[1])
				newLnum = parseIntOrZero(m[3])
			}
			hdr := trim
			s := lipgloss.NewStyle().Foreground(theme.AccentSecondary).Width(width)
			out = append(out, s.Render(hdr))

		case strings.HasPrefix(trim, "+"):
			out = append(out, renderDiffLine(trim, 0, newLnum, theme, width))
			newLnum++

		case strings.HasPrefix(trim, "-"):
			out = append(out, renderDiffLine(trim, oldLnum, 0, theme, width))
			oldLnum++

		default:
			// Context line (both old and new present)
			if oldLnum > 0 || newLnum > 0 {
				out = append(out, renderDiffLine(trim, oldLnum, newLnum, theme, width))
				oldLnum++
				newLnum++
			} else {
				// Lines before any hunk header — render as plain
				s := lipgloss.NewStyle().Foreground(theme.Text).Width(width)
				out = append(out, s.Render(trim))
			}
		}
	}

	return strings.Join(out, "\n")
}

// sideBySideDiffBlock renders a diff as two aligned columns: old file on the
// left, new file on the right, each with its own line numbers. Deleted lines
// leave a gap on the right, added lines a gap on the left; changed pairs sit
// on the same row.
func sideBySideDiffBlock(body string, width int, theme chatTheme) string {
	lines := strings.Split(strings.TrimRight(body, "\n"), "\n")
	if len(lines) == 0 || (len(lines) == 1 && lines[0] == "") {
		return ""
	}
	const gap = 2
	cellW := (width - gap) / 2

	oldLnum := 0
	newLnum := 0

	joinRow := func(left, right string) string {
		return lipgloss.JoinHorizontal(lipgloss.Top, left, strings.Repeat(" ", gap), right)
	}
	blank := renderSBSCell(0, "", theme, cellW)

	type pendingOld struct {
		content string
		oldNum  int
	}
	var pending []pendingOld
	var out []string

	flushPending := func() {
		for _, p := range pending {
			out = append(out, joinRow(renderSBSCell(p.oldNum, "-"+p.content, theme, cellW), blank))
		}
		pending = nil
	}

	for _, line := range lines {
		trim := strings.TrimRight(line, "\n\r")

		switch {
		case strings.HasPrefix(trim, "+++ ") || strings.HasPrefix(trim, "--- "):
			flushPending()
			out = append(out, lipgloss.NewStyle().Foreground(theme.TextDim).Width(width).Render(trim))

		case strings.HasPrefix(trim, "@@"):
			flushPending()
			if m := reHunk.FindStringSubmatch(trim); m != nil {
				oldLnum = parseIntOrZero(m[1])
				newLnum = parseIntOrZero(m[3])
			}
			out = append(out, lipgloss.NewStyle().Foreground(theme.AccentSecondary).Width(width).Render(trim))

		case strings.HasPrefix(trim, "+"):
			content := trim[1:]
			if len(pending) > 0 {
				p := pending[0]
				pending = pending[1:]
				out = append(out, joinRow(
					renderSBSCell(p.oldNum, "-"+p.content, theme, cellW),
					renderSBSCell(newLnum, "+"+content, theme, cellW)))
			} else {
				out = append(out, joinRow(blank, renderSBSCell(newLnum, "+"+content, theme, cellW)))
			}
			newLnum++

		case strings.HasPrefix(trim, "-"):
			pending = append(pending, pendingOld{content: trim[1:], oldNum: oldLnum})
			oldLnum++

		default:
			flushPending()
			if oldLnum > 0 || newLnum > 0 {
				content := strings.TrimPrefix(trim, " ")
				out = append(out, joinRow(
					renderSBSCell(oldLnum, content, theme, cellW),
					renderSBSCell(newLnum, content, theme, cellW)))
				oldLnum++
				newLnum++
			} else {
				out = append(out, lipgloss.NewStyle().Foreground(theme.Text).Width(width).Render(trim))
			}
		}
	}
	flushPending()

	return strings.Join(out, "\n")
}

// renderSBSCell renders one column cell: right-aligned line number, a thin
// border, then the content wrapped to the cell width. A zero num with empty
// line yields an empty placeholder cell.
func renderSBSCell(num int, line string, theme chatTheme, cellWidth int) string {
	numWidth := 4
	numStr := ""
	if num > 0 {
		numStr = fmt.Sprintf("%d", num)
	}
	prefix := lipgloss.NewStyle().Foreground(theme.TextDim).Render(fmt.Sprintf("%*s", numWidth, numStr)) +
		lipgloss.NewStyle().Foreground(theme.Border).Render(" │ ")
	remaining := max(1, cellWidth-numWidth-3)

	var style lipgloss.Style
	switch {
	case strings.HasPrefix(line, "+"):
		style = lipgloss.NewStyle().Foreground(theme.Success)
	case strings.HasPrefix(line, "-"):
		style = lipgloss.NewStyle().Foreground(theme.Error)
	default:
		style = lipgloss.NewStyle().Foreground(theme.Text)
	}
	content := style.Width(remaining).Render(line)
	return lipgloss.JoinHorizontal(lipgloss.Top, prefix, content)
}

// renderDiffLine renders a single diff line with formatted line numbers.
func renderDiffLine(line string, oldNum, newNum int, theme chatTheme, width int) string {
	numWidth := 4

	oldStr := ""
	if oldNum > 0 {
		oldStr = fmt.Sprintf("%d", oldNum)
	}
	newStr := ""
	if newNum > 0 {
		newStr = fmt.Sprintf("%d", newNum)
	}

	oldPad := fmt.Sprintf("%*s", numWidth, oldStr)
	newPad := fmt.Sprintf("%*s", numWidth, newStr)

	prefix := lipgloss.NewStyle().Foreground(theme.TextDim).Render(oldPad) +
		lipgloss.NewStyle().Foreground(theme.Border).Render(" │ ") +
		lipgloss.NewStyle().Foreground(theme.TextDim).Render(newPad) +
		lipgloss.NewStyle().Foreground(theme.Border).Render(" │ ")

	prefixLen := stripANSILen(prefix)
	remaining := max(1, width-prefixLen)

	var contentStyle lipgloss.Style
	switch {
	case strings.HasPrefix(line, "+"):
		contentStyle = lipgloss.NewStyle().Foreground(theme.Success)
	case strings.HasPrefix(line, "-"):
		contentStyle = lipgloss.NewStyle().Foreground(theme.Error)
	default:
		contentStyle = lipgloss.NewStyle().Foreground(theme.Text)
	}
	content := contentStyle.Width(remaining).Render(line)

	return prefix + content
}

// stripANSILen returns the length of a string with all ANSI escape sequences removed.
func stripANSILen(s string) int {
	return len(reANSISGR.ReplaceAllString(s, ""))
}

// parseIntOrZero parses a string as an int, returning 0 on failure.
func parseIntOrZero(s string) int {
	var n int
	for _, r := range s {
		if r >= '0' && r <= '9' {
			n = n*10 + int(r-'0')
		} else {
			return 0
		}
	}
	return n
}
