package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

func (m ChatModel) renderStatsOverlay() string {
	return renderStatsOverlayPanel(m.theme(), m.statsSnapshot(), m.width, m.height)
}

func (m ChatModel) renderTraceOverlay() string {
	return renderTraceOverlayPanel(m.theme(), m.renderedToolsBuf(), m.config.DebugLogPath, m.width, m.height)
}

func (m ChatModel) renderSearchOverlay() string {
	theme := m.theme()
	boxW := min(72, max(42, m.width-10))
	boxH := 7

	titleStyle := lipgloss.NewStyle().Foreground(theme.AccentPrimary).Bold(true)
	textStyle := lipgloss.NewStyle().Foreground(theme.Text)
	dimStyle := lipgloss.NewStyle().Foreground(theme.TextDim)
	inputStyle := lipgloss.NewStyle().Foreground(theme.Text).Background(theme.PanelBG)

	paneName := "agent"
	if m.searchPane == focusTools {
		paneName = "tools"
	}
	status := fmt.Sprintf("%d matches", len(m.searchMatches))
	if len(m.searchMatches) == 1 {
		status = "1 match"
	}
	if len(m.searchMatches) == 0 && strings.TrimSpace(m.searchQuery) != "" {
		status = "No matches"
	}
	inner := lipgloss.JoinVertical(lipgloss.Left,
		titleStyle.Render("Search"),
		dimStyle.Render("Pane: "+paneName),
		inputStyle.Render("Query: "+m.searchQuery),
		textStyle.Render(status),
		dimStyle.Render("Enter jump • Esc close"),
	)
	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(theme.BorderFocus).
		Background(theme.HeaderBG).
		Padding(1, 2).
		Width(boxW - 6).
		Height(boxH - 4).
		Render(inner)
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, box)
}

func (m ChatModel) renderFilesOverlay() string {
	theme := m.theme()
	boxW := min(96, max(42, m.width-6))
	boxH := min(30, max(12, m.height-4))
	contentHeight := max(1, boxH-8)

	titleStyle := lipgloss.NewStyle().Foreground(theme.AccentPrimary).Bold(true)
	selectedStyle := lipgloss.NewStyle().Foreground(theme.HeaderFG).Background(theme.AccentPrimary).Bold(true)
	textStyle := lipgloss.NewStyle().Foreground(theme.Text)
	dimStyle := lipgloss.NewStyle().Foreground(theme.TextDim)
	inputStyle := lipgloss.NewStyle().Foreground(theme.Text).Background(theme.PanelBG)
	if m.filesViewing {
		return m.renderFileViewerOverlay(boxW, boxH, contentHeight, titleStyle, textStyle, dimStyle)
	}

	lines := make([]string, 0, min(len(m.filesFiltered), contentHeight))
	start := 0
	if m.filesCursor >= contentHeight {
		start = m.filesCursor - contentHeight + 1
	}
	for i := 0; i < contentHeight && start+i < len(m.filesFiltered); i++ {
		idx := start + i
		line := m.filesFiltered[idx]
		if idx == m.filesCursor {
			lines = append(lines, selectedStyle.Render(line))
		} else {
			lines = append(lines, textStyle.Render(line))
		}
	}
	if len(lines) == 0 {
		lines = append(lines, dimStyle.Render("No matching files"))
	}
	title := "Add context file (@...)"
	footer := "Type to filter • Enter insert • Esc close"
	if m.filesBrowser {
		title = "Browse workspace files"
		footer = "Type to filter • Enter view • @ add context • Esc close"
	}
	inner := lipgloss.JoinVertical(lipgloss.Left,
		titleStyle.Render(title),
		inputStyle.Render("Query: "+m.filesQuery),
		"",
		lipgloss.NewStyle().Width(boxW-6).Height(contentHeight).Render(strings.Join(lines, "\n")),
		"",
		dimStyle.Render(footer),
	)
	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(theme.BorderFocus).
		Background(theme.HeaderBG).
		Padding(1, 2).
		Width(boxW - 6).
		Height(boxH - 4).
		Render(inner)
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, box)
}

