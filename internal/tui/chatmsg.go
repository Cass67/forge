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
		style := lipgloss.NewStyle().
			Foreground(theme.TextDim).
			Width(width).
			Align(lipgloss.Center)
		return style.Render(m.Content)
	}

	if m.Kind == MsgWorking {
		header := strings.TrimSpace(m.Header)
		if header == "" {
			header = "Working"
		}
		titleStyle := lipgloss.NewStyle().
			Foreground(theme.TextDim).
			Bold(true)
		contentStyle := lipgloss.NewStyle().
			Foreground(theme.TextDim).
			Width(width)
		if strings.TrimSpace(m.Content) == "" {
			return titleStyle.Render(header)
		}
		return lipgloss.JoinVertical(lipgloss.Left,
			titleStyle.Render(header),
			contentStyle.Render(m.Content),
		)
	}

	bc := m.borderColor(theme)
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
