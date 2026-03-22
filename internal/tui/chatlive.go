package tui

import (
	"fmt"
	"strings"
	"time"

	"forge/internal/agent/tools"
	"forge/internal/llm"

	"github.com/gdamore/tcell/v2"
)

type ChatLiveConfig struct {
	Model           string
	WorkDir         string
	AvailableModels []string
	SwitchModel     func(name string) (newModel string, err error)
	ClearHistory    func()
	ApprovalCh      <-chan tools.Action
	ResponseCh      chan<- bool
}

type ChatLiveResult struct {
	Aborted bool
	Input   string
}

type chatLiveModel struct {
	model     string
	workDir   string
	width     int
	height    int
	turn      int
	agentBuf  string
	toolsBuf  string
	agentScrl int
	toolsScrl int
	focusR    bool
	inputBuf  string
	inputPos  int
	busy      bool
	approval  *tools.Action
	status    string
	// model picker overlay
	modelPicker   bool
	modelList     []string
	modelCursor   int
	switchModelFn func(string) (string, error)
	clearHistFn   func()
	// info/error flash message
	flash string
	// stats
	lastExpandable string
	statsDuration  time.Duration
	statsUsage     llm.Usage
}

// RunChatLive runs the split-pane live view for forge chat.
// It returns when the user types /exit or presses Escape, or sends input.
// The caller runs this in a loop: get input → run agent → repeat.
func RunChatLive(events <-chan llm.Event, cfg ChatLiveConfig, inputCh chan<- string, doneCh <-chan struct{}) ChatLiveResult {
	screen, err := tcell.NewScreen()
	if err != nil {
		return ChatLiveResult{Aborted: true}
	}
	if err := screen.Init(); err != nil {
		return ChatLiveResult{Aborted: true}
	}
	defer screen.Fini()

	screen.Clear()
	w, h := screen.Size()

	m := chatLiveModel{
		model:         cfg.Model,
		workDir:       cfg.WorkDir,
		width:         w,
		height:        h,
		status:        "ready",
		modelList:     cfg.AvailableModels,
		switchModelFn: cfg.SwitchModel,
		clearHistFn:   cfg.ClearHistory,
	}

	keysCh := make(chan tcell.Event, 32)
	go func() {
		for {
			ev := screen.PollEvent()
			if ev == nil {
				close(keysCh)
				return
			}
			keysCh <- ev
		}
	}()

	m.render(screen)

	for {
		select {
		case kev, ok := <-keysCh:
			if !ok {
				return ChatLiveResult{Aborted: true}
			}
			switch msg := kev.(type) {
			case *tcell.EventResize:
				screen.Sync()
				m.width, m.height = msg.Size()
			case *tcell.EventKey:
				result, done := m.handleKey(msg, inputCh)
				if done {
					return result
				}
			}

		case ev, ok := <-events:
			if !ok {
				return ChatLiveResult{Aborted: true}
			}
			m.handleEvent(ev)

		case action, ok := <-cfg.ApprovalCh:
			if ok {
				m.approval = &action
				m.status = "approve? [y/n]"
			}

		case <-doneCh:
			m.busy = false
			m.status = "ready"
			m.turn++
		}

		m.render(screen)
	}
}

