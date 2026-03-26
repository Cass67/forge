package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// MsgKind identifies the type of chat message.
type MsgKind int

const (
	MsgUser    MsgKind = iota // User input
	MsgAgent                  // Agent response
	MsgForge                  // Forge steering input
	MsgWorking                // Inline progress / working-state update
	MsgStatus                 // Status line (e.g. "Agent complete")
)

// ChatMessage is a single message in the conversation.
type ChatMessage struct {
	Kind    MsgKind
	Header  string // e.g. "You • 22:59:50" (empty for agent streaming)
	Content string // message body (may be multi-line)
}

func (m ChatMessage) accentColor(theme chatTheme) lipgloss.Color {
	switch m.Kind {
	case MsgUser:
		return theme.Success
	case MsgAgent:
		return theme.AccentPrimary
	case MsgForge:
		return theme.AccentSecondary
	case MsgWorking:
		return theme.TextDim
	default:
		return theme.Border
	}
}

// Render returns the styled string for this message at the given width.
func (m ChatMessage) Render(width int, theme chatTheme) string {
	if width < 10 {
		width = 10
	}

	if m.Kind == MsgStatus {
		body := RenderSemanticPlain(strings.TrimSpace(m.Content), profileStatus, theme)
		return lipgloss.NewStyle().
			Background(theme.AppBG).
			Width(width).
			Render(body)
	}

	if m.Kind == MsgWorking {
		body := RenderSemanticPlain(strings.TrimSpace(m.Content), profileStatus, theme)
		prefix := lipgloss.NewStyle().
			Background(theme.AppBG).
			Foreground(theme.TextDim).
			Render("· ")
		return lipgloss.NewStyle().
			Background(theme.AppBG).
			Width(width).
			Render(prefix + body)
	}

	headerColor := m.accentColor(theme)
	header := strings.TrimSpace(m.Header)
	content := strings.TrimRight(m.Content, "\n")
	var blocks []string
	if header != "" {
		blocks = append(blocks, renderMessageHeader(header, width, theme, headerColor))
	}
	if strings.TrimSpace(content) != "" {
		body := renderMessageContent(content, max(10, width-2), theme)
		blocks = append(blocks, lipgloss.NewStyle().
			Background(theme.AppBG).
			Width(width).
			Render(indentRenderedBlock(body, "  ")))
	}
	return lipgloss.NewStyle().
		Background(theme.AppBG).
		Width(width).
		Render(lipgloss.JoinVertical(lipgloss.Left, blocks...))
}

func renderMessageHeader(header string, width int, theme chatTheme, accent lipgloss.Color) string {
	name, meta, found := strings.Cut(strings.TrimSpace(header), " • ")
	if !found {
		return lipgloss.NewStyle().
			Background(theme.AppBG).
			Foreground(accent).
			Bold(true).
			Width(width).
			Render(header)
	}
	return lipgloss.NewStyle().
		Background(theme.AppBG).
		Width(width).
		Render(lipgloss.JoinHorizontal(
			lipgloss.Left,
			lipgloss.NewStyle().Background(theme.AppBG).Foreground(accent).Bold(true).Render(strings.TrimSpace(name)),
			lipgloss.NewStyle().Background(theme.AppBG).Foreground(theme.Border).Render(" • "),
			lipgloss.NewStyle().Background(theme.AppBG).Foreground(theme.TextDim).Render(strings.TrimSpace(meta)),
		))
}

func indentRenderedBlock(text, prefix string) string {
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		lines[i] = prefix + line
	}
	return strings.Join(lines, "\n")
}
