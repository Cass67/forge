package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
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
		rendered = append(rendered, lipgloss.NewStyle().
			Background(theme.AppBG).
			Foreground(theme.Text).
			Width(width).
			Render(body))
	}
	return strings.Join(rendered, "\n\n")
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
	lines := strings.Split(strings.TrimRight(body, "\n"), "\n")
	rendered := make([]string, 0, len(lines))
	for _, line := range lines {
		style := lipgloss.NewStyle().
			Foreground(theme.Text).
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
