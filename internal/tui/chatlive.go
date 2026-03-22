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
	copyFn    func(string) error
	// model picker overlay
	modelPicker      bool
	modelList        []string
	modelCursor      int
	sessionsPicker   bool
	sessionsCursor   int
	sessionsList     []chatSessionEntry
	sessionRename    bool
	sessionRenameBuf string
	sessionRenamePos int
	switchModelFn    func(string) (string, error)
	clearHistFn      func()
	// info/error flash message
	flash string
	// stats
	lastExpandable   string
	statsDuration    time.Duration
	statsUsage       llm.Usage
	scrollDragPane   string
	scrollDragOff    int
	dividerDrag      bool
	leftPaneWidth    int
	toolsVisible     bool
	agentFollow      bool
	toolsFollow      bool
	helpOverlay      bool
	searchOverlay    bool
	searchQuery      string
	searchPos        int
	searchPane       string
	searchMatches    []int
	searchCurrent    int
	searchLineStarts []int
	turnStartedAt    time.Time
	spinnerFrame     int
	themeLowContrast bool
	lastToolResult   string
	lastCodeBlock    string
	timeline         []string
	selectionPane    string
	selectionStart   int
	selectionEnd     int
	selectionActive  bool
	selectionDrag    bool
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
		model:         cfg.Model,
		workDir:       cfg.WorkDir,
		width:         w,
		height:        h,
		status:        "ready",
		copyFn:        copyToClipboard,
		modelList:     cfg.AvailableModels,
		switchModelFn: cfg.SwitchModel,
		clearHistFn:   cfg.ClearHistory,
		toolsVisible:  true,
		agentFollow:   true,
		toolsFollow:   true,
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
				m.spinnerFrame = (m.spinnerFrame + 1) % 8
			} else {
				continue
			}
		}

		m.render(screen)
	}
}

