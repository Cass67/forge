package tui

import (
	"strings"

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
	style := lipgloss.NewStyle().
		Foreground(theme.Text).
		Background(theme.AppBG)
	rendered := make([]string, 0, len(lines))
	for _, line := range lines {
		rendered = append(rendered, style.Width(width).Render(RenderSemanticPlain(line, profileProse, theme)))
	}
	return strings.Join(rendered, "\n")
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