func (m ChatModel) renderFileViewerOverlay(boxW, boxH, contentHeight int, titleStyle, textStyle, dimStyle lipgloss.Style) string {
	lines := strings.Split(m.filesViewText, "\n")
	if len(lines) == 0 {
		lines = []string{""}
	}
	m.filesViewScroll = clamp(m.filesViewScroll, 0, max(0, len(lines)-contentHeight))
	start := m.filesViewScroll
	end := min(len(lines), start+contentHeight)
	visible := make([]string, 0, contentHeight)
	lineNoWidth := len(fmt.Sprintf("%d", len(lines)))
	bodyW := max(20, boxW-10)
	for i := start; i < end; i++ {
		prefix := fmt.Sprintf("%*d ", lineNoWidth, i+1)
		line := prefix + lines[i]
		visible = append(visible, textStyle.Render(truncate(line, bodyW-1)))
	}
	for len(visible) < contentHeight {
		visible = append(visible, "")
	}
	for i := range visible {
		plainWidth := ansiPrintableWidth(visible[i])
		if plainWidth < bodyW {
			visible[i] += strings.Repeat(" ", bodyW-plainWidth)
		}
	}
	body := strings.Join(visible, "\n")
	footer := fmt.Sprintf("lines %d-%d/%d • j/k scroll • PgUp/PgDn • a/@ add context • q back • Esc close", min(len(lines), start+1), end, len(lines))
	inner := lipgloss.JoinVertical(lipgloss.Left,
		titleStyle.Render("File preview"),
		dimStyle.Render(m.filesViewPath),
		"",
		lipgloss.NewStyle().Width(boxW-6).Height(contentHeight).Render(body),
		"",
		dimStyle.Render(truncate(footer, max(20, boxW-8))),
	)
	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(m.theme().BorderFocus).
		Background(m.theme().HeaderBG).
		Padding(1, 2).
		Width(boxW - 6).
		Height(boxH - 4).
		Render(inner)
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, box)
}

func (m ChatModel) renderSessionRenameOverlay() string {
	theme := m.theme()
	boxW := min(64, max(38, m.width-10))
	boxH := 7
	titleStyle := lipgloss.NewStyle().Foreground(theme.AccentPrimary).Bold(true)
	textStyle := lipgloss.NewStyle().Foreground(theme.Text)
	dimStyle := lipgloss.NewStyle().Foreground(theme.TextDim)
	inner := lipgloss.JoinVertical(lipgloss.Left,
		titleStyle.Render("Rename session"),
		textStyle.Render("name> "+m.sessionRenameBuf),
		"",
		dimStyle.Render("Enter save • Esc cancel"),
	)
	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(theme.BorderFocus).
		Background(theme.HeaderBG).
		Padding(1, 2).
		Width(boxW - 6).
		Height(boxH - 4).
		Render(inner)
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, box)
}

func (m ChatModel) renderSessionsOverlay() string {
	theme := m.theme()
	boxW := min(88, max(56, m.width-6))
	boxH := min(28, max(12, m.height-4))
	contentHeight := max(1, boxH-6)

	titleStyle := lipgloss.NewStyle().Foreground(theme.AccentPrimary).Bold(true)
	selectedStyle := lipgloss.NewStyle().Foreground(theme.HeaderFG).Background(theme.AccentPrimary).Bold(true)
	textStyle := lipgloss.NewStyle().Foreground(theme.Text)
	dimStyle := lipgloss.NewStyle().Foreground(theme.TextDim)

	lines := make([]string, 0, min(len(m.sessionsList), contentHeight))
	start := 0
	if m.sessionsCursor >= contentHeight {
		start = m.sessionsCursor - contentHeight + 1
	}
	for i := 0; i < contentHeight && start+i < len(m.sessionsList); i++ {
		idx := start + i
		entry := m.sessionsList[idx]
		line := fmt.Sprintf("%d. %s  (%s)", idx+1, entry.name, formatSessionTimestamp(entry.modTime))
		if idx == m.sessionsCursor {
			lines = append(lines, selectedStyle.Render(line))
		} else {
			lines = append(lines, textStyle.Render(line))
		}
	}
	footer := dimStyle.Render("Enter restore • r rename • d delete • 1-9 quick restore • Esc close")
	inner := lipgloss.JoinVertical(lipgloss.Left,
		titleStyle.Render("Sessions"),
		"",
		lipgloss.NewStyle().Width(boxW-6).Height(contentHeight).Render(strings.Join(lines, "\n")),
		"",
		footer,
	)
	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(theme.BorderFocus).
		Background(theme.HeaderBG).
		Padding(1, 2).
		Width(boxW - 6).
		Height(boxH - 4).
		Render(inner)
	if m.sessionRenaming {
		return m.renderSessionRenameOverlay()
	}
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, box)
}

