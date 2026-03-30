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
		AppBG:           lipgloss.Color("#000000"),
		PanelBG:         lipgloss.Color("#000000"),
		HeaderBG:        lipgloss.Color("#000000"),
		HeaderFG:        lipgloss.Color("#f2f6fb"),
		Border:          lipgloss.Color("#1f2b38"),
		BorderFocus:     lipgloss.Color("#79c0ff"),
		Text:            lipgloss.Color("#eaf0f8"),
		TextDim:         lipgloss.Color("#8d9aae"),
		AccentPrimary:   lipgloss.Color("#79c0ff"),
		AccentSecondary: lipgloss.Color("#f0c674"),
		Success:         lipgloss.Color("#6fda9c"),
		Warning:         lipgloss.Color("#f0c674"),
		Error:           lipgloss.Color("#ff7f7f"),
	},
	{
		ID:              "codex",
		Label:           "codex",
		LowContrast:     false,
		AppBG:           lipgloss.Color("#000000"),
		PanelBG:         lipgloss.Color("#000000"),
		HeaderBG:        lipgloss.Color("#000000"),
		HeaderFG:        lipgloss.Color("#f3f6fb"),
		Border:          lipgloss.Color("#1d2b3a"),
		BorderFocus:     lipgloss.Color("#66b6ff"),
		Text:            lipgloss.Color("#e6edf7"),
		TextDim:         lipgloss.Color("#8c9cb0"),
		AccentPrimary:   lipgloss.Color("#58a6ff"),
		AccentSecondary: lipgloss.Color("#f2cc60"),
		Success:         lipgloss.Color("#5fd38d"),
		Warning:         lipgloss.Color("#f2cc60"),
		Error:           lipgloss.Color("#ff8787"),
	},
	{
		ID:              "opencode",
		Label:           "opencode",
		LowContrast:     false,
		AppBG:           lipgloss.Color("#000000"),
		PanelBG:         lipgloss.Color("#000000"),
		HeaderBG:        lipgloss.Color("#000000"),
		HeaderFG:        lipgloss.Color("#f2f5fb"),
		Border:          lipgloss.Color("#243244"),
		BorderFocus:     lipgloss.Color("#7cc4ff"),
		Text:            lipgloss.Color("#e5ecf7"),
		TextDim:         lipgloss.Color("#90a0b7"),
		AccentPrimary:   lipgloss.Color("#6bb7ff"),
		AccentSecondary: lipgloss.Color("#ffcf73"),
		Success:         lipgloss.Color("#64d79a"),
		Warning:         lipgloss.Color("#ffcf73"),
		Error:           lipgloss.Color("#ff8e8e"),
	},
	{
		ID:              "low",
		Label:           "low contrast",
		LowContrast:     true,
		AppBG:           lipgloss.Color("#000000"),
		PanelBG:         lipgloss.Color("#000000"),
		HeaderBG:        lipgloss.Color("#000000"),
		HeaderFG:        lipgloss.Color("#c9d1d9"),
		Border:          lipgloss.Color("#3b434b"),
		BorderFocus:     lipgloss.Color("#7fbf7f"),
		Text:            lipgloss.Color("#c9d1d9"),
		TextDim:         lipgloss.Color("#8b949e"),
		AccentPrimary:   lipgloss.Color("#7fbf7f"),
		AccentSecondary: lipgloss.Color("#d5b26e"),
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
		AppBG:           lipgloss.Color("#000000"),
		PanelBG:         lipgloss.Color("#000000"),
		HeaderBG:        lipgloss.Color("#000000"),
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
		ID:              "midnight-ink",
		Label:           "midnight ink",
		LowContrast:     false,
		AppBG:           lipgloss.Color("#0a0e1a"),
		PanelBG:         lipgloss.Color("#0a0e1a"),
		HeaderBG:        lipgloss.Color("#131a2e"),
		HeaderFG:        lipgloss.Color("#e8eef6"),
		Border:          lipgloss.Color("#1e2a42"),
		BorderFocus:     lipgloss.Color("#3a5a8a"),
		Text:            lipgloss.Color("#d4dce8"),
		TextDim:         lipgloss.Color("#6b7d96"),
		AccentPrimary:   lipgloss.Color("#5b9bd5"),
		AccentSecondary: lipgloss.Color("#d4a954"),
		Success:         lipgloss.Color("#6ec89b"),
		Warning:         lipgloss.Color("#d4a954"),
		Error:           lipgloss.Color("#d46e6e"),
	},
	{
		ID:              "eclipse",
		Label:           "eclipse",
		LowContrast:     false,
		AppBG:           lipgloss.Color("#000000"),
		PanelBG:         lipgloss.Color("#000000"),
		HeaderBG:        lipgloss.Color("#000000"),
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
	"codex":        "codex",
	"opencode":     "opencode",
	"open-code":    "opencode",
	"open_code":    "opencode",
	"low":          "low",
	"low-contrast": "low",
	"lowcontrast":  "low",
	"light":        "light",
	"dusk":         "dusk",
	"midnight-ink": "midnight-ink",
	"midnight_ink": "midnight-ink",
	"midnightink":  "midnight-ink",
	"midnight":     "midnight-ink",
	"ink":          "midnight-ink",
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
