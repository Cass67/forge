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

func (m ChatMessage) borderColor(theme chatTheme) lipgloss.Color {
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
		return lipgloss.NewStyle().
			Foreground(theme.TextDim).
			Width(width).
			Render(strings.TrimSpace(m.Content))
	}

	if m.Kind == MsgWorking {
		return lipgloss.NewStyle().
			Foreground(theme.TextDim).
			Italic(true).
			Width(width).
			Render(strings.TrimSpace(m.Content))
	}

	headerColor := m.borderColor(theme)
	header := strings.TrimSpace(m.Header)
	content := strings.TrimRight(m.Content, "\n")
	var blocks []string
	if header != "" {
		blocks = append(blocks, lipgloss.NewStyle().
			Foreground(headerColor).
			Bold(true).
			Width(width).
			Render(header))
	}
	if strings.TrimSpace(content) != "" {
		blocks = append(blocks, lipgloss.NewStyle().
			Foreground(theme.Text).
			Width(width).
			Render(content))
	}
	return lipgloss.JoinVertical(lipgloss.Left, blocks...)
}