func (m ChatModel) renderHelpOverlay() string {
	theme := m.theme()
	boxW := min(108, max(72, m.width-6))
	boxH := min(32, max(20, m.height-4))
	contentHeight := max(1, boxH-7)
	lines := m.helpLines()
	maxScroll := max(0, len(lines)-contentHeight)
	if m.helpScroll > maxScroll {
		m.helpScroll = maxScroll
	}

	titleStyle := lipgloss.NewStyle().Foreground(theme.AccentPrimary).Bold(true)
	activeTabStyle := lipgloss.NewStyle().Background(theme.AccentPrimary).Foreground(theme.HeaderFG).Bold(true).Padding(0, 1)
	inactiveTabStyle := lipgloss.NewStyle().Background(theme.HeaderBG).Foreground(theme.TextDim).Padding(0, 1)
	textStyle := lipgloss.NewStyle().Foreground(theme.Text)
	dimStyle := lipgloss.NewStyle().Foreground(theme.TextDim)

	tabs := m.helpTabs()
	renderedTabs := make([]string, 0, len(tabs))
	for i, tab := range tabs {
		if i == m.helpTab {
			renderedTabs = append(renderedTabs, activeTabStyle.Render(tab))
		} else {
			renderedTabs = append(renderedTabs, inactiveTabStyle.Render(tab))
		}
	}

	visible := make([]string, 0, contentHeight)
	for i := 0; i < contentHeight; i++ {
		idx := m.helpScroll + i
		if idx >= len(lines) {
			break
		}
		visible = append(visible, textStyle.Render(lines[idx]))
	}
	content := strings.Join(visible, "\n")
	footer := fmt.Sprintf("Tab %d/%d • lines %d-%d/%d • ←/→ switch tabs • ↑/↓ scroll • Esc closes", m.helpTab+1, len(tabs), min(len(lines), m.helpScroll+1), min(len(lines), m.helpScroll+contentHeight), len(lines))

	inner := lipgloss.JoinVertical(lipgloss.Left,
		titleStyle.Render("Help"),
		strings.Join(renderedTabs, " "),
		"",
		lipgloss.NewStyle().Width(boxW-6).Height(contentHeight).Render(content),
		"",
		dimStyle.Render(footer),
	)

	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(theme.BorderFocus).
		Background(theme.HeaderBG).
		Padding(1, 2).
		Width(boxW - 6).
		Height(boxH - 4).
		Render(inner)

	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, box)
}

func (m ChatModel) renderModelsOverlay() string {
	theme := m.theme()
	boxW := min(96, max(56, m.width-6))
	boxH := min(28, max(14, m.height-4))
	contentHeight := max(1, boxH-8)

	titleStyle := lipgloss.NewStyle().Foreground(theme.AccentPrimary).Bold(true)
	selectedStyle := lipgloss.NewStyle().Foreground(theme.HeaderFG).Background(theme.AccentPrimary).Bold(true)
	textStyle := lipgloss.NewStyle().Foreground(theme.Text)
	dimStyle := lipgloss.NewStyle().Foreground(theme.TextDim)
	inputStyle := lipgloss.NewStyle().Foreground(theme.Text).Background(theme.PanelBG)

	lines := make([]string, 0, min(len(m.modelsFiltered), contentHeight))
	start := 0
	if m.modelsCursor >= contentHeight {
		start = m.modelsCursor - contentHeight + 1
	}
	for i := 0; i < contentHeight && start+i < len(m.modelsFiltered); i++ {
		idx := start + i
		line := fmt.Sprintf("%d. %s", idx+1, m.modelOptionLabel(m.modelsFiltered[idx]))
		if idx == m.modelsCursor {
			lines = append(lines, selectedStyle.Render(line))
		} else {
			lines = append(lines, textStyle.Render(line))
		}
	}
	queryLine := inputStyle.Render("Query: " + m.modelsQuery)
	if len(lines) == 0 {
		lines = append(lines, dimStyle.Render("No matching models"))
	}
	rangeText := "0 models"
	if len(m.modelsFiltered) > 0 {
		rangeText = fmt.Sprintf("%d-%d/%d", start+1, min(len(m.modelsFiltered), start+len(lines)), len(m.modelsFiltered))
	}
	footer := dimStyle.Render("Type to filter • ↑/↓ select • Enter choose • " + rangeText + " • Esc close")
	inner := lipgloss.JoinVertical(lipgloss.Left,
		titleStyle.Render("Models"),
		queryLine,
		"",
		lipgloss.NewStyle().Width(boxW-6).Height(contentHeight).Render(strings.Join(lines, "\n")),
		"",
		footer,
	)
	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(theme.BorderFocus).
		Background(theme.HeaderBG).
		Padding(1, 2).
		Width(boxW - 6).
		Height(boxH - 4).
		Render(inner)
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, box)
}

