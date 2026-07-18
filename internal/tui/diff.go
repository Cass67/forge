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
// with line numbers, +/- styling, and word-level highlighting. Wide terminals
// get a side-by-side two-column view; narrow ones the unified view.
func enhancedDiffBlock(body string, width int, theme chatTheme) string {
	if width >= sideBySideMinWidth {
		if out := sideBySideDiffBlock(body, width, theme); out != "" {
			return out
		}
	}
	return unifiedDiffBlock(body, width, theme)
}

func unifiedDiffBlock(body string, width int, theme chatTheme) string {
	lines := strings.Split(strings.TrimRight(body, "\n"), "\n")
	if len(lines) == 0 || (len(lines) == 1 && lines[0] == "") {
		return ""
	}
	var out []string
	reHunk := regexp.MustCompile(`^@@ -(\d+),(\d+) \+(\d+),(\d+) @@(.*)`)

	// Track old/new line numbers across hunks
	oldLnum := 0
	newLnum := 0

	// Buffer for word-diff pairing: pending old lines
	type pendingOld struct {
		content string
		oldNum  int
	}
	var pending []pendingOld

	flushPending := func() {
		for _, p := range pending {
			out = append(out, renderDiffLine("-"+p.content, p.oldNum, 0, theme, width, ""))
		}
		pending = nil
	}

	for _, line := range lines {
		trim := strings.TrimRight(line, "\n\r")

		switch {
		case strings.HasPrefix(trim, "+++ ") || strings.HasPrefix(trim, "--- "):
			flushPending()
			s := lipgloss.NewStyle().Foreground(theme.TextDim).Width(width)
			out = append(out, s.Render(trim))

		case strings.HasPrefix(trim, "@@"):
			flushPending()
			m := reHunk.FindStringSubmatch(trim)
			if m != nil {
				oldLnum = parseIntOrZero(m[1])
				newLnum = parseIntOrZero(m[3])
			}
			hdr := trim
			s := lipgloss.NewStyle().Foreground(theme.AccentSecondary).Width(width)
			out = append(out, s.Render(hdr))

		case strings.HasPrefix(trim, "+"):
			content := trim[1:] // strip the '+'
			if len(pending) > 0 {
				// Pair with the first pending old line for word-level diff
				p := pending[0]
				pending = pending[1:]

				oldContent := p.content
				oldHighlight, newHighlight := wordDiff(oldContent, content)
				out = append(out, renderDiffLine("-"+oldContent, p.oldNum, 0, theme, width, oldHighlight))
				out = append(out, renderDiffLine("+"+content, 0, newLnum, theme, width, newHighlight))
				newLnum++
			} else {
				out = append(out, renderDiffLine(trim, 0, newLnum, theme, width, ""))
				newLnum++
			}

		case strings.HasPrefix(trim, "-"):
			content := trim[1:] // strip the '-'
			pending = append(pending, pendingOld{content: content, oldNum: oldLnum})
			oldLnum++

		default:
			flushPending()
			// Context line (both old and new present)
			if oldLnum > 0 || newLnum > 0 {
				out = append(out, renderDiffLine(trim, oldLnum, newLnum, theme, width, ""))
				oldLnum++
				newLnum++
			} else {
				// Lines before any hunk header — render as plain
				s := lipgloss.NewStyle().Foreground(theme.Text).Width(width)
				out = append(out, s.Render(trim))
			}
		}
	}
	flushPending()

	return strings.Join(out, "\n")
}

