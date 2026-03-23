package tui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/viewport"
)

func TestViewportPipeline(t *testing.T) {
	const termWidth = 80
	const bodyH = 19 // typical: 24 - 1 - 4

	m := NewChatModel(ChatLiveConfig{Model: "test", WorkDir: "/tmp"})
	m.width = termWidth
	m.height = 24
	vp := viewport.New(m.chatPaneWidth(), bodyH)
	m.chatViewport = vp

	m.messages = []ChatMessage{
		{Kind: MsgAgent, Header: "Agent • 14:40:00",
			Content: "I'm Forge, a coding agent.\n\nI can help with:\n- Reading files\n- Running commands\n\n**What would you like to work on today?** Just tell me!\n"},
		{Kind: MsgUser, Header: "You • 14:41:00",
			Content: "audit this codebase"},
	}
	m.refreshViewport()

	chatPaneWidth := m.chatPaneWidth()
	chatBodyHeight := m.chatViewport.Height
	chatInnerWidth := max(1, chatPaneWidth-2)
	chatContentWidth := max(1, chatInnerWidth-1)

	fmt.Printf("termWidth=%d chatPaneWidth=%d chatInnerWidth=%d chatContentWidth=%d bodyH=%d\n",
		termWidth, chatPaneWidth, chatInnerWidth, chatContentWidth, bodyH)

	chatView := m.chatViewport.View()
	chatLines := strings.Split(chatView, "\n")
	fmt.Printf("viewport.View() lines: %d\n", len(chatLines))

	scrollbar := scrollbarColumn(len(strings.Split(m.chatContent, "\n")), m.chatViewport.Height, m.chatViewport.YOffset, chatBodyHeight)
	chatBody := joinWithScrollbar(chatLines, scrollbar, chatContentWidth, chatBodyHeight)
	bodyLines := strings.Split(chatBody, "\n")
	fmt.Printf("joinWithScrollbar lines: %d\n", len(bodyLines))

	// Find blank lines adjacent to borders in chatBody
	for i, l := range bodyLines {
		stripped := strippedLine(l)
		trimmed := strings.TrimSpace(stripped)
		if strings.ContainsAny(trimmed, "╭╰╯─") {
			fmt.Printf("  body line %3d: BORDER\n", i)
		} else if trimmed == "" || trimmed == "│" {
			fmt.Printf("  body line %3d: [blank/scrollbar-only]\n", i)
		}
	}
}
