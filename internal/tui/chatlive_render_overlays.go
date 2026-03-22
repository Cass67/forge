package tui

import (
	"fmt"
	"strings"

	"forge/internal/copilot"

	"github.com/gdamore/tcell/v2"
)

func (m *chatLiveModel) renderHelpOverlay(screen tcell.Screen) {
	w, h := screen.Size()
	boxW := min(86, max(54, w-8))
	boxH := min(23, max(16, h-6))
	x0 := (w - boxW) / 2
	y0 := (h - boxH) / 2
	backdrop := tcell.StyleDefault.Background(tcell.GetColor("#000000")).Foreground(tcell.GetColor("#000000"))
	for yy := max(0, y0-1); yy < min(h, y0+boxH+1); yy++ {
		for xx := max(0, x0-2); xx < min(w, x0+boxW+2); xx++ {
			screen.SetContent(xx, yy, ' ', nil, backdrop)
		}
	}
	boxStyle := tcell.StyleDefault.Background(tcell.GetColor("#161b22")).Foreground(tcell.GetColor("#58a6ff"))
	drawBox(screen, x0, y0, boxW, boxH, boxStyle)
	titleStyle := tcell.StyleDefault.Background(tcell.GetColor("#161b22")).Foreground(tcell.GetColor("#56d364")).Bold(true)
	textStyle := tcell.StyleDefault.Background(tcell.GetColor("#161b22")).Foreground(tcell.GetColor("#c9d1d9"))
	dimStyle := tcell.StyleDefault.Background(tcell.GetColor("#161b22")).Foreground(tcell.GetColor("#8b949e"))
	drawText(screen, x0+2, y0, titleStyle, fitWidth(" Keyboard shortcuts ", max(1, boxW-4)))
	lines := []string{
		"F1 / Ctrl-K      open help",
		"F2               toggle low-contrast theme",
		"Enter            send current prompt",
		"Shift-Enter      insert newline in prompt",
		"← / → / ↑ / ↓    move prompt cursor",
		"Alt-← / Alt-→    focus agent/tools pane",
		"Alt-↑ / Alt-↓    scroll focused pane",
		"Ctrl-F / /find   search current pane",
		"n / N            next / previous search hit",
		"PgUp / PgDn      scroll focused pane",
		"Mouse wheel      scroll hovered pane",
		"Drag divider     resize split panes",
		"Click scrollbar  page / drag scroll thumb",
		"Click + drag     select pane text",
		"Middle click     export hovered pane / selection",
		"/models          open model picker",
		"/sessions        restore / rename / delete session",
		"/save [name]     save session snapshot",
		"/restore [name]  restore session snapshot",
		"/toggle tools    show / hide tools pane",
		"/stats           show latest turn stats / Copilot quota",
		"/copy code       export latest code block",
		"/copy result     export latest tool result",
		"/expand          expand last truncated result",
		"/skills          list available skills",
		"/<skill>         activate a loaded skill",
		"/clear           clear panes (and history when available)",
		"/exit            leave live mode",
	}
	for i, line := range lines {
		if y0+2+i >= y0+boxH-2 {
			break
		}
		drawText(screen, x0+2, y0+2+i, textStyle, fitWidth(line, max(1, boxW-4)))
	}
	drawText(screen, x0+2, y0+boxH-2, dimStyle, fitWidth("Esc / Enter / ? closes this overlay", max(1, boxW-4)))
}