// sideBySideDiffBlock renders a diff as two aligned columns: old file on the
// left, new file on the right, each with its own line numbers. Deleted lines
// leave a gap on the right, added lines a gap on the left; changed pairs sit
// on the same row with word-level highlighting.
func sideBySideDiffBlock(body string, width int, theme chatTheme) string {
	lines := strings.Split(strings.TrimRight(body, "\n"), "\n")
	if len(lines) == 0 || (len(lines) == 1 && lines[0] == "") {
		return ""
	}
	const gap = 2
	cellW := (width - gap) / 2
	reHunk := regexp.MustCompile(`^@@ -(\d+),(\d+) \+(\d+),(\d+) @@(.*)`)

	oldLnum := 0
	newLnum := 0

	joinRow := func(left, right string) string {
		return lipgloss.JoinHorizontal(lipgloss.Top, left, strings.Repeat(" ", gap), right)
	}
	blank := renderSBSCell(0, "", "", theme, cellW)

	type pendingOld struct {
		content string
		oldNum  int
	}
	var pending []pendingOld
	var out []string

	flushPending := func() {
		for _, p := range pending {
			out = append(out, joinRow(renderSBSCell(p.oldNum, "-"+p.content, "", theme, cellW), blank))
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
				oldHL, newHL := wordDiff(p.content, content)
				out = append(out, joinRow(
					renderSBSCell(p.oldNum, "-"+p.content, oldHL, theme, cellW),
					renderSBSCell(newLnum, "+"+content, newHL, theme, cellW)))
			} else {
				out = append(out, joinRow(blank, renderSBSCell(newLnum, "+"+content, "", theme, cellW)))
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
					renderSBSCell(oldLnum, content, "", theme, cellW),
					renderSBSCell(newLnum, content, "", theme, cellW)))
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
// border, then the (possibly word-highlighted) content wrapped to the cell
// width. A zero num with empty line yields an empty placeholder cell.
func renderSBSCell(num int, line string, wordHighlight string, theme chatTheme, cellWidth int) string {
	numWidth := 4
	numStr := ""
	if num > 0 {
		numStr = fmt.Sprintf("%d", num)
	}
	prefix := lipgloss.NewStyle().Foreground(theme.TextDim).Render(fmt.Sprintf("%*s", numWidth, numStr)) +
		lipgloss.NewStyle().Foreground(theme.Border).Render(" │ ")
	remaining := max(1, cellWidth-numWidth-3)

	var content string
	if wordHighlight != "" {
		content = renderWordHighlightedLine(line, wordHighlight, theme, remaining)
	} else {
		var style lipgloss.Style
		switch {
		case strings.HasPrefix(line, "+"):
			style = lipgloss.NewStyle().Foreground(theme.Success)
		case strings.HasPrefix(line, "-"):
			style = lipgloss.NewStyle().Foreground(theme.Error)
		default:
			style = lipgloss.NewStyle().Foreground(theme.Text)
		}
		content = style.Width(remaining).Render(line)
	}
	return lipgloss.JoinHorizontal(lipgloss.Top, prefix, content)
}

// renderDiffLine renders a single diff line with formatted line numbers
// and optional word-level highlighting markup.
// highlightWordRanges is a compact string of highlighted ranges, or empty.
func renderDiffLine(line string, oldNum, newNum int, theme chatTheme, width int, wordHighlight string) string {
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

	// Build the content portion with word-level highlighting if available
	var content string
	if wordHighlight != "" {
		content = renderWordHighlightedLine(line, wordHighlight, theme, remaining)
	} else {
		var contentStyle lipgloss.Style
		switch {
		case strings.HasPrefix(line, "+"):
			contentStyle = lipgloss.NewStyle().Foreground(theme.Success)
		case strings.HasPrefix(line, "-"):
			contentStyle = lipgloss.NewStyle().Foreground(theme.Error)
		default:
			contentStyle = lipgloss.NewStyle().Foreground(theme.Text)
		}
		content = contentStyle.Width(remaining).Render(line)
	}

	return prefix + content
}

// wordDiff computes word-level differences between two strings.
// Returns two strings (old markup, new markup) where differing words
// are wrapped with special markers for renderWordHighlightedLine.
// The markers use \x00 (NUL) as delimiter: \x00d\x00 for deleted, \x00a\x00 for added.
func wordDiff(oldText, newText string) (oldHighlight, newHighlight string) {
	if oldText == newText {
		return "", "" // no differences
	}

	oldWords := splitWords(oldText)
	newWords := splitWords(newText)

	// Build a simple LCS-based diff
	// Compute which old words survive and which are new
	lcs := lcsStrings(oldWords, newWords)

	oldMarkup := markupWords(oldWords, lcs, true)
	newMarkup := markupWords(newWords, lcs, false)

	return oldMarkup, newMarkup
}

// markupWords returns a string with word-level markers for highlighting.
// For old words: words not in LCS get \x00d\x00...\x00d\x00 markers
// For new words: words not in LCS get \x00a\x00...\x00a\x00 markers
func markupWords(words, lcs []string, isOld bool) string {
	var parts []string
	i := 0 // index into words
	j := 0 // index into lcs

	marker := string([]byte{0, 'd', 0}) // deleted marker for old
	if !isOld {
		marker = string([]byte{0, 'a', 0}) // added marker for new
	}

	for i < len(words) {
		if j < len(lcs) && words[i] == lcs[j] {
			// Common word — emit as-is
			parts = append(parts, words[i])
			j++
			i++
		} else {
			// Word not in LCS — wrap with markers
			parts = append(parts, marker+words[i]+marker)
			i++
		}
	}
	return strings.Join(parts, "")
}

// splitWords splits text into whitespace-delimited words, preserving
// whitespace as separate tokens so we can reconstruct the string.
func splitWords(text string) []string {
	if text == "" {
		return nil
	}
	// Split on whitespace boundaries, keeping the delimiter as a token
	var words []string
	buf := strings.Builder{}
	for _, r := range text {
		if r == ' ' || r == '\t' {
			if buf.Len() > 0 {
				words = append(words, buf.String())
				buf.Reset()
			}
			words = append(words, string(r))
		} else {
			buf.WriteRune(r)
		}
	}
	if buf.Len() > 0 {
		words = append(words, buf.String())
	}
	return words
}

// lcsStrings computes the longest common subsequence of two string slices.
func lcsStrings(a, b []string) []string {
	if len(a) == 0 || len(b) == 0 {
		return nil
	}
	m, n := len(a), len(b)
	dp := make([][]int, m+1)
	for i := range dp {
		dp[i] = make([]int, n+1)
	}
	for i := 1; i <= m; i++ {
		for j := 1; j <= n; j++ {
			if a[i-1] == b[j-1] {
				dp[i][j] = dp[i-1][j-1] + 1
			} else {
				if dp[i-1][j] > dp[i][j-1] {
					dp[i][j] = dp[i-1][j]
				} else {
					dp[i][j] = dp[i][j-1]
				}
			}
		}
	}
	// Backtrack to extract LCS
	var result []string
	i, j := m, n
	for i > 0 && j > 0 {
		if a[i-1] == b[j-1] {
			result = append(result, a[i-1])
			i--
			j--
		} else if dp[i-1][j] > dp[i][j-1] {
			i--
		} else {
			j--
		}
	}
	// Reverse
	for left, right := 0, len(result)-1; left < right; left, right = left+1, right-1 {
		result[left], result[right] = result[right], result[left]
	}
	return result
}

// renderWordHighlightedLine takes a diff line and a word-highlight markup string,
// and returns a styled version with inline highlighting.
// The markup uses \x00d\x00...\x00d\x00 for deleted words and \x00a\x00...\x00a\x00 for added words.
func renderWordHighlightedLine(line string, markup string, theme chatTheme, width int) string {
	// Determine line prefix for styling
	var contentStyle lipgloss.Style
	linePrefix := ""
	switch {
	case strings.HasPrefix(line, "+"):
		contentStyle = lipgloss.NewStyle().Foreground(theme.Success)
		linePrefix = "+"
	case strings.HasPrefix(line, "-"):
		contentStyle = lipgloss.NewStyle().Foreground(theme.Error)
		linePrefix = "-"
	default:
		contentStyle = lipgloss.NewStyle().Foreground(theme.Text)
	}

	delStyle := lipgloss.NewStyle().
		Foreground(theme.Error).
		Background(theme.HeaderBG)

	addStyle := lipgloss.NewStyle().
		Foreground(theme.Success).
		Background(theme.HeaderBG)

	delMarker := string([]byte{0, 'd', 0})
	addMarker := string([]byte{0, 'a', 0})

	// Build the content from markup segments
	var result strings.Builder

	remaining := markup
	for remaining != "" {
		// Find next marker
		delIdx := strings.Index(remaining, delMarker)
		addIdx := strings.Index(remaining, addMarker)

		if delIdx == -1 && addIdx == -1 {
			// No more highlighted words
			result.WriteString(contentStyle.Render(remaining))
			break
		}

		var marker string
		var markerStyle lipgloss.Style
		var idx int

		if delIdx >= 0 && (addIdx == -1 || delIdx < addIdx) {
			marker = delMarker
			markerStyle = delStyle
			idx = delIdx
		} else {
			marker = addMarker
			markerStyle = addStyle
			idx = addIdx
		}

		// Emit text before the marker
		if idx > 0 {
			result.WriteString(contentStyle.Render(remaining[:idx]))
		}

		// Skip the opening marker
		after := remaining[idx+3:]
		endIdx := strings.Index(after, marker)
		if endIdx == -1 {
			// Unclosed marker — emit rest as highlighted
			result.WriteString(markerStyle.Render(after))
			break
		}

		// Emit the highlighted word
		word := after[:endIdx]
		result.WriteString(markerStyle.Render(word))

		// Continue after the closing marker
		remaining = after[endIdx+3:]
	}

	// Prepend the diff prefix character (+/-) and apply width padding
	fullContent := linePrefix + result.String()
	if width > 0 {
		return contentStyle.Width(width).Render(fullContent)
	}
	return contentStyle.Render(fullContent)
}

// stripANSILen returns the length of a string with all ANSI escape sequences removed.
func stripANSILen(s string) int {
	re := regexp.MustCompile(`\x1b\[[0-9;]*m`)
	return len(re.ReplaceAllString(s, ""))
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
