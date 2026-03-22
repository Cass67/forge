package tui

import "github.com/charmbracelet/lipgloss"

var (
	styleBright   = lipgloss.NewStyle().Foreground(lipgloss.Color("#f0f6fc"))
	styleMid      = lipgloss.NewStyle().Foreground(lipgloss.Color("#b1bac4"))
	styleDim      = lipgloss.NewStyle().Foreground(lipgloss.Color("#8b949e"))
	styleGreen    = lipgloss.NewStyle().Foreground(lipgloss.Color("#56d364"))
	styleDimGreen = lipgloss.NewStyle().Foreground(lipgloss.Color("#2ea043"))
	styleRed      = lipgloss.NewStyle().Foreground(lipgloss.Color("#ff7b72"))
	styleYellow   = lipgloss.NewStyle().Foreground(lipgloss.Color("#e3b341"))
	styleBold     = lipgloss.NewStyle().Foreground(lipgloss.Color("#f0f6fc")).Bold(true)
)
