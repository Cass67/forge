package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// MsgKind identifies the type of chat message.
type MsgKind int

const (
	MsgUser       MsgKind = iota // User input
	MsgAgent                     // Agent response
	MsgForge                     // Forge steering input
	MsgPlan                      // Persistent plan/todo state
	MsgWorking                   // Inline progress / working-state update
	MsgStatus                    // Status line (e.g. "Agent complete")
	MsgCheckpoint                // Tool progress checkpoint ("• Ran …" with output)
	MsgReasoning                 // The model's thinking, shown apart from its answer
)

// ChatMessage is a single message in the conversation.
type ChatMessage struct {
	Kind    MsgKind
	Header  string // e.g. "You • 22:59:50" (empty for agent streaming)
	Content string // message body (may be multi-line)
	Key     string // dedupe key for upserted status messages (e.g. per exec session)
}

func (m ChatMessage) accentColor(theme chatTheme) lipgloss.Color {
	switch m.Kind {
	case MsgUser:
		return theme.Success
	case MsgAgent:
		return theme.AccentPrimary
	case MsgForge:
		return theme.AccentSecondary
	case MsgStatus, MsgWorking, MsgReasoning:
		return theme.TextDim
	case MsgPlan:
		return theme.Warning
	default:
		return theme.TextDim
	}
}

// Render returns the styled string for this message at the given width.
func (m ChatMessage) Render(width int, theme chatTheme) string {
	if width < 10 {
		width = 10
	}

	if m.Kind == MsgCheckpoint {
		return renderCheckpointBlock(m.Content, width, theme)
	}

	if m.Kind == MsgStatus {
		body := RenderSemanticPlain(strings.TrimSpace(m.Content), profileStatus, theme)
		return lipgloss.NewStyle().
			Foreground(theme.TextDim).
			Width(width).
			Render(body)
	}

	if m.Kind == MsgReasoning {
		// Dim and italic so thinking never reads as the answer.
		return lipgloss.NewStyle().
			Foreground(theme.TextDim).
			Italic(true).
			Width(width).
			Render(strings.TrimSpace(m.Content))
	}

	if m.Kind == MsgWorking {
		body := RenderSemanticPlain(strings.TrimSpace(m.Content), profileStatus, theme)
		prefix := lipgloss.NewStyle().
			Foreground(theme.TextDim).
			Render("· ")
		return lipgloss.NewStyle().
			Foreground(theme.TextDim).
			Width(width).
			Render(prefix + body)
	}

	if m.Kind == MsgPlan {
		header := strings.TrimSpace(m.Header)
		if header == "" {
			header = "Plan"
		}
		body := renderPlanContent(m.Content, width, theme)
		blocks := []string{
			renderMessageHeader(header, width, theme, m.accentColor(theme)),
			lipgloss.NewStyle().
				Width(width).
				Render(body),
		}
		return lipgloss.NewStyle().
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
		body := renderMessageContent(content, width, theme)
		blocks = append(blocks, lipgloss.NewStyle().
			Width(width).
			Render(body))
	}
	return lipgloss.NewStyle().
		Width(width).
		Render(lipgloss.JoinVertical(lipgloss.Left, blocks...))
}

func renderMessageHeader(header string, width int, theme chatTheme, accent lipgloss.Color) string {
	name, meta, found := strings.Cut(strings.TrimSpace(header), " • ")
	if !found {
		return lipgloss.NewStyle().
			Foreground(accent).
			Bold(true).
			Render(header)
	}
	return lipgloss.JoinHorizontal(
		lipgloss.Left,
		lipgloss.NewStyle().Foreground(accent).Bold(true).Render(strings.TrimSpace(name)),
		lipgloss.NewStyle().Foreground(theme.Border).Render(" • "),
		lipgloss.NewStyle().Foreground(theme.TextDim).Render(strings.TrimSpace(meta)),
	)
}

// renderCheckpointBlock renders a tool checkpoint ("• Ran cmd" + output lines)
// as a compact left-railed block: command highlighted, output dimmed.
func renderCheckpointBlock(content string, width int, theme chatTheme) string {
	lines := strings.Split(strings.TrimSpace(content), "\n")
	if len(lines) == 0 {
		return ""
	}
	innerWidth := max(10, width-2)
	head := strings.TrimPrefix(lines[0], "• ")
	headStyle := lipgloss.NewStyle().Foreground(theme.AccentSecondary).Width(innerWidth)
	dimStyle := lipgloss.NewStyle().Foreground(theme.TextDim).Width(innerWidth)
	out := []string{headStyle.Render(head)}
	for _, line := range lines[1:] {
		out = append(out, dimStyle.Render(strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "└"))))
	}
	return lipgloss.NewStyle().
		Border(lipgloss.NormalBorder(), false, false, false, true).
		BorderForeground(theme.Border).
		PaddingLeft(1).
		Render(strings.Join(out, "\n"))
}