func (m *chatLiveModel) handleKey(ev *tcell.EventKey, inputCh chan<- string) (ChatLiveResult, bool) {
	// Model picker mode
	if m.modelPicker {
		return m.handleModelPickerKey(ev), false
	}

	// Approval mode: y/n only
	if m.approval != nil {
		switch ev.Key() {
		case tcell.KeyRune:
			switch ev.Rune() {
			case 'y', 'Y':
				m.approval = nil
				m.status = "running"
				select {
				case inputCh <- "__approve_yes":
				default:
				}
			case 'n', 'N':
				m.approval = nil
				m.status = "running"
				select {
				case inputCh <- "__approve_no":
				default:
				}
			}
		case tcell.KeyEscape:
			return ChatLiveResult{Aborted: true}, true
		}
		return ChatLiveResult{}, false
	}

	switch ev.Key() {
	case tcell.KeyEscape:
		return ChatLiveResult{Aborted: true}, true

	case tcell.KeyLeft:
		if !m.busy {
			m.focusR = false
		}
	case tcell.KeyRight:
		if !m.busy {
			m.focusR = true
		}
	case tcell.KeyUp:
		m.scrollFocused(-1)
	case tcell.KeyDown:
		m.scrollFocused(1)
	case tcell.KeyPgUp:
		m.scrollFocused(-(m.bodyHeight() / 2))
	case tcell.KeyPgDn:
		m.scrollFocused(m.bodyHeight() / 2)

	case tcell.KeyEnter:
		if m.busy {
			return ChatLiveResult{}, false
		}
		input := strings.TrimSpace(m.inputBuf)
		if input == "" {
			return ChatLiveResult{}, false
		}
		if input == "/exit" || input == "/quit" {
			return ChatLiveResult{Aborted: false, Input: input}, true
		}
		// Handle slash commands locally
		if strings.HasPrefix(input, "/") {
			m.handleSlashCommand(input)
			m.inputBuf = ""
			m.inputPos = 0
			return ChatLiveResult{}, false
		}
		m.inputBuf = ""
		m.inputPos = 0
		m.flash = ""
		m.agentBuf = ""
		m.toolsBuf = ""
		m.agentScrl = 0
		m.toolsScrl = 0
		m.lastExpandable = ""
		m.statsDuration = 0
		m.statsUsage = llm.Usage{}
		m.busy = true
		m.status = "running"
		inputCh <- input
		return ChatLiveResult{}, false

	case tcell.KeyBackspace, tcell.KeyBackspace2:
		if m.inputPos > 0 {
			runes := []rune(m.inputBuf)
			m.inputBuf = string(runes[:m.inputPos-1]) + string(runes[m.inputPos:])
			m.inputPos--
		}

	case tcell.KeyDelete:
		runes := []rune(m.inputBuf)
		if m.inputPos < len(runes) {
			m.inputBuf = string(runes[:m.inputPos]) + string(runes[m.inputPos+1:])
		}

	case tcell.KeyCtrlA:
		m.inputPos = 0
	case tcell.KeyCtrlE:
		m.inputPos = len([]rune(m.inputBuf))
	case tcell.KeyCtrlU:
		m.inputBuf = ""
		m.inputPos = 0

	case tcell.KeyRune:
		m.flash = "" // clear flash on typing
		runes := []rune(m.inputBuf)
		newRunes := make([]rune, 0, len(runes)+1)
		newRunes = append(newRunes, runes[:m.inputPos]...)
		newRunes = append(newRunes, ev.Rune())
		newRunes = append(newRunes, runes[m.inputPos:]...)
		m.inputBuf = string(newRunes)
		m.inputPos++
	}

	return ChatLiveResult{}, false
}

func (m *chatLiveModel) handleSlashCommand(input string) {
	switch {
	case input == "/help":
		m.flash = "/model — pick  /models — list  /expand — show full  /clear — reset  /exit — quit"
	case input == "/models":
		m.openModelPicker()
	case input == "/model":
		m.openModelPicker()
	case strings.HasPrefix(input, "/model "):
		arg := strings.TrimSpace(strings.TrimPrefix(input, "/model "))
		if arg == "" {
			return
		}
		resolved := resolveModelName(m.modelList, arg)
		if resolved == "" {
			m.flash = fmt.Sprintf("unknown model %q — try /models", arg)
			return
		}
		if m.switchModelFn != nil {
			newModel, err := m.switchModelFn(resolved)
			if err != nil {
				m.flash = fmt.Sprintf("error: %v", err)
				return
			}
			m.model = newModel
			m.flash = fmt.Sprintf("switched to %s", newModel)
		}
	case input == "/expand":
		if m.lastExpandable != "" {
			m.toolsBuf += "\n" + m.lastExpandable + "\n"
			m.toolsScrl = m.toolsMaxScroll()
			m.lastExpandable = ""
			m.flash = "expanded"
		} else {
			m.flash = "nothing to expand"
		}
	case input == "/clear":
		if m.clearHistFn != nil {
			m.clearHistFn()
		}
		m.agentBuf = ""
		m.toolsBuf = ""
		m.agentScrl = 0
		m.toolsScrl = 0
		m.flash = "conversation cleared"
	default:
		m.flash = fmt.Sprintf("unknown command: %s (try /help)", input)
	}
}

