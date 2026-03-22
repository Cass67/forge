package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"
)

func (m *chatLiveModel) render(screen tcell.Screen) {
	started := time.Now()
	w, h := screen.Size()
	m.width, m.height = w, h
	screen.Clear()

	colors := m.renderColors()
	styles := m.renderStyles(colors)
	fillRect(screen, 0, 0, w, h, tcell.StyleDefault.Background(colors.bg))

	spinnerFrames := []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧"}
	m.renderHeader(screen, w, styles.status, spinnerFrames)

	leftX, leftY, leftW, leftH := m.leftPaneRect()
	rightX, rightY, rightW, rightH := m.rightPaneRect()
	inputX, inputY, inputW, inputH := m.inputRect()

	m.renderChrome(screen, colors.bg, colors.panel, colors.border, colors.borderFocus, leftX, leftY, leftW, leftH, rightX, rightY, rightW, rightH, inputX, inputY, inputW, inputH)
	m.renderTitlesAndStatus(screen, styles.titleDim, styles.titleFocus, styles.bodyDim, inputX, inputY, inputW, inputH, leftX, leftY, leftW, rightX, rightY, rightW, spinnerFrames)
	m.renderPaneBodies(screen, styles.body, styles.bodyDim, styles.accent, styles.prompt, styles.titleFocus, colors.panel, colors.bright, colors.dim, colors.blue, colors.purple, colors.orange, colors.cyan, colors.green, colors.red, styles.diffAdd, styles.diffRm, leftX, leftY, leftW, rightX, rightY, rightW)
	m.renderInputArea(screen, styles.bodyDim, styles.prompt, styles.input, styles.approval, inputX, inputY, inputW, inputH, colors.panel, colors.yellow)
	m.renderOverlays(screen)
	screen.Show()
	m.recordFullRenderProfile(time.Since(started))
}

func (m *chatLiveModel) renderSpinnerOnly(screen tcell.Screen) {
	started := time.Now()
	w, h := screen.Size()
	m.width, m.height = w, h
	colors := m.renderColors()
	styles := m.renderStyles(colors)
	spinnerFrames := []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧"}

	fillRect(screen, 0, 0, w, 1, tcell.StyleDefault.Background(colors.bg))
	m.renderHeader(screen, w, styles.status, spinnerFrames)

	leftX, leftY, leftW, _ := m.leftPaneRect()
	rightX, rightY, rightW, _ := m.rightPaneRect()
	inputX, inputY, inputW, inputH := m.inputRect()

	fillRect(screen, leftX+1, leftY, max(1, leftW-2), 1, tcell.StyleDefault.Background(colors.panel))
	if m.panes.layout.toolsVisible {
		fillRect(screen, rightX+1, rightY, max(1, rightW-2), 1, tcell.StyleDefault.Background(colors.panel))
	}
	if inputY > 1 {
		fillRect(screen, inputX+1, inputY-1, max(1, inputW-2), 1, tcell.StyleDefault.Background(colors.bg))
	}
	fillRect(screen, inputX+1, inputY, max(1, inputW-2), 1, tcell.StyleDefault.Background(colors.panel))
	m.renderTitlesAndStatus(screen, styles.titleDim, styles.titleFocus, styles.bodyDim, inputX, inputY, inputW, inputH, leftX, leftY, leftW, rightX, rightY, rightW, spinnerFrames)
	m.renderOverlays(screen)
	screen.Show()
	m.recordSpinnerRenderProfile(time.Since(started))
}

type chatRenderColors struct {
	bg          tcell.Color
	panel       tcell.Color
	border      tcell.Color
	borderFocus tcell.Color
	bright      tcell.Color
	mid         tcell.Color
	dim         tcell.Color
	green       tcell.Color
	yellow      tcell.Color
	blue        tcell.Color
	purple      tcell.Color
	orange      tcell.Color
	cyan        tcell.Color
	red         tcell.Color
}

type chatRenderStyles struct {
	status     tcell.Style
	body       tcell.Style
	bodyDim    tcell.Style
	titleDim   tcell.Style
	titleFocus tcell.Style
	prompt     tcell.Style
	input      tcell.Style
	approval   tcell.Style
	accent     tcell.Style
	diffAdd    tcell.Style
	diffRm     tcell.Style
}

