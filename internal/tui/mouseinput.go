package tui

import (
	"regexp"

	tea "github.com/charmbracelet/bubbletea"
)

var (
	mouseTrackingSequencePattern = regexp.MustCompile(`(?:\x1b)?\[<\d+;\d+;\d+[mM]`)
	mouseTrackingFragmentPattern = regexp.MustCompile(`^<\d+;\d+;\d+[mM]$`)
)

func stripMouseTrackingSequences(text string) string {
	if text == "" {
		return ""
	}
	return mouseTrackingSequencePattern.ReplaceAllString(text, "")
}

func startsMouseTrackingSequence(msg tea.KeyMsg) bool {
	return msg.Type == tea.KeyRunes && msg.Alt && string(msg.Runes) == "["
}

func isMouseTrackingFragment(msg tea.KeyMsg) bool {
	return msg.Type == tea.KeyRunes && !msg.Paste && mouseTrackingFragmentPattern.MatchString(string(msg.Runes))
}