func (m *chatLiveModel) openModelPicker() {
	m.modelPicker = true
	m.modelCursor = 0
	// Position cursor on current model
	for i, name := range m.modelList {
		if name == m.model {
			m.modelCursor = i
			break
		}
	}
}

func (m *chatLiveModel) handleModelPickerKey(ev *tcell.EventKey) ChatLiveResult {
	switch ev.Key() {
	case tcell.KeyEscape:
		m.modelPicker = false
	case tcell.KeyUp:
		if m.modelCursor > 0 {
			m.modelCursor--
		}
	case tcell.KeyDown:
		if m.modelCursor < len(m.modelList)-1 {
			m.modelCursor++
		}
	case tcell.KeyEnter:
		if m.modelCursor >= 0 && m.modelCursor < len(m.modelList) {
			picked := m.modelList[m.modelCursor]
			if m.switchModelFn != nil {
				newModel, err := m.switchModelFn(picked)
				if err != nil {
					m.flash = fmt.Sprintf("error: %v", err)
				} else {
					m.model = newModel
					m.flash = fmt.Sprintf("switched to %s", newModel)
				}
			}
		}
		m.modelPicker = false
	case tcell.KeyRune:
		// Number shortcuts 1-9
		r := ev.Rune()
		if r >= '1' && r <= '9' {
			idx := int(r - '1')
			if idx < len(m.modelList) {
				picked := m.modelList[idx]
				if m.switchModelFn != nil {
					newModel, err := m.switchModelFn(picked)
					if err != nil {
						m.flash = fmt.Sprintf("error: %v", err)
					} else {
						m.model = newModel
						m.flash = fmt.Sprintf("switched to %s", newModel)
					}
				}
				m.modelPicker = false
			}
		}
	}
	return ChatLiveResult{}
}

// resolveModelName resolves user input to a model name from the list
func resolveModelName(models []string, input string) string {
	var idx int
	if _, err := fmt.Sscanf(input, "%d", &idx); err == nil && idx >= 1 && idx <= len(models) {
		return models[idx-1]
	}
	for _, m := range models {
		if strings.EqualFold(m, input) {
			return m
		}
	}
	for _, m := range models {
		if strings.Contains(strings.ToLower(m), strings.ToLower(input)) {
			return m
		}
	}
	return ""
}