func (m *chatLiveModel) renderColors() chatRenderColors {
	colors := chatRenderColors{
		bg:          tcell.GetColor("#0d1117"),
		panel:       tcell.GetColor("#161b22"),
		border:      tcell.GetColor("#30363d"),
		borderFocus: tcell.GetColor("#58a6ff"),
		bright:      tcell.GetColor("#f0f6fc"),
		mid:         tcell.GetColor("#b1bac4"),
		dim:         tcell.GetColor("#8b949e"),
		green:       tcell.GetColor("#56d364"),
		yellow:      tcell.GetColor("#e3b341"),
		blue:        tcell.GetColor("#58a6ff"),
		purple:      tcell.GetColor("#d2a8ff"),
		orange:      tcell.GetColor("#f0883e"),
		cyan:        tcell.GetColor("#79c0ff"),
		red:         tcell.GetColor("#f85149"),
	}
	if m.themeLowContrast {
		colors.bg = tcell.GetColor("#11161c")
		colors.panel = tcell.GetColor("#1b2128")
		colors.border = tcell.GetColor("#46515c")
		colors.borderFocus = tcell.GetColor("#7aa2c9")
		colors.bright = tcell.GetColor("#d7dee5")
		colors.mid = tcell.GetColor("#b7c0c9")
		colors.dim = tcell.GetColor("#98a3ad")
		colors.green = tcell.GetColor("#7fbf9a")
		colors.yellow = tcell.GetColor("#c9b37a")
		colors.blue = tcell.GetColor("#7aa2c9")
		colors.purple = tcell.GetColor("#b3a1c9")
		colors.orange = tcell.GetColor("#c99b73")
		colors.cyan = tcell.GetColor("#86b7c4")
		colors.red = tcell.GetColor("#c98585")
	}
	return colors
}

func (m *chatLiveModel) renderStyles(colors chatRenderColors) chatRenderStyles {
	return chatRenderStyles{
		status:     tcell.StyleDefault.Background(colors.bg).Foreground(colors.mid),
		body:       tcell.StyleDefault.Background(colors.panel).Foreground(colors.bright),
		bodyDim:    tcell.StyleDefault.Background(colors.panel).Foreground(colors.dim),
		titleDim:   tcell.StyleDefault.Background(colors.panel).Foreground(colors.dim),
		titleFocus: tcell.StyleDefault.Background(colors.panel).Foreground(colors.green).Bold(true),
		prompt:     tcell.StyleDefault.Background(colors.panel).Foreground(colors.green).Bold(true),
		input:      tcell.StyleDefault.Background(colors.panel).Foreground(colors.bright),
		approval:   tcell.StyleDefault.Background(colors.panel).Foreground(colors.yellow).Bold(true),
		accent:     tcell.StyleDefault.Background(colors.panel).Foreground(colors.blue),
		diffAdd:    tcell.StyleDefault.Foreground(tcell.GetColor("#56d364")).Background(tcell.GetColor("#0f2d16")),
		diffRm:     tcell.StyleDefault.Foreground(colors.red).Background(tcell.GetColor("#3d1117")),
	}
}

func (m *chatLiveModel) renderOverlays(screen tcell.Screen) {
	if m.overlays.models.visible {
		m.renderModelPicker(screen)
	}
	if m.overlays.sessions.visible {
		m.renderSessionsPicker(screen)
	}
	if m.overlays.helpVisible {
		m.renderHelpOverlay(screen)
	}
	if m.overlays.statsVisible {
		m.renderStatsOverlay(screen)
	}
	if m.overlays.search.visible {
		m.renderSearchOverlay(screen)
	}
	if m.overlays.files.visible {
		m.renderFilePicker(screen)
	}
}

func (m *chatLiveModel) renderHeader(screen tcell.Screen, width int, styleStatus tcell.Style, spinnerFrames []string) {
	themeLabel := "default"
	if m.themeLowContrast {
		themeLabel = "low"
	}
	headerLeft := fmt.Sprintf(" forge • %s • %s • theme:%s ", m.model, shortPath(m.workDir), themeLabel)
	statusLabel := strings.ToUpper(m.status)
	if strings.TrimSpace(statusLabel) == "" {
		statusLabel = "READY"
	}
	if m.state != nil && len(m.skills) > 0 {
		active := make([]string, 0, len(m.skills))
		for _, s := range m.skills {
			if m.state.SkillActivated(s.Name) {
				active = append(active, "/"+s.Name)
			}
		}
		if len(active) > 0 {
			statusLabel += " · SKILLS " + strings.Join(active, ",")
		}
	}
	headerRight := fmt.Sprintf(" %s ", statusLabel)
	if m.busy {
		elapsed := time.Since(m.display.turnStartedAt)
		if m.display.turnStartedAt.IsZero() {
			elapsed = 0
		}
		headerRight = fmt.Sprintf(" %s %s  ⏱ %.1fs ", spinnerFrames[m.display.spinnerFrame%len(spinnerFrames)], strings.ToUpper(m.status), elapsed.Seconds())
	}
	if m.display.statsDuration > 0 && !m.busy {
		headerRight += fmt.Sprintf("  ⏱ %.1fs", m.display.statsDuration.Seconds())
		if m.display.statsUsage.InputTokens > 0 {
			headerRight += fmt.Sprintf("  ↑%d ↓%d", m.display.statsUsage.InputTokens, m.display.statsUsage.OutputTokens)
		}
		if q := m.display.statsUsage.CopilotQuota; q != nil {
			if q.Unlimited {
				headerRight += "  PR ∞"
			} else if q.Remaining > 0 {
				headerRight += fmt.Sprintf("  PR %d", q.Remaining)
			} else if q.PercentRemaining > 0 {
				headerRight += fmt.Sprintf("  PR %.0f%%", q.PercentRemaining)
			}
		}
	}
	drawText(screen, 0, 0, styleStatus, fitWidth(headerLeft, width))
	drawRightText(screen, 0, 0, width, styleStatus, headerRight)
}

