package tui

import "github.com/charmbracelet/lipgloss"

var (
	styleBright   = lipgloss.NewStyle().Foreground(lipgloss.Color("#ebebeb"))
	styleMid      = lipgloss.NewStyle().Foreground(lipgloss.Color("#666666"))
	styleDim      = lipgloss.NewStyle().Foreground(lipgloss.Color("#3a3a3a"))
	styleGreen    = lipgloss.NewStyle().Foreground(lipgloss.Color("#4ade80"))
	styleDimGreen = lipgloss.NewStyle().Foreground(lipgloss.Color("#1a5c30"))
	styleRed      = lipgloss.NewStyle().Foreground(lipgloss.Color("#f87171"))
	styleYellow   = lipgloss.NewStyle().Foreground(lipgloss.Color("#fbbf24"))
	styleBold     = lipgloss.NewStyle().Foreground(lipgloss.Color("#ebebeb")).Bold(true)
)