func (m *chatLiveModel) handleEvent(ev llm.Event) {
	switch ev.Kind {
	case llm.EventToken:
		// Auto-scroll only if already at bottom
		wasAtBottom := m.agentScrl >= m.agentMaxScroll()
		// Add accent border to agent text
		lines := strings.Split(ev.Text, "\n")
		for i, line := range lines {
			if i < len(lines)-1 {
				m.agentBuf += " │ " + line + "\n"
			} else if line != "" {
				m.agentBuf += " │ " + line
			}
		}
		if wasAtBottom {
			m.agentScrl = m.agentMaxScroll()
		}

	case llm.EventToolCall:
		wasAtBottom := m.toolsScrl >= m.toolsMaxScroll()
		m.toolsBuf += fmt.Sprintf("● %s  %s\n", ev.Agent, ev.Text)
		if wasAtBottom {
			m.toolsScrl = m.toolsMaxScroll()
		}

	case llm.EventToolResult:
		wasAtBottom := m.toolsScrl >= m.toolsMaxScroll()
		if ev.IsError {
			m.toolsBuf += fmt.Sprintf("  ✗ %s\n", ev.Text)
		} else {
			// Show diff if available
			if ev.Content != "" {
				diffLines := strings.Split(ev.Content, "\n")
				shown := 0
				for _, dl := range diffLines {
					if dl == "" {
						continue
					}
					if shown >= 10 {
						remaining := len(diffLines) - shown
						m.toolsBuf += fmt.Sprintf("  ... (%d more, /expand)\n", remaining)
						m.lastExpandable = ev.Content
						break
					}
					m.toolsBuf += fmt.Sprintf("  %s\n", dl)
					shown++
				}
			}
			m.toolsBuf += fmt.Sprintf("  ✓ %s\n", ev.Text)
		}
		if wasAtBottom {
			m.toolsScrl = m.toolsMaxScroll()
		}

	case llm.EventError:
		wasAtBottom := m.toolsScrl >= m.toolsMaxScroll()
		m.toolsBuf += fmt.Sprintf("  ✗ %s\n", ev.Text)
		if wasAtBottom {
			m.toolsScrl = m.toolsMaxScroll()
		}

	case llm.EventStats:
		m.statsDuration = ev.Duration
		m.statsUsage = ev.Usage

	case llm.EventDone:
		m.busy = false
		m.status = "ready"
	}
}