func (m *chatLiveModel) renderChrome(screen tcell.Screen, colorBg, colorPanel, colorBorder, colorBorderFocus tcell.Color, leftX, leftY, leftW, leftH, rightX, rightY, rightW, rightH, inputX, inputY, inputW, inputH int) {
	leftBorder := colorBorder
	rightBorder := colorBorder
	if m.panes.focusR {
		rightBorder = colorBorderFocus
	} else {
		leftBorder = colorBorderFocus
	}

	drawBox(screen, leftX, leftY, leftW, leftH, tcell.StyleDefault.Background(colorPanel).Foreground(leftBorder))
	if m.panes.layout.toolsVisible {
		drawBox(screen, rightX, rightY, rightW, rightH, tcell.StyleDefault.Background(colorPanel).Foreground(rightBorder))
	}
	drawBox(screen, inputX, inputY, inputW, inputH, tcell.StyleDefault.Background(colorPanel).Foreground(colorBorder))
	dividerStyle := tcell.StyleDefault.Background(colorBg).Foreground(colorBorder)
	if m.panes.layout.dividerDrag {
		dividerStyle = tcell.StyleDefault.Background(colorBg).Foreground(colorBorderFocus).Bold(true)
	}
	if m.panes.layout.toolsVisible {
		for yy := leftY; yy < leftY+leftH; yy++ {
			screen.SetContent(rightX-1, yy, '⋮', nil, dividerStyle)
		}
	}
}

func (m *chatLiveModel) renderTitlesAndStatus(screen tcell.Screen, styleTitleDim, styleTitleFocus, styleBodyDim tcell.Style, inputX, inputY, inputW, inputH, leftX, leftY, leftW, rightX, rightY, rightW int, spinnerFrames []string) {
	leftTitle := styleTitleDim
	rightTitle := styleTitleDim
	if m.panes.focusR {
		rightTitle = styleTitleFocus
	} else {
		leftTitle = styleTitleFocus
	}

	leftBadge := " Agent "
	rightBadge := " Tools "
	inputBadge := " Steering "
	if m.overlays.search.pane == "left" && strings.TrimSpace(m.overlays.search.query) != "" {
		if len(m.overlays.search.matches) > 0 {
			leftBadge = fmt.Sprintf(" Agent • %d/%d search ", max(1, m.overlays.search.current+1), len(m.overlays.search.matches))
		} else {
			leftBadge = " Agent • 0 search "
		}
	}
	if m.overlays.search.pane == "right" && strings.TrimSpace(m.overlays.search.query) != "" {
		if len(m.overlays.search.matches) > 0 {
			rightBadge = fmt.Sprintf(" Tools • %d/%d search ", max(1, m.overlays.search.current+1), len(m.overlays.search.matches))
		} else {
			rightBadge = " Tools • 0 search "
		}
	}
	if m.panes.agent.follow {
		leftBadge += "• follow "
	}
	if m.panes.tools.follow {
		rightBadge += "• follow "
	}
	drawText(screen, leftX+2, leftY, leftTitle, fitWidth(leftBadge, max(1, leftW-4)))
	if m.panes.layout.toolsVisible {
		drawText(screen, rightX+2, rightY, rightTitle, fitWidth(rightBadge, max(1, rightW-4)))
	}
	statusStrip := fmt.Sprintf(" status: %s ", m.status)
	activeSkills := ""
	if m.state != nil && len(m.skills) > 0 {
		active := make([]string, 0, len(m.skills))
		for _, s := range m.skills {
			if m.state.SkillActivated(s.Name) {
				active = append(active, "/"+s.Name)
			}
		}
		if len(active) > 0 {
			activeSkills = " active skills: " + strings.Join(active, " ") + " "
		}
	}
	if m.display.prof.enabled {
		statusStrip += " • " + m.profileSummary()
	}
	if len(m.display.timeline) > 0 {
		statusStrip += " • " + strings.Join(m.display.timeline[max(0, len(m.display.timeline)-3):], "  •  ")
	}
	if m.busy {
		elapsed := time.Since(m.display.turnStartedAt)
		if m.display.turnStartedAt.IsZero() {
			elapsed = 0
		}
		statusStrip = fmt.Sprintf(" status: %s %s • running • %.1fs ", spinnerFrames[m.display.spinnerFrame%len(spinnerFrames)], m.status, elapsed.Seconds())
	}
	if m.approval != nil {
		statusStrip = " status: approval needed "
	}
	if inputY > 2 && activeSkills != "" {
		drawText(screen, inputX+2, inputY-2, styleBodyDim, fitWidth(activeSkills, max(1, inputW-4)))
	}
	if inputY > 1 {
		drawText(screen, inputX+2, inputY-1, styleBodyDim, fitWidth(statusStrip, max(1, inputW-4)))
	}
	drawText(screen, inputX+2, inputY, styleBodyDim, fitWidth(inputBadge, max(1, inputW-4)))
	footerLegend := chatFooterLegend(max(1, inputW-4))
	if footerLegend != "" {
		drawRightText(screen, inputX+1, inputY, inputW-2, styleBodyDim, footerLegend)
	}

	leftScroll := scrollLabelWithFollow(m.panes.agent.scroll, m.agentMaxScroll(), m.panes.agent.follow)
	rightScroll := scrollLabelWithFollow(m.panes.tools.scroll, m.toolsMaxScroll(), m.panes.tools.follow)
	drawRightText(screen, leftX+1, leftY, leftW-2, styleBodyDim, leftScroll)
	if m.panes.layout.toolsVisible {
		drawRightText(screen, rightX+1, rightY, rightW-2, styleBodyDim, rightScroll)
	}
}

