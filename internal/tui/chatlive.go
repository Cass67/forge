package tui

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"forge/internal/agent/tools"
	"forge/internal/llm"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
	"github.com/gdamore/tcell/v2"
	"github.com/mattn/go-runewidth"
	"github.com/muesli/reflow/wordwrap"
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

type chatSessionSnapshot struct {
	SavedAt          time.Time `json:"saved_at"`
	Model            string    `json:"model"`
	WorkDir          string    `json:"work_dir"`
	AgentBuf         string    `json:"agent_buf"`
	ToolsBuf         string    `json:"tools_buf"`
	InputBuf         string    `json:"input_buf"`
	InputPos         int       `json:"input_pos"`
	AgentScrl        int       `json:"agent_scrl"`
	ToolsScrl        int       `json:"tools_scrl"`
	LeftPaneWidth    int       `json:"left_pane_width"`
	ToolsVisible     bool      `json:"tools_visible"`
	FocusRight       bool      `json:"focus_right"`
	AgentFollow      bool      `json:"agent_follow"`
	ToolsFollow      bool      `json:"tools_follow"`
	SearchQuery      string    `json:"search_query"`
	SearchPane       string    `json:"search_pane"`
	SearchCurrent    int       `json:"search_current"`
	SearchMatches    []int     `json:"search_matches"`
	SearchLineStarts []int     `json:"search_line_starts"`
	Turn             int       `json:"turn"`
}

type chatSessionEntry struct {
	name    string
	path    string
	modTime time.Time
}

type chatOverlayState struct {
	helpVisible bool
	search      chatSearchState
	models      chatModelPickerState
	sessions    chatSessionsState
}

type chatSearchState struct {
	visible    bool
	query      string
	pos        int
	pane       string
	matches    []int
	current    int
	lineStarts []int
}

type chatModelPickerState struct {
	visible bool
	list    []string
	cursor  int
}

type chatSessionsState struct {
	visible bool
	cursor  int
	list    []chatSessionEntry
	rename  chatRenameState
}

type chatRenameState struct {
	active bool
	buf    string
	pos    int
}

type chatPaneState struct {
	agent   chatPaneBufferState
	tools   chatPaneBufferState
	focusR  bool
	layout  chatPaneLayoutState
	selectn chatSelectionState
}

type chatPaneBufferState struct {
	buf    string
	scroll int
	follow bool
}

type chatPaneLayoutState struct {
	leftWidth    int
	toolsVisible bool
	dividerDrag  bool
	scrollDrag   chatScrollDragState
}

type chatScrollDragState struct {
	pane   string
	offset int
}

type chatSelectionState struct {
	pane   string
	start  int
	end    int
	active bool
	drag   bool
}

type chatDisplayState struct {
	flash          string
	lastExpandable string
	lastToolResult string
	lastCodeBlock  string
	timeline       []string
	turnStartedAt  time.Time
	spinnerFrame   int
	statsDuration  time.Duration
	statsUsage     llm.Usage
}

