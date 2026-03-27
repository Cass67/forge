package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

type chatTheme struct {
	ID              string
	Label           string
	LowContrast     bool
	AppBG           lipgloss.Color
	PanelBG         lipgloss.Color
	HeaderBG        lipgloss.Color
	HeaderFG        lipgloss.Color
	Border          lipgloss.Color
	BorderFocus     lipgloss.Color
	Text            lipgloss.Color
	TextDim         lipgloss.Color
	AccentPrimary   lipgloss.Color
	AccentSecondary lipgloss.Color
	Success         lipgloss.Color
	Warning         lipgloss.Color
	Error           lipgloss.Color
}

var chatThemeRegistry = []chatTheme{
	{
		ID:              "default",
		Label:           "default",
		LowContrast:     false,
		AppBG:           lipgloss.Color("#0b1016"),
		PanelBG:         lipgloss.Color("#0b1016"),
		HeaderBG:        lipgloss.Color("#0b1016"),
		HeaderFG:        lipgloss.Color("#f2f6fb"),
		Border:          lipgloss.Color("#1f2b38"),
		BorderFocus:     lipgloss.Color("#79c0ff"),
		Text:            lipgloss.Color("#eaf0f8"),
		TextDim:         lipgloss.Color("#8d9aae"),
		AccentPrimary:   lipgloss.Color("#79c0ff"),
		AccentSecondary: lipgloss.Color("#6fd3cc"),
		Success:         lipgloss.Color("#7fd1a5"),
		Warning:         lipgloss.Color("#e7c06a"),
		Error:           lipgloss.Color("#ff8f8f"),
	},
	{
		ID:              "low",
		Label:           "low contrast",
		LowContrast:     true,
		AppBG:           lipgloss.Color("#0d1117"),
		PanelBG:         lipgloss.Color("#0d1117"),
		HeaderBG:        lipgloss.Color("#0d1117"),
		HeaderFG:        lipgloss.Color("#c9d1d9"),
		Border:          lipgloss.Color("#3b434b"),
		BorderFocus:     lipgloss.Color("#7fbf7f"),
		Text:            lipgloss.Color("#c9d1d9"),
		TextDim:         lipgloss.Color("#8b949e"),
		AccentPrimary:   lipgloss.Color("#7fbf7f"),
		AccentSecondary: lipgloss.Color("#8cb4ff"),
		Success:         lipgloss.Color("#7fbf7f"),
		Warning:         lipgloss.Color("#d29922"),
		Error:           lipgloss.Color("#f85149"),
	},
	{
		ID:              "light",
		Label:           "light",
		LowContrast:     false,
		AppBG:           lipgloss.Color("#ffffff"),
		PanelBG:         lipgloss.Color("#ffffff"),
		HeaderBG:        lipgloss.Color("#ffffff"),
		HeaderFG:        lipgloss.Color("#24292f"),
		Border:          lipgloss.Color("#d0d7de"),
		BorderFocus:     lipgloss.Color("#0969da"),
		Text:            lipgloss.Color("#24292f"),
		TextDim:         lipgloss.Color("#57606a"),
		AccentPrimary:   lipgloss.Color("#0969da"),
		AccentSecondary: lipgloss.Color("#8250df"),
		Success:         lipgloss.Color("#1a7f37"),
		Warning:         lipgloss.Color("#9a6700"),
		Error:           lipgloss.Color("#cf222e"),
	},
	{
		ID:              "dusk",
		Label:           "dusk",
		LowContrast:     false,
		AppBG:           lipgloss.Color("#0f172a"),
		PanelBG:         lipgloss.Color("#0f172a"),
		HeaderBG:        lipgloss.Color("#0f172a"),
		HeaderFG:        lipgloss.Color("#e2e8f0"),
		Border:          lipgloss.Color("#334155"),
		BorderFocus:     lipgloss.Color("#c084fc"),
		Text:            lipgloss.Color("#e2e8f0"),
		TextDim:         lipgloss.Color("#94a3b8"),
		AccentPrimary:   lipgloss.Color("#38bdf8"),
		AccentSecondary: lipgloss.Color("#c084fc"),
		Success:         lipgloss.Color("#4ade80"),
		Warning:         lipgloss.Color("#fbbf24"),
		Error:           lipgloss.Color("#fb7185"),
	},
	{
		ID:              "eclipse",
		Label:           "eclipse",
		LowContrast:     false,
		AppBG:           lipgloss.Color("#0a0a0a"),
		PanelBG:         lipgloss.Color("#0a0a0a"),
		HeaderBG:        lipgloss.Color("#18181b"),
		HeaderFG:        lipgloss.Color("#e4e4e7"),
		Border:          lipgloss.Color("#3f3f46"),
		BorderFocus:     lipgloss.Color("#34d399"),
		Text:            lipgloss.Color("#e4e4e7"),
		TextDim:         lipgloss.Color("#a1a1aa"),
		AccentPrimary:   lipgloss.Color("#34d399"),
		AccentSecondary: lipgloss.Color("#60a5fa"),
		Success:         lipgloss.Color("#34d399"),
		Warning:         lipgloss.Color("#fbbf24"),
		Error:           lipgloss.Color("#f87171"),
	},
}

var chatThemeAliases = map[string]string{
	"default":      "default",
	"dark":         "default",
	"low":          "low",
	"low-contrast": "low",
	"lowcontrast":  "low",
	"light":        "light",
	"dusk":         "dusk",
	"eclipse":      "eclipse",
}

func lookupChatTheme(name string) (chatTheme, bool) {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" {
		name = "default"
	}
	if alias, ok := chatThemeAliases[name]; ok {
		name = alias
	}
	for _, theme := range chatThemeRegistry {
		if theme.ID == name {
			return theme, true
		}
	}
	return chatTheme{}, false
}

func orderedChatThemes() []chatTheme {
	out := make([]chatTheme, len(chatThemeRegistry))
	copy(out, chatThemeRegistry)
	return out
}

func (m ChatModel) theme() chatTheme {
	theme, ok := lookupChatTheme(m.themeID)
	if !ok {
		theme, _ = lookupChatTheme("default")
	}
	return theme
}

func (m *ChatModel) applyTheme(themeID string) bool {
	theme, ok := lookupChatTheme(themeID)
	if !ok {
		m.flash = fmt.Sprintf("unknown theme %q", themeID)
		return false
	}
	m.themeID = theme.ID
	m.refreshViewport()
	m.flash = "theme: " + theme.Label
	return true
}

func (m *ChatModel) cycleTheme() {
	current := m.theme().ID
	themes := orderedChatThemes()
	for i, theme := range themes {
		if theme.ID == current {
			next := themes[(i+1)%len(themes)]
			m.themeID = next.ID
			m.refreshViewport()
			m.flash = "theme: " + next.Label
			return
		}
	}
	if len(themes) > 0 {
		m.themeID = themes[0].ID
		m.refreshViewport()
		m.flash = "theme: " + themes[0].Label
	}
}