func (m *chatLiveModel) renderPaneBodies(screen tcell.Screen, styleBody, styleBodyDim, styleAccent, stylePrompt, styleTitleFocus tcell.Style, colorPanel, colorBright, colorDim, colorBlue, colorPurple, colorOrange, colorCyan, colorGreen, colorRed tcell.Color, styleDiffAdd, styleDiffRm tcell.Style, leftX, leftY, leftW, rightX, rightY, rightW int) {
	leftContentW := m.leftContentWidth()
	rightContentW := m.rightContentWidth()
	leftVisibleH := m.agentVisibleHeight()
	rightVisibleH := m.toolsVisibleHeight()

	leftWrapped := m.wrappedLines(&m.panes.agent, leftContentW)
	rightWrapped := m.wrappedLines(&m.panes.tools, rightContentW)
	leftLineStarts := m.wrappedLineStartsForPane(&m.panes.agent, leftContentW)
	rightLineStarts := m.wrappedLineStartsForPane(&m.panes.tools, rightContentW)
	leftLines := m.paneLines(&m.panes.agent, leftContentW, leftVisibleH, m.panes.agent.scroll)
	rightLines := m.paneLines(&m.panes.tools, rightContentW, rightVisibleH, m.panes.tools.scroll)

	leftQuery := ""
	rightQuery := ""
	if m.overlays.search.pane == "left" {
		leftQuery = strings.TrimSpace(m.overlays.search.query)
	}
	if m.overlays.search.pane == "right" {
		rightQuery = strings.TrimSpace(m.overlays.search.query)
	}

	agentCodeStyle := tcell.StyleDefault.Background(tcell.GetColor("#0d1117")).Foreground(tcell.GetColor("#c9d1d9"))
	agentCodeBorderStyle := tcell.StyleDefault.Background(colorPanel).Foreground(tcell.GetColor("#30363d"))
	agentCodeHeaderStyle := tcell.StyleDefault.Background(tcell.GetColor("#0d1117")).Foreground(tcell.GetColor("#7ee787")).Bold(true)
	agentBubbleStyle := tcell.StyleDefault.Background(tcell.GetColor("#1a2332")).Foreground(colorBright)
	agentBubbleBorderStyle := tcell.StyleDefault.Background(colorPanel).Foreground(tcell.GetColor("#58a6ff"))
	agentBubbleDimStyle := tcell.StyleDefault.Background(tcell.GetColor("#1a2332")).Foreground(colorDim)
	userBg := tcell.GetColor("#1d2a1d")
	userBorder := tcell.GetColor("#56d364")
	if m.themeLowContrast {
		userBg = tcell.GetColor("#202820")
		userBorder = tcell.GetColor("#7fbf7f")
	}
	userBubbleStyle := tcell.StyleDefault.Background(userBg).Foreground(colorBright)
	userBubbleBorderStyle := tcell.StyleDefault.Background(colorPanel).Foreground(userBorder)
	userBubbleDimStyle := tcell.StyleDefault.Background(userBg).Foreground(colorDim)
	forgeBubbleStyle := tcell.StyleDefault.Background(tcell.GetColor("#241a2f")).Foreground(colorBright)
	forgeBubbleBorderStyle := tcell.StyleDefault.Background(colorPanel).Foreground(tcell.GetColor("#d2a8ff"))
	forgeBubbleDimStyle := tcell.StyleDefault.Background(tcell.GetColor("#241a2f")).Foreground(colorDim)
	inCodeBlock := false
	codeLang := ""
	conversationSection := "agent"
	for row := 0; row < leftVisibleH; row++ {
		y := leftY + 1 + row
		line := ""
		lineIndex := m.panes.agent.scroll + row
		if row < len(leftLines) {
			line = leftLines[row]
		}
		prevLine := ""
		if row > 0 && row-1 < len(leftLines) {
			prevLine = leftLines[row-1]
		}
		nextLine := ""
		if row+1 < len(leftLines) {
			nextLine = leftLines[row+1]
		}
		matchStart, isCurrent, hasMatch := 0, false, false
		if leftQuery != "" {
			matchStart, isCurrent, hasMatch = m.searchHighlightForLine(lineIndex)
		}
		trimmed := strings.TrimSpace(strings.TrimPrefix(line, " │ "))
		if strings.HasPrefix(trimmed, "```") {
			if !inCodeBlock {
				codeLang = strings.TrimSpace(strings.TrimPrefix(trimmed, "```"))
				label := "╭─ code"
				if codeLang != "" {
					label = "╭─ code: " + codeLang
				}
				fillRect(screen, leftX+1, y, leftContentW, 1, agentCodeStyle)
				drawText(screen, leftX+1, y, agentCodeBorderStyle, "▎")
				drawText(screen, leftX+3, y, agentCodeHeaderStyle, fitWidth(label, max(1, leftContentW-3)))
				inCodeBlock = true
			} else {
				fillRect(screen, leftX+1, y, leftContentW, 1, agentCodeStyle)
				drawText(screen, leftX+1, y, agentCodeBorderStyle, "▎")
				drawText(screen, leftX+3, y, agentCodeHeaderStyle, fitWidth("╰─ end code", max(1, leftContentW-3)))
				inCodeBlock = false
				codeLang = ""
			}
			continue
		}
		if inCodeBlock {
			fillRect(screen, leftX+1, y, leftContentW, 1, agentCodeStyle)
			drawText(screen, leftX+1, y, agentCodeBorderStyle, "▎")
			codeText := strings.TrimPrefix(line, " │ ")
			if hasMatch {
				drawHighlightedText(screen, leftX+3, y, codeText, max(1, leftContentW-3), agentCodeStyle, leftQuery, matchStart, isCurrent)
			} else {
				m.drawChromaCodeLine(screen, leftX+3, y, codeText, max(1, leftContentW-3), codeLang, agentCodeStyle)
			}
			continue
		}
		selected := m.lineHasSelection("left", m.panes.agent.scroll+row, leftWrapped, leftLineStarts)
		content := strings.TrimPrefix(line, " │ ")
		trimmedContent := strings.TrimSpace(content)
		bubbleStyle := agentBubbleStyle
		bubbleBorderStyle := agentBubbleBorderStyle
		bubbleDimStyle := agentBubbleDimStyle
		borderGlyph := "▎"
		textStyle := bubbleStyle
		trimmedLine := strings.TrimSpace(line)
		prevTrimmed := strings.TrimSpace(strings.TrimPrefix(prevLine, " │ "))
		bubbleX := leftX + 1
		bubbleW := leftContentW
		textX := leftX + 3
		textW := max(1, leftContentW-2)
		agentSectionStart := strings.HasPrefix(line, " │ ") && (strings.TrimSpace(prevLine) == "" || strings.HasPrefix(prevTrimmed, "You • ") || strings.HasPrefix(prevTrimmed, "Forge • ") || !strings.HasPrefix(prevLine, " │ "))
		if strings.HasPrefix(trimmedLine, "You • ") {
			conversationSection = "user"
			content = "╭─ " + trimmedLine
			trimmedContent = trimmedLine
			bubbleStyle = userBubbleStyle
			bubbleBorderStyle = userBubbleBorderStyle
			bubbleDimStyle = userBubbleDimStyle
			borderGlyph = " "
			textStyle = bubbleDimStyle.Bold(true)
			inset := min(6, max(2, leftContentW/6))
			bubbleX = leftX + 1 + inset
			bubbleW = max(1, leftContentW-inset)
			textX = bubbleX + 1
			textW = max(1, bubbleW-1)
		} else if strings.HasPrefix(trimmedLine, "Forge • ") {
			conversationSection = "forge"
			content = "╭─ " + trimmedLine
			trimmedContent = trimmedLine
			bubbleStyle = forgeBubbleStyle
			bubbleBorderStyle = forgeBubbleBorderStyle
			bubbleDimStyle = forgeBubbleDimStyle
			borderGlyph = " "
			textStyle = bubbleDimStyle.Bold(true)
			textX = bubbleX + 1
			textW = max(1, bubbleW-1)
		} else if agentSectionStart {
			conversationSection = "agent"
			content = "╭─ " + trimmedContent
			trimmedContent = strings.TrimSpace(strings.TrimPrefix(content, "╭─ "))
			borderGlyph = " "
			textStyle = bubbleDimStyle.Bold(true)
			textX = bubbleX + 1
			textW = max(1, bubbleW-1)
		} else if !strings.HasPrefix(line, " │ ") {
			switch conversationSection {
			case "user":
				bubbleStyle = userBubbleStyle
				bubbleBorderStyle = userBubbleBorderStyle
				bubbleDimStyle = userBubbleDimStyle
				borderGlyph = "▎"
				inset := min(6, max(2, leftContentW/6))
				bubbleX = leftX + 1 + inset
				bubbleW = max(1, leftContentW-inset)
				textX = bubbleX + 2
				textW = max(1, bubbleW-2)
			case "forge":
				bubbleStyle = forgeBubbleStyle
				bubbleBorderStyle = forgeBubbleBorderStyle
				bubbleDimStyle = forgeBubbleDimStyle
				borderGlyph = "▎"
			default:
				conversationSection = "agent"
			}
		}
		fillRect(screen, bubbleX, y, bubbleW, 1, bubbleStyle)
		if selected {
			fillRect(screen, bubbleX, y, bubbleW, 1, bubbleStyle.Background(tcell.GetColor("#2f81f7")).Foreground(tcell.ColorBlack))
		}
		if strings.HasPrefix(trimmedContent, "- ") || strings.HasPrefix(trimmedContent, "* ") {
			borderGlyph = "•"
			textStyle = bubbleDimStyle
		} else if strings.HasSuffix(trimmedContent, ":") && len(trimmedContent) < max(12, leftContentW/2) {
			borderGlyph = "▌"
		}
		nextTrimmed := strings.TrimSpace(strings.TrimPrefix(nextLine, " │ "))
		sectionEnds := false
		if strings.HasPrefix(line, " │ ") {
			sectionEnds = nextLine == "" || strings.HasPrefix(nextTrimmed, "You • ") || strings.HasPrefix(nextTrimmed, "Forge • ")
		} else if trimmedLine != "" {
			sectionEnds = nextLine == ""
		}
		drawText(screen, bubbleX, y, bubbleBorderStyle, borderGlyph)
		drawStyledAgentLine(screen, textX, y, content, textW, textStyle, styleAccent, leftQuery, hasMatch, matchStart, isCurrent)
		if sectionEnds && row+1 < leftVisibleH && row+1 < len(leftLines) && strings.TrimSpace(leftLines[row+1]) == "" {
			capY := y + 1
			fillRect(screen, bubbleX, capY, bubbleW, 1, bubbleStyle)
			drawStyledAgentLine(screen, textX, capY, "╰─", textW, bubbleBorderStyle, styleAccent, "", false, 0, false)
		}
	}
	if m.panes.layout.toolsVisible {
		toolCodeStyle := tcell.StyleDefault.Background(tcell.GetColor("#0d1117")).Foreground(tcell.GetColor("#c9d1d9"))
		toolCodeBorderStyle := tcell.StyleDefault.Background(colorPanel).Foreground(tcell.GetColor("#30363d"))
		toolCodeHeaderStyle := tcell.StyleDefault.Background(tcell.GetColor("#0d1117")).Foreground(colorCyan).Bold(true)
		toolBubbleStyle := tcell.StyleDefault.Background(tcell.GetColor("#11161d")).Foreground(colorBright)
		toolBubbleBorderStyle := tcell.StyleDefault.Background(colorPanel).Foreground(colorBlue)
		toolBubbleDimStyle := tcell.StyleDefault.Background(tcell.GetColor("#11161d")).Foreground(colorDim)
		inToolCodeBlock := false
		toolCodeLang := ""
		for row := 0; row < rightVisibleH; row++ {
			y := rightY + 1 + row
			line := ""
			lineIndex := m.panes.tools.scroll + row
			if row < len(rightLines) {
				line = rightLines[row]
			}
			matchStart, isCurrent, hasMatch := 0, false, false
			if rightQuery != "" {
				matchStart, isCurrent, hasMatch = m.searchHighlightForLine(lineIndex)
			}
			selected := m.lineHasSelection("right", m.panes.tools.scroll+row, rightWrapped, rightLineStarts)
			trimmed := strings.TrimSpace(line)
			content := strings.TrimLeft(line, " ")
			isToolHeader := strings.HasPrefix(trimmed, "● ") || strings.HasPrefix(trimmed, "────────────────────────")
			isCodeLine := strings.HasPrefix(trimmed, "result:") || strings.HasPrefix(trimmed, "+") || strings.HasPrefix(trimmed, "-") || strings.HasPrefix(trimmed, "@@") || strings.HasPrefix(trimmed, "...")
			if strings.HasPrefix(trimmed, "result:") {
				lang := ""
				lower := strings.ToLower(content)
				switch {
				case strings.Contains(lower, "diff") || strings.Contains(lower, "patch"):
					lang = "diff"
				case strings.Contains(lower, ".go") || strings.Contains(lower, " go"):
					lang = "go"
				case strings.Contains(lower, ".json") || strings.Contains(lower, " json"):
					lang = "json"
				case strings.Contains(lower, ".md") || strings.Contains(lower, " markdown"):
					lang = "markdown"
				case strings.Contains(lower, "shell") || strings.Contains(lower, "bash") || strings.Contains(lower, "sh"):
					lang = "bash"
				}
				toolCodeLang = lang
				fillRect(screen, rightX+1, y, rightContentW, 1, toolCodeStyle)
				if selected {
					fillRect(screen, rightX+1, y, rightContentW, 1, toolCodeStyle.Background(tcell.GetColor("#2f81f7")).Foreground(tcell.ColorBlack))
				}
				drawText(screen, rightX+1, y, toolCodeBorderStyle, "▎")
				label := "╭─ result"
				if toolCodeLang != "" {
					label += ": " + toolCodeLang
				}
				drawText(screen, rightX+3, y, toolCodeHeaderStyle, fitWidth(label, max(1, rightContentW-3)))
				inToolCodeBlock = true
				continue
			}
			if inToolCodeBlock && !isCodeLine {
				fillRect(screen, rightX+1, y, rightContentW, 1, toolCodeStyle)
				if selected {
					fillRect(screen, rightX+1, y, rightContentW, 1, toolCodeStyle.Background(tcell.GetColor("#2f81f7")).Foreground(tcell.ColorBlack))
				}
				drawText(screen, rightX+1, y, toolCodeBorderStyle, "▎")
				drawText(screen, rightX+3, y, toolCodeHeaderStyle, fitWidth("╰─ end result", max(1, rightContentW-3)))
				inToolCodeBlock = false
				toolCodeLang = ""
			}
			if inToolCodeBlock && isCodeLine {
				fillRect(screen, rightX+1, y, rightContentW, 1, toolCodeStyle)
				if selected {
					fillRect(screen, rightX+1, y, rightContentW, 1, toolCodeStyle.Background(tcell.GetColor("#2f81f7")).Foreground(tcell.ColorBlack))
				}
				drawText(screen, rightX+1, y, toolCodeBorderStyle, "▎")
				if hasMatch {
					drawHighlightedText(screen, rightX+3, y, content, max(1, rightContentW-3), toolCodeStyle, rightQuery, matchStart, isCurrent)
				} else {
					m.drawChromaCodeLine(screen, rightX+3, y, content, max(1, rightContentW-3), toolCodeLang, toolCodeStyle)
				}
				continue
			}
			fillRect(screen, rightX+1, y, rightContentW, 1, toolBubbleStyle)
			if selected {
				fillRect(screen, rightX+1, y, rightContentW, 1, toolBubbleStyle.Background(tcell.GetColor("#2f81f7")).Foreground(tcell.ColorBlack))
			}
			borderGlyph := "▎"
			lineStyle := toolBubbleStyle
			if isToolHeader {
				borderGlyph = "▌"
			} else if strings.HasPrefix(trimmed, "status:") || strings.HasPrefix(trimmed, "✓") || strings.HasPrefix(trimmed, "✗") {
				borderGlyph = "•"
				lineStyle = toolBubbleDimStyle
			}
			drawText(screen, rightX+1, y, toolBubbleBorderStyle, borderGlyph)
			drawStyledToolLine(screen, rightX+3, y, content, max(1, rightContentW-2), lineStyle,
				colorBlue, colorPurple, colorOrange, colorCyan, colorGreen, colorRed,
				styleDiffAdd, styleDiffRm, rightQuery, hasMatch, matchStart, isCurrent)
		}
		if inToolCodeBlock {
			y := rightY + rightVisibleH
			if y < rightY+1+rightVisibleH {
				fillRect(screen, rightX+1, y, rightContentW, 1, toolCodeStyle)
				drawText(screen, rightX+1, y, toolCodeBorderStyle, "▎")
				drawText(screen, rightX+3, y, toolCodeHeaderStyle, fitWidth("╰─ end result", max(1, rightContentW-3)))
			}
		}
	}

	leftThumbStyle := styleTitleFocus
	rightThumbStyle := styleTitleFocus
	if m.panes.layout.scrollDrag.pane == "left" {
		leftThumbStyle = stylePrompt
	}
	if m.panes.layout.scrollDrag.pane == "right" {
		rightThumbStyle = stylePrompt
	}
	drawScrollbar(screen, leftX+leftW-2, leftY+1, leftVisibleH, len(leftWrapped), leftVisibleH, m.panes.agent.scroll, styleBodyDim, leftThumbStyle)
	if m.panes.layout.toolsVisible {
		drawScrollbar(screen, rightX+rightW-2, rightY+1, rightVisibleH, len(rightWrapped), rightVisibleH, m.panes.tools.scroll, styleBodyDim, rightThumbStyle)
	}

	if strings.TrimSpace(m.panes.agent.buf) == "" {
		drawText(screen, leftX+2, leftY+2, styleBodyDim, fitWidth("Waiting for agent output…", max(1, leftContentW-1)))
	}
	if m.panes.layout.toolsVisible && strings.TrimSpace(m.panes.tools.buf) == "" {
		drawText(screen, rightX+2, rightY+2, styleBodyDim, fitWidth("Tool calls, diffs, and results appear here.", max(1, rightContentW-1)))
		drawText(screen, rightX+2, rightY+3, styleBodyDim, fitWidth("Use the scrollbar, wheel, or drag the divider to resize.", max(1, rightContentW-1)))
	}
}