func (m *chatLiveModel) render(screen tcell.Screen) {
	w, h := screen.Size()
	m.width, m.height = w, h
	screen.Clear()

	colorBright := tcell.GetColor("#f0f6fc")
	colorMid := tcell.GetColor("#b1bac4")
	colorDim := tcell.GetColor("#8b949e")
	colorGreen := tcell.GetColor("#56d364")
	colorYellow := tcell.GetColor("#e3b341")

	styleBody := tcell.StyleDefault.Foreground(colorBright)
	styleStatus := tcell.StyleDefault.Foreground(colorMid)
	styleDivider := tcell.StyleDefault.Foreground(colorDim)
	styleTitleDim := tcell.StyleDefault.Foreground(colorDim)
	styleTitleFocus := tcell.StyleDefault.Foreground(colorGreen).Bold(true)
	styleInput := tcell.StyleDefault.Foreground(colorBright)
	stylePrompt := tcell.StyleDefault.Foreground(colorGreen).Bold(true)
	styleApproval := tcell.StyleDefault.Foreground(colorYellow).Bold(true)

	// Status line (row 0)
	statusText := fmt.Sprintf(" forge chat (%s) — %s  [%s]", m.model, shortPath(m.workDir), m.status)
	if m.turn > 0 {
		statusText = fmt.Sprintf(" forge chat (%s) — %s  turn %d  [%s]", m.model, shortPath(m.workDir), m.turn, m.status)
	}
	if m.statsDuration > 0 && !m.busy {
		stats := fmt.Sprintf("  ⏱ %.1fs", m.statsDuration.Seconds())
		if m.statsUsage.InputTokens > 0 {
			stats += fmt.Sprintf(" ↑%d ↓%d", m.statsUsage.InputTokens, m.statsUsage.OutputTokens)
		}
		statusText += stats
	}
	drawText(screen, 0, 0, styleStatus, fitWidth(statusText, w))

	// Body area
	bodyTop := 1
	bodyH := m.bodyHeight()
	leftW := (w - 1) / 2
	rightW := w - 1 - leftW

	leftStyle := styleTitleDim
	rightStyle := styleTitleDim
	if m.focusR {
		rightStyle = styleTitleFocus
	} else {
		leftStyle = styleTitleFocus
	}

	leftLines := m.paneLines("Agent", m.agentBuf, leftW, bodyH, m.agentScrl)
	rightLines := m.paneLines("Tools", m.toolsBuf, rightW, bodyH, m.toolsScrl)

	colorBlue := tcell.GetColor("#58a6ff")
	colorPurple := tcell.GetColor("#d2a8ff")
	colorOrange := tcell.GetColor("#f0883e")
	colorCyan := tcell.GetColor("#79c0ff")
	colorRed := tcell.GetColor("#f85149")
	styleDiffAdd := tcell.StyleDefault.Foreground(tcell.GetColor("#56d364")).Background(tcell.GetColor("#0f2d16"))
	styleDiffRm := tcell.StyleDefault.Foreground(colorRed).Background(tcell.GetColor("#3d1117"))
	styleAccent := tcell.StyleDefault.Foreground(colorBlue)

	for row := 0; row < bodyH; row++ {
		leftText := leftLines[row]
		if row == 0 {
			drawText(screen, 0, bodyTop+row, chooseStyle(row, leftStyle, styleBody), fitWidth(leftText, leftW))
		} else {
			// Apply accent border coloring to " │ " prefix
			drawStyledAgentLine(screen, 0, bodyTop+row, leftText, leftW, styleBody, styleAccent)
		}
		screen.SetContent(leftW, bodyTop+row, '│', nil, styleDivider)
		rightText := rightLines[row]
		if row == 0 {
			drawText(screen, leftW+1, bodyTop+row, chooseStyle(row, rightStyle, styleBody), fitWidth(rightText, rightW))
		} else {
			drawStyledToolLine(screen, leftW+1, bodyTop+row, rightText, rightW, styleBody,
				colorBlue, colorPurple, colorOrange, colorCyan, colorGreen, colorRed,
				styleDiffAdd, styleDiffRm)
		}
	}

	// Input area (bottom 2 rows: separator + input)
	inputRow := h - 1
	sepRow := h - 2

	// Separator
	for x := 0; x < w; x++ {
		screen.SetContent(x, sepRow, '─', nil, styleDivider)
	}

	// Approval or input
	if m.approval != nil {
		approvalText := fmt.Sprintf(" %s — approve? [y/n] ", m.approval.Summary)
		drawText(screen, 0, inputRow, styleApproval, fitWidth(approvalText, w))
	} else if m.busy {
		drawText(screen, 0, inputRow, styleStatus, fitWidth(" thinking...", w))
	} else {
		prompt := " forge> "
		drawText(screen, 0, inputRow, stylePrompt, prompt)
		drawText(screen, len(prompt), inputRow, styleInput, fitWidth(m.inputBuf, w-len(prompt)))
		screen.ShowCursor(len(prompt)+m.inputPos, inputRow)
	}

	// Flash message (show on separator line)
	if m.flash != "" {
		styleFlash := tcell.StyleDefault.Foreground(colorYellow)
		flashText := " " + m.flash + " "
		drawText(screen, 0, sepRow, styleFlash, fitWidth(flashText, w))
	}

	// Model picker overlay
	if m.modelPicker {
		m.renderModelPicker(screen)
	}

	screen.Show()
}

func (m *chatLiveModel) paneLines(title, content string, width, height, scroll int) []string {
	lines := make([]string, 0, height)
	lines = append(lines, " "+title)
	bodyH := height - 1
	bodyLines := wrapPlain(content, width)
	if len(bodyLines) == 0 {
		bodyLines = []string{""}
	}
	scroll = clamp(scroll, 0, max(0, len(bodyLines)-bodyH))
	end := min(len(bodyLines), scroll+bodyH)
	lines = append(lines, bodyLines[scroll:end]...)
	for len(lines) < height {
		lines = append(lines, "")
	}
	if len(lines) > height {
		lines = lines[:height]
	}
	return lines
}

func (m *chatLiveModel) bodyHeight() int {
	// status(1) + body + separator(1) + input(1)
	if m.height <= 4 {
		return 1
	}
	return m.height - 3
}

