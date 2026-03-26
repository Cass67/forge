package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

const (
	chatComposerMinBodyLines = 2
	chatComposerMaxBodyLines = 4
)

type ComposerAction struct {
	SubmitText string
	CancelTurn bool
	Exit       bool
}

type ChatComposer struct {
	text   string
	cursor int
}

func NewChatComposer() ChatComposer {
	return ChatComposer{}
}

func (c ChatComposer) Text() string {
	return c.text
}

func (c ChatComposer) Cursor() int {
	return c.cursor
}

func (c *ChatComposer) SetText(text string) {
	c.text = text
	c.cursor = len([]rune(c.text))
}

func (c *ChatComposer) SetCursor(cursor int) {
	c.cursor = clamp(cursor, 0, len([]rune(c.text)))
}

func (c *ChatComposer) InsertString(text string) {
	if text == "" {
		return
	}
	runes := []rune(c.text)
	insert := []rune(text)
	pos := clamp(c.cursor, 0, len(runes))
	next := make([]rune, 0, len(runes)+len(insert))
	next = append(next, runes[:pos]...)
	next = append(next, insert...)
	next = append(next, runes[pos:]...)
	c.text = string(next)
	c.cursor = pos + len(insert)
}

func (c *ChatComposer) clear() {
	c.text = ""
	c.cursor = 0
}

func (c *ChatComposer) deleteBackward() {
	runes := []rune(c.text)
	if len(runes) == 0 || c.cursor == 0 {
		return
	}
	pos := clamp(c.cursor, 0, len(runes))
	c.text = string(append(runes[:pos-1], runes[pos:]...))
	c.cursor = pos - 1
}

func (c ChatComposer) Height(width int) int {
	return len(c.visibleLines(width))
}

func (c ChatComposer) visibleLines(width int) []string {
	width = max(1, width)
	lines := []string{fitCell("Prompt", width)}
	bodyLines := c.visibleBodyLines(max(1, width-2))
	for i, line := range bodyLines {
		prefix := "> "
		if i > 0 {
			prefix = "  "
		}
		lines = append(lines, fitCell(prefix+line, width))
	}
	return lines
}

func (c ChatComposer) Render(theme chatTheme, width int) string {
	if width <= 0 {
		return ""
	}
	lines := c.visibleLines(width)
	out := make([]string, 0, len(lines))
	out = append(out, lipgloss.NewStyle().
		Foreground(theme.TextDim).
		Bold(true).
		Width(width).
		Render(lines[0]))

	bodyStyle := lipgloss.NewStyle().
		Foreground(theme.Text).
		Width(width)
	if strings.TrimSpace(c.text) == "" {
		bodyStyle = bodyStyle.Foreground(theme.TextDim)
	}
	for _, line := range lines[1:] {
		out = append(out, bodyStyle.Render(line))
	}
	return strings.Join(out, "\n")
}

func (c *ChatComposer) HandleKey(msg tea.KeyMsg, busy bool) ComposerAction {
	switch msg.Type {
	case tea.KeyCtrlC:
		if busy {
			return ComposerAction{CancelTurn: true}
		}
		c.clear()
	case tea.KeyCtrlD:
		if !busy && strings.TrimSpace(c.text) == "" {
			return ComposerAction{Exit: true}
		}
	case tea.KeyEnter:
		// Terminals typically expose modified Enter as alt+enter.
		if msg.Alt {
			c.InsertString("\n")
			return ComposerAction{}
		}
		text := strings.TrimSpace(c.text)
		if text == "" {
			return ComposerAction{}
		}
		c.clear()
		return ComposerAction{SubmitText: text}
	case tea.KeyBackspace:
		c.deleteBackward()
	case tea.KeyLeft:
		if c.cursor > 0 {
			c.cursor--
		}
	case tea.KeyRight:
		if c.cursor < len([]rune(c.text)) {
			c.cursor++
		}
	case tea.KeyHome:
		c.cursor = 0
	case tea.KeyEnd:
		c.cursor = len([]rune(c.text))
	case tea.KeySpace:
		c.InsertString(" ")
	case tea.KeyRunes:
		c.InsertString(string(msg.Runes))
	}
	return ComposerAction{}
}

func (c ChatComposer) visibleBodyLines(width int) []string {
	if strings.TrimSpace(c.text) == "" {
		lines := []string{"Type a message or /help"}
		for len(lines) < chatComposerMinBodyLines {
			lines = append(lines, "")
		}
		return lines
	}

	wrapped := composerWrappedLines(c.text, width)
	visibleCount := clamp(len(wrapped), chatComposerMinBodyLines, chatComposerMaxBodyLines)
	cursorLine := composerCursorLine(c.text, c.cursor, width)
	start := 0
	if len(wrapped) > visibleCount {
		start = clamp(cursorLine-visibleCount+1, 0, len(wrapped)-visibleCount)
	}
	end := min(len(wrapped), start+visibleCount)
	lines := append([]string(nil), wrapped[start:end]...)
	for len(lines) < visibleCount {
		lines = append(lines, "")
	}
	return lines
}

func composerCursorLine(text string, cursor, width int) int {
	runes := []rune(text)
	cursor = clamp(cursor, 0, len(runes))
	lines := composerWrappedLines(string(runes[:cursor]), width)
	if len(lines) == 0 {
		return 0
	}
	return len(lines) - 1
}

func composerWrappedLines(text string, width int) []string {
	width = max(1, width)
	if text == "" {
		return nil
	}

	rawLines := strings.Split(text, "\n")
	out := make([]string, 0, len(rawLines))
	for _, raw := range rawLines {
		runes := []rune(raw)
		if len(runes) == 0 {
			out = append(out, "")
			continue
		}
		for len(runes) > 0 {
			take := min(width, len(runes))
			out = append(out, string(runes[:take]))
			runes = runes[take:]
		}
	}
	return out
}
