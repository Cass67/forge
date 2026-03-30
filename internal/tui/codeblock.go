package tui

import (
	"strings"
	"unicode"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/reflow/wordwrap"
)

type messageBlock struct {
	Fenced bool
	Lang   string
	Body   string
}

func renderMessageContent(content string, width int, theme chatTheme) string {
	if width < 10 {
		width = 10
	}
	blocks := parseMessageBlocks(content)
	rendered := make([]string, 0, len(blocks))
	for _, block := range blocks {
		body := strings.TrimRight(block.Body, "\n")
		if strings.TrimSpace(body) == "" {
			continue
		}
		if block.Fenced {
			rendered = append(rendered, renderCodeBlock(block.Lang, body, width, theme))
			continue
		}
		rendered = append(rendered, renderWrappedProseBlock(body, width, theme))
	}
	return strings.Join(rendered, "\n\n")
}

func renderWrappedProseBlock(body string, width int, theme chatTheme) string {
	lines := wrapProseLines(body, width)
	rendered := make([]string, 0, len(lines))
	for _, line := range lines {
		rendered = append(rendered, padStyledWidth(renderProseLine(line, theme), width))
	}
	return strings.Join(rendered, "\n")
}

func renderProseLine(line string, theme chatTheme) string {
	switch {
	case isMarkdownHeading(line):
		return renderMarkdownHeading(line, theme)
	case isMarkdownListItem(line):
		return renderMarkdownListItem(line, theme)
	default:
		return renderEmphasizedSemantic(line, profileProse, theme)
	}
}

func renderMarkdownHeading(line string, theme chatTheme) string {
	level := 0
	for level < len(line) && line[level] == '#' {
		level++
	}
	marker := lipgloss.NewStyle().
		Foreground(theme.AccentPrimary).
		Bold(true).
		Render(strings.Repeat("#", level))
	title := strings.TrimSpace(line[level:])
	if title == "" {
		return marker
	}
	titleStyle := lipgloss.NewStyle().
		Foreground(theme.AccentPrimary).
		Bold(true)
	return marker + " " + titleStyle.Render(stripStrongMarkers(title))
}

func renderMarkdownListItem(line string, theme chatTheme) string {
	indent, marker, body := splitMarkdownListItem(line)
	indentStyled := lipgloss.NewStyle().Render(indent)
	markerStyled := lipgloss.NewStyle().
		Foreground(theme.AccentSecondary).
		Bold(true).
		Render(marker)
	body = strings.TrimLeft(body, " ")
	if body == "" {
		return indentStyled + markerStyled
	}
	return indentStyled + markerStyled + " " + renderEmphasizedSemantic(body, profileProse, theme)
}

func renderEmphasizedSemantic(line string, profile semanticProfile, theme chatTheme) string {
	segments := splitStrongSegments(line)
	if len(segments) == 0 {
		return RenderSemanticPlain(line, profile, theme)
	}
	var out strings.Builder
	for _, seg := range segments {
		rendered := RenderSemanticPlain(seg.text, profile, theme)
		if seg.strong {
			rendered = lipgloss.NewStyle().
				Foreground(theme.Text).
				Bold(true).
				Render(rendered)
		}
		out.WriteString(rendered)
	}
	return out.String()
}

type proseSegment struct {
	text   string
	strong bool
}

func splitStrongSegments(line string) []proseSegment {
	if !strings.Contains(line, "**") {
		return []proseSegment{{text: line}}
	}
	segments := make([]proseSegment, 0, 4)
	var plain strings.Builder
	flushPlain := func() {
		if plain.Len() == 0 {
			return
		}
		segments = append(segments, proseSegment{text: plain.String()})
		plain.Reset()
	}

	inCode := false
	for i := 0; i < len(line); {
		if line[i] == '`' {
			inCode = !inCode
			plain.WriteByte(line[i])
			i++
			continue
		}
		if !inCode && i+1 < len(line) && line[i] == '*' && line[i+1] == '*' {
			end := strings.Index(line[i+2:], "**")
			if end < 0 {
				plain.WriteString(line[i:])
				break
			}
			flushPlain()
			strongText := line[i+2 : i+2+end]
			segments = append(segments, proseSegment{text: strongText, strong: true})
			i += 4 + end
			continue
		}
		plain.WriteByte(line[i])
		i++
	}
	flushPlain()
	return segments
}

func stripStrongMarkers(text string) string {
	var out strings.Builder
	for _, seg := range splitStrongSegments(text) {
		out.WriteString(seg.text)
	}
	return out.String()
}

func isMarkdownHeading(line string) bool {
	line = strings.TrimLeft(line, " ")
	if line == "" || line[0] != '#' {
		return false
	}
	i := 0
	for i < len(line) && line[i] == '#' {
		i++
	}
	return i > 0 && i < len(line) && line[i] == ' '
}

func isMarkdownListItem(line string) bool {
	_, marker, body := splitMarkdownListItem(line)
	return marker != "" && strings.TrimSpace(body) != ""
}

