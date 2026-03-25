package format

import (
	"fmt"
	"os"
	"strings"
	"time"
)

type Style int

const (
	StyleNormal Style = iota
	StyleDim
	StyleBold
	StyleToolBlue
	StyleToolPurple
	StyleToolOrange
	StyleToolCyan
	StyleDiffAdd
	StyleDiffRemove
	StyleDiffHunk
	StyleAccentBorder
	StyleSuccess
	StyleError
	StyleWarning
	StyleStats
)

type Span struct {
	Text  string
	Style Style
}

type Line struct {
	Spans []Span
}

type ToolStatus int

const (
	StatusRunning ToolStatus = iota
	StatusSuccess
	StatusError
	StatusPending
)

func AgentLine(text string) Line {
	return Line{Spans: []Span{
		{Text: " │ ", Style: StyleAccentBorder},
		{Text: text, Style: StyleNormal},
	}}
}

func ToolStyle(name string) Style {
	switch name {
	case "edit_file":
		return StyleToolPurple
	case "write_file":
		return StyleToolOrange
	case "run_command":
		return StyleToolCyan
	default:
		return StyleToolBlue
	}
}

func isExpandedTool(name string) bool {
	return name == "edit_file" || name == "write_file"
}

func Diff(raw string) []Line {
	var lines []Line
	for _, s := range strings.Split(raw, "\n") {
		if s == "" {
			continue
		}
		switch {
		case strings.HasPrefix(s, "---") || strings.HasPrefix(s, "+++"):
			lines = append(lines, Line{Spans: []Span{{Text: "  " + s, Style: StyleDiffHunk}}})
		case strings.HasPrefix(s, "@@"):
			lines = append(lines, Line{Spans: []Span{{Text: "  " + s, Style: StyleDiffHunk}}})
		case strings.HasPrefix(s, "+"):
			lines = append(lines, Line{Spans: []Span{{Text: "  " + s, Style: StyleDiffAdd}}})
		case strings.HasPrefix(s, "-"):
			lines = append(lines, Line{Spans: []Span{{Text: "  " + s, Style: StyleDiffRemove}}})
		default:
			lines = append(lines, Line{Spans: []Span{{Text: "  " + s, Style: StyleDim}}})
		}
	}
	return lines
}

func ToolBox(name, summary, detail string, status ToolStatus, width int) []Line {
	if width < 20 {
		width = 20
	}
	innerW := width - 4
	ts := ToolStyle(name)

	statusText := ""
	statusStyle := StyleSuccess
	switch status {
	case StatusSuccess:
		statusText = "✓"
	case StatusError:
		statusText = "✗"
		statusStyle = StyleError
	case StatusRunning:
		statusText = "..."
		statusStyle = StyleDim
	case StatusPending:
		statusText = "?"
		statusStyle = StyleWarning
	}

	var lines []Line

	// Top border
	topBar := "┌" + strings.Repeat("─", width-2) + "┐"
	lines = append(lines, Line{Spans: []Span{{Text: topBar, Style: StyleDim}}})

	// Header line
	headerParts := []Span{
		{Text: "│ ", Style: StyleDim},
		{Text: "● ", Style: ts},
		{Text: name, Style: ts},
	}
	if summary != "" {
		headerParts = append(headerParts, Span{Text: "  " + summary, Style: StyleDim})
	}
	if statusText != "" {
		usedLen := 2 + 2 + len(name)
		if summary != "" {
			usedLen += 2 + len(summary)
		}
		statusLen := 1 + len(statusText)
		pad := innerW - usedLen - statusLen
		if pad < 1 {
			pad = 1
		}
		headerParts = append(headerParts, Span{Text: strings.Repeat(" ", pad), Style: StyleNormal})
		headerParts = append(headerParts, Span{Text: statusText, Style: statusStyle})
	}
	headerParts = append(headerParts, Span{Text: " │", Style: StyleDim})
	lines = append(lines, Line{Spans: headerParts})

	// Detail content (for expanded tools or failed commands)
	if detail != "" && (isExpandedTool(name) || status == StatusError) {
		diffLines := Diff(detail)
		for _, dl := range diffLines {
			row := []Span{{Text: "│", Style: StyleDim}}
			row = append(row, dl.Spans...)
			padLen := width - 2 - spanTextLen(dl.Spans)
			if padLen > 0 {
				row = append(row, Span{Text: strings.Repeat(" ", padLen), Style: StyleNormal})
			}
			row = append(row, Span{Text: "│", Style: StyleDim})
			lines = append(lines, Line{Spans: row})
		}
	}

	// Bottom border
	botBar := "└" + strings.Repeat("─", width-2) + "┘"
	lines = append(lines, Line{Spans: []Span{{Text: botBar, Style: StyleDim}}})

	return lines
}

