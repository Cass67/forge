package tui

import (
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

func withTrueColorProfile(t *testing.T) {
	t.Helper()
	prevProfile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() {
		lipgloss.SetColorProfile(prevProfile)
	})
}

func ansiBackground(color lipgloss.Color) string {
	hex := strings.TrimPrefix(string(color), "#")
	if len(hex) != 6 {
		return ""
	}
	r, _ := strconv.ParseInt(hex[0:2], 16, 0)
	g, _ := strconv.ParseInt(hex[2:4], 16, 0)
	b, _ := strconv.ParseInt(hex[4:6], 16, 0)
	return fmt.Sprintf("\x1b[48;2;%d;%d;%dm", r, g, b)
}

func ansiBackgroundFragment(color lipgloss.Color) string {
	hex := strings.TrimPrefix(string(color), "#")
	if len(hex) != 6 {
		return ""
	}
	r, _ := strconv.ParseInt(hex[0:2], 16, 0)
	g, _ := strconv.ParseInt(hex[2:4], 16, 0)
	b, _ := strconv.ParseInt(hex[4:6], 16, 0)
	return fmt.Sprintf("48;2;%d;%d;%d", r, g, b)
}