func (m *chatLiveModel) renderStatsOverlay(screen tcell.Screen) {
	boxW := min(76, max(46, m.width-10))
	boxH := 10
	if m.display.statsUsage.CopilotQuota != nil {
		boxH = 14
	}
	if m.display.liveCopilotQuota != nil || m.display.liveQuotaLoading || m.display.liveQuotaErr != "" {
		boxH = 20
	}
	x0 := (m.width - boxW) / 2
	y0 := (m.height - boxH) / 2
	backdrop := tcell.StyleDefault.Background(tcell.GetColor("#000000")).Foreground(tcell.GetColor("#000000"))
	for yy := max(0, y0-1); yy < min(m.height, y0+boxH+1); yy++ {
		for xx := max(0, x0-2); xx < min(m.width, x0+boxW+2); xx++ {
			screen.SetContent(xx, yy, ' ', nil, backdrop)
		}
	}
	boxStyle := tcell.StyleDefault.Background(tcell.GetColor("#161b22")).Foreground(tcell.GetColor("#58a6ff"))
	titleStyle := tcell.StyleDefault.Background(tcell.GetColor("#161b22")).Foreground(tcell.GetColor("#56d364")).Bold(true)
	textStyle := tcell.StyleDefault.Background(tcell.GetColor("#161b22")).Foreground(tcell.GetColor("#c9d1d9"))
	dimStyle := tcell.StyleDefault.Background(tcell.GetColor("#161b22")).Foreground(tcell.GetColor("#8b949e"))
	drawBox(screen, x0, y0, boxW, boxH, boxStyle)
	drawText(screen, x0+2, y0, titleStyle, fitWidth(" Latest turn stats ", max(1, boxW-4)))

	lines := []string{}
	if m.display.statsDuration > 0 {
		lines = append(lines, fmt.Sprintf("Duration: %.1fs", m.display.statsDuration.Seconds()))
	} else {
		lines = append(lines, "Duration: n/a")
	}
	lines = append(lines,
		fmt.Sprintf("Input tokens:  %d", m.display.statsUsage.InputTokens),
		fmt.Sprintf("Output tokens: %d", m.display.statsUsage.OutputTokens),
	)
	if q := m.display.statsUsage.CopilotQuota; q != nil {
		lines = append(lines, "")
		lines = append(lines, "Copilot premium (last turn):")
		if q.Unlimited {
			lines = append(lines, "  Unlimited")
		} else {
			if q.Remaining > 0 {
				lines = append(lines, fmt.Sprintf("  Remaining: %d", q.Remaining))
			} else if q.PercentRemaining > 0 {
				lines = append(lines, fmt.Sprintf("  Remaining: %.0f%%", q.PercentRemaining))
			}
			if q.Used > 0 || q.Included > 0 {
				lines = append(lines, fmt.Sprintf("  Used: %d/%d", q.Used, q.Included))
			}
			if q.ResetAt != "" {
				lines = append(lines, fmt.Sprintf("  Reset: %s", q.ResetAt))
			}
		}
	}
	if m.display.liveQuotaLoading || m.display.liveQuotaErr != "" || m.display.liveCopilotQuota != nil {
		lines = append(lines, "")
		lines = append(lines, "Copilot allowance (live):")
		if m.display.liveQuotaLoading {
			lines = append(lines, "  Loading…")
		} else if m.display.liveQuotaErr != "" {
			lines = append(lines, "  Error: "+m.display.liveQuotaErr)
		} else if live := m.display.liveCopilotQuota; live != nil {
			for _, name := range []string{"chat", "completions", "premium"} {
				if q, ok := live.Windows[name]; ok {
					lines = append(lines, "  "+name+": "+copilot.FormatQuota(q))
				}
			}
		}
	}
	for i, line := range lines {
		if y0+2+i >= y0+boxH-2 {
			break
		}
		drawText(screen, x0+2, y0+2+i, textStyle, fitWidth(line, max(1, boxW-4)))
	}
	drawText(screen, x0+2, y0+boxH-2, dimStyle, fitWidth("Esc / Enter / click closes this overlay", max(1, boxW-4)))
}

func (m *chatLiveModel) renderFilePicker(screen tcell.Screen) {
	x0, y0, boxW, boxH, visibleStart, visibleCount := m.filePickerLayout()
	backdrop := tcell.StyleDefault.Background(tcell.GetColor("#000000")).Foreground(tcell.GetColor("#000000"))
	for yy := max(0, y0-1); yy < min(m.height, y0+boxH+1); yy++ {
		for xx := max(0, x0-2); xx < min(m.width, x0+boxW+2); xx++ {
			screen.SetContent(xx, yy, ' ', nil, backdrop)
		}
	}
	boxStyle := tcell.StyleDefault.Background(tcell.GetColor("#161b22")).Foreground(tcell.GetColor("#58a6ff"))
	titleStyle := tcell.StyleDefault.Background(tcell.GetColor("#161b22")).Foreground(tcell.GetColor("#56d364")).Bold(true)
	textStyle := tcell.StyleDefault.Background(tcell.GetColor("#161b22")).Foreground(tcell.GetColor("#c9d1d9"))
	dimStyle := tcell.StyleDefault.Background(tcell.GetColor("#161b22")).Foreground(tcell.GetColor("#8b949e"))
	selectedStyle := tcell.StyleDefault.Background(tcell.GetColor("#1f6feb")).Foreground(tcell.GetColor("#ffffff")).Bold(true)
	drawBox(screen, x0, y0, boxW, boxH, boxStyle)
	drawText(screen, x0+2, y0, titleStyle, fitWidth(" Add context file (@...) ", max(1, boxW-4)))
	drawText(screen, x0+2, y0+1, dimStyle, fitWidth("Type to filter, Enter to insert, Esc to close", max(1, boxW-4)))
	drawText(screen, x0+2, y0+2, textStyle, fitWidth("Query: "+m.overlays.files.query, max(1, boxW-4)))
	for i := 0; i < visibleCount; i++ {
		idx := visibleStart + i
		if idx >= len(m.overlays.files.filtered) {
			break
		}
		style := textStyle
		prefix := "  "
		if idx == m.overlays.files.cursor {
			style = selectedStyle
			prefix = "› "
		}
		drawText(screen, x0+2, y0+3+i, style, fitWidth(prefix+m.overlays.files.filtered[idx], max(1, boxW-4)))
	}
	if len(m.overlays.files.filtered) == 0 {
		drawText(screen, x0+2, y0+4, dimStyle, fitWidth("No matching files", max(1, boxW-4)))
	}
}