func spanTextLen(spans []Span) int {
	n := 0
	for _, s := range spans {
		n += len(s.Text)
	}
	return n
}

func Truncate(output string, maxLines int) (string, bool) {
	lines := strings.Split(output, "\n")
	if len(lines) <= maxLines {
		return output, false
	}
	truncated := strings.Join(lines[:maxLines], "\n")
	remaining := len(lines) - maxLines
	truncated += fmt.Sprintf("\n... (%d more lines)", remaining)
	return truncated, true
}

func Stats(duration time.Duration, inputTokens, outputTokens int) Line {
	s := fmt.Sprintf(" ⏱ %.1fs", duration.Seconds())
	if inputTokens > 0 || outputTokens > 0 {
		s += fmt.Sprintf(" · ↑%s ↓%s tokens", formatCount(inputTokens), formatCount(outputTokens))
	}
	return Line{Spans: []Span{{Text: s, Style: StyleStats}}}
}

func formatCount(n int) string {
	if n >= 1000 {
		return fmt.Sprintf("%.1fk", float64(n)/1000)
	}
	return fmt.Sprintf("%d", n)
}

func Approval(action, path string) Line {
	return Line{Spans: []Span{
		{Text: " " + action + "?", Style: StyleWarning},
		{Text: " [y]es", Style: StyleSuccess},
		{Text: " [n]o", Style: StyleError},
	}}
}

// NoColor returns true if colors should be disabled (NO_COLOR env var).
func NoColor() bool {
	_, set := os.LookupEnv("NO_COLOR")
	return set
}

// use256Color returns true if terminal supports 256 colors.
func use256Color() bool {
	if NoColor() {
		return false
	}
	if ct := os.Getenv("COLORTERM"); ct == "truecolor" || ct == "24bit" {
		return true
	}
	return strings.Contains(os.Getenv("TERM"), "256color")
}

func styleToANSI(s Style) string {
	ext := use256Color()
	switch s {
	case StyleDim:
		return "\033[2m"
	case StyleBold:
		return "\033[1m"
	case StyleToolBlue:
		if ext {
			return "\033[38;5;75m"
		}
		return "\033[34m"
	case StyleToolPurple:
		if ext {
			return "\033[38;5;141m"
		}
		return "\033[35m"
	case StyleToolOrange:
		if ext {
			return "\033[38;5;215m"
		}
		return "\033[33m"
	case StyleToolCyan:
		if ext {
			return "\033[38;5;110m"
		}
		return "\033[36m"
	case StyleDiffAdd:
		if ext {
			return "\033[32m\033[48;5;22m"
		}
		return "\033[32m"
	case StyleDiffRemove:
		if ext {
			return "\033[31m\033[48;5;52m"
		}
		return "\033[31m"
	case StyleDiffHunk:
		return "\033[2m"
	case StyleAccentBorder:
		if ext {
			return "\033[38;5;75m"
		}
		return "\033[34m"
	case StyleSuccess:
		return "\033[32m"
	case StyleError:
		return "\033[31m"
	case StyleWarning:
		return "\033[33m"
	case StyleStats:
		return "\033[2m"
	default:
		return ""
	}
}

const ansiReset = "\033[0m"

func LineToANSI(line Line, colors bool) string {
	var sb strings.Builder
	for _, span := range line.Spans {
		if colors {
			code := styleToANSI(span.Style)
			if code != "" {
				sb.WriteString(code)
				sb.WriteString(span.Text)
				sb.WriteString(ansiReset)
				continue
			}
		}
		sb.WriteString(span.Text)
	}
	return sb.String()
}

func ToANSI(lines []Line, colors bool) string {
	var sb strings.Builder
	for i, line := range lines {
		if i > 0 {
			sb.WriteByte('\n')
		}
		sb.WriteString(LineToANSI(line, colors))
	}
	return sb.String()
}