type chatLiveModel struct {
	model            string
	workDir          string
	width            int
	height           int
	turn             int
	panes            chatPaneState
	inputBuf         string
	inputPos         int
	busy             bool
	approval         *tools.Action
	status           string
	copyFn           func(string) error
	overlays         chatOverlayState
	display          chatDisplayState
	switchModelFn    func(string) (string, error)
	clearHistFn      func()
	themeLowContrast bool
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

	screen.EnableMouse()
	screen.Clear()
	w, h := screen.Size()

	m := chatLiveModel{
		model:   cfg.Model,
		workDir: cfg.WorkDir,
		width:   w,
		height:  h,
		status:  "ready",
		copyFn:  copyToClipboard,
		overlays: chatOverlayState{
			models: chatModelPickerState{list: cfg.AvailableModels},
		},
		panes: chatPaneState{
			agent:  chatPaneBufferState{follow: true},
			tools:  chatPaneBufferState{follow: true},
			layout: chatPaneLayoutState{toolsVisible: true},
		},
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

	spinnerTicker := time.NewTicker(120 * time.Millisecond)
	defer spinnerTicker.Stop()

	for {
		select {
		case kev, ok := <-keysCh:
			if !ok {
				m.autoSaveSession()
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
			case *tcell.EventMouse:
				m.handleMouse(msg)
			}

		case ev, ok := <-events:
			if !ok {
				m.autoSaveSession()
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
		case <-spinnerTicker.C:
			if m.busy {
				m.display.spinnerFrame = (m.display.spinnerFrame + 1) % 8
			} else {
				continue
			}
		}

		m.render(screen)
	}
}

func (m *chatLiveModel) handleKey(ev *tcell.EventKey, inputCh chan<- string) (ChatLiveResult, bool) {
	// Search overlay mode
	if m.overlays.search.visible {
		return m.handleSearchOverlayKey(ev), false
	}

	// Help overlay mode
	if m.overlays.helpVisible {
		return m.handleHelpOverlayKey(ev), false
	}

	// Model picker mode
	if m.overlays.models.visible {
		return m.handleModelPickerKey(ev), false
	}

	// Sessions picker mode
	if m.overlays.sessions.visible {
		return m.handleSessionsPickerKey(ev), false
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
			m.autoSaveSession()
			return ChatLiveResult{Aborted: true}, true
		}
		return ChatLiveResult{}, false
	}

	switch ev.Key() {
	case tcell.KeyEscape:
		m.autoSaveSession()
		return ChatLiveResult{Aborted: true}, true

	case tcell.KeyLeft:
		if !m.busy {
			m.panes.focusR = false
		}
	case tcell.KeyRight:
		if !m.busy {
			m.panes.focusR = true
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
		input := strings.TrimSpace(m.inputBuf)
		if input == "" {
			return ChatLiveResult{}, false
		}
		if input == "/exit" || input == "/quit" {
			m.autoSaveSession()
			return ChatLiveResult{Aborted: false, Input: input}, true
		}
		if m.busy {
			m.inputBuf = ""
			m.inputPos = 0
			m.display.flash = ""
			m.appendSteeringInput(input)
			select {
			case inputCh <- input:
			default:
			}
			return ChatLiveResult{}, false
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
		m.display.flash = ""
		m.display.lastExpandable = ""
		m.display.statsDuration = 0
		m.display.statsUsage = llm.Usage{}
		m.display.turnStartedAt = time.Now()
		m.appendTurnStart(input)
		m.busy = true
		m.status = "running"
		m.display.spinnerFrame = 0
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
	case tcell.KeyCtrlF:
		m.openSearchOverlay()
	case tcell.KeyCtrlK:
		m.overlays.helpVisible = true
	case tcell.KeyF1:
		m.overlays.helpVisible = true
	case tcell.KeyF2:
		m.themeLowContrast = !m.themeLowContrast
		if m.themeLowContrast {
			m.display.flash = "theme: low contrast"
		} else {
			m.display.flash = "theme: default"
		}

	case tcell.KeyRune:
		switch ev.Rune() {
		case 'n':
			if m.searchNext(1) {
				return ChatLiveResult{}, false
			}
		case 'N':
			if m.searchNext(-1) {
				return ChatLiveResult{}, false
			}
		}
		m.display.flash = "" // clear flash on typing
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

func (m *chatLiveModel) paneLines(content string, width, height, scroll int) []string {
	bodyLines := wrapPaneContent(content, width)
	if len(bodyLines) == 0 {
		bodyLines = []string{""}
	}
	scroll = clamp(scroll, 0, max(0, len(bodyLines)-height))
	end := min(len(bodyLines), scroll+height)
	lines := append([]string(nil), bodyLines[scroll:end]...)
	for len(lines) < height {
		lines = append(lines, "")
	}
	if len(lines) > height {
		lines = lines[:height]
	}
	return lines
}

func (m *chatLiveModel) bodyHeight() int {
	if m.height <= 8 {
		return max(1, m.height-4)
	}
	return m.height - 5
}

func (m *chatLiveModel) scrollFocused(delta int) {
	if m.panes.focusR {
		m.panes.tools.scroll = clamp(m.panes.tools.scroll+delta, 0, m.toolsMaxScroll())
		m.panes.tools.follow = m.panes.tools.scroll >= m.toolsMaxScroll()
	} else {
		m.panes.agent.scroll = clamp(m.panes.agent.scroll+delta, 0, m.agentMaxScroll())
		m.panes.agent.follow = m.panes.agent.scroll >= m.agentMaxScroll()
	}
}

func (m *chatLiveModel) agentMaxScroll() int {
	return max(0, totalWrappedLines(m.panes.agent.buf, m.leftContentWidth())-m.agentVisibleHeight())
}

func (m *chatLiveModel) toolsMaxScroll() int {
	return max(0, totalWrappedLines(m.panes.tools.buf, m.rightContentWidth())-m.toolsVisibleHeight())
}

func (m *chatLiveModel) modelPickerLayout() (x0, y0, maxW, boxH, visibleStart, visibleCount int) {
	maxW = 0
	for _, name := range m.overlays.models.list {
		if stringWidth(name)+8 > maxW {
			maxW = stringWidth(name) + 8
		}
	}
	if maxW > m.width-4 {
		maxW = m.width - 4
	}
	boxH = len(m.overlays.models.list) + 4
	if boxH > m.height-2 {
		boxH = m.height - 2
	}
	x0 = (m.width - maxW) / 2
	y0 = (m.height - boxH) / 2
	visibleCount = boxH - 4
	if visibleCount < 1 {
		visibleCount = 1
	}
	if m.overlays.models.cursor >= visibleStart+visibleCount {
		visibleStart = m.overlays.models.cursor - visibleCount + 1
	}
	if m.overlays.models.cursor < visibleStart {
		visibleStart = m.overlays.models.cursor
	}
	return
}

func (m *chatLiveModel) searchTarget() (pane string, content string, width int, visible int, scroll *int, follow *bool) {
	if m.overlays.search.pane == "right" && m.panes.layout.toolsVisible {
		return "right", m.panes.tools.buf, m.rightContentWidth(), m.toolsVisibleHeight(), &m.panes.tools.scroll, &m.panes.tools.follow
	}
	return "left", m.panes.agent.buf, m.leftContentWidth(), m.agentVisibleHeight(), &m.panes.agent.scroll, &m.panes.agent.follow
}

func (m *chatLiveModel) updateSearchMatches(jump bool) {
	_, content, width, visible, scroll, follow := m.searchTarget()
	m.overlays.search.matches = nil
	m.overlays.search.lineStarts = nil
	m.overlays.search.current = -1
	query := strings.ToLower(strings.TrimSpace(m.overlays.search.query))
	if query == "" {
		m.display.flash = "search cleared"
		return
	}
	lines := wrapPaneContent(content, width)
	for i, line := range lines {
		lower := strings.ToLower(line)
		start := 0
		foundInLine := false
		for {
			idx := strings.Index(lower[start:], query)
			if idx < 0 {
				break
			}
			idx += start
			m.overlays.search.matches = append(m.overlays.search.matches, i)
			m.overlays.search.lineStarts = append(m.overlays.search.lineStarts, idx)
			start = idx + len(query)
			foundInLine = true
		}
		if foundInLine && start == 0 {
			m.overlays.search.matches = append(m.overlays.search.matches, i)
			m.overlays.search.lineStarts = append(m.overlays.search.lineStarts, 0)
		}
	}
	if len(m.overlays.search.matches) == 0 {
		m.display.flash = fmt.Sprintf("no matches for %q", m.overlays.search.query)
		return
	}
	m.overlays.search.current = 0
	line := m.overlays.search.matches[m.overlays.search.current]
	*scroll = clamp(line-(visible/2), 0, max(0, len(lines)-visible))
	*follow = *scroll >= max(0, len(lines)-visible)
	m.display.flash = fmt.Sprintf("%d match(es) in %s pane", len(m.overlays.search.matches), map[bool]string{true: "tools", false: "agent"}[m.overlays.search.pane == "right"])
	if jump && len(m.overlays.search.matches) > 1 {
		m.searchNext(1)
	}
}

func (m *chatLiveModel) searchNext(delta int) bool {
	if len(m.overlays.search.matches) == 0 {
		return false
	}
	_, content, width, visible, scroll, follow := m.searchTarget()
	lines := wrapPaneContent(content, width)
	if m.overlays.search.current < 0 {
		m.overlays.search.current = 0
	} else {
		m.overlays.search.current = (m.overlays.search.current + delta + len(m.overlays.search.matches)) % len(m.overlays.search.matches)
	}
	line := m.overlays.search.matches[m.overlays.search.current]
	*scroll = clamp(line-(visible/2), 0, max(0, len(lines)-visible))
	*follow = *scroll >= max(0, len(lines)-visible)
	m.display.flash = fmt.Sprintf("match %d/%d for %q", m.overlays.search.current+1, len(m.overlays.search.matches), m.overlays.search.query)
	return true
}

func (m *chatLiveModel) sessionsPickerLayout() (x0, y0, maxW, boxH, visibleStart, visibleCount int) {
	maxW = 0
	for _, session := range m.overlays.sessions.list {
		label := session.name + "  " + formatSessionTimestamp(session.modTime)
		if stringWidth(label)+8 > maxW {
			maxW = stringWidth(label) + 8
		}
	}
	if maxW > m.width-4 {
		maxW = m.width - 4
	}
	boxH = len(m.overlays.sessions.list) + 4
	if boxH > m.height-2 {
		boxH = m.height - 2
	}
	x0 = (m.width - maxW) / 2
	y0 = (m.height - boxH) / 2
	visibleCount = boxH - 4
	if visibleCount < 1 {
		visibleCount = 1
	}
	if m.overlays.sessions.cursor >= visibleStart+visibleCount {
		visibleStart = m.overlays.sessions.cursor - visibleCount + 1
	}
	if m.overlays.sessions.cursor < visibleStart {
		visibleStart = m.overlays.sessions.cursor
	}
	return
}

// drawStyledAgentLine renders an agent pane line with colored " │ " accent border.
func drawStyledAgentLine(screen tcell.Screen, x, y int, text string, maxW int, bodyStyle, accentStyle tcell.Style, searchQuery string, highlight bool, matchStart int, isCurrent bool) {
	_ = accentStyle
	if highlight {
		drawHighlightedText(screen, x, y, text, maxW, bodyStyle, searchQuery, matchStart, isCurrent)
		return
	}
	drawText(screen, x, y, bodyStyle, fitWidth(text, maxW))
}

// drawStyledToolLine renders a tool pane line with appropriate colors for
// tool names, diff lines, status indicators, etc.
func drawStyledToolLine(screen tcell.Screen, x, y int, text string, maxW int, bodyStyle tcell.Style,
	colorBlue, colorPurple, colorOrange, colorCyan, colorGreen, colorRed tcell.Color,
	styleDiffAdd, styleDiffRm tcell.Style, searchQuery string, highlight bool, matchStart int, isCurrent bool) {

	trimmed := strings.TrimSpace(text)
	fitted := fitWidth(text, maxW)
	if highlight {
		base := bodyStyle
		switch {
		case strings.HasPrefix(trimmed, "✓"):
			base = tcell.StyleDefault.Foreground(colorGreen)
		case strings.HasPrefix(trimmed, "✗"):
			base = tcell.StyleDefault.Foreground(colorRed)
		case strings.HasPrefix(trimmed, "+"):
			base = styleDiffAdd
		case strings.HasPrefix(trimmed, "-"):
			base = styleDiffRm
		case strings.HasPrefix(trimmed, "@@"), strings.HasPrefix(trimmed, "..."):
			base = tcell.StyleDefault.Foreground(tcell.GetColor("#8b949e"))
		}
		drawHighlightedText(screen, x, y, text, maxW, base, searchQuery, matchStart, isCurrent)
		return
	}

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

func (m *chatLiveModel) searchHighlightForLine(lineIndex int) (matchStart int, isCurrent bool, ok bool) {
	for i, line := range m.overlays.search.matches {
		if line == lineIndex {
			start := 0
			if i < len(m.overlays.search.lineStarts) {
				start = m.overlays.search.lineStarts[i]
			}
			return start, i == m.overlays.search.current, true
		}
	}
	return 0, false, false
}

func drawHighlightedText(screen tcell.Screen, x, y int, text string, maxW int, base tcell.Style, query string, matchStart int, isCurrent bool) {
	fitted := fitWidth(text, maxW)
	query = strings.TrimSpace(query)
	if fitted == "" || query == "" {
		drawText(screen, x, y, base, fitted)
		return
	}
	lowerText := strings.ToLower(fitted)
	lowerQuery := strings.ToLower(query)
	idx := strings.Index(lowerText, lowerQuery)
	if matchStart >= 0 && matchStart < len(lowerText) {
		idx = strings.Index(lowerText[matchStart:], lowerQuery)
		if idx >= 0 {
			idx += matchStart
		}
	}
	if idx < 0 {
		drawText(screen, x, y, base, fitted)
		return
	}
	highlight := base.Background(tcell.GetColor("#3fb950")).Foreground(tcell.ColorBlack).Bold(true)
	if isCurrent {
		highlight = base.Background(tcell.GetColor("#e3b341")).Foreground(tcell.ColorBlack).Bold(true)
	}
	prefix := fitted[:idx]
	match := fitted[idx:min(len(fitted), idx+len(lowerQuery))]
	suffix := fitted[min(len(fitted), idx+len(lowerQuery)):]
	drawText(screen, x, y, base, prefix)
	xPos := x + stringWidth(prefix)
	for _, r := range match {
		screen.SetContent(xPos, y, r, nil, highlight)
		xPos += runewidth.RuneWidth(r)
	}
	drawText(screen, xPos, y, base, suffix)
}

var chromaStyle = styles.Get("github-dark")

func drawChromaCodeLine(screen tcell.Screen, x, y int, text string, maxW int, lang string, base tcell.Style) {
	text = fitWidth(text, maxW)
	if text == "" {
		return
	}
	lexer := chromaLexer(lang, text)
	if lexer == nil || chromaStyle == nil {
		drawText(screen, x, y, base, text)
		return
	}
	it, err := lexer.Tokenise(nil, text)
	if err != nil {
		drawText(screen, x, y, base, text)
		return
	}
	xPos := x
	for token := it(); token != chroma.EOF && xPos < x+maxW; token = it() {
		style := chromaTokenStyle(base, chromaStyle.Get(token.Type))
		for _, r := range token.Value {
			w := runewidth.RuneWidth(r)
			if w <= 0 {
				w = 1
			}
			if xPos+w > x+maxW {
				return
			}
			screen.SetContent(xPos, y, r, nil, style)
			xPos += w
		}
	}
}

func chromaLexer(lang, text string) chroma.Lexer {
	lang = strings.TrimSpace(strings.ToLower(lang))
	if lang != "" {
		if lexer := lexers.Get(lang); lexer != nil {
			return chroma.Coalesce(lexer)
		}
	}
	if lexer := lexers.Match("snippet." + lang); lexer != nil {
		return chroma.Coalesce(lexer)
	}
	if lexer := lexers.Analyse(text); lexer != nil {
		return chroma.Coalesce(lexer)
	}
	if lexer := lexers.Fallback; lexer != nil {
		return chroma.Coalesce(lexer)
	}
	return nil
}

func chromaTokenStyle(base tcell.Style, entry chroma.StyleEntry) tcell.Style {
	style := base
	if entry.Colour.IsSet() {
		style = style.Foreground(tcell.GetColor("#" + entry.Colour.String()))
	}
	if entry.Background.IsSet() {
		style = style.Background(tcell.GetColor("#" + entry.Background.String()))
	}
	if entry.Bold == chroma.Yes {
		style = style.Bold(true)
	}
	if entry.Italic == chroma.Yes {
		style = style.Italic(true)
	}
	if entry.Underline == chroma.Yes {
		style = style.Underline(true)
	}
	return style
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

func (m *chatLiveModel) leftPaneRect() (int, int, int, int) {
	bodyTop := 1
	bodyH := m.bodyHeight()
	if !m.panes.layout.toolsVisible {
		return 0, bodyTop, m.width, bodyH
	}
	leftW := m.panes.layout.leftWidth
	if leftW == 0 {
		leftW = ((m.width - 1) * 7) / 10
	}
	leftW = clamp(leftW, 20, max(20, m.width-23))
	return 0, bodyTop, leftW, bodyH
}

func (m *chatLiveModel) rightPaneRect() (int, int, int, int) {
	if !m.panes.layout.toolsVisible {
		return 0, 1, 0, m.bodyHeight()
	}
	leftX, bodyTop, leftW, bodyH := m.leftPaneRect()
	rightX := leftX + leftW + 1
	rightW := m.width - rightX
	return rightX, bodyTop, max(20, rightW), bodyH
}

func (m *chatLiveModel) inputRect() (int, int, int, int) {
	return 0, m.height - 3, m.width, 3
}

func (m *chatLiveModel) setLeftPaneWidth(x int) {
	m.panes.layout.leftWidth = clamp(x, 20, max(20, m.width-23))
}

func (m *chatLiveModel) leftContentWidth() int {
	_, _, w, _ := m.leftPaneRect()
	return max(1, w-3)
}

func (m *chatLiveModel) rightContentWidth() int {
	_, _, w, _ := m.rightPaneRect()
	return max(1, w-3)
}

func (m *chatLiveModel) agentVisibleHeight() int {
	_, _, _, h := m.leftPaneRect()
	return max(1, h-2)
}

func (m *chatLiveModel) toolsVisibleHeight() int {
	_, _, _, h := m.rightPaneRect()
	return max(1, h-2)
}

func (m *chatLiveModel) snapshot() chatSessionSnapshot {
	return chatSessionSnapshot{
		SavedAt:          time.Now(),
		Model:            m.model,
		WorkDir:          m.workDir,
		AgentBuf:         m.panes.agent.buf,
		ToolsBuf:         m.panes.tools.buf,
		InputBuf:         m.inputBuf,
		InputPos:         m.inputPos,
		AgentScrl:        m.panes.agent.scroll,
		ToolsScrl:        m.panes.tools.scroll,
		LeftPaneWidth:    m.panes.layout.leftWidth,
		ToolsVisible:     m.panes.layout.toolsVisible,
		FocusRight:       m.panes.focusR,
		AgentFollow:      m.panes.agent.follow,
		ToolsFollow:      m.panes.tools.follow,
		SearchQuery:      m.overlays.search.query,
		SearchPane:       m.overlays.search.pane,
		SearchCurrent:    m.overlays.search.current,
		SearchMatches:    append([]int(nil), m.overlays.search.matches...),
		SearchLineStarts: append([]int(nil), m.overlays.search.lineStarts...),
		Turn:             m.turn,
	}
}

func (m *chatLiveModel) applySnapshot(s chatSessionSnapshot) {
	m.model = s.Model
	m.workDir = s.WorkDir
	m.panes.agent.buf = s.AgentBuf
	m.panes.tools.buf = s.ToolsBuf
	m.inputBuf = s.InputBuf
	m.inputPos = clamp(s.InputPos, 0, utf8.RuneCountInString(s.InputBuf))
	m.panes.agent.scroll = max(0, s.AgentScrl)
	m.panes.tools.scroll = max(0, s.ToolsScrl)
	m.panes.layout.leftWidth = s.LeftPaneWidth
	m.panes.layout.toolsVisible = s.ToolsVisible
	m.panes.focusR = s.FocusRight && s.ToolsVisible
	m.panes.agent.follow = s.AgentFollow
	m.panes.tools.follow = s.ToolsFollow
	m.overlays.search.query = s.SearchQuery
	m.overlays.search.pane = s.SearchPane
	m.overlays.search.current = s.SearchCurrent
	m.overlays.search.matches = append([]int(nil), s.SearchMatches...)
	m.overlays.search.lineStarts = append([]int(nil), s.SearchLineStarts...)
	m.turn = s.Turn
}

func (m *chatLiveModel) saveSession(name string) error {
	path, err := chatSessionFile(name)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(m.snapshot(), "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func (m *chatLiveModel) restoreSession(name string) error {
	path, err := chatSessionFile(name)
	if err != nil {
		return err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var s chatSessionSnapshot
	if err := json.Unmarshal(data, &s); err != nil {
		return err
	}
	m.applySnapshot(s)
	return nil
}

func chatSessionsDir() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil || base == "" {
		home, homeErr := os.UserHomeDir()
		if homeErr != nil {
			if err != nil {
				return "", err
			}
			return "", homeErr
		}
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, "forge", "chat-sessions"), nil
}

func chatSessionFile(name string) (string, error) {
	dir, err := chatSessionsDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, sanitizeChatSessionName(name)+".json"), nil
}

func sanitizeChatSessionName(name string) string {
	name = strings.TrimSpace(name)
	name = strings.ReplaceAll(name, string(filepath.Separator), "-")
	name = strings.ReplaceAll(name, "/", "-")
	name = strings.ReplaceAll(name, "\\", "-")
	name = strings.ReplaceAll(name, " ", "-")
	return strings.Trim(name, "-.")
}

func defaultChatSessionName() (string, error) {
	return time.Now().Format("2006-01-02T15-04-05"), nil
}

func (m *chatLiveModel) beginSelectionFromMouse(pane string, x, y int) {
	idx, ok := m.paneIndexFromMouse(pane, x, y)
	if !ok {
		m.clearSelection()
		return
	}
	m.panes.selectn.pane = pane
	m.panes.selectn.start = idx
	m.panes.selectn.end = idx
	m.panes.selectn.active = true
	m.panes.selectn.drag = true
}

func (m *chatLiveModel) updateSelectionFromMouse(pane string, x, y int) {
	if !m.panes.selectn.active || m.panes.selectn.pane != pane {
		return
	}
	idx, ok := m.paneIndexFromMouse(pane, x, y)
	if !ok {
		return
	}
	m.panes.selectn.end = idx
}

func (m *chatLiveModel) clearSelection() {
	m.panes.selectn.pane = ""
	m.panes.selectn.start = 0
	m.panes.selectn.end = 0
	m.panes.selectn.active = false
	m.panes.selectn.drag = false
}

func (m *chatLiveModel) selectedText(pane string) string {
	if !m.panes.selectn.active || m.panes.selectn.pane != pane {
		return ""
	}
	content, width := m.selectionContentAndWidth(pane)
	if width <= 0 {
		return ""
	}
	wrapped := wrapPaneContent(content, width)
	if len(wrapped) == 0 {
		return ""
	}
	start, end := m.selectionOrderedRange()
	runes := []rune(strings.Join(wrapped, "\n"))
	if start < 0 || end < start || start >= len(runes) {
		return ""
	}
	if end >= len(runes) {
		end = len(runes) - 1
	}
	return string(runes[start : end+1])
}

func (m *chatLiveModel) lineHasSelection(pane string, lineIndex int, wrapped []string) bool {
	if !m.panes.selectn.active || m.panes.selectn.pane != pane || lineIndex < 0 || lineIndex >= len(wrapped) {
		return false
	}
	lineStarts := wrappedLineStarts(wrapped)
	lineStart := lineStarts[lineIndex]
	lineEnd := lineStart + len([]rune(wrapped[lineIndex])) - 1
	if lineIndex < len(wrapped)-1 {
		lineEnd++
	}
	start, end := m.selectionOrderedRange()
	return end >= lineStart && start <= lineEnd
}

func (m *chatLiveModel) selectionOrderedRange() (int, int) {
	if m.panes.selectn.start <= m.panes.selectn.end {
		return m.panes.selectn.start, m.panes.selectn.end
	}
	return m.panes.selectn.end, m.panes.selectn.start
}

func (m *chatLiveModel) selectionContentAndWidth(pane string) (string, int) {
	if pane == "right" {
		return m.panes.tools.buf, m.rightContentWidth()
	}
	return m.panes.agent.buf, m.leftContentWidth()
}

func (m *chatLiveModel) paneIndexFromMouse(pane string, x, y int) (int, bool) {
	var paneX, paneY, paneW, paneH, scroll int
	var width int
	var content string
	if pane == "right" {
		paneX, paneY, paneW, paneH = m.rightPaneRect()
		scroll = m.panes.tools.scroll
		width = m.rightContentWidth()
		content = m.panes.tools.buf
	} else {
		paneX, paneY, paneW, paneH = m.leftPaneRect()
		scroll = m.panes.agent.scroll
		width = m.leftContentWidth()
		content = m.panes.agent.buf
	}
	if x < paneX+1 || x >= paneX+paneW-1 || y <= paneY || y >= paneY+paneH-1 {
		return 0, false
	}
	wrapped := wrapPaneContent(content, width)
	if len(wrapped) == 0 {
		wrapped = []string{""}
	}
	lineIndex := clamp(scroll+(y-(paneY+1)), 0, len(wrapped)-1)
	line := wrapped[lineIndex]
	col := clamp(x-(paneX+1), 0, len([]rune(line)))
	lineStarts := wrappedLineStarts(wrapped)
	return lineStarts[lineIndex] + col, true
}

func wrappedLineStarts(lines []string) []int {
	starts := make([]int, len(lines))
	pos := 0
	for i, line := range lines {
		starts[i] = pos
		pos += len([]rune(line))
		if i < len(lines)-1 {
			pos++
		}
	}
	return starts
}

func (m *chatLiveModel) copyBufferToFile(kind, content string) error {
	copyFn := m.copyFn
	if copyFn == nil {
		copyFn = copyToClipboard
	}
	if err := copyFn(content); err == nil {
		return nil
	}
	dir, err := chatSessionsDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	name := fmt.Sprintf("export-%s-%s.txt", sanitizeChatSessionName(kind), time.Now().Format("2006-01-02T15-04-05"))
	return os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644)
}

func copyToClipboard(content string) error {
	commands := [][]string{
		{"pbcopy"},
		{"wl-copy"},
		{"xclip", "-selection", "clipboard"},
		{"xsel", "--clipboard", "--input"},
	}
	for _, args := range commands {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Stdin = strings.NewReader(content)
		if err := cmd.Run(); err == nil {
			return nil
		}
	}
	return fmt.Errorf("no clipboard command available")
}

func (m *chatLiveModel) pushTimeline(msg string) {
	msg = strings.TrimSpace(msg)
	if msg == "" {
		return
	}
	entry := fmt.Sprintf("%s %s", time.Now().Format("15:04:05"), msg)
	m.display.timeline = append(m.display.timeline, entry)
	if len(m.display.timeline) > 12 {
		m.display.timeline = m.display.timeline[len(m.display.timeline)-12:]
	}
}

func extractLastCodeBlock(content string) string {
	lines := strings.Split(content, "\n")
	var blocks []string
	inBlock := false
	var current []string
	for _, line := range lines {
		trimmed := strings.TrimSpace(strings.TrimPrefix(line, " │ "))
		if strings.HasPrefix(trimmed, "```") {
			if !inBlock {
				inBlock = true
				current = nil
			} else {
				blocks = append(blocks, strings.Join(current, "\n"))
				inBlock = false
				current = nil
			}
			continue
		}
		if inBlock {
			current = append(current, strings.TrimPrefix(line, " │ "))
		}
	}
	if len(blocks) == 0 {
		return ""
	}
	return blocks[len(blocks)-1]
}

func (m *chatLiveModel) autoSaveSession() {
	_ = m.saveSession("last-session")
}

func listChatSessions() ([]chatSessionEntry, error) {
	dir, err := chatSessionsDir()
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var sessions []chatSessionEntry
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		sessions = append(sessions, chatSessionEntry{
			name:    strings.TrimSuffix(entry.Name(), ".json"),
			path:    filepath.Join(dir, entry.Name()),
			modTime: info.ModTime(),
		})
	}
	sort.Slice(sessions, func(i, j int) bool {
		if sessions[i].modTime.Equal(sessions[j].modTime) {
			return sessions[i].name < sessions[j].name
		}
		return sessions[i].modTime.After(sessions[j].modTime)
	})
	return sessions, nil
}

func latestChatSessionName() (string, error) {
	sessions, err := listChatSessions()
	if err != nil {
		return "", err
	}
	if len(sessions) == 0 {
		return "", fmt.Errorf("no saved sessions")
	}
	return sessions[0].name, nil
}

func deleteChatSession(name string) error {
	path, err := chatSessionFile(name)
	if err != nil {
		return err
	}
	return os.Remove(path)
}

func renameChatSession(oldName, newName string) error {
	oldPath, err := chatSessionFile(oldName)
	if err != nil {
		return err
	}
	newPath, err := chatSessionFile(newName)
	if err != nil {
		return err
	}
	if _, err := os.Stat(newPath); err == nil {
		return fmt.Errorf("session already exists")
	} else if !os.IsNotExist(err) {
		return err
	}
	return os.Rename(oldPath, newPath)
}

func formatSessionTimestamp(t time.Time) string {
	if t.IsZero() {
		return "unknown time"
	}
	return t.Local().Format("2006-01-02 15:04:05")
}

func fillRect(screen tcell.Screen, x, y, w, h int, style tcell.Style) {
	for yy := y; yy < y+h; yy++ {
		for xx := x; xx < x+w; xx++ {
			screen.SetContent(xx, yy, ' ', nil, style)
		}
	}
}

func drawBox(screen tcell.Screen, x, y, w, h int, style tcell.Style) {
	if w <= 1 || h <= 1 {
		return
	}
	fillRect(screen, x, y, w, h, style)
	for xx := x + 1; xx < x+w-1; xx++ {
		screen.SetContent(xx, y, '─', nil, style)
		screen.SetContent(xx, y+h-1, '─', nil, style)
	}
	for yy := y + 1; yy < y+h-1; yy++ {
		screen.SetContent(x, yy, '│', nil, style)
		screen.SetContent(x+w-1, yy, '│', nil, style)
	}
	screen.SetContent(x, y, '┌', nil, style)
	screen.SetContent(x+w-1, y, '┐', nil, style)
	screen.SetContent(x, y+h-1, '└', nil, style)
	screen.SetContent(x+w-1, y+h-1, '┘', nil, style)
}

func drawRightText(screen tcell.Screen, x, y, width int, style tcell.Style, text string) {
	tw := stringWidth(text)
	if tw >= width {
		drawText(screen, x, y, style, fitWidth(text, width))
		return
	}
	drawText(screen, x+width-tw, y, style, text)
}

func scrollLabel(scroll, maxScroll int) string {
	if maxScroll <= 0 {
		return ""
	}
	pct := int(float64(scroll) / float64(maxScroll) * 100)
	if scroll >= maxScroll {
		pct = 100
	}
	return fmt.Sprintf("%3d%%", pct)
}

func scrollLabelWithFollow(scroll, maxScroll int, follow bool) string {
	base := scrollLabel(scroll, maxScroll)
	if follow {
		if base == "" {
			return "follow"
		}
		return base + " • follow"
	}
	return base
}

func totalWrappedLines(content string, width int) int {
	return len(wrapPaneContent(content, width))
}

func drawScrollbar(screen tcell.Screen, x, y, height, totalLines, visibleLines, scroll int, trackStyle, thumbStyle tcell.Style) {
	if height <= 0 {
		return
	}
	if height >= 1 {
		screen.SetContent(x, y, '▲', nil, trackStyle)
	}
	if height >= 2 {
		screen.SetContent(x, y+height-1, '▼', nil, trackStyle)
	}
	trackTop := y + 1
	trackH := max(0, height-2)
	for i := 0; i < trackH; i++ {
		screen.SetContent(x, trackTop+i, '│', nil, trackStyle)
	}
	if totalLines <= visibleLines || totalLines <= 0 || trackH <= 0 {
		return
	}
	thumbY, thumbH := scrollbarThumb(trackTop, trackH, totalLines, visibleLines, scroll)
	for i := 0; i < thumbH; i++ {
		screen.SetContent(x, thumbY+i, '█', nil, thumbStyle)
	}
}

func scrollbarThumb(trackTop, trackH, totalLines, visibleLines, scroll int) (int, int) {
	if trackH <= 0 || totalLines <= visibleLines || totalLines <= 0 {
		return trackTop, 0
	}
	thumbH := max(1, visibleLines*trackH/max(1, totalLines))
	if thumbH > trackH {
		thumbH = trackH
	}
	maxScroll := max(1, totalLines-visibleLines)
	thumbY := trackTop
	if trackH > thumbH {
		thumbY = trackTop + (scroll*(trackH-thumbH))/maxScroll
	}
	return thumbY, thumbH
}

func scrollbarScrollForClick(clickY, paneY, paneH, totalLines, visibleLines int) int {
	trackTop := paneY + 1
	trackH := max(1, paneH-2)
	if totalLines <= visibleLines {
		return 0
	}
	thumbY, thumbH := scrollbarThumb(trackTop, trackH, totalLines, visibleLines, 0)
	current := 0
	_ = thumbY
	_ = thumbH
	if clickY < trackTop {
		clickY = trackTop
	}
	if clickY >= trackTop+trackH {
		clickY = trackTop + trackH - 1
	}
	rel := clickY - trackTop
	maxScroll := max(0, totalLines-visibleLines)
	if trackH <= 1 || maxScroll == 0 {
		return 0
	}
	current = clamp((rel*maxScroll)/(trackH-1), 0, maxScroll)
	return clamp(current, 0, maxScroll)
}

func scrollbarScrollForDrag(mouseY, paneY, paneH, totalLines, visibleLines, dragOffset int) int {
	trackTop := paneY + 1
	trackH := max(1, paneH-2)
	if totalLines <= visibleLines {
		return 0
	}
	_, thumbH := scrollbarThumb(trackTop, trackH, totalLines, visibleLines, 0)
	thumbTop := mouseY - dragOffset
	if thumbTop < trackTop {
		thumbTop = trackTop
	}
	if thumbTop > trackTop+trackH-thumbH {
		thumbTop = trackTop + trackH - thumbH
	}
	maxScroll := max(0, totalLines-visibleLines)
	if trackH <= thumbH || maxScroll == 0 {
		return 0
	}
	rel := thumbTop - trackTop
	return clamp((rel*maxScroll)/(trackH-thumbH), 0, maxScroll)
}

func wrapPaneContent(s string, width int) []string {
	if width < 1 {
		return []string{""}
	}
	var out []string
	for _, line := range strings.Split(s, "\n") {
		if line == "" {
			out = append(out, "")
			continue
		}
		prefix, rest := linePrefix(line)
		firstWidth := max(1, width-stringWidth(prefix))
		wrapped := wordwrap.String(rest, firstWidth)
		parts := strings.Split(wrapped, "\n")
		for i, part := range parts {
			if i == 0 {
				out = append(out, prefix+part)
			} else {
				cont := strings.Repeat(" ", stringWidth(prefix))
				contWrapped := clipToWidth(cont+part, width)
				out = append(out, contWrapped)
			}
		}
	}
	if len(out) == 0 {
		return []string{""}
	}
	return out
}

func linePrefix(line string) (string, string) {
	for _, prefix := range []string{" │ ", "● ", "  ✓ ", "  ✗ ", "  ... ", "  +", "  -", "  @@"} {
		if strings.HasPrefix(line, prefix) {
			return prefix, strings.TrimPrefix(line, prefix)
		}
	}
	if strings.HasPrefix(line, "  ") {
		return "  ", strings.TrimPrefix(line, "  ")
	}
	return "", line
}

func inputViewport(input string, cursorPos, width int) (string, int) {
	runes := []rune(input)
	if cursorPos < 0 {
		cursorPos = 0
	}
	if cursorPos > len(runes) {
		cursorPos = len(runes)
	}
	start := 0
	for start < cursorPos {
		segment := string(runes[start:cursorPos])
		if stringWidth(segment) <= max(0, width-1) {
			break
		}
		start++
	}
	visible := string(runes[start:])
	visible = clipToWidth(visible, width)
	cursorX := stringWidth(string(runes[start:cursorPos]))
	if cursorX > width {
		cursorX = width
	}
	return visible, cursorX
}

func inputCursorFromScreenX(input string, currentPos, screenX, totalWidth int) int {
	prompt := " forge> "
	promptW := stringWidth(prompt)
	if screenX <= promptW {
		return 0
	}
	contentX := screenX - promptW
	visible, _ := inputViewport(input, currentPos, max(1, totalWidth-promptW))
	fullRunes := []rune(input)
	visibleRunes := []rune(visible)
	start := 0
	for start < len(fullRunes) {
		candidate := string(fullRunes[start:])
		candidate = clipToWidth(candidate, max(1, totalWidth-promptW))
		if candidate == visible {
			break
		}
		start++
	}
	acc := 0
	for i, r := range visibleRunes {
		rw := runewidth.RuneWidth(r)
		if contentX <= acc+rw/2 {
			return start + i
		}
		acc += rw
		if contentX < acc {
			return start + i + 1
		}
	}
	return start + len(visibleRunes)
}

func stringWidth(s string) int {
	return runewidth.StringWidth(s)
}

func clipToWidth(s string, width int) string {
	if width <= 0 {
		return ""
	}
	var b strings.Builder
	used := 0
	for len(s) > 0 {
		r, size := utf8.DecodeRuneInString(s)
		rw := runewidth.RuneWidth(r)
		if used+rw > width {
			break
		}
		b.WriteRune(r)
		used += rw
		s = s[size:]
	}
	return b.String()
}