func (m ChatModel) renderProvidersOverlay() string {
	theme := m.theme()
	boxW := min(96, max(64, m.width-6))
	boxH := min(30, max(14, m.height-4))
	contentHeight := max(1, boxH-9)

	titleStyle := lipgloss.NewStyle().Foreground(theme.AccentPrimary).Bold(true)
	selectedStyle := lipgloss.NewStyle().Foreground(theme.HeaderFG).Background(theme.AccentPrimary).Bold(true)
	textStyle := lipgloss.NewStyle().Foreground(theme.Text)
	dimStyle := lipgloss.NewStyle().Foreground(theme.TextDim)
	inputStyle := lipgloss.NewStyle().Foreground(theme.Text).Background(theme.PanelBG)

	lines := make([]string, 0, min(len(m.providersList), contentHeight))
	start := 0
	if m.providersCursor >= contentHeight {
		start = m.providersCursor - contentHeight + 1
	}
	for i := 0; i < contentHeight && start+i < len(m.providersList); i++ {
		idx := start + i
		provider := m.providersList[idx]
		line := provider.Label
		if provider.Status != "" {
			line += " — " + provider.Status
		}
		if provider.DefaultModel != "" {
			line += " (" + provider.DefaultModel + ")"
		}
		if idx == m.providersCursor {
			lines = append(lines, selectedStyle.Render(line))
		} else {
			lines = append(lines, textStyle.Render(line))
		}
	}

	keyLine := ""
	if m.providerPromptingKey {
		if m.providerPromptMasked {
			keyLine = m.providerPromptLabel + ": " + strings.Repeat("*", len([]rune(m.providerKeyInput)))
		} else {
			keyLine = m.providerPromptLabel + ": " + m.providerKeyInput
		}
	}
	footerText := "↑/↓ select • Enter configure/select • d delete credential • Esc close"
	if m.providerPromptingKey {
		if m.providerAuthProvider == "claude" {
			footerText = keyLabel("Ctrl+O open browser • Enter submit pasted callback/code • Esc cancel")
		} else {
			footerText = "Enter save key • Esc cancel"
		}
	} else if m.providerAuthWaiting {
		footerText = "o/click open browser • complete sign-in there • Esc cancel"
	}
	authLines := []string{}
	authWidth := max(1, boxW-6)
	if m.providerAuthWaiting || (m.providerPromptingKey && m.providerAuthProvider == "claude" && m.providerAuthURL != "") {
		if m.providerAuthURL != "" {
			authLines = append(authLines, textStyle.Render("Open URL (click here or press o):"))
			authLines = append(authLines, textStyle.Render(providerAuthHyperlink(wrapProviderAuthValue(m.providerAuthURL, authWidth), m.providerAuthURL)))
		}
		if m.providerAuthCode != "" {
			authLines = append(authLines, textStyle.Render("Code:"))
			authLines = append(authLines, textStyle.Render(wrapProviderAuthValue(m.providerAuthCode, authWidth)))
		}
	}
	if m.providerPromptingKey && m.providerAuthProvider == "claude" {
		keyLine = inputStyle.Render(keyLine)
	} else if keyLine != "" {
		keyLine = textStyle.Render(keyLine)
	}
	inner := lipgloss.JoinVertical(lipgloss.Left,
		titleStyle.Render("Providers"),
		dimStyle.Render("Select a provider. API-key providers prompt for a key; ChatGPT/Claude/Copilot can sign in here."),
		"",
		lipgloss.NewStyle().Width(authWidth).Height(contentHeight).Render(strings.Join(lines, "\n")),
		"",
		lipgloss.NewStyle().Width(authWidth).Render(strings.Join(authLines, "\n")),
		keyLine,
		dimStyle.Render(m.providerStatus),
		dimStyle.Render(footerText),
	)
	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(theme.BorderFocus).
		Background(theme.HeaderBG).
		Padding(1, 2).
		Width(boxW - 6).
		Height(boxH - 4).
		Render(inner)
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, box)
}