func (m *chatLiveModel) scrollFocused(delta int) {
	if m.focusR {
		m.toolsScrl = clamp(m.toolsScrl+delta, 0, m.toolsMaxScroll())
	} else {
		m.agentScrl = clamp(m.agentScrl+delta, 0, m.agentMaxScroll())
	}
}

func (m *chatLiveModel) agentMaxScroll() int {
	w := max(1, (m.width-1)/2)
	return max(0, len(wrapPlain(m.agentBuf, w))-(m.bodyHeight()-1))
}

func (m *chatLiveModel) toolsMaxScroll() int {
	w := m.width - 1 - ((m.width - 1) / 2)
	return max(0, len(wrapPlain(m.toolsBuf, max(1, w)))-(m.bodyHeight()-1))
}

func (m *chatLiveModel) renderModelPicker(screen tcell.Screen) {
	colorBg := tcell.GetColor("#161b22")
	colorBorder := tcell.GetColor("#30363d")
	colorBright := tcell.GetColor("#f0f6fc")
	colorGreen := tcell.GetColor("#56d364")
	colorDim := tcell.GetColor("#8b949e")

	styleBg := tcell.StyleDefault.Background(colorBg).Foreground(colorBright)
	styleBorder := tcell.StyleDefault.Background(colorBg).Foreground(colorBorder)
	styleCursor := tcell.StyleDefault.Background(colorGreen).Foreground(tcell.ColorBlack).Bold(true)
	styleCurrent := tcell.StyleDefault.Background(colorBg).Foreground(colorGreen)
	styleIdx := tcell.StyleDefault.Background(colorBg).Foreground(colorDim)

	// Calculate overlay dimensions
	maxW := 0
	for _, name := range m.modelList {
		if len(name)+8 > maxW {
			maxW = len(name) + 8
		}
	}
	if maxW > m.width-4 {
		maxW = m.width - 4
	}
	boxH := len(m.modelList) + 4 // title + blank + items + blank
	if boxH > m.height-2 {
		boxH = m.height - 2
	}

	// Center the box
	x0 := (m.width - maxW) / 2
	y0 := (m.height - boxH) / 2

	// Draw background
	for y := y0; y < y0+boxH; y++ {
		for x := x0; x < x0+maxW; x++ {
			screen.SetContent(x, y, ' ', nil, styleBg)
		}
	}

	// Draw border
	for x := x0; x < x0+maxW; x++ {
		screen.SetContent(x, y0, '─', nil, styleBorder)
		screen.SetContent(x, y0+boxH-1, '─', nil, styleBorder)
	}
	for y := y0; y < y0+boxH; y++ {
		screen.SetContent(x0, y, '│', nil, styleBorder)
		screen.SetContent(x0+maxW-1, y, '│', nil, styleBorder)
	}
	screen.SetContent(x0, y0, '┌', nil, styleBorder)
	screen.SetContent(x0+maxW-1, y0, '┐', nil, styleBorder)
	screen.SetContent(x0, y0+boxH-1, '└', nil, styleBorder)
	screen.SetContent(x0+maxW-1, y0+boxH-1, '┘', nil, styleBorder)

	// Title
	title := " Select Model "
	drawText(screen, x0+(maxW-len(title))/2, y0, styleBg.Bold(true), title)

	// Model list
	visibleStart := 0
	visibleCount := boxH - 4
	if visibleCount < 1 {
		visibleCount = 1
	}
	if m.modelCursor >= visibleStart+visibleCount {
		visibleStart = m.modelCursor - visibleCount + 1
	}
	if m.modelCursor < visibleStart {
		visibleStart = m.modelCursor
	}

	for i := 0; i < visibleCount && visibleStart+i < len(m.modelList); i++ {
		idx := visibleStart + i
		name := m.modelList[idx]
		row := y0 + 2 + i
		numStr := fmt.Sprintf("%d. ", idx+1)

		if idx == m.modelCursor {
			line := fmt.Sprintf(" ▸ %s%s", numStr, name)
			drawText(screen, x0+1, row, styleCursor, fitWidth(line, maxW-2))
		} else if name == m.model {
			line := fmt.Sprintf("   %s%s", numStr, name)
			drawText(screen, x0+1, row, styleCurrent, fitWidth(line, maxW-2))
		} else {
			drawText(screen, x0+1, row, styleIdx, fmt.Sprintf("   %s", numStr))
			drawText(screen, x0+1+3+len(numStr), row, styleBg, fitWidth(name, maxW-2-3-len(numStr)))
		}
	}
}

