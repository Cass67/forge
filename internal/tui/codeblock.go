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
	rendered := make([]messageBlock, 0, len(blocks))
	for _, block := range blocks {
		body := strings.TrimRight(block.Body, "\n")
		if strings.TrimSpace(body) == "" {
			continue
		}
		block.Body = body
		rendered = append(rendered, block)
	}

	parts := make([]string, 0, len(rendered))
	for i, block := range rendered {
		if i > 0 {
			prev := rendered[i-1]
			if !prev.Fenced && !block.Fenced {
				parts = append(parts, "")
			}
		}
		if block.Fenced {
			parts = append(parts, renderCodeBlock(block.Lang, block.Body, width, theme))
			continue
		}
		parts = append(parts, renderWrappedProseBlock(block.Body, width, theme))
	}
	return strings.Join(parts, "\n")
}

func renderWrappedProseBlock(body string, width int, theme chatTheme) string {
	lines := wrapProseLines(body, width)
	rendered := make([]string, 0, len(lines))
	for i, line := range lines {
		next := ""
		if i+1 < len(lines) {
			next = lines[i+1]
		}
		rendered = append(rendered, padStyledWidth(renderProseLine(line, next, theme), width))
	}
	return strings.Join(rendered, "\n")
}

func renderProseLine(line, nextLine string, theme chatTheme) string {
	switch {
	case isMarkdownHeading(line):
		return renderMarkdownHeading(line, theme)
	case isMarkdownListItem(line):
		return renderMarkdownListItem(line, theme)
	case isColonHeader(line):
		return renderColonHeader(line, theme)
	case isBareHeader(line, nextLine):
		return renderBareHeader(line, theme)
	default:
		return renderEmphasizedSemantic(line, profileProse, theme)
	}
}

// isColonHeader detects lines that look like section labels, e.g. "Architecture:"
// or "**Key files:**". Weaker models often use these instead of markdown headings.
func isColonHeader(line string) bool {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return false
	}
	// Must end with a colon (possibly after bold markers).
	if !strings.HasSuffix(trimmed, ":") && !strings.HasSuffix(trimmed, ":**") {
		return false
	}
	// Strip trailing bold markers for length check.
	clean := strings.TrimSuffix(trimmed, "**")
	clean = strings.TrimSuffix(clean, ":")
	clean = strings.TrimSpace(clean)
	// Strip leading bold markers too.
	clean = strings.TrimPrefix(clean, "**")
	clean = strings.TrimSpace(clean)
	// Must be short enough to be a header (not a full sentence ending with colon).
	if len(clean) > 60 {
		return false
	}
	// Must not be a code path or URL (contains / or .).
	if strings.ContainsAny(clean, "/.") {
		return false
	}
	return clean != ""
}

func renderColonHeader(line string, theme chatTheme) string {
	return lipgloss.NewStyle().
		Foreground(theme.AccentPrimary).
		Bold(true).
		Render(stripStrongMarkers(line))
}

// isBareHeader detects standalone title-case lines that weaker models emit as
// section headers without any markdown marker or trailing colon, e.g.:
//
//	"Summary"
//	"What I inspected (source-grounded)"
//	"Summary (grounded to README.md and cmd/forge/main.go)"
//
// nextLine is used as a strong context signal: a line immediately followed by
// a list item is almost certainly a section header even when it is long or
// contains path characters.
func isBareHeader(line, nextLine string) bool {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return false
	}
	// Must not already be handled by other cases (no leading markers).
	if strings.ContainsAny(string(trimmed[0]), "-*#>`") {
		return false
	}
	// Must start with an uppercase letter.
	r := []rune(trimmed)
	if !unicode.IsUpper(r[0]) {
		return false
	}
	// Must not end with sentence-ending punctuation (those are sentences, not headers).
	last := r[len(r)-1]
	if last == '.' || last == ',' || last == ';' || last == '?' || last == '!' || last == ':' {
		return false
	}
	// When the next line is a list item, we have strong evidence this is a header.
	// Relax the length and character constraints in that case.
	if isMarkdownListItem(nextLine) {
		return len(trimmed) <= 120
	}
	// Without context, apply stricter limits: short, no path separators or inline dashes.
	if len(trimmed) > 45 {
		return false
	}
	if strings.ContainsAny(trimmed, "/\\") || strings.Contains(trimmed, "–") || strings.Contains(trimmed, "—") {
		return false
	}
	return true
}

func renderBareHeader(line string, theme chatTheme) string {
	return lipgloss.NewStyle().
		Foreground(theme.AccentPrimary).
		Bold(true).
		Render(strings.TrimSpace(line))
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
	if j > 0 && j < len(trimmed) && trimmed[j] == ')' && (j+1 >= len(trimmed) || trimmed[j+1] == ' ') {
		bodyStart := j + 1
		if bodyStart < len(trimmed) && trimmed[bodyStart] == ' ' {
			bodyStart++
		}
		return indent, trimmed[:j+1], trimmed[bodyStart:]
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
		body := strings.TrimLeft(raw[newline+1:], "\n")
		return strings.TrimSpace(raw[:newline]), body
	}
	return strings.TrimSpace(raw), ""
}

func renderCodeBlock(lang, body string, width int, theme chatTheme) string {
	lang = strings.TrimSpace(lang)
	label := "CODE"
	if lang != "" {
		label = strings.ToUpper(lang)
	}
	innerWidth := max(8, width)
	title := lipgloss.NewStyle().
		Foreground(codeBlockBorder(lang, theme)).
		Bold(true).
		Render(label)
	content := renderCodeBlockBody(lang, body, innerWidth, theme)
	return strings.Join([]string{padStyledWidth(title, innerWidth), content}, "\n")
}

func renderCodeBlockBody(lang, body string, width int, theme chatTheme) string {
	if strings.EqualFold(lang, "diff") || strings.EqualFold(lang, "patch") {
		return enhancedDiffBlock(body, width, theme)
	}
	lines := normalizeCodeBlockBodyLines(lang, body)
	rendered := make([]string, 0, len(lines))
	for _, line := range lines {
		style := lipgloss.NewStyle().
			Foreground(theme.Text).
			Background(theme.AppBG).
			Width(width)

		rendered = append(rendered, style.Render(line))
	}
	return strings.Join(rendered, "\n")
}

func normalizeCodeBlockBodyLines(lang, body string) []string {
	trimmed := strings.TrimRight(body, "\n")
	if trimmed == "" {
		return []string{""}
	}
	dropBlankLines := isPlainTextOutputLang(lang)
	raw := strings.Split(trimmed, "\n")
	lines := make([]string, 0, len(raw))
	prevBlank := false
	for _, line := range raw {
		fragment := strings.TrimSpace(line)
		if len(lines) > 0 && strings.HasSuffix(lines[len(lines)-1], "-") && isLikelyShortFlagFragment(fragment) {
			lines[len(lines)-1] += fragment
			prevBlank = false
			continue
		}
		blank := strings.TrimSpace(line) == ""
		if blank && dropBlankLines {
			continue
		}
		if blank && prevBlank {
			continue
		}
		prevBlank = blank
		lines = append(lines, line)
	}
	return lines
}

func isPlainTextOutputLang(lang string) bool {
	switch strings.ToLower(strings.TrimSpace(lang)) {
	case "", "text", "txt", "plain", "output", "log":
		return true
	default:
		return false
	}
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
