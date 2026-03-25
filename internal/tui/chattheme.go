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
		AppBG:           lipgloss.Color("#0b0f12"),
		PanelBG:         lipgloss.Color("#10161c"),
		HeaderBG:        lipgloss.Color("#0b0f12"),
		HeaderFG:        lipgloss.Color("#edf2f7"),
		Border:          lipgloss.Color("#26313a"),
		BorderFocus:     lipgloss.Color("#6ec8ff"),
		Text:            lipgloss.Color("#edf2f7"),
		TextDim:         lipgloss.Color("#93a0ab"),
		AccentPrimary:   lipgloss.Color("#6ec8ff"),
		AccentSecondary: lipgloss.Color("#ffb86b"),
		Success:         lipgloss.Color("#87d7a0"),
		Warning:         lipgloss.Color("#ffcf7a"),
		Error:           lipgloss.Color("#ff8f8f"),
	},
	{
		ID:              "low",
		Label:           "low contrast",
		LowContrast:     true,
		AppBG:           lipgloss.Color("#0b0f14"),
		PanelBG:         lipgloss.Color("#0d1117"),
		HeaderBG:        lipgloss.Color("#161b22"),
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
		AppBG:           lipgloss.Color("#f6f8fa"),
		PanelBG:         lipgloss.Color("#ffffff"),
		HeaderBG:        lipgloss.Color("#eaeef2"),
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
		AppBG:           lipgloss.Color("#111827"),
		PanelBG:         lipgloss.Color("#0f172a"),
		HeaderBG:        lipgloss.Color("#1e293b"),
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
}

var chatThemeAliases = map[string]string{
	"default":      "default",
	"dark":         "default",
	"low":          "low",
	"low-contrast": "low",
	"lowcontrast":  "low",
	"light":        "light",
	"dusk":         "dusk",
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