func (m *chatLiveModel) renderSessionsPicker(screen tcell.Screen) {
	x0, y0, maxW, boxH, visibleStart, visibleCount := m.sessionsPickerLayout()
	backdrop := tcell.StyleDefault.Background(tcell.GetColor("#000000")).Foreground(tcell.GetColor("#000000"))
	for yy := max(0, y0-1); yy < min(m.height, y0+boxH+1); yy++ {
		for xx := max(0, x0-2); xx < min(m.width, x0+maxW+2); xx++ {
			screen.SetContent(xx, yy, ' ', nil, backdrop)
		}
	}
	boxStyle := tcell.StyleDefault.Background(tcell.GetColor("#161b22")).Foreground(tcell.GetColor("#58a6ff"))
	drawBox(screen, x0, y0, maxW, boxH, boxStyle)
	titleStyle := tcell.StyleDefault.Background(tcell.GetColor("#161b22")).Foreground(tcell.GetColor("#56d364")).Bold(true)
	textStyle := tcell.StyleDefault.Background(tcell.GetColor("#161b22")).Foreground(tcell.GetColor("#c9d1d9"))
	dimStyle := tcell.StyleDefault.Background(tcell.GetColor("#161b22")).Foreground(tcell.GetColor("#8b949e"))
	drawText(screen, x0+2, y0, titleStyle, fitWidth(" Sessions ", max(1, maxW-4)))
	for row := 0; row < visibleCount; row++ {
		idx := visibleStart + row
		if idx >= len(m.overlays.sessions.list) {
			break
		}
		entry := m.overlays.sessions.list[idx]
		prefix := fmt.Sprintf("%d.", idx+1)
		if idx > 8 {
			prefix = "•"
		}
		line := fmt.Sprintf(" %s %-28s %s ", prefix, entry.name, entry.modTime.Format("2006-01-02 15:04"))
		style := textStyle
		if idx == m.overlays.sessions.cursor {
			style = titleStyle
		}
		drawText(screen, x0+2, y0+2+row, style, fitWidth(line, max(1, maxW-4)))
	}
	drawText(screen, x0+2, y0+boxH-2, dimStyle, fitWidth("Enter restore • r rename • d delete • Esc close", max(1, maxW-4)))
	if m.overlays.sessions.rename.active {
		m.renderSessionRenameOverlay(screen)
	}
}

func (m *chatLiveModel) renderSessionRenameOverlay(screen tcell.Screen) {
	boxW := min(64, max(38, m.width-10))
	boxH := 5
	x0 := (m.width - boxW) / 2
	y0 := (m.height - boxH) / 2
	backdrop := tcell.StyleDefault.Background(tcell.GetColor("#000000")).Foreground(tcell.GetColor("#000000"))
	for yy := max(0, y0-1); yy < min(m.height, y0+boxH+1); yy++ {
		for xx := max(0, x0-2); xx < min(m.width, x0+boxW+2); xx++ {
			screen.SetContent(xx, yy, ' ', nil, backdrop)
		}
	}
	boxStyle := tcell.StyleDefault.Background(tcell.GetColor("#161b22")).Foreground(tcell.GetColor("#58a6ff"))
	drawBox(screen, x0, y0, boxW, boxH, boxStyle)
	titleStyle := tcell.StyleDefault.Background(tcell.GetColor("#161b22")).Foreground(tcell.GetColor("#56d364")).Bold(true)
	textStyle := tcell.StyleDefault.Background(tcell.GetColor("#161b22")).Foreground(tcell.GetColor("#c9d1d9"))
	drawText(screen, x0+2, y0, titleStyle, fitWidth(" Rename session ", max(1, boxW-4)))
	prompt := " name> "
	avail := max(1, boxW-4-stringWidth(prompt))
	visibleInput, cursorX := inputViewport(m.overlays.sessions.rename.buf, m.overlays.sessions.rename.pos, avail)
	drawText(screen, x0+2, y0+2, titleStyle, prompt)
	drawText(screen, x0+2+stringWidth(prompt), y0+2, textStyle, fitWidth(visibleInput, avail))
	screen.ShowCursor(x0+2+stringWidth(prompt)+cursorX, y0+2)
}