func (m *chatLiveModel) renderInputArea(screen tcell.Screen, styleBodyDim, stylePrompt, styleInput, styleApproval tcell.Style, inputX, inputY, inputW, inputH int, colorPanel, colorYellow tcell.Color) {
	if m.display.requiredSkillWarning != "" && inputH > 0 {
		styleFlash := tcell.StyleDefault.Background(colorPanel).Foreground(colorYellow)
		drawText(screen, inputX+2, inputY+1, styleFlash, fitWidth("! "+m.display.requiredSkillWarning, max(1, inputW-4)))
	} else if m.display.flash != "" && inputH > 0 {
		styleFlash := tcell.StyleDefault.Background(colorPanel).Foreground(colorYellow)
		drawText(screen, inputX+2, inputY+1, styleFlash, fitWidth("! "+m.display.flash, max(1, inputW-4)))
	} else if inputH > 0 {
		steer := "Steer the agent: clarify constraints, ask for changes, /copy code, /copy result, F1 help"
		if m.busy {
			steer = "Busy mode queues steering in runtime: Enter send, Shift-Enter newline, or use /clear"
		}
		drawText(screen, inputX+2, inputY+1, styleBodyDim, fitWidth(steer, max(1, inputW-4)))
	}

	layout := m.inputLayout(max(1, inputW-2))
	inputLineY := inputY + 2
	if m.approval != nil {
		approvalText := fmt.Sprintf(" %s — approve? [y/n] ", m.approval.Summary)
		drawText(screen, inputX+1, inputLineY, styleApproval, fitWidth(approvalText, max(1, inputW-2)))
		screen.HideCursor()
	} else {
		promptW := stringWidth(layout.prompt)
		for i, line := range layout.visibleLines {
			y := inputLineY + i
			prefix := layout.prompt
			if i > 0 {
				prefix = strings.Repeat(" ", promptW)
			}
			drawText(screen, inputX+1, y, stylePrompt, fitWidth(prefix, promptW))
			drawText(screen, inputX+1+promptW, y, styleInput, fitWidth(line.text, layout.contentWidth))
		}
		cursorY := inputLineY + clamp(layout.visibleCursorLine, 0, len(layout.visibleLines)-1)
		screen.ShowCursor(inputX+1+promptW+layout.cursorX, cursorY)
	}
}
