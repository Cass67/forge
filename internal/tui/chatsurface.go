package tui

import tea "github.com/charmbracelet/bubbletea"

type SurfaceModeConfig struct {
	UseAltScreen         bool
	EnableMouseCapture   bool
	EnableBracketedPaste bool
	EnableLiveRegion     bool
}

func programOptionsForSurfaceMode(mode SurfaceModeConfig) []tea.ProgramOption {
	opts := make([]tea.ProgramOption, 0, 3)

	if mode.UseAltScreen {
		opts = append(opts, tea.WithAltScreen())
	}
	if mode.EnableMouseCapture {
		opts = append(opts, tea.WithMouseCellMotion())
	}
	if !mode.EnableBracketedPaste {
		opts = append(opts, tea.WithoutBracketedPaste())
	}

	// Bubble Tea has no dedicated program option for live-region support.
	_ = mode.EnableLiveRegion

	return opts
}