func (m *chatLiveModel) renderSearchOverlay(screen tcell.Screen) {
	boxW := min(72, max(42, m.width-10))
	boxH := 6
	x0 := (m.width - boxW) / 2
	y0 := (m.height - boxH) / 2
	backdrop := tcell.StyleDefault.Background(tcell.GetColor("#000000")).Foreground(tcell.GetColor("#000000"))
	for yy := max(0, y0-1); yy < min(m.height, y0+boxH+1); yy++ {
		for xx := max(0, x0-2); xx < min(m.width, x0+boxW+2); xx++ {
			screen.SetContent(xx, yy, ' ', nil, backdrop)
		}
	}
	boxStyle := tcell.StyleDefault.Background(tcell.GetColor("#161b22")).Foreground(tcell.GetColor("#58a6ff"))
	titleStyle := tcell.StyleDefault.Background(tcell.GetColor("#161b22")).Foreground(tcell.GetColor("#56d364")).Bold(true)
	textStyle := tcell.StyleDefault.Background(tcell.GetColor("#161b22")).Foreground(tcell.GetColor("#c9d1d9"))
	dimStyle := tcell.StyleDefault.Background(tcell.GetColor("#161b22")).Foreground(tcell.GetColor("#8b949e"))
	drawBox(screen, x0, y0, boxW, boxH, boxStyle)
	title := fmt.Sprintf(" Search %s pane ", m.overlays.search.pane)
	drawText(screen, x0+2, y0, titleStyle, fitWidth(title, max(1, boxW-4)))
	prompt := " find> "
	avail := max(1, boxW-4-stringWidth(prompt))
	visibleInput, cursorX := inputViewport(m.overlays.search.query, m.overlays.search.pos, avail)
	drawText(screen, x0+2, y0+2, titleStyle, prompt)
	drawText(screen, x0+2+stringWidth(prompt), y0+2, textStyle, fitWidth(visibleInput, avail))
	count := "no matches"
	if q := strings.TrimSpace(m.overlays.search.query); q == "" {
		count = "type to search"
	} else if len(m.overlays.search.matches) > 0 {
		count = fmt.Sprintf("%d/%d matches • n/N to navigate", max(1, m.overlays.search.current+1), len(m.overlays.search.matches))
	}
	drawText(screen, x0+2, y0+3, dimStyle, fitWidth(count, max(1, boxW-4)))
	drawText(screen, x0+2, y0+4, dimStyle, fitWidth("Enter apply • Esc cancel", max(1, boxW-4)))
	screen.ShowCursor(x0+2+stringWidth(prompt)+cursorX, y0+2)
}

func (m *chatLiveModel) renderModelPicker(screen tcell.Screen) {
	x0, y0, maxW, boxH, visibleStart, visibleCount := m.modelPickerLayout()
	backdrop := tcell.StyleDefault.Background(tcell.GetColor("#000000")).Foreground(tcell.GetColor("#000000"))
	for yy := max(0, y0-1); yy < min(m.height, y0+boxH+1); yy++ {
		for xx := max(0, x0-2); xx < min(m.width, x0+maxW+2); xx++ {
			screen.SetContent(xx, yy, ' ', nil, backdrop)
		}
	}
	boxStyle := tcell.StyleDefault.Background(tcell.GetColor("#161b22")).Foreground(tcell.GetColor("#58a6ff"))
	drawBox(screen, x0, y0, maxW, boxH, boxStyle)
	titleStyle := tcell.StyleDefault.Background(tcell.GetColor("#161b22")).Foreground(tcell.GetColor("#56d364")).Bold(true)
	textStyle := tcell.StyleDefault.Background(tcell.GetColor("#161b22")).Foreground(tcell.GetColor("#c9d1d9"))
	dimStyle := tcell.StyleDefault.Background(tcell.GetColor("#161b22")).Foreground(tcell.GetColor("#8b949e"))
	drawText(screen, x0+2, y0, titleStyle, fitWidth(" Model picker ", max(1, maxW-4)))
	for row := 0; row < visibleCount; row++ {
		idx := visibleStart + row
		if idx >= len(m.overlays.models.list) {
			break
		}
		name := m.overlays.models.list[idx]
		prefix := fmt.Sprintf("%d.", idx+1)
		if idx > 8 {
			prefix = "•"
		}
		line := fmt.Sprintf(" %s %s ", prefix, name)
		style := textStyle
		if idx == m.overlays.models.cursor {
			style = titleStyle
		}
		drawText(screen, x0+2, y0+2+row, style, fitWidth(line, max(1, maxW-4)))
	}
	drawText(screen, x0+2, y0+boxH-2, dimStyle, fitWidth("Enter select • 1-9 shortcut • Esc close", max(1, maxW-4)))
}