func splitMarkdownListItem(line string) (indent, marker, body string) {
	i := 0
	for i < len(line) && (line[i] == ' ' || line[i] == '\t') {
		i++
	}
	indent = line[:i]
	trimmed := line[i:]
	if strings.HasPrefix(trimmed, "- ") || strings.HasPrefix(trimmed, "* ") {
		return indent, trimmed[:1], trimmed[2:]
	}
	j := 0
	for j < len(trimmed) && unicode.IsDigit(rune(trimmed[j])) {
		j++
	}
	if j > 0 && j+1 < len(trimmed) && trimmed[j] == '.' && trimmed[j+1] == ' ' {
		return indent, trimmed[:j+1], trimmed[j+2:]
	}
	return indent, "", line
}

func wrapProseLines(text string, width int) []string {
	width = max(1, width)
	text = strings.TrimRight(text, "\n")
	if text == "" {
		return []string{""}
	}

	out := make([]string, 0, 8)
	for _, raw := range strings.Split(text, "\n") {
		if raw == "" {
			out = append(out, "")
			continue
		}
		wrapped := wordwrap.String(raw, width)
		out = append(out, strings.Split(wrapped, "\n")...)
	}
	if len(out) == 0 {
		return []string{""}
	}
	return out
}

func parseMessageBlocks(content string) []messageBlock {
	parts := strings.Split(content, "```")
	blocks := make([]messageBlock, 0, len(parts))
	for idx, part := range parts {
		if idx%2 == 0 {
			blocks = append(blocks, messageBlock{Body: part})
			continue
		}
		lang, body := parseFenceBody(part)
		blocks = append(blocks, messageBlock{
			Fenced: true,
			Lang:   lang,
			Body:   body,
		})
	}
	return blocks
}

func parseFenceBody(raw string) (string, string) {
	raw = strings.TrimLeft(raw, "\n")
	if raw == "" {
		return "", ""
	}
	if newline := strings.IndexByte(raw, '\n'); newline >= 0 {
		return strings.TrimSpace(raw[:newline]), raw[newline+1:]
	}
	return strings.TrimSpace(raw), ""
}

func renderCodeBlock(lang, body string, width int, theme chatTheme) string {
	lang = strings.TrimSpace(lang)
	label := "CODE"
	if lang != "" {
		label = strings.ToUpper(lang)
	}
	innerWidth := max(8, width-4)
	title := lipgloss.NewStyle().
		Foreground(theme.HeaderBG).
		Background(codeBlockBorder(lang, theme)).
		Bold(true).
		Padding(0, 1).
		Render(label)
	content := renderCodeBlockBody(lang, body, innerWidth, theme)
	stack := lipgloss.JoinVertical(lipgloss.Left, title, content)
	return lipgloss.NewStyle().
		Border(lipgloss.NormalBorder()).
		BorderForeground(codeBlockBorder(lang, theme)).
		Background(theme.AppBG).
		Padding(0, 1).
		Width(innerWidth).
		Render(stack)
}

func renderCodeBlockBody(lang, body string, width int, theme chatTheme) string {
	lines := normalizeCodeBlockBodyLines(body)
	rendered := make([]string, 0, len(lines))
	for _, line := range lines {
		style := lipgloss.NewStyle().
			Foreground(theme.Text).
			Background(theme.AppBG).
			Width(width)
		if strings.EqualFold(lang, "diff") || strings.EqualFold(lang, "patch") {
			switch {
			case strings.HasPrefix(line, "+"):
				style = style.Foreground(theme.Success)
			case strings.HasPrefix(line, "-"):
				style = style.Foreground(theme.Error)
			case strings.HasPrefix(line, "@@"):
				style = style.Foreground(theme.AccentSecondary)
			}
		}
		rendered = append(rendered, style.Render(line))
	}
	return strings.Join(rendered, "\n")
}

func normalizeCodeBlockBodyLines(body string) []string {
	trimmed := strings.TrimRight(body, "\n")
	if trimmed == "" {
		return []string{""}
	}
	raw := strings.Split(trimmed, "\n")
	lines := make([]string, 0, len(raw))
	for _, line := range raw {
		fragment := strings.TrimSpace(line)
		if len(lines) > 0 && strings.HasSuffix(lines[len(lines)-1], "-") && isLikelyShortFlagFragment(fragment) {
			lines[len(lines)-1] += fragment
			continue
		}
		lines = append(lines, line)
	}
	return lines
}

func isLikelyShortFlagFragment(fragment string) bool {
	if len(fragment) == 0 || len(fragment) > 3 {
		return false
	}
	for _, r := range fragment {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			continue
		}
		return false
	}
	return true
}

func codeBlockBorder(lang string, theme chatTheme) lipgloss.Color {
	switch strings.ToLower(strings.TrimSpace(lang)) {
	case "diff", "patch":
		return theme.AccentSecondary
	case "bash", "sh", "shell", "zsh":
		return theme.Warning
	default:
		return theme.BorderFocus
	}
}
