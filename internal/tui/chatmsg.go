package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// MsgKind identifies the type of chat message.
type MsgKind int

const (
	MsgUser   MsgKind = iota // User input
	MsgAgent                 // Agent response
	MsgForge                 // Forge steering input
	MsgStatus                // Status line (e.g. "Agent complete")
)

// ChatMessage is a single message in the conversation.
type ChatMessage struct {
	Kind    MsgKind
	Header  string // e.g. "You • 22:59:50" (empty for agent streaming)
	Content string // message body (may be multi-line)
}

func (m ChatMessage) borderColor() lipgloss.Color {
	switch m.Kind {
	case MsgUser:
		return lipgloss.Color("#56d364")
	case MsgAgent:
		return lipgloss.Color("#58a6ff")
	case MsgForge:
		return lipgloss.Color("#d2a8ff")
	default:
		return lipgloss.Color("#484f58")
	}
}

// Render returns the styled string for this message at the given width.
func (m ChatMessage) Render(width int, theme chatTheme) string {
	if width < 10 {
		width = 10
	}

	if m.Kind == MsgStatus {
		style := lipgloss.NewStyle().
			Foreground(theme.TextDim).
			Width(width).
			Align(lipgloss.Center)
		return style.Render(m.Content)
	}

	bc := theme.Border
	switch m.Kind {
	case MsgUser:
		bc = theme.AccentPrimary
	case MsgAgent:
		bc = theme.AccentSecondary
	case MsgForge:
		bc = theme.Warning
	}
	boxBg := theme.PanelBG
	headerBg := theme.HeaderBG
	innerWidth := width - 2

	var headerBlock string
	if m.Header != "" {
		headerStyle := lipgloss.NewStyle().
			Background(headerBg).
			Foreground(bc).
			Bold(true).
			Width(innerWidth)
		headerBlock = headerStyle.Render(m.Header)
	}

	contentStyle := lipgloss.NewStyle().
		Background(boxBg).
		Foreground(theme.Text).
		Width(innerWidth)
	// Strip trailing blank lines so boxes hug their last content line
	contentLines := strings.Split(m.Content, "\n")
	for len(contentLines) > 0 && strings.TrimSpace(contentLines[len(contentLines)-1]) == "" {
		contentLines = contentLines[:len(contentLines)-1]
	}
	contentBlock := contentStyle.Render(strings.Join(contentLines, "\n"))

	var inner string
	if headerBlock != "" {
		sepStyle := lipgloss.NewStyle().
			Background(boxBg).
			Foreground(bc).
			Width(innerWidth)
		sep := sepStyle.Render(strings.Repeat("─", innerWidth))
		inner = lipgloss.JoinVertical(lipgloss.Left, headerBlock, sep, contentBlock)
	} else {
		inner = contentBlock
	}

	boxStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(bc).
		Background(boxBg).
		Width(width - 2)
	return boxStyle.Render(inner)
}
