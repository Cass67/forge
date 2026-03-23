package tui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/viewport"
)

func TestViewOutput(t *testing.T) {
	// Test with tools visible but no toolsBuf (single pane, full width)
	for _, termWidth := range []int{80, 120} {
		m := NewChatModel(ChatLiveConfig{Model: "test-model", WorkDir: "/tmp"})
		m.width = termWidth
		m.height = 24
		m.toolsVisible = false // no tools pane
		bodyH := max(3, 24-1-4)
		vp := viewport.New(m.chatPaneWidth(), bodyH)
		m.chatViewport = vp
		m.chatViewport.Height = bodyH

		m.messages = []ChatMessage{
			{Kind: MsgAgent, Header: "Agent • 14:40:00",
				Content: "I'm Forge, a coding agent designed to help you work on software development.\n\nI can help with:\n- Reading, editing, and creating files\n- Running git commands (builds, tests, linters)\n- Searching and navigating files and directories\n\n**What would you like to work on today?** Just tell me!\n\n"},
			{Kind: MsgUser, Header: "You • 14:41:00",
				Content: "audit this codebase"},
		}
		m.refreshViewport()

		view := m.View()
		viewLines := strings.Split(view, "\n")

		fmt.Printf("\n=== termWidth=%d chatPaneWidth=%d ===\n", termWidth, m.chatPaneWidth())
		gapFound := false
		for i := 1; i < len(viewLines)-1; i++ {
			prev := strings.TrimSpace(strippedLine(viewLines[i-1]))
			curr := strings.TrimSpace(strippedLine(viewLines[i]))
			next := strings.TrimSpace(strippedLine(viewLines[i+1]))

			// Is this line blank/empty and sandwiched between box content?
			if curr == "" || curr == "│" {
				if strings.ContainsAny(prev, "╯─╭╰│") && strings.ContainsAny(next, "╯─╭╰│") {
					fmt.Printf("  GAP at line %d: prev=%q curr=%q next=%q\n",
						i,
						prev[:min2(40, len(prev))],
						curr[:min2(40, len(curr))],
						next[:min2(40, len(next))],
					)
					gapFound = true
				}
			}
		}
		if !gapFound {
			fmt.Println("  No gaps found!")
		}
	}
}

func min2(a, b int) int {
	if a < b {
		return a
	}
	return b
}
