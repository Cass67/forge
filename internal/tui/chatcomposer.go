package tui

import (
	"fmt"
	"strings"

	"forge/internal/chatstate"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

const (
	chatComposerMinBodyLines = 1
	chatComposerMaxBodyLines = 7
)

type ComposerAction struct {
	SubmitText  string
	CancelTurn  bool
	Exit        bool
	Attachments []chatstate.ChatAttachment
}

type ChatComposer struct {
	text         string
	cursor       int
	minBodyLines int
	maxBodyLines int
	attachments  []chatstate.ChatAttachment
	workDir      string
}

type composerVisibleLine struct {
	text      string
	cursorCol int
	hasCursor bool
}

func NewChatComposer() ChatComposer {
	return ChatComposer{
		minBodyLines: chatComposerMinBodyLines,
		maxBodyLines: chatComposerMaxBodyLines,
	}
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

func (c *ChatComposer) SetLineBudget(minBodyLines, maxBodyLines int) {
	minBodyLines = max(1, minBodyLines)
	maxBodyLines = max(minBodyLines, maxBodyLines)
	c.minBodyLines = minBodyLines
	c.maxBodyLines = maxBodyLines
}

func (c *ChatComposer) SetWorkDir(dir string) {
	c.workDir = dir
}

func (c ChatComposer) Attachments() []chatstate.ChatAttachment {
	return c.attachments
}

func (c *ChatComposer) SetAttachments(attachments []chatstate.ChatAttachment) {
	c.attachments = append([]chatstate.ChatAttachment(nil), attachments...)
}

func (c *ChatComposer) RemoveLastAttachment() bool {
	if len(c.attachments) == 0 {
		return false
	}
	c.attachments = c.attachments[:len(c.attachments)-1]
	return true
}

func (c *ChatComposer) ClearAttachments() {
	c.attachments = nil
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
	bodyWidth := chatComposerBodyWidth(width)
	return len(c.visibleBodyLineViews(bodyWidth)) + 1
}

func (c ChatComposer) visibleLines(width int) []string {
	width = max(1, width)
	lines := []string{}
	bodyLines := c.visibleBodyLines(max(1, width-2))
	for _, line := range bodyLines {
		prefix := "  "
		lines = append(lines, fitCell(prefix+line, width))
	}
	return lines
}

func chatComposerBodyWidth(width int) int {
	return max(1, width-3)
}

func (c ChatComposer) Render(theme chatTheme, width int) string {
	if width <= 0 {
		return ""
	}
	bodyWidth := chatComposerBodyWidth(width)
	bodyLines := c.visibleBodyLineViews(bodyWidth)

	hintText := "Enter send"
	switch {
	case width >= 50:
		hintText = "Enter send • Alt+Enter newline"
	case width >= 34:
		hintText = "Enter send • newline"
	}
	if width >= 62 {
		hintText = "Enter send • Alt+Enter newline • Esc cancel"
	}

	divider := lipgloss.NewStyle().
		Foreground(theme.Border).
		Render(strings.Repeat("─", width))
	out := make([]string, 0, len(bodyLines)+len(c.attachments)+1)
	out = append(out, divider)

	// Render attachment chips
	dimStyle := lipgloss.NewStyle().Foreground(theme.TextDim)
	accentStyle := lipgloss.NewStyle().Foreground(theme.AccentPrimary)
	for i, att := range c.attachments {
		chip := fmt.Sprintf(" 📎 %s  %dx%d  %s",
			att.Name, att.Width, att.Height, chatstate.FormatSize(att.Size))
		if i == 0 {
			out = append(out, "  "+accentStyle.Render(chip))
		} else {
			out = append(out, "  "+dimStyle.Render(chip))
		}
	}

	for i, line := range bodyLines {
		var prefix string
		var prefixStyle lipgloss.Style
		empty := c.text == ""
		if empty {
			prefix = "  "
			prefixStyle = lipgloss.NewStyle().Foreground(theme.TextDim)
			if i == 0 {
				if len(c.attachments) > 0 {
					line.text = " Add message... (" + hintText + ")"
				} else {
					line.text = " " + strings.TrimSpace("Ask Forge anything... ("+hintText+")")
				}
				line.hasCursor = true
				line.cursorCol = 0
			}
		} else {
			prefix = "┃ "
			if i == 0 {
				prefixStyle = lipgloss.NewStyle().Foreground(theme.AccentPrimary)
			} else {
				prefixStyle = lipgloss.NewStyle().Foreground(theme.Border)
			}
		}

		textStyle := lipgloss.NewStyle().Foreground(theme.Text)
		if empty {
			textStyle = lipgloss.NewStyle().Foreground(theme.TextDim)
		}
		body := renderComposerTextLine(line.text, bodyWidth, line.hasCursor, line.cursorCol, textStyle)

		out = append(out, lipgloss.JoinHorizontal(
			lipgloss.Left,
			lipgloss.NewStyle().Foreground(theme.AppBG).Render(" "),
			prefixStyle.Render(prefix),
			body,
		))
	}

	return strings.Join(out, "\n")
}

// Bubble Tea doesn't expose a separate Shift flag on KeyMsg. Modified Enter is
// surfaced via the Alt bit, so Forge treats that representation as the
// Shift+Enter multiline path.
func isComposerMultilineEnter(msg tea.KeyMsg) bool {
	return msg.Type == tea.KeyEnter && msg.Alt
}

func (c *ChatComposer) HandleKey(msg tea.KeyMsg, busy bool) ComposerAction {
	switch msg.Type {
	case tea.KeyCtrlC:
		if busy {
			return ComposerAction{CancelTurn: true}
		}
		c.clear()
	case tea.KeyCtrlD:
		if !busy && c.text == "" {
			return ComposerAction{Exit: true}
		}
	case tea.KeyEnter:
		if isComposerMultilineEnter(msg) {
			c.InsertString("\n")
			return ComposerAction{}
		}
		text := strings.TrimSpace(c.text)
		if text == "" && len(c.attachments) == 0 {
			return ComposerAction{}
		}
		submitted := c.attachments
		c.clear()
		c.ClearAttachments()
		return ComposerAction{SubmitText: text, Attachments: submitted}
	case tea.KeyBackspace:
		if c.text == "" && c.cursor == 0 {
			if c.RemoveLastAttachment() {
				return ComposerAction{}
			}
		}
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
		text := string(msg.Runes)
		if !msg.Paste {
			text = stripMouseTrackingSequences(text)
		}
		text = c.processPasteImages(text, msg.Paste)
		c.InsertString(text)
	}
	return ComposerAction{}
}

func (c *ChatComposer) processPasteImages(text string, isPaste bool) string {
	if len(c.attachments) >= chatstate.MaxAttachments {
		return text
	}
	// Only scan for image paths when the text looks like it contains a path.
	// On most terminals, drag-and-drop produces either a bracketed paste
	// (isPaste=true) or inserts a path starting with / or ~ or file://.
	if !isPaste && !strings.HasPrefix(strings.TrimSpace(text), "/") &&
		!strings.HasPrefix(strings.TrimSpace(text), "~") &&
		!strings.HasPrefix(strings.TrimSpace(text), "file://") {
		return text
	}
	refs := chatstate.DetectImageReferences(text, c.workDir)
	remaining := text
	for _, ref := range refs {
		if len(c.attachments) >= chatstate.MaxAttachments {
			break
		}
		att, err := chatstate.ValidateImageAttachment(ref)
		if err != nil {
			continue
		}
		remaining = stripImagePath(remaining, ref)
		remaining = stripImagePath(remaining, "file://"+ref)
		for _, q := range []string{"'", "\""} {
			remaining = strings.TrimSpace(strings.ReplaceAll(remaining, q+ref+q, ""))
		}
		c.attachments = append(c.attachments, *att)
	}
	return strings.TrimSpace(remaining)
}

func stripImagePath(text, path string) string {
	result := strings.ReplaceAll(text, path, "")
	result = strings.ReplaceAll(result, strings.ReplaceAll(path, " ", "\\ "), "")
	result = strings.ReplaceAll(result, path, "")
	return result
}

func (c ChatComposer) visibleBodyLines(width int) []string {
	views := c.visibleBodyLineViews(width)
	lines := make([]string, 0, len(views))
	for _, view := range views {
		lines = append(lines, view.text)
	}
	return lines
}

func (c ChatComposer) visibleBodyLineViews(width int) []composerVisibleLine {
	width = max(1, width)
	minBodyLines := max(1, c.minBodyLines)
	maxBodyLines := max(minBodyLines, c.maxBodyLines)

	if c.text == "" {
		return []composerVisibleLine{{text: "Ask Forge anything...", hasCursor: true}}
	}

	wrapped := composerWrappedLines(c.text, width)
	visibleCount := clamp(len(wrapped), minBodyLines, maxBodyLines)
	cursorLine, cursorCol := composerCursorPosition(c.text, c.cursor, width)
	start := 0
	if len(wrapped) > visibleCount {
		start = clamp(cursorLine-visibleCount+1, 0, len(wrapped)-visibleCount)
	}
	end := min(len(wrapped), start+visibleCount)
	lines := make([]composerVisibleLine, 0, visibleCount)
	for i, line := range wrapped[start:end] {
		lineNo := start + i
		lines = append(lines, composerVisibleLine{
			text:      line,
			cursorCol: cursorCol,
			hasCursor: lineNo == cursorLine,
		})
	}
	for len(lines) < visibleCount {
		lines = append(lines, composerVisibleLine{})
	}
	return lines
}

func composerCursorLine(text string, cursor, width int) int {
	line, _ := composerCursorPosition(text, cursor, width)
	return line
}

func composerCursorPosition(text string, cursor, width int) (int, int) {
	runes := []rune(text)
	cursor = clamp(cursor, 0, len(runes))
	lines := composerWrappedLines(string(runes[:cursor]), width)
	if len(lines) == 0 {
		return 0, 0
	}
	return len(lines) - 1, len([]rune(lines[len(lines)-1]))
}

func renderComposerTextLine(text string, width int, hasCursor bool, cursorCol int, textStyle lipgloss.Style) string {
	width = max(1, width)
	runes := []rune(fitCell(text, width))
	if !hasCursor {
		return textStyle.Render(string(runes))
	}
	cursorCol = clamp(cursorCol, 0, width-1)
	before := textStyle.Render(string(runes[:cursorCol]))
	cursor := styleCursor.Render(string(runes[cursorCol]))
	after := textStyle.Render(string(runes[cursorCol+1:]))
	return before + cursor + after
}

func composerWrappedLines(text string, width int) []string {
	width = max(1, width)
	if text == "" {
		return nil
	}

	rawLines := strings.Split(text, "\n")
	out := make([]string, 0, len(rawLines))
	for _, raw := range rawLines {
		out = append(out, composerWrapRawLine(raw, width)...)
	}
	return out
}

func composerWrapRawLine(raw string, width int) []string {
	width = max(1, width)
	if raw == "" {
		return []string{""}
	}

	runes := []rune(raw)
	lines := make([]string, 0, max(1, (len(runes)+width-1)/width))
	for len(runes) > width {
		breakAt := composerWrapBreak(runes, width)
		lines = append(lines, string(runes[:breakAt]))
		runes = runes[breakAt:]
	}
	lines = append(lines, string(runes))
	return lines
}

func composerWrapBreak(runes []rune, width int) int {
	limit := min(width, len(runes))
	for i := limit - 1; i >= 0; i-- {
		if runes[i] == ' ' || runes[i] == '\t' {
			return i + 1
		}
	}
	return limit
}