// drawStyledAgentLine renders an agent pane line with colored " │ " accent border.
func drawStyledAgentLine(screen tcell.Screen, x, y int, text string, maxW int, bodyStyle, accentStyle tcell.Style) {
	if strings.HasPrefix(text, " │ ") {
		drawText(screen, x, y, accentStyle, " │ ")
		rest := text[len(" │ "):]
		drawText(screen, x+3, y, bodyStyle, fitWidth(rest, maxW-3))
	} else {
		drawText(screen, x, y, bodyStyle, fitWidth(text, maxW))
	}
}

// drawStyledToolLine renders a tool pane line with appropriate colors for
// tool names, diff lines, status indicators, etc.
func drawStyledToolLine(screen tcell.Screen, x, y int, text string, maxW int, bodyStyle tcell.Style,
	colorBlue, colorPurple, colorOrange, colorCyan, colorGreen, colorRed tcell.Color,
	styleDiffAdd, styleDiffRm tcell.Style) {

	trimmed := strings.TrimSpace(text)
	fitted := fitWidth(text, maxW)

	switch {
	case strings.HasPrefix(trimmed, "● "):
		// Tool call line: "● tool_name  summary"
		parts := strings.SplitN(trimmed[len("● "):], "  ", 2)
		toolName := parts[0]
		toolColor := colorBlue
		switch toolName {
		case "edit_file":
			toolColor = colorPurple
		case "write_file":
			toolColor = colorOrange
		case "run_command":
			toolColor = colorCyan
		}
		style := tcell.StyleDefault.Foreground(toolColor).Bold(true)
		drawText(screen, x, y, style, fitWidth("● "+toolName, maxW))
		if len(parts) > 1 {
			offset := len("● ") + len(toolName) + 2
			dimStyle := tcell.StyleDefault.Foreground(tcell.GetColor("#8b949e"))
			drawText(screen, x+offset, y, dimStyle, fitWidth(parts[1], maxW-offset))
		}

	case strings.HasPrefix(trimmed, "✓"):
		style := tcell.StyleDefault.Foreground(colorGreen)
		drawText(screen, x, y, style, fitted)

	case strings.HasPrefix(trimmed, "✗"):
		style := tcell.StyleDefault.Foreground(colorRed)
		drawText(screen, x, y, style, fitted)

	case strings.HasPrefix(trimmed, "+"):
		drawText(screen, x, y, styleDiffAdd, fitted)

	case strings.HasPrefix(trimmed, "-"):
		drawText(screen, x, y, styleDiffRm, fitted)

	case strings.HasPrefix(trimmed, "@@"):
		dimStyle := tcell.StyleDefault.Foreground(tcell.GetColor("#8b949e"))
		drawText(screen, x, y, dimStyle, fitted)

	case strings.HasPrefix(trimmed, "..."):
		dimStyle := tcell.StyleDefault.Foreground(tcell.GetColor("#8b949e"))
		drawText(screen, x, y, dimStyle, fitted)

	default:
		drawText(screen, x, y, bodyStyle, fitted)
	}
}

func shortPath(path string) string {
	home := ""
	// try to shorten with ~
	if idx := strings.Index(path, "/Users/"); idx >= 0 {
		parts := strings.SplitN(path[idx:], "/", 4)
		if len(parts) >= 4 {
			return "~/" + parts[3]
		}
	}
	if home != "" && strings.HasPrefix(path, home) {
		return "~" + path[len(home):]
	}
	return path
}
