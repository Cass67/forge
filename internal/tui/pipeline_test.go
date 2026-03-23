package tui

import (
	"fmt"
	"strings"
	"testing"
	"unicode"

	"github.com/charmbracelet/bubbles/viewport"
)

// strippedLine returns a line with ANSI codes and non-printable chars replaced by their visual equivalents.
func strippedLine(s string) string {
	var b strings.Builder
	inEsc := false
	for _, r := range s {
		if r == '\x1b' {
			inEsc = true
		} else if inEsc {
			if r == 'm' || r == 'A' || r == 'B' || r == 'C' || r == 'D' || r == 'K' {
				inEsc = false
			}
		} else if r > 127 || unicode.IsPrint(r) {
			b.WriteRune(r)
		}
	}
	return strings.TrimRight(b.String(), " ")
}

func TestFullPipeline(t *testing.T) {
	const termWidth = 80
	const termHeight = 24
	headerH := 1
	inputH := 4
	bodyH := termHeight - headerH - inputH

	m := NewChatModel(ChatLiveConfig{Model: "test", WorkDir: "/tmp"})
	// Simulate a window size message
	m.width = termWidth
	m.height = termHeight
	vp := viewport.New(m.chatPaneWidth(), bodyH)
	m.chatViewport = vp

	// Add realistic messages
	m.messages = []ChatMessage{
		{Kind: MsgAgent, Header: "Agent • 14:40:00",
			Content: "I'm Forge, a coding agent.\n\nI can help with:\n- Reading files\n- Running commands\n\n**What would you like to work on today?** Just tell me!\n"},
		{Kind: MsgUser, Header: "You • 14:41:00",
			Content: "audit this codebase"},
	}
	m.refreshViewport()

	content := m.chatContent
	lines := strings.Split(content, "\n")
	fmt.Printf("Total lines in content: %d\n", len(lines))

	prevWasBorder := false
	for i, l := range lines {
		stripped := strippedLine(l)
		trimmed := strings.TrimSpace(stripped)
		isBorder := strings.ContainsAny(trimmed, "╭╰")
		isBlank := trimmed == ""

		if isBlank && prevWasBorder {
			t.Errorf("BLANK LINE at %d after border line!", i)
		}
		if isBorder {
			fmt.Printf("  line %3d: BORDER\n", i)
		} else if isBlank {
			fmt.Printf("  line %3d: [blank]\n", i)
		}
		prevWasBorder = isBorder || strings.ContainsAny(trimmed, "╯")
	}
}