func (m *chatLiveModel) handleKey(ev *tcell.EventKey, inputCh chan<- string) (ChatLiveResult, bool) {
	// Search overlay mode
	if m.searchOverlay {
		return m.handleSearchOverlayKey(ev), false
	}

	// Help overlay mode
	if m.helpOverlay {
		return m.handleHelpOverlayKey(ev), false
	}

	// Model picker mode
	if m.modelPicker {
		return m.handleModelPickerKey(ev), false
	}

	// Sessions picker mode
	if m.sessionsPicker {
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
			m.flash = ""
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
		m.flash = ""
		m.lastExpandable = ""
		m.statsDuration = 0
		m.statsUsage = llm.Usage{}
		m.turnStartedAt = time.Now()
		m.appendTurnStart(input)
		m.busy = true
		m.status = "running"
		m.spinnerFrame = 0
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
		m.helpOverlay = true
	case tcell.KeyF1:
		m.helpOverlay = true
	case tcell.KeyF2:
		m.themeLowContrast = !m.themeLowContrast
		if m.themeLowContrast {
			m.flash = "theme: low contrast"
		} else {
			m.flash = "theme: default"
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
		m.helpOverlay = true
		m.flash = "shortcuts help opened"
	case input == "/find":
		m.openSearchOverlay()
	case strings.HasPrefix(input, "/find "):
		m.openSearchOverlay()
		m.searchQuery = strings.TrimSpace(strings.TrimPrefix(input, "/find "))
		m.searchPos = len([]rune(m.searchQuery))
		m.updateSearchMatches(true)
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
	case input == "/toggle tools":
		m.toolsVisible = !m.toolsVisible
		if !m.toolsVisible {
			m.focusR = false
			m.flash = "tools pane hidden"
		} else {
			m.flash = "tools pane shown"
		}
	case input == "/toggle tools on":
		m.toolsVisible = true
		m.flash = "tools pane shown"
	case input == "/toggle tools off":
		m.toolsVisible = false
		m.focusR = false
		m.flash = "tools pane hidden"
	case input == "/theme":
		m.themeLowContrast = !m.themeLowContrast
		if m.themeLowContrast {
			m.flash = "theme: low contrast"
		} else {
			m.flash = "theme: default"
		}
	case input == "/theme low":
		m.themeLowContrast = true
		m.flash = "theme: low contrast"
	case input == "/theme default":
		m.themeLowContrast = false
		m.flash = "theme: default"
	case input == "/copy agent":
		if err := m.copyBufferToFile("agent", m.agentBuf); err != nil {
			m.flash = fmt.Sprintf("copy failed: %v", err)
		} else {
			m.flash = "agent pane exported"
		}
	case input == "/copy tools":
		if err := m.copyBufferToFile("tools", m.toolsBuf); err != nil {
			m.flash = fmt.Sprintf("copy failed: %v", err)
		} else {
			m.flash = "tools pane exported"
		}
	case input == "/copy code":
		if strings.TrimSpace(m.lastCodeBlock) == "" {
			m.flash = "copy failed: no code block yet"
		} else if err := m.copyBufferToFile("code", m.lastCodeBlock); err != nil {
			m.flash = fmt.Sprintf("copy failed: %v", err)
		} else {
			m.flash = "latest code block exported"
		}
	case input == "/copy result":
		if strings.TrimSpace(m.lastToolResult) == "" {
			m.flash = "copy failed: no tool result yet"
		} else if err := m.copyBufferToFile("result", m.lastToolResult); err != nil {
			m.flash = fmt.Sprintf("copy failed: %v", err)
		} else {
			m.flash = "latest tool result exported"
		}
	case input == "/save":
		name, err := defaultChatSessionName()
		if err != nil {
			m.flash = fmt.Sprintf("save failed: %v", err)
			return
		}
		if err := m.saveSession(name); err != nil {
			m.flash = fmt.Sprintf("save failed: %v", err)
			return
		}
		m.flash = fmt.Sprintf("session saved: %s", name)
	case strings.HasPrefix(input, "/save "):
		name := sanitizeChatSessionName(strings.TrimSpace(strings.TrimPrefix(input, "/save ")))
		if name == "" {
			m.flash = "save failed: missing session name"
			return
		}
		if err := m.saveSession(name); err != nil {
			m.flash = fmt.Sprintf("save failed: %v", err)
			return
		}
		m.flash = fmt.Sprintf("session saved: %s", name)
	case input == "/restore":
		name, err := latestChatSessionName()
		if err != nil {
			m.flash = fmt.Sprintf("restore failed: %v", err)
			return
		}
		if err := m.restoreSession(name); err != nil {
			m.flash = fmt.Sprintf("restore failed: %v", err)
			return
		}
		m.flash = fmt.Sprintf("session restored: %s", name)
	case strings.HasPrefix(input, "/restore "):
		name := sanitizeChatSessionName(strings.TrimSpace(strings.TrimPrefix(input, "/restore ")))
		if name == "" {
			m.flash = "restore failed: missing session name"
			return
		}
		if err := m.restoreSession(name); err != nil {
			m.flash = fmt.Sprintf("restore failed: %v", err)
			return
		}
		m.flash = fmt.Sprintf("session restored: %s", name)
	case input == "/sessions":
		m.openSessionsPicker()
	case input == "/clear", input == "/clear all":
		if m.clearHistFn != nil {
			m.clearHistFn()
		}
		m.agentBuf = ""
		m.toolsBuf = ""
		m.agentScrl = 0
		m.toolsScrl = 0
		m.searchMatches = nil
		m.searchCurrent = -1
		m.searchLineStarts = nil
		m.flash = "conversation cleared"
	case input == "/clear agent":
		m.agentBuf = ""
		m.agentScrl = 0
		if m.searchPane == "left" {
			m.searchMatches = nil
			m.searchCurrent = -1
			m.searchLineStarts = nil
		}
		m.flash = "agent pane cleared"
	case input == "/clear tools":
		m.toolsBuf = ""
		m.toolsScrl = 0
		if m.searchPane == "right" {
			m.searchMatches = nil
			m.searchCurrent = -1
			m.searchLineStarts = nil
		}
		m.flash = "tools pane cleared"
	default:
		m.flash = fmt.Sprintf("unknown command: %s (try /help)", input)
	}
}

func (m *chatLiveModel) appendSteeringInput(input string) {
	stamp := time.Now().Format("15:04:05")
	m.agentBuf += fmt.Sprintf("\nSteer • %s\n→ %s\n", stamp, input)
	if m.toolsBuf != "" && !strings.HasSuffix(m.toolsBuf, "\n\n") {
		m.toolsBuf += "\n"
	}
	m.toolsBuf += fmt.Sprintf("────────────────────────\n● steering\n  queued while busy • %s\n  → %s\n", stamp, input)
	m.pushTimeline(fmt.Sprintf("steer %s", input))
	m.agentFollow = true
	m.toolsFollow = true
	m.agentScrl = m.agentMaxScroll()
	m.toolsScrl = m.toolsMaxScroll()
	m.flash = "steering sent"
}

func (m *chatLiveModel) appendTurnStart(input string) {
	started := m.turnStartedAt
	if started.IsZero() {
		started = time.Now()
	}
	stamp := started.Format("15:04:05")
	sep := fmt.Sprintf("\n%s\n", strings.Repeat("─", 28))
	if strings.TrimSpace(m.agentBuf) == "" {
		sep = ""
	}
	m.agentBuf += fmt.Sprintf("%sYou • %s\n%s\n", sep, stamp, input)
	if strings.TrimSpace(m.toolsBuf) != "" {
		m.toolsBuf += fmt.Sprintf("\n%s\n", strings.Repeat("─", 28))
	}
	m.agentFollow = true
	m.toolsFollow = true
	m.agentScrl = m.agentMaxScroll()
	m.toolsScrl = m.toolsMaxScroll()
}

func (m *chatLiveModel) openSearchOverlay() {
	if m.focusR {
		m.searchPane = "right"
	} else {
		m.searchPane = "left"
	}
	m.searchOverlay = true
	m.searchPos = len([]rune(m.searchQuery))
	m.updateSearchMatches(false)
}

func (m *chatLiveModel) handleSearchOverlayKey(ev *tcell.EventKey) ChatLiveResult {
	switch ev.Key() {
	case tcell.KeyEscape:
		m.searchOverlay = false
	case tcell.KeyEnter:
		m.updateSearchMatches(true)
		m.searchOverlay = false
	case tcell.KeyLeft:
		if m.searchPos > 0 {
			m.searchPos--
		}
	case tcell.KeyRight:
		if m.searchPos < len([]rune(m.searchQuery)) {
			m.searchPos++
		}
	case tcell.KeyBackspace, tcell.KeyBackspace2:
		if m.searchPos > 0 {
			runes := []rune(m.searchQuery)
			m.searchQuery = string(runes[:m.searchPos-1]) + string(runes[m.searchPos:])
			m.searchPos--
			m.updateSearchMatches(false)
		}
	case tcell.KeyDelete:
		runes := []rune(m.searchQuery)
		if m.searchPos < len(runes) {
			m.searchQuery = string(runes[:m.searchPos]) + string(runes[m.searchPos+1:])
			m.updateSearchMatches(false)
		}
	case tcell.KeyCtrlA:
		m.searchPos = 0
	case tcell.KeyCtrlE:
		m.searchPos = len([]rune(m.searchQuery))
	case tcell.KeyCtrlU:
		m.searchQuery = ""
		m.searchPos = 0
		m.updateSearchMatches(false)
	case tcell.KeyRune:
		runes := []rune(m.searchQuery)
		newRunes := make([]rune, 0, len(runes)+1)
		newRunes = append(newRunes, runes[:m.searchPos]...)
		newRunes = append(newRunes, ev.Rune())
		newRunes = append(newRunes, runes[m.searchPos:]...)
		m.searchQuery = string(newRunes)
		m.searchPos++
		m.updateSearchMatches(false)
	}
	return ChatLiveResult{}
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

func (m *chatLiveModel) handleHelpOverlayKey(ev *tcell.EventKey) ChatLiveResult {
	switch ev.Key() {
	case tcell.KeyEscape, tcell.KeyEnter:
		m.helpOverlay = false
	case tcell.KeyRune:
		if ev.Rune() == '?' || ev.Rune() == 'q' || ev.Rune() == 'Q' {
			m.helpOverlay = false
		}
	}
	return ChatLiveResult{}
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
		m.pickModel(m.modelCursor)
	case tcell.KeyRune:
		// Number shortcuts 1-9
		r := ev.Rune()
		if r >= '1' && r <= '9' {
			idx := int(r - '1')
			if idx < len(m.modelList) {
				m.modelCursor = idx
				m.pickModel(idx)
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

func (m *chatLiveModel) openSessionsPicker() {
	sessions, err := listChatSessions()
	if err != nil {
		m.flash = fmt.Sprintf("sessions failed: %v", err)
		return
	}
	if len(sessions) == 0 {
		m.flash = "no saved sessions"
		return
	}
	m.sessionsList = sessions
	m.sessionsPicker = true
	m.sessionRename = false
	m.sessionsCursor = 0
	for i, session := range sessions {
		if session.name == "last-session" {
			m.sessionsCursor = i
			break
		}
	}
}

func (m *chatLiveModel) handleSessionsPickerKey(ev *tcell.EventKey) ChatLiveResult {
	if m.sessionRename {
		return m.handleSessionRenameKey(ev)
	}
	switch ev.Key() {
	case tcell.KeyEscape:
		m.sessionsPicker = false
	case tcell.KeyUp:
		if m.sessionsCursor > 0 {
			m.sessionsCursor--
		}
	case tcell.KeyDown:
		if m.sessionsCursor < len(m.sessionsList)-1 {
			m.sessionsCursor++
		}
	case tcell.KeyEnter:
		m.restorePickedSession(m.sessionsCursor)
	case tcell.KeyRune:
		r := ev.Rune()
		if r >= '1' && r <= '9' {
			idx := int(r - '1')
			if idx < len(m.sessionsList) {
				m.sessionsCursor = idx
				m.restorePickedSession(idx)
				return ChatLiveResult{}
			}
		}
		switch r {
		case 'd', 'D':
			m.deletePickedSession(m.sessionsCursor)
		case 'r', 'R':
			m.beginRenamePickedSession(m.sessionsCursor)
		}
	}
	return ChatLiveResult{}
}

func (m *chatLiveModel) restorePickedSession(idx int) {
	if idx < 0 || idx >= len(m.sessionsList) {
		return
	}
	name := m.sessionsList[idx].name
	if err := m.restoreSession(name); err != nil {
		m.flash = fmt.Sprintf("restore failed: %v", err)
		return
	}
	m.sessionsPicker = false
	m.flash = fmt.Sprintf("session restored: %s", name)
}

func (m *chatLiveModel) beginRenamePickedSession(idx int) {
	if idx < 0 || idx >= len(m.sessionsList) {
		return
	}
	name := m.sessionsList[idx].name
	if name == "last-session" {
		m.flash = "cannot rename last-session"
		return
	}
	m.sessionRename = true
	m.sessionRenameBuf = name
	m.sessionRenamePos = len([]rune(name))
}

func (m *chatLiveModel) handleSessionRenameKey(ev *tcell.EventKey) ChatLiveResult {
	switch ev.Key() {
	case tcell.KeyEscape:
		m.sessionRename = false
	case tcell.KeyEnter:
		m.commitRenamePickedSession()
	case tcell.KeyLeft:
		if m.sessionRenamePos > 0 {
			m.sessionRenamePos--
		}
	case tcell.KeyRight:
		if m.sessionRenamePos < len([]rune(m.sessionRenameBuf)) {
			m.sessionRenamePos++
		}
	case tcell.KeyBackspace, tcell.KeyBackspace2:
		if m.sessionRenamePos > 0 {
			runes := []rune(m.sessionRenameBuf)
			m.sessionRenameBuf = string(runes[:m.sessionRenamePos-1]) + string(runes[m.sessionRenamePos:])
			m.sessionRenamePos--
		}
	case tcell.KeyDelete:
		runes := []rune(m.sessionRenameBuf)
		if m.sessionRenamePos < len(runes) {
			m.sessionRenameBuf = string(runes[:m.sessionRenamePos]) + string(runes[m.sessionRenamePos+1:])
		}
	case tcell.KeyCtrlA:
		m.sessionRenamePos = 0
	case tcell.KeyCtrlE:
		m.sessionRenamePos = len([]rune(m.sessionRenameBuf))
	case tcell.KeyCtrlU:
		m.sessionRenameBuf = ""
		m.sessionRenamePos = 0
	case tcell.KeyRune:
		runes := []rune(m.sessionRenameBuf)
		newRunes := make([]rune, 0, len(runes)+1)
		newRunes = append(newRunes, runes[:m.sessionRenamePos]...)
		newRunes = append(newRunes, ev.Rune())
		newRunes = append(newRunes, runes[m.sessionRenamePos:]...)
		m.sessionRenameBuf = string(newRunes)
		m.sessionRenamePos++
	}
	return ChatLiveResult{}
}

func (m *chatLiveModel) commitRenamePickedSession() {
	if m.sessionsCursor < 0 || m.sessionsCursor >= len(m.sessionsList) {
		m.sessionRename = false
		return
	}
	oldName := m.sessionsList[m.sessionsCursor].name
	newName := sanitizeChatSessionName(m.sessionRenameBuf)
	if newName == "" {
		m.flash = "rename failed: missing session name"
		return
	}
	if oldName == newName {
		m.sessionRename = false
		return
	}
	if err := renameChatSession(oldName, newName); err != nil {
		m.flash = fmt.Sprintf("rename failed: %v", err)
		return
	}
	m.sessionRename = false
	m.flash = fmt.Sprintf("session renamed: %s → %s", oldName, newName)
	m.openSessionsPicker()
}

func (m *chatLiveModel) deletePickedSession(idx int) {
	if idx < 0 || idx >= len(m.sessionsList) {
		return
	}
	name := m.sessionsList[idx].name
	if name == "last-session" {
		m.flash = "cannot delete last-session"
		return
	}
	if err := deleteChatSession(name); err != nil {
		m.flash = fmt.Sprintf("delete failed: %v", err)
		return
	}
	m.flash = fmt.Sprintf("session deleted: %s", name)
	m.openSessionsPicker()
}

func (m *chatLiveModel) pickModel(idx int) {
	if idx < 0 || idx >= len(m.modelList) {
		return
	}
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

func (m *chatLiveModel) handleMouse(ev *tcell.EventMouse) {
	x, y := ev.Position()
	buttons := ev.Buttons()

	if buttons == tcell.ButtonNone {
		if m.scrollDragPane != "" {
			m.scrollDragPane = ""
			m.scrollDragOff = 0
		}
		if m.dividerDrag {
			m.dividerDrag = false
		}
		return
	}

	if m.helpOverlay {
		if buttons&tcell.Button1 != 0 {
			m.helpOverlay = false
		}
		return
	}

	if m.modelPicker {
		if buttons&tcell.WheelUp != 0 {
			if m.modelCursor > 0 {
				m.modelCursor--
			}
			return
		}
		if buttons&tcell.WheelDown != 0 {
			if m.modelCursor < len(m.modelList)-1 {
				m.modelCursor++
			}
			return
		}
		if buttons&tcell.Button1 != 0 {
			x0, y0, maxW, boxH, visibleStart, visibleCount := m.modelPickerLayout()
			if x >= x0+1 && x < x0+maxW-1 && y >= y0+2 && y < y0+2+visibleCount && y < y0+boxH-1 {
				idx := visibleStart + (y - (y0 + 2))
				if idx >= 0 && idx < len(m.modelList) {
					m.modelCursor = idx
					m.pickModel(idx)
				}
				return
			}
			m.modelPicker = false
		}
		return
	}

	if m.sessionsPicker {
		if buttons&tcell.WheelUp != 0 {
			if m.sessionsCursor > 0 {
				m.sessionsCursor--
			}
			return
		}
		if buttons&tcell.WheelDown != 0 {
			if m.sessionsCursor < len(m.sessionsList)-1 {
				m.sessionsCursor++
			}
			return
		}
		if buttons&tcell.Button1 != 0 {
			x0, y0, maxW, boxH, visibleStart, visibleCount := m.sessionsPickerLayout()
			if x >= x0+1 && x < x0+maxW-1 && y >= y0+2 && y < y0+2+visibleCount && y < y0+boxH-1 {
				idx := visibleStart + (y - (y0 + 2))
				if idx >= 0 && idx < len(m.sessionsList) {
					m.sessionsCursor = idx
					m.restorePickedSession(idx)
				}
				return
			}
			m.sessionsPicker = false
		}
		return
	}

	leftX, leftY, leftW, leftH := m.leftPaneRect()
	rightX, rightY, rightW, rightH := m.rightPaneRect()
	inputX, inputY, inputW, inputH := m.inputRect()
	leftScrollX := leftX + leftW - 2
	rightScrollX := rightX + rightW - 2
	dividerX := leftX + leftW
	inputLineY := inputY + inputH - 1
	if inputH >= 2 {
		inputLineY = inputY + 1
	}

	inLeft := x >= leftX && x < leftX+leftW && y >= leftY && y < leftY+leftH
	inRight := m.toolsVisible && x >= rightX && x < rightX+rightW && y >= rightY && y < rightY+rightH
	inLeftScroll := inLeft && x == leftScrollX && y > leftY && y < leftY+leftH-1
	inRightScroll := m.toolsVisible && inRight && x == rightScrollX && y > rightY && y < rightY+rightH-1
	inDivider := m.toolsVisible && x == dividerX && y >= leftY && y < leftY+leftH

	if buttons&tcell.WheelUp != 0 {
		switch {
		case inRight:
			m.focusR = true
			m.toolsScrl = clamp(m.toolsScrl-3, 0, m.toolsMaxScroll())
			m.toolsFollow = m.toolsScrl >= m.toolsMaxScroll()
		case inLeft:
			m.focusR = false
			m.agentScrl = clamp(m.agentScrl-3, 0, m.agentMaxScroll())
			m.agentFollow = m.agentScrl >= m.agentMaxScroll()
		default:
			m.scrollFocused(-3)
		}
		return
	}
	if buttons&tcell.WheelDown != 0 {
		switch {
		case inRight:
			m.focusR = true
			m.toolsScrl = clamp(m.toolsScrl+3, 0, m.toolsMaxScroll())
			m.toolsFollow = m.toolsScrl >= m.toolsMaxScroll()
		case inLeft:
			m.focusR = false
			m.agentScrl = clamp(m.agentScrl+3, 0, m.agentMaxScroll())
			m.agentFollow = m.agentScrl >= m.agentMaxScroll()
		default:
			m.scrollFocused(3)
		}
		return
	}

	if buttons&tcell.Button2 != 0 {
		switch {
		case inLeft:
			m.focusR = false
			content := m.selectedText("left")
			flash := "agent pane exported"
			if content == "" {
				content = m.agentBuf
			} else {
				flash = "agent selection copied"
			}
			if err := m.copyBufferToFile("agent", content); err != nil {
				m.flash = fmt.Sprintf("copy failed: %v", err)
			} else {
				m.flash = flash
			}
			return
		case inRight:
			m.focusR = true
			content := m.selectedText("right")
			flash := "tools pane exported"
			if content == "" {
				content = m.toolsBuf
			} else {
				flash = "tools selection copied"
			}
			if err := m.copyBufferToFile("tools", content); err != nil {
				m.flash = fmt.Sprintf("copy failed: %v", err)
			} else {
				m.flash = flash
			}
			return
		}
	}

	if buttons&tcell.Button1 != 0 {
		if m.selectionDrag {
			switch {
			case inLeft:
				m.focusR = false
				m.updateSelectionFromMouse("left", x, y)
				return
			case inRight:
				m.focusR = true
				m.updateSelectionFromMouse("right", x, y)
				return
			default:
				m.selectionDrag = false
			}
		}
		if m.dividerDrag {
			m.setLeftPaneWidth(x)
			return
		}
		if m.scrollDragPane != "" {
			switch m.scrollDragPane {
			case "left":
				m.focusR = false
				m.agentScrl = scrollbarScrollForDrag(y, leftY, leftH, totalWrappedLines(m.agentBuf, m.leftContentWidth()), m.agentVisibleHeight(), m.scrollDragOff)
				m.agentFollow = m.agentScrl >= m.agentMaxScroll()
			case "right":
				m.focusR = true
				m.toolsScrl = scrollbarScrollForDrag(y, rightY, rightH, totalWrappedLines(m.toolsBuf, m.rightContentWidth()), m.toolsVisibleHeight(), m.scrollDragOff)
				m.toolsFollow = m.toolsScrl >= m.toolsMaxScroll()
			}
			return
		}

		switch {
		case inLeftScroll:
			m.focusR = false
			total := totalWrappedLines(m.agentBuf, m.leftContentWidth())
			visible := m.agentVisibleHeight()
			thumbTop, thumbH := scrollbarThumb(leftY+2, max(1, visible-2), total, visible, m.agentScrl)
			switch {
			case y == leftY+1:
				m.agentScrl = clamp(m.agentScrl-1, 0, m.agentMaxScroll())
			case y == leftY+leftH-2:
				m.agentScrl = clamp(m.agentScrl+1, 0, m.agentMaxScroll())
			case y >= thumbTop && y < thumbTop+thumbH:
				m.scrollDragPane = "left"
				m.scrollDragOff = y - thumbTop
			case y < thumbTop:
				m.agentScrl = clamp(m.agentScrl-visible+1, 0, m.agentMaxScroll())
			default:
				m.agentScrl = clamp(m.agentScrl+visible-1, 0, m.agentMaxScroll())
			}
			m.agentFollow = m.agentScrl >= m.agentMaxScroll()
			return
		case inRightScroll:
			m.focusR = true
			total := totalWrappedLines(m.toolsBuf, m.rightContentWidth())
			visible := m.toolsVisibleHeight()
			thumbTop, thumbH := scrollbarThumb(rightY+2, max(1, visible-2), total, visible, m.toolsScrl)
			switch {
			case y == rightY+1:
				m.toolsScrl = clamp(m.toolsScrl-1, 0, m.toolsMaxScroll())
			case y == rightY+rightH-2:
				m.toolsScrl = clamp(m.toolsScrl+1, 0, m.toolsMaxScroll())
			case y >= thumbTop && y < thumbTop+thumbH:
				m.scrollDragPane = "right"
				m.scrollDragOff = y - thumbTop
			case y < thumbTop:
				m.toolsScrl = clamp(m.toolsScrl-visible+1, 0, m.toolsMaxScroll())
			default:
				m.toolsScrl = clamp(m.toolsScrl+visible-1, 0, m.toolsMaxScroll())
			}
			m.toolsFollow = m.toolsScrl >= m.toolsMaxScroll()
			return
		case inDivider:
			m.dividerDrag = true
			m.setLeftPaneWidth(x)
			return
		case inLeft:
			m.focusR = false
			m.beginSelectionFromMouse("left", x, y)
			return
		case inRight:
			m.focusR = true
			m.beginSelectionFromMouse("right", x, y)
			return
		case !m.busy && m.approval == nil && y == inputLineY && x >= inputX+1 && x < inputX+inputW-1:
			m.inputPos = inputCursorFromScreenX(m.inputBuf, m.inputPos, x-(inputX+1), max(1, inputW-2))
			return
		}
	}
}

func (m *chatLiveModel) handleEvent(ev llm.Event) {
	switch ev.Kind {
	case llm.EventToken:
		wasAtBottom := m.agentFollow || m.agentScrl >= m.agentMaxScroll()
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
		m.pushTimeline(fmt.Sprintf("tool %s", ev.Agent))
		wasAtBottom := m.toolsFollow || m.toolsScrl >= m.toolsMaxScroll()
		if m.toolsBuf != "" && !strings.HasSuffix(m.toolsBuf, "\n\n") {
			m.toolsBuf += "\n"
		}
		m.toolsBuf += fmt.Sprintf("────────────────────────\n")
		m.toolsBuf += fmt.Sprintf("● %s\n", ev.Agent)
		m.toolsBuf += fmt.Sprintf("  %s\n", ev.Text)
		if wasAtBottom {
			m.toolsScrl = m.toolsMaxScroll()
		}

	case llm.EventToolResult:
		if ev.IsError {
			m.pushTimeline("tool error")
		} else {
			m.pushTimeline("tool result")
		}
		wasAtBottom := m.toolsFollow || m.toolsScrl >= m.toolsMaxScroll()
		if ev.Content != "" {
			m.lastToolResult = ev.Content
		} else if ev.Text != "" {
			m.lastToolResult = ev.Text
		}
		if ev.IsError {
			m.toolsBuf += fmt.Sprintf("  status: ✗ %s\n", ev.Text)
		} else {
			if ev.Content != "" {
				diffLines := strings.Split(ev.Content, "\n")
				shown := 0
				m.toolsBuf += "  result:\n"
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
			m.toolsBuf += fmt.Sprintf("  status: ✓ %s\n", ev.Text)
		}
		if wasAtBottom {
			m.toolsScrl = m.toolsMaxScroll()
		}

	case llm.EventError:
		m.pushTimeline("error")
		wasAtBottom := m.toolsFollow || m.toolsScrl >= m.toolsMaxScroll()
		m.toolsBuf += fmt.Sprintf("  ✗ %s\n", ev.Text)
		if wasAtBottom {
			m.toolsScrl = m.toolsMaxScroll()
		}

	case llm.EventStats:
		m.statsDuration = ev.Duration
		m.statsUsage = ev.Usage

	case llm.EventDone:
		m.pushTimeline("done")
		m.busy = false
		m.status = "ready"
		finished := time.Now()
		stamp := finished.Format("15:04:05")
		if strings.TrimSpace(m.agentBuf) != "" {
			m.agentBuf += fmt.Sprintf("\nAgent complete • %s\n", stamp)
		}
		if strings.TrimSpace(m.toolsBuf) != "" {
			statusLine := fmt.Sprintf("status: complete • %s", stamp)
			if m.statsDuration > 0 {
				statusLine += fmt.Sprintf(" • %.1fs", m.statsDuration.Seconds())
			}
			m.toolsBuf += statusLine + "\n"
		}
	}
}

func (m *chatLiveModel) render(screen tcell.Screen) {
	w, h := screen.Size()
	m.width, m.height = w, h
	screen.Clear()

	colorBg := tcell.GetColor("#0d1117")
	colorPanel := tcell.GetColor("#161b22")
	colorBorder := tcell.GetColor("#30363d")
	colorBorderFocus := tcell.GetColor("#58a6ff")
	colorBright := tcell.GetColor("#f0f6fc")
	colorMid := tcell.GetColor("#b1bac4")
	colorDim := tcell.GetColor("#8b949e")
	colorGreen := tcell.GetColor("#56d364")
	colorYellow := tcell.GetColor("#e3b341")
	colorBlue := tcell.GetColor("#58a6ff")
	colorPurple := tcell.GetColor("#d2a8ff")
	colorOrange := tcell.GetColor("#f0883e")
	colorCyan := tcell.GetColor("#79c0ff")
	colorRed := tcell.GetColor("#f85149")
	if m.themeLowContrast {
		colorBg = tcell.GetColor("#11161c")
		colorPanel = tcell.GetColor("#1b2128")
		colorBorder = tcell.GetColor("#46515c")
		colorBorderFocus = tcell.GetColor("#7aa2c9")
		colorBright = tcell.GetColor("#d7dee5")
		colorMid = tcell.GetColor("#b7c0c9")
		colorDim = tcell.GetColor("#98a3ad")
		colorGreen = tcell.GetColor("#7fbf9a")
		colorYellow = tcell.GetColor("#c9b37a")
		colorBlue = tcell.GetColor("#7aa2c9")
		colorPurple = tcell.GetColor("#b3a1c9")
		colorOrange = tcell.GetColor("#c99b73")
		colorCyan = tcell.GetColor("#86b7c4")
		colorRed = tcell.GetColor("#c98585")
	}

	styleStatus := tcell.StyleDefault.Background(colorBg).Foreground(colorMid)
	styleBody := tcell.StyleDefault.Background(colorPanel).Foreground(colorBright)
	styleBodyDim := tcell.StyleDefault.Background(colorPanel).Foreground(colorDim)
	styleTitleDim := tcell.StyleDefault.Background(colorPanel).Foreground(colorDim)
	styleTitleFocus := tcell.StyleDefault.Background(colorPanel).Foreground(colorGreen).Bold(true)
	stylePrompt := tcell.StyleDefault.Background(colorPanel).Foreground(colorGreen).Bold(true)
	styleInput := tcell.StyleDefault.Background(colorPanel).Foreground(colorBright)
	styleApproval := tcell.StyleDefault.Background(colorPanel).Foreground(colorYellow).Bold(true)
	styleAccent := tcell.StyleDefault.Background(colorPanel).Foreground(colorBlue)
	styleDiffAdd := tcell.StyleDefault.Foreground(tcell.GetColor("#56d364")).Background(tcell.GetColor("#0f2d16"))
	styleDiffRm := tcell.StyleDefault.Foreground(colorRed).Background(tcell.GetColor("#3d1117"))

	fillRect(screen, 0, 0, w, h, tcell.StyleDefault.Background(colorBg))

	spinnerFrames := []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧"}
	themeLabel := "default"
	if m.themeLowContrast {
		themeLabel = "low"
	}
	headerLeft := fmt.Sprintf(" forge • %s • %s • theme:%s ", m.model, shortPath(m.workDir), themeLabel)
	headerRight := fmt.Sprintf(" %s ", strings.ToUpper(m.status))
	if m.busy {
		elapsed := time.Since(m.turnStartedAt)
		if m.turnStartedAt.IsZero() {
			elapsed = 0
		}
		headerRight = fmt.Sprintf(" %s %s  ⏱ %.1fs ", spinnerFrames[m.spinnerFrame%len(spinnerFrames)], strings.ToUpper(m.status), elapsed.Seconds())
	}
	if m.statsDuration > 0 && !m.busy {
		headerRight += fmt.Sprintf("  ⏱ %.1fs", m.statsDuration.Seconds())
		if m.statsUsage.InputTokens > 0 {
			headerRight += fmt.Sprintf("  ↑%d ↓%d", m.statsUsage.InputTokens, m.statsUsage.OutputTokens)
		}
	}
	drawText(screen, 0, 0, styleStatus, fitWidth(headerLeft, w))
	drawRightText(screen, 0, 0, w, styleStatus, headerRight)

	leftX, leftY, leftW, leftH := m.leftPaneRect()
	rightX, rightY, rightW, rightH := m.rightPaneRect()
	inputX, inputY, inputW, inputH := m.inputRect()

	leftBorder := colorBorder
	rightBorder := colorBorder
	if m.focusR {
		rightBorder = colorBorderFocus
	} else {
		leftBorder = colorBorderFocus
	}

	drawBox(screen, leftX, leftY, leftW, leftH, tcell.StyleDefault.Background(colorPanel).Foreground(leftBorder))
	if m.toolsVisible {
		drawBox(screen, rightX, rightY, rightW, rightH, tcell.StyleDefault.Background(colorPanel).Foreground(rightBorder))
	}
	drawBox(screen, inputX, inputY, inputW, inputH, tcell.StyleDefault.Background(colorPanel).Foreground(colorBorder))
	dividerStyle := tcell.StyleDefault.Background(colorBg).Foreground(colorBorder)
	if m.dividerDrag {
		dividerStyle = tcell.StyleDefault.Background(colorBg).Foreground(colorBorderFocus).Bold(true)
	}
	if m.toolsVisible {
		for yy := leftY; yy < leftY+leftH; yy++ {
			screen.SetContent(rightX-1, yy, '⋮', nil, dividerStyle)
		}
	}

	leftTitle := styleTitleDim
	rightTitle := styleTitleDim
	if m.focusR {
		rightTitle = styleTitleFocus
	} else {
		leftTitle = styleTitleFocus
	}

	leftBadge := " Agent "
	rightBadge := " Tools "
	inputBadge := " Steering "
	if m.searchPane == "left" && strings.TrimSpace(m.searchQuery) != "" {
		if len(m.searchMatches) > 0 {
			leftBadge = fmt.Sprintf(" Agent • %d/%d search ", max(1, m.searchCurrent+1), len(m.searchMatches))
		} else {
			leftBadge = " Agent • 0 search "
		}
	}
	if m.searchPane == "right" && strings.TrimSpace(m.searchQuery) != "" {
		if len(m.searchMatches) > 0 {
			rightBadge = fmt.Sprintf(" Tools • %d/%d search ", max(1, m.searchCurrent+1), len(m.searchMatches))
		} else {
			rightBadge = " Tools • 0 search "
		}
	}
	if m.agentFollow {
		leftBadge += "• follow "
	}
	if m.toolsFollow {
		rightBadge += "• follow "
	}
	drawText(screen, leftX+2, leftY, leftTitle, fitWidth(leftBadge, max(1, leftW-4)))
	if m.toolsVisible {
		drawText(screen, rightX+2, rightY, rightTitle, fitWidth(rightBadge, max(1, rightW-4)))
	}
	statusStrip := fmt.Sprintf(" status: %s ", m.status)
	if len(m.timeline) > 0 {
		statusStrip += " • " + strings.Join(m.timeline[max(0, len(m.timeline)-3):], "  •  ")
	}
	if m.busy {
		elapsed := time.Since(m.turnStartedAt)
		if m.turnStartedAt.IsZero() {
			elapsed = 0
		}
		statusStrip = fmt.Sprintf(" status: %s %s • running • %.1fs ", spinnerFrames[m.spinnerFrame%len(spinnerFrames)], m.status, elapsed.Seconds())
	}
	if m.approval != nil {
		statusStrip = " status: approval needed "
	}
	if inputY > 1 {
		drawText(screen, inputX+2, inputY-1, styleBodyDim, fitWidth(statusStrip, max(1, inputW-4)))
	}
	drawText(screen, inputX+2, inputY, styleBodyDim, fitWidth(inputBadge, max(1, inputW-4)))
	footerLegend := " F1 help • F2 theme • /copy code • /copy result • /sessions "
	if inputH > 2 {
		drawRightText(screen, inputX+1, inputY, inputW-2, styleBodyDim, footerLegend)
	}

	leftScroll := scrollLabelWithFollow(m.agentScrl, m.agentMaxScroll(), m.agentFollow)
	rightScroll := scrollLabelWithFollow(m.toolsScrl, m.toolsMaxScroll(), m.toolsFollow)
	drawRightText(screen, leftX+1, leftY, leftW-2, styleBodyDim, leftScroll)
	if m.toolsVisible {
		drawRightText(screen, rightX+1, rightY, rightW-2, styleBodyDim, rightScroll)
	}

	leftContentW := m.leftContentWidth()
	rightContentW := m.rightContentWidth()
	leftVisibleH := m.agentVisibleHeight()
	rightVisibleH := m.toolsVisibleHeight()

	leftLines := m.paneLines(m.agentBuf, leftContentW, leftVisibleH, m.agentScrl)
	rightLines := m.paneLines(m.toolsBuf, rightContentW, rightVisibleH, m.toolsScrl)
	leftWrapped := wrapPaneContent(m.agentBuf, leftContentW)
	rightWrapped := wrapPaneContent(m.toolsBuf, rightContentW)

	leftQuery := ""
	rightQuery := ""
	if m.searchPane == "left" {
		leftQuery = strings.TrimSpace(m.searchQuery)
	}
	if m.searchPane == "right" {
		rightQuery = strings.TrimSpace(m.searchQuery)
	}

	agentCodeStyle := tcell.StyleDefault.Background(tcell.GetColor("#0d1117")).Foreground(tcell.GetColor("#c9d1d9"))
	agentCodeBorderStyle := tcell.StyleDefault.Background(colorPanel).Foreground(tcell.GetColor("#30363d"))
	agentCodeHeaderStyle := tcell.StyleDefault.Background(tcell.GetColor("#0d1117")).Foreground(tcell.GetColor("#7ee787")).Bold(true)
	agentBubbleStyle := tcell.StyleDefault.Background(tcell.GetColor("#1a2332")).Foreground(colorBright)
	agentBubbleBorderStyle := tcell.StyleDefault.Background(colorPanel).Foreground(tcell.GetColor("#58a6ff"))
	agentBubbleDimStyle := tcell.StyleDefault.Background(tcell.GetColor("#1a2332")).Foreground(colorDim)
	inCodeBlock := false
	codeLang := ""
	for row := 0; row < leftVisibleH; row++ {
		y := leftY + 1 + row
		line := ""
		lineIndex := m.agentScrl + row
		if row < len(leftLines) {
			line = leftLines[row]
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
				drawChromaCodeLine(screen, leftX+3, y, codeText, max(1, leftContentW-3), codeLang, agentCodeStyle)
			}
			continue
		}
		fillRect(screen, leftX+1, y, leftContentW, 1, agentBubbleStyle)
		if m.lineHasSelection("left", m.agentScrl+row, leftWrapped) {
			fillRect(screen, leftX+1, y, leftContentW, 1, agentBubbleStyle.Background(tcell.GetColor("#2f81f7")).Foreground(tcell.ColorBlack))
		}
		content := strings.TrimPrefix(line, " │ ")
		trimmedContent := strings.TrimSpace(content)
		borderGlyph := "▎"
		textStyle := agentBubbleStyle
		if strings.HasPrefix(trimmedContent, "- ") || strings.HasPrefix(trimmedContent, "* ") {
			borderGlyph = "•"
			textStyle = agentBubbleDimStyle
		} else if strings.HasSuffix(trimmedContent, ":") && len(trimmedContent) < max(12, leftContentW/2) {
			borderGlyph = "▌"
		}
		drawText(screen, leftX+1, y, agentBubbleBorderStyle, borderGlyph)
		drawStyledAgentLine(screen, leftX+3, y, content, max(1, leftContentW-2), textStyle, styleAccent, leftQuery, hasMatch, matchStart, isCurrent)
	}
	if m.toolsVisible {
		for row := 0; row < rightVisibleH; row++ {
			y := rightY + 1 + row
			line := ""
			lineIndex := m.toolsScrl + row
			if row < len(rightLines) {
				line = rightLines[row]
			}
			matchStart, isCurrent, hasMatch := 0, false, false
			if rightQuery != "" {
				matchStart, isCurrent, hasMatch = m.searchHighlightForLine(lineIndex)
			}
			if m.lineHasSelection("right", m.toolsScrl+row, rightWrapped) {
				fillRect(screen, rightX+1, y, rightContentW, 1, styleBody.Background(tcell.GetColor("#2f81f7")).Foreground(tcell.ColorBlack))
			}
			drawStyledToolLine(screen, rightX+1, y, line, rightContentW, styleBody,
				colorBlue, colorPurple, colorOrange, colorCyan, colorGreen, colorRed,
				styleDiffAdd, styleDiffRm, rightQuery, hasMatch, matchStart, isCurrent)
		}
	}

	leftThumbStyle := styleTitleFocus
	rightThumbStyle := styleTitleFocus
	if m.scrollDragPane == "left" {
		leftThumbStyle = stylePrompt
	}
	if m.scrollDragPane == "right" {
		rightThumbStyle = stylePrompt
	}
	drawScrollbar(screen, leftX+leftW-2, leftY+1, leftVisibleH, totalWrappedLines(m.agentBuf, leftContentW), leftVisibleH, m.agentScrl, styleBodyDim, leftThumbStyle)
	if m.toolsVisible {
		drawScrollbar(screen, rightX+rightW-2, rightY+1, rightVisibleH, totalWrappedLines(m.toolsBuf, rightContentW), rightVisibleH, m.toolsScrl, styleBodyDim, rightThumbStyle)
	}

	if strings.TrimSpace(m.agentBuf) == "" {
		drawText(screen, leftX+2, leftY+2, styleBodyDim, fitWidth("Waiting for agent output…", max(1, leftContentW-1)))
	}
	if m.toolsVisible && strings.TrimSpace(m.toolsBuf) == "" {
		drawText(screen, rightX+2, rightY+2, styleBodyDim, fitWidth("Tool calls, diffs, and results appear here.", max(1, rightContentW-1)))
		drawText(screen, rightX+2, rightY+3, styleBodyDim, fitWidth("Use the scrollbar, wheel, or drag the divider to resize.", max(1, rightContentW-1)))
	}

	if m.flash != "" && inputH > 0 {
		styleFlash := tcell.StyleDefault.Background(colorPanel).Foreground(colorYellow)
		drawText(screen, inputX+2, inputY+1, styleFlash, fitWidth("! "+m.flash, max(1, inputW-4)))
	} else if inputH > 0 {
		steer := "Steer the agent: clarify constraints, ask for changes, /copy code, /copy result, F1 help"
		if m.busy {
			steer = "Busy mode queues steering in runtime: send corrections, constraints, next steps, or /clear"
		}
		drawText(screen, inputX+2, inputY+1, styleBodyDim, fitWidth(steer, max(1, inputW-4)))
	}

	inputLineY := inputY + inputH - 1
	if inputH >= 2 {
		inputLineY = inputY + 1
	}
	if m.approval != nil {
		approvalText := fmt.Sprintf(" %s — approve? [y/n] ", m.approval.Summary)
		drawText(screen, inputX+1, inputLineY, styleApproval, fitWidth(approvalText, max(1, inputW-2)))
		screen.HideCursor()
	} else {
		prompt := " steer> "
		if m.busy {
			prompt = " steer+> "
		}
		avail := max(1, inputW-2-stringWidth(prompt))
		visibleInput, cursorX := inputViewport(m.inputBuf, m.inputPos, avail)
		drawText(screen, inputX+1, inputLineY, stylePrompt, fitWidth(prompt, stringWidth(prompt)))
		drawText(screen, inputX+1+stringWidth(prompt), inputLineY, styleInput, fitWidth(visibleInput, avail))
		screen.ShowCursor(inputX+1+stringWidth(prompt)+cursorX, inputLineY)
	}

	if m.modelPicker {
		m.renderModelPicker(screen)
	}
	if m.sessionsPicker {
		m.renderSessionsPicker(screen)
	}
	if m.helpOverlay {
		m.renderHelpOverlay(screen)
	}
	if m.searchOverlay {
		m.renderSearchOverlay(screen)
	}

	screen.Show()
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
	if m.focusR {
		m.toolsScrl = clamp(m.toolsScrl+delta, 0, m.toolsMaxScroll())
		m.toolsFollow = m.toolsScrl >= m.toolsMaxScroll()
	} else {
		m.agentScrl = clamp(m.agentScrl+delta, 0, m.agentMaxScroll())
		m.agentFollow = m.agentScrl >= m.agentMaxScroll()
	}
}

func (m *chatLiveModel) agentMaxScroll() int {
	return max(0, totalWrappedLines(m.agentBuf, m.leftContentWidth())-m.agentVisibleHeight())
}

func (m *chatLiveModel) toolsMaxScroll() int {
	return max(0, totalWrappedLines(m.toolsBuf, m.rightContentWidth())-m.toolsVisibleHeight())
}

func (m *chatLiveModel) modelPickerLayout() (x0, y0, maxW, boxH, visibleStart, visibleCount int) {
	maxW = 0
	for _, name := range m.modelList {
		if stringWidth(name)+8 > maxW {
			maxW = stringWidth(name) + 8
		}
	}
	if maxW > m.width-4 {
		maxW = m.width - 4
	}
	boxH = len(m.modelList) + 4
	if boxH > m.height-2 {
		boxH = m.height - 2
	}
	x0 = (m.width - maxW) / 2
	y0 = (m.height - boxH) / 2
	visibleCount = boxH - 4
	if visibleCount < 1 {
		visibleCount = 1
	}
	if m.modelCursor >= visibleStart+visibleCount {
		visibleStart = m.modelCursor - visibleCount + 1
	}
	if m.modelCursor < visibleStart {
		visibleStart = m.modelCursor
	}
	return
}

func (m *chatLiveModel) renderHelpOverlay(screen tcell.Screen) {
	colorBg := tcell.GetColor("#161b22")
	colorBorder := tcell.GetColor("#58a6ff")
	colorBright := tcell.GetColor("#f0f6fc")
	colorDim := tcell.GetColor("#8b949e")
	colorGreen := tcell.GetColor("#56d364")
	colorYellow := tcell.GetColor("#e3b341")

	styleBg := tcell.StyleDefault.Background(colorBg).Foreground(colorBright)
	styleBorder := tcell.StyleDefault.Background(colorBg).Foreground(colorBorder)
	styleDim := tcell.StyleDefault.Background(colorBg).Foreground(colorDim)
	styleKey := tcell.StyleDefault.Background(colorBg).Foreground(colorGreen).Bold(true)
	styleWarn := tcell.StyleDefault.Background(colorBg).Foreground(colorYellow)

	lines := []string{
		"Navigation",
		"↑/↓ or wheel      scroll focused pane",
		"PgUp/PgDn         page scroll focused pane",
		"←/→ or click      focus Agent/Tools pane",
		"drag divider      resize panes",
		"drag scrollbar    scrub pane position",
		"",
		"Input",
		"Enter             send message",
		"Ctrl-A / Ctrl-E   move to line start/end",
		"Backspace/Delete  edit input",
		"Ctrl-U            clear input",
		"",
		"Commands",
		"/model, /models   open model picker",
		"/model <name>     switch directly",
		"/expand           expand truncated tool output",
		"/toggle tools     show/hide tools pane",
		"/save [name]      save chat session",
		"/restore [name]   restore saved session",
		"/sessions         browse and restore saved sessions",
		"/exit, /quit      leave chat",
		"",
		"Overlays",
		"F1 or Ctrl-K      toggle shortcuts help",
		"Esc               close overlay or exit chat",
		"",
		"Approval",
		"y / n             approve or deny tool action",
	}

	maxW := 58
	for _, line := range lines {
		if stringWidth(line)+4 > maxW {
			maxW = stringWidth(line) + 4
		}
	}
	if maxW > m.width-4 {
		maxW = m.width - 4
	}
	boxH := len(lines) + 4
	if boxH > m.height-2 {
		boxH = m.height - 2
	}
	x0 := (m.width - maxW) / 2
	y0 := (m.height - boxH) / 2

	for y := y0; y < y0+boxH; y++ {
		for x := x0; x < x0+maxW; x++ {
			screen.SetContent(x, y, ' ', nil, styleBg)
		}
	}
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

	drawText(screen, x0+2, y0, styleBorder.Bold(true), " Keyboard Shortcuts ")
	drawRightText(screen, x0+1, y0, maxW-2, styleDim, "press Esc to close")

	visibleLines := min(len(lines), boxH-4)
	for i := 0; i < visibleLines; i++ {
		row := y0 + 2 + i
		line := lines[i]
		if line == "" {
			continue
		}
		if !strings.Contains(line, "  ") {
			drawText(screen, x0+2, row, styleWarn.Bold(true), fitWidth(line, maxW-4))
			continue
		}
		parts := strings.Fields(line)
		keyPart := strings.Join(parts[:min(2, len(parts))], " ")
		descPart := strings.TrimSpace(strings.TrimPrefix(line, keyPart))
		drawText(screen, x0+2, row, styleKey, fitWidth(keyPart, 18))
		drawText(screen, x0+20, row, styleBg, fitWidth(descPart, maxW-22))
	}

	footer := " F1/Ctrl-K or /help • Enter/Esc/q close • click anywhere to dismiss "
	drawText(screen, x0+2, y0+boxH-1, styleDim, fitWidth(footer, maxW-4))
}

func (m *chatLiveModel) renderSessionsPicker(screen tcell.Screen) {
	colorBg := tcell.GetColor("#161b22")
	colorBorder := tcell.GetColor("#30363d")
	colorBright := tcell.GetColor("#f0f6fc")
	colorGreen := tcell.GetColor("#56d364")
	colorDim := tcell.GetColor("#8b949e")
	colorMuted := tcell.GetColor("#6e7681")

	styleBg := tcell.StyleDefault.Background(colorBg).Foreground(colorBright)
	styleBorder := tcell.StyleDefault.Background(colorBg).Foreground(colorBorder)
	styleCursor := tcell.StyleDefault.Background(colorGreen).Foreground(tcell.ColorBlack).Bold(true)
	styleIdx := tcell.StyleDefault.Background(colorBg).Foreground(colorDim)
	styleMuted := tcell.StyleDefault.Background(colorBg).Foreground(colorMuted)

	x0, y0, maxW, boxH, visibleStart, visibleCount := m.sessionsPickerLayout()

	for y := y0; y < y0+boxH; y++ {
		for x := x0; x < x0+maxW; x++ {
			screen.SetContent(x, y, ' ', nil, styleBg)
		}
	}
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

	drawText(screen, x0+2, y0, styleBorder.Bold(true), " Sessions ")
	drawRightText(screen, x0+1, y0, maxW-2, styleIdx, fmt.Sprintf("%d saved", len(m.sessionsList)))

	for i := 0; i < visibleCount && visibleStart+i < len(m.sessionsList); i++ {
		idx := visibleStart + i
		session := m.sessionsList[idx]
		row := y0 + 2 + i
		prefix := fmt.Sprintf("%d. ", idx+1)
		label := prefix + session.name
		timeLabel := formatSessionTimestamp(session.modTime)
		if idx == m.sessionsCursor {
			drawText(screen, x0+2, row, styleCursor, fitWidth(label, maxW-4))
			drawRightText(screen, x0+2, row, maxW-4, styleCursor, fitWidth(timeLabel, maxW-8))
		} else {
			drawText(screen, x0+2, row, styleIdx, fitWidth(prefix, 4))
			drawText(screen, x0+2+stringWidth(prefix), row, styleBg, fitWidth(session.name, maxW-4-stringWidth(prefix)-stringWidth(timeLabel)-1))
			drawRightText(screen, x0+2, row, maxW-4, styleMuted, timeLabel)
		}
	}

	footer := " Enter restore • r rename • d delete • Esc close "
	drawText(screen, x0+2, y0+boxH-1, styleIdx, fitWidth(footer, maxW-4))

	if m.sessionRename {
		m.renderSessionRenameOverlay(screen)
	}
}

func (m *chatLiveModel) renderSessionRenameOverlay(screen tcell.Screen) {
	colorBg := tcell.GetColor("#161b22")
	colorBorder := tcell.GetColor("#30363d")
	colorBright := tcell.GetColor("#f0f6fc")
	colorDim := tcell.GetColor("#8b949e")
	styleBorder := tcell.StyleDefault.Background(colorBg).Foreground(colorBorder)
	styleDim := tcell.StyleDefault.Background(colorBg).Foreground(colorDim)
	styleInput := tcell.StyleDefault.Background(colorBg).Foreground(colorBright).Bold(true)

	w := min(60, max(30, m.width-8))
	h := 5
	x0 := (m.width - w) / 2
	y0 := (m.height - h) / 2
	drawBox(screen, x0, y0, w, h, styleBorder)
	drawText(screen, x0+2, y0, styleBorder.Bold(true), " Rename Session ")
	drawText(screen, x0+2, y0+1, styleDim, fitWidth("Enter a new name for the selected session.", w-4))
	drawText(screen, x0+2, y0+2, styleInput, fitWidth(m.sessionRenameBuf, w-4))
	footer := " Enter save • Esc cancel "
	drawText(screen, x0+2, y0+h-1, styleDim, fitWidth(footer, w-4))

	cursorX := x0 + 2 + stringWidth(string([]rune(m.sessionRenameBuf)[:min(m.sessionRenamePos, len([]rune(m.sessionRenameBuf)))]))
	if cursorX >= x0+w-1 {
		cursorX = x0 + w - 2
	}
	screen.ShowCursor(cursorX, y0+2)
}

func (m *chatLiveModel) searchTarget() (pane string, content string, width int, visible int, scroll *int, follow *bool) {
	if m.searchPane == "right" && m.toolsVisible {
		return "right", m.toolsBuf, m.rightContentWidth(), m.toolsVisibleHeight(), &m.toolsScrl, &m.toolsFollow
	}
	return "left", m.agentBuf, m.leftContentWidth(), m.agentVisibleHeight(), &m.agentScrl, &m.agentFollow
}

func (m *chatLiveModel) updateSearchMatches(jump bool) {
	_, content, width, visible, scroll, follow := m.searchTarget()
	m.searchMatches = nil
	m.searchLineStarts = nil
	m.searchCurrent = -1
	query := strings.ToLower(strings.TrimSpace(m.searchQuery))
	if query == "" {
		m.flash = "search cleared"
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
			m.searchMatches = append(m.searchMatches, i)
			m.searchLineStarts = append(m.searchLineStarts, idx)
			start = idx + len(query)
			foundInLine = true
		}
		if foundInLine && start == 0 {
			m.searchMatches = append(m.searchMatches, i)
			m.searchLineStarts = append(m.searchLineStarts, 0)
		}
	}
	if len(m.searchMatches) == 0 {
		m.flash = fmt.Sprintf("no matches for %q", m.searchQuery)
		return
	}
	m.searchCurrent = 0
	line := m.searchMatches[m.searchCurrent]
	*scroll = clamp(line-(visible/2), 0, max(0, len(lines)-visible))
	*follow = *scroll >= max(0, len(lines)-visible)
	m.flash = fmt.Sprintf("%d match(es) in %s pane", len(m.searchMatches), map[bool]string{true: "tools", false: "agent"}[m.searchPane == "right"])
	if jump && len(m.searchMatches) > 1 {
		m.searchNext(1)
	}
}

func (m *chatLiveModel) searchNext(delta int) bool {
	if len(m.searchMatches) == 0 {
		return false
	}
	_, content, width, visible, scroll, follow := m.searchTarget()
	lines := wrapPaneContent(content, width)
	if m.searchCurrent < 0 {
		m.searchCurrent = 0
	} else {
		m.searchCurrent = (m.searchCurrent + delta + len(m.searchMatches)) % len(m.searchMatches)
	}
	line := m.searchMatches[m.searchCurrent]
	*scroll = clamp(line-(visible/2), 0, max(0, len(lines)-visible))
	*follow = *scroll >= max(0, len(lines)-visible)
	m.flash = fmt.Sprintf("match %d/%d for %q", m.searchCurrent+1, len(m.searchMatches), m.searchQuery)
	return true
}

func (m *chatLiveModel) renderSearchOverlay(screen tcell.Screen) {
	colorBg := tcell.GetColor("#161b22")
	colorBorder := tcell.GetColor("#56d364")
	colorBright := tcell.GetColor("#f0f6fc")
	colorDim := tcell.GetColor("#8b949e")

	styleBg := tcell.StyleDefault.Background(colorBg).Foreground(colorBright)
	styleBorder := tcell.StyleDefault.Background(colorBg).Foreground(colorBorder)
	styleDim := tcell.StyleDefault.Background(colorBg).Foreground(colorDim)
	styleInput := tcell.StyleDefault.Background(colorBg).Foreground(colorBright)

	boxW := min(max(40, m.width*2/3), max(20, m.width-4))
	boxH := 5
	x0 := (m.width - boxW) / 2
	y0 := (m.height - boxH) / 2

	for y := y0; y < y0+boxH; y++ {
		for x := x0; x < x0+boxW; x++ {
			screen.SetContent(x, y, ' ', nil, styleBg)
		}
	}
	for x := x0; x < x0+boxW; x++ {
		screen.SetContent(x, y0, '─', nil, styleBorder)
		screen.SetContent(x, y0+boxH-1, '─', nil, styleBorder)
	}
	for y := y0; y < y0+boxH; y++ {
		screen.SetContent(x0, y, '│', nil, styleBorder)
		screen.SetContent(x0+boxW-1, y, '│', nil, styleBorder)
	}
	screen.SetContent(x0, y0, '┌', nil, styleBorder)
	screen.SetContent(x0+boxW-1, y0, '┐', nil, styleBorder)
	screen.SetContent(x0, y0+boxH-1, '└', nil, styleBorder)
	screen.SetContent(x0+boxW-1, y0+boxH-1, '┘', nil, styleBorder)

	paneLabel := "Agent"
	if m.searchPane == "right" {
		paneLabel = "Tools"
	}
	drawText(screen, x0+2, y0, styleBorder.Bold(true), fmt.Sprintf(" Search %s ", paneLabel))
	status := "no query"
	if len(m.searchMatches) > 0 {
		current := max(1, m.searchCurrent+1)
		status = fmt.Sprintf("%d/%d", current, len(m.searchMatches))
	} else if strings.TrimSpace(m.searchQuery) != "" {
		status = "0 matches"
	}
	drawRightText(screen, x0+1, y0, boxW-2, styleDim, status)

	prompt := " find> "
	avail := max(1, boxW-4-stringWidth(prompt))
	visibleInput, cursorX := inputViewport(m.searchQuery, m.searchPos, avail)
	drawText(screen, x0+2, y0+2, styleDim, prompt)
	drawText(screen, x0+2+stringWidth(prompt), y0+2, styleInput, fitWidth(visibleInput, avail))
	screen.ShowCursor(x0+2+stringWidth(prompt)+cursorX, y0+2)

	footer := " Enter apply • Esc cancel • n/N next/prev after search "
	drawText(screen, x0+2, y0+boxH-1, styleDim, fitWidth(footer, boxW-4))
}

func (m *chatLiveModel) sessionsPickerLayout() (x0, y0, maxW, boxH, visibleStart, visibleCount int) {
	maxW = 0
	for _, session := range m.sessionsList {
		label := session.name + "  " + formatSessionTimestamp(session.modTime)
		if stringWidth(label)+8 > maxW {
			maxW = stringWidth(label) + 8
		}
	}
	if maxW > m.width-4 {
		maxW = m.width - 4
	}
	boxH = len(m.sessionsList) + 4
	if boxH > m.height-2 {
		boxH = m.height - 2
	}
	x0 = (m.width - maxW) / 2
	y0 = (m.height - boxH) / 2
	visibleCount = boxH - 4
	if visibleCount < 1 {
		visibleCount = 1
	}
	if m.sessionsCursor >= visibleStart+visibleCount {
		visibleStart = m.sessionsCursor - visibleCount + 1
	}
	if m.sessionsCursor < visibleStart {
		visibleStart = m.sessionsCursor
	}
	return
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

	x0, y0, maxW, boxH, visibleStart, visibleCount := m.modelPickerLayout()

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
	drawText(screen, x0+2, y0, styleBorder.Bold(true), title)
	drawRightText(screen, x0+1, y0, maxW-2, styleIdx, fmt.Sprintf("%d models", len(m.modelList)))

	// Model list
	for i := 0; i < visibleCount && visibleStart+i < len(m.modelList); i++ {
		idx := visibleStart + i
		name := m.modelList[idx]
		row := y0 + 2 + i
		marker := "  "
		if name == m.model {
			marker = "✓ "
		}
		prefix := fmt.Sprintf("%s%d. ", marker, idx+1)

		if idx == m.modelCursor {
			line := fmt.Sprintf("▸ %s%s", prefix, name)
			drawText(screen, x0+1, row, styleCursor, fitWidth(line, maxW-2))
		} else if name == m.model {
			line := fmt.Sprintf("  %s%s", prefix, name)
			drawText(screen, x0+1, row, styleCurrent, fitWidth(line, maxW-2))
		} else {
			drawText(screen, x0+1, row, styleIdx, fitWidth("  "+prefix, maxW-2))
			prefixW := stringWidth("  " + prefix)
			drawText(screen, x0+1+prefixW, row, styleBg, fitWidth(name, maxW-2-prefixW))
		}
	}

	footer := " Enter/click select • Esc cancel • wheel scroll • 1-9 quick pick "
	drawText(screen, x0+2, y0+boxH-1, styleIdx, fitWidth(footer, maxW-4))
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
	for i, line := range m.searchMatches {
		if line == lineIndex {
			start := 0
			if i < len(m.searchLineStarts) {
				start = m.searchLineStarts[i]
			}
			return start, i == m.searchCurrent, true
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
	if !m.toolsVisible {
		return 0, bodyTop, m.width, bodyH
	}
	leftW := m.leftPaneWidth
	if leftW == 0 {
		leftW = ((m.width - 1) * 7) / 10
	}
	leftW = clamp(leftW, 20, max(20, m.width-23))
	return 0, bodyTop, leftW, bodyH
}

func (m *chatLiveModel) rightPaneRect() (int, int, int, int) {
	if !m.toolsVisible {
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
	m.leftPaneWidth = clamp(x, 20, max(20, m.width-23))
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
		AgentBuf:         m.agentBuf,
		ToolsBuf:         m.toolsBuf,
		InputBuf:         m.inputBuf,
		InputPos:         m.inputPos,
		AgentScrl:        m.agentScrl,
		ToolsScrl:        m.toolsScrl,
		LeftPaneWidth:    m.leftPaneWidth,
		ToolsVisible:     m.toolsVisible,
		FocusRight:       m.focusR,
		AgentFollow:      m.agentFollow,
		ToolsFollow:      m.toolsFollow,
		SearchQuery:      m.searchQuery,
		SearchPane:       m.searchPane,
		SearchCurrent:    m.searchCurrent,
		SearchMatches:    append([]int(nil), m.searchMatches...),
		SearchLineStarts: append([]int(nil), m.searchLineStarts...),
		Turn:             m.turn,
	}
}

func (m *chatLiveModel) applySnapshot(s chatSessionSnapshot) {
	m.model = s.Model
	m.workDir = s.WorkDir
	m.agentBuf = s.AgentBuf
	m.toolsBuf = s.ToolsBuf
	m.inputBuf = s.InputBuf
	m.inputPos = clamp(s.InputPos, 0, utf8.RuneCountInString(s.InputBuf))
	m.agentScrl = max(0, s.AgentScrl)
	m.toolsScrl = max(0, s.ToolsScrl)
	m.leftPaneWidth = s.LeftPaneWidth
	m.toolsVisible = s.ToolsVisible
	m.focusR = s.FocusRight && s.ToolsVisible
	m.agentFollow = s.AgentFollow
	m.toolsFollow = s.ToolsFollow
	m.searchQuery = s.SearchQuery
	m.searchPane = s.SearchPane
	m.searchCurrent = s.SearchCurrent
	m.searchMatches = append([]int(nil), s.SearchMatches...)
	m.searchLineStarts = append([]int(nil), s.SearchLineStarts...)
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
	m.selectionPane = pane
	m.selectionStart = idx
	m.selectionEnd = idx
	m.selectionActive = true
	m.selectionDrag = true
}

func (m *chatLiveModel) updateSelectionFromMouse(pane string, x, y int) {
	if !m.selectionActive || m.selectionPane != pane {
		return
	}
	idx, ok := m.paneIndexFromMouse(pane, x, y)
	if !ok {
		return
	}
	m.selectionEnd = idx
}

func (m *chatLiveModel) clearSelection() {
	m.selectionPane = ""
	m.selectionStart = 0
	m.selectionEnd = 0
	m.selectionActive = false
	m.selectionDrag = false
}

func (m *chatLiveModel) selectedText(pane string) string {
	if !m.selectionActive || m.selectionPane != pane {
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
	if !m.selectionActive || m.selectionPane != pane || lineIndex < 0 || lineIndex >= len(wrapped) {
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
	if m.selectionStart <= m.selectionEnd {
		return m.selectionStart, m.selectionEnd
	}
	return m.selectionEnd, m.selectionStart
}

func (m *chatLiveModel) selectionContentAndWidth(pane string) (string, int) {
	if pane == "right" {
		return m.toolsBuf, m.rightContentWidth()
	}
	return m.agentBuf, m.leftContentWidth()
}

func (m *chatLiveModel) paneIndexFromMouse(pane string, x, y int) (int, bool) {
	var paneX, paneY, paneW, paneH, scroll int
	var width int
	var content string
	if pane == "right" {
		paneX, paneY, paneW, paneH = m.rightPaneRect()
		scroll = m.toolsScrl
		width = m.rightContentWidth()
		content = m.toolsBuf
	} else {
		paneX, paneY, paneW, paneH = m.leftPaneRect()
		scroll = m.agentScrl
		width = m.leftContentWidth()
		content = m.agentBuf
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
	m.timeline = append(m.timeline, entry)
	if len(m.timeline) > 12 {
		m.timeline = m.timeline[len(m.timeline)-12:]
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
