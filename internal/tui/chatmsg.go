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
	MsgPlan                   // Persistent plan/todo state
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
	case MsgPlan:
		return theme.Warning
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
	appBG := theme.appSurface()

	if m.Kind == MsgStatus {
		body := RenderSemanticPlain(strings.TrimSpace(m.Content), profileStatus, theme)
		return lipgloss.NewStyle().
			Foreground(theme.Text).
			Background(appBG).
			Width(width).
			Render(body)
	}

	if m.Kind == MsgWorking {
		body := RenderSemanticPlain(strings.TrimSpace(m.Content), profileStatus, theme)
		prefix := lipgloss.NewStyle().
			Foreground(theme.TextDim).
			Background(appBG).
			Render("· ")
		return lipgloss.NewStyle().
			Foreground(theme.Text).
			Background(appBG).
			Width(width).
			Render(prefix + body)
	}

	if m.Kind == MsgPlan {
		headerColor := m.accentColor(theme)
		header := strings.TrimSpace(m.Header)
		if header == "" {
			header = "Plan"
		}
		body := RenderSemanticPlain(strings.TrimSpace(m.Content), profileProse, theme)
		blocks := []string{
			renderMessageHeader(header, width, theme, headerColor),
			lipgloss.NewStyle().
				Width(width).
				Render(indentRenderedBlock(body, "  ")),
		}
		return lipgloss.NewStyle().
			Background(appBG).
			Width(width).
			Render(lipgloss.JoinVertical(lipgloss.Left, blocks...))
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
			Width(width).
			Render(indentRenderedBlock(body, "  ")))
	}
	return lipgloss.NewStyle().
		Background(appBG).
		Width(width).
		Render(lipgloss.JoinVertical(lipgloss.Left, blocks...))
}

func renderMessageHeader(header string, width int, theme chatTheme, accent lipgloss.Color) string {
	headerBG := theme.appSurface()
	rail := lipgloss.NewStyle().
		Foreground(accent).
		Background(headerBG).
		Bold(true).
		Render("▌ ")
	name, meta, found := strings.Cut(strings.TrimSpace(header), " • ")
	if !found {
		return rail + lipgloss.NewStyle().
			Foreground(accent).
			Background(headerBG).
			Bold(true).
			Render(header)
	}
	return lipgloss.JoinHorizontal(
		lipgloss.Left,
		rail,
		lipgloss.NewStyle().Foreground(accent).Background(headerBG).Bold(true).Render(strings.TrimSpace(name)),
		lipgloss.NewStyle().Foreground(theme.Border).Background(headerBG).Render(" • "),
		lipgloss.NewStyle().Foreground(theme.TextDim).Background(headerBG).Render(strings.TrimSpace(meta)),
	)
}

func indentRenderedBlock(text, prefix string) string {
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		lines[i] = prefix + line
	}
	return strings.Join(lines, "\n")
}
