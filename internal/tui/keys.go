package tui

import tea "github.com/charmbracelet/bubbletea"

// Key constants used across all screens.
const (
	KeyQuit       = "q"
	KeyPause      = "p"
	KeySnapshot   = "s"
	KeyToggleView = "v"
	KeyRetry      = "e"
	KeyAttach     = "a"
	KeyOpen       = "o"
	KeyNewSession = "n"
	KeyFix        = "f"
)

// isQuit reports whether a key message is the quit key.
func isQuit(msg tea.KeyMsg) bool {
	return msg.String() == KeyQuit
}
