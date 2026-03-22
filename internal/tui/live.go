package tui

import (
	"fmt"
	"strings"

	"forge/internal/llm"
	"forge/internal/output"
	"forge/internal/session"

	"github.com/gdamore/tcell/v2"
	"github.com/mattn/go-runewidth"
	"github.com/muesli/reflow/wordwrap"
)

type LiveConfig struct {
	WriterModel  string
	AuditorModel string
	Gate         *session.TurnGate
}

type LiveResult struct {
	Aborted           bool
	FeedbackRequested bool
	FeedbackPass      int
	FeedbackPassName  string
	OutputDir         string
	Err               error
}

type liveModel struct {
	writerModel    string
	auditorModel   string
	width          int
	height         int
	currentPass    int
	totalPasses    int
	currentRound   int
	totalRounds    int
	passName       string
	phase          string
	writerBuf      string
	auditorBuf     string
	writerScroll   int
	auditorScroll  int
	focusRight     bool
	manualMode     bool
	waitingAdvance bool
	waitingAgent   string
	writerTurnGap  bool
	auditorTurnGap bool
}

func RunLive(events <-chan llm.Event, totalPasses, totalRounds int, cfg LiveConfig, outputDir string) LiveResult {
	screen, err := tcell.NewScreen()
	if err != nil {
		return LiveResult{Aborted: true, OutputDir: outputDir, Err: err}
	}
	if err := screen.Init(); err != nil {
		return LiveResult{Aborted: true, OutputDir: outputDir, Err: err}
	}
	defer screen.Fini()

	screen.Clear()

	model := liveModel{
		writerModel:  cfg.WriterModel,
		auditorModel: cfg.AuditorModel,
		manualMode:   cfg.Gate != nil && cfg.Gate.Enabled(),
		totalPasses:  totalPasses,
		totalRounds:  totalRounds,
		passName:     "starting",
		phase:        "starting",
	}

	eventCh := make(chan tcell.Event, 32)
	go func() {
		for {
			ev := screen.PollEvent()
			if ev == nil {
				close(eventCh)
				return
			}
			eventCh <- ev
		}
	}()

	for {
		model.render(screen)
		select {
		case ev, ok := <-eventCh:
			if !ok {
				return LiveResult{Aborted: true, OutputDir: outputDir}
			}
			switch msg := ev.(type) {
			case *tcell.EventResize:
				screen.Sync()
				model.width, model.height = msg.Size()
			case *tcell.EventKey:
				switch msg.Key() {
				case tcell.KeyLeft:
					model.focusRight = false
				case tcell.KeyRight:
					model.focusRight = true
				case tcell.KeyUp:
					model.scrollFocused(-1)
				case tcell.KeyDown:
					model.scrollFocused(1)
				case tcell.KeyPgUp:
					model.scrollFocused(-(model.bodyHeight() / 2))
				case tcell.KeyPgDn:
					model.scrollFocused(model.bodyHeight() / 2)
				case tcell.KeyHome:
					model.setFocusedScroll(0)
				case tcell.KeyEnd:
					model.setFocusedScroll(model.focusedMaxScroll())
				case tcell.KeyRune:
					switch msg.Rune() {
					case 'q':
						return LiveResult{Aborted: true, OutputDir: outputDir}
					case 'm':
						if cfg.Gate != nil {
							model.manualMode = cfg.Gate.Toggle()
							if !model.manualMode {
								model.waitingAdvance = false
								model.waitingAgent = ""
							}
						}
					case ' ', 'c':
						if cfg.Gate != nil && model.waitingAdvance {
							cfg.Gate.Advance()
							model.waitingAdvance = false
							model.waitingAgent = ""
						}
					}
				case tcell.KeyEnter:
					if cfg.Gate != nil && model.waitingAdvance {
						cfg.Gate.Advance()
						model.waitingAdvance = false
						model.waitingAgent = ""
					}
				case tcell.KeyEscape:
					return LiveResult{Aborted: true, OutputDir: outputDir}
				}
			}
		case ev, ok := <-events:
			if !ok {
				return LiveResult{OutputDir: outputDir}
			}
			switch ev.Kind {
			case llm.EventPassStart:
				model.currentPass = ev.Pass
				if ev.PassName != "" {
					model.passName = ev.PassName
				} else {
					model.passName = llm.PassName(ev.Pass)
				}
				model.phase = "starting"
			case llm.EventRoundStart:
				model.currentPass = ev.Pass
				model.currentRound = ev.Round
				model.phase = "writer"
				model.writerTurnGap = model.writerBuf != ""
				model.auditorTurnGap = model.auditorBuf != ""
			case llm.EventToken:
				switch ev.Agent {
				case "writer":
					model.phase = "writer"
					model.writerBuf = appendTurnText(model.writerBuf, &model.writerTurnGap, ev.Text)
					model.writerScroll = model.writerMaxScroll()
				case "auditor":
					model.phase = "auditor"
					model.auditorBuf = appendTurnText(model.auditorBuf, &model.auditorTurnGap, ev.Text)
					model.auditorScroll = model.auditorMaxScroll()
				case "summarizer":
					model.phase = "summarizing"
				}
			case llm.EventRoundEnd:
				model.currentPass = ev.Pass
				model.currentRound = ev.Round
				model.phase = "summarizing"
			case llm.EventAgentDone:
				if cfg.Gate != nil && cfg.Gate.Enabled() {
					model.waitingAdvance = true
					model.waitingAgent = ev.Agent
				}
			case llm.EventPassEnd:
				model.currentPass = ev.Pass
				model.phase = "pass done"
			case llm.EventFeedbackRequest:
				return LiveResult{FeedbackRequested: true, FeedbackPass: ev.Pass, FeedbackPassName: ev.PassName, OutputDir: outputDir}
			case llm.EventDone:
				return LiveResult{OutputDir: outputDir}
			case llm.EventAbort, llm.EventError:
				return LiveResult{Aborted: true, OutputDir: outputDir, Err: ev.Err}
			}
		}
	}
}

func (m *liveModel) render(screen tcell.Screen) {
	w, h := screen.Size()
	m.width, m.height = w, h
	screen.Clear()

	colorBright := tcell.GetColor("#f0f6fc")
	colorMid := tcell.GetColor("#b1bac4")
	colorDim := tcell.GetColor("#8b949e")
	colorGreen := tcell.GetColor("#56d364")

	styleBody := tcell.StyleDefault.Foreground(colorBright)
	styleStatus := tcell.StyleDefault.Foreground(colorMid)
	styleDivider := tcell.StyleDefault.Foreground(colorDim)
	styleTitleDim := tcell.StyleDefault.Foreground(colorDim)
	styleTitleFocus := tcell.StyleDefault.Foreground(colorGreen).Bold(true)

	drawText(screen, 0, 0, styleStatus, fitWidth(m.statusLine(), w))

	bodyTop := 1
	bodyHeight := m.bodyHeight()
	leftWidth := (w - 1) / 2
	rightWidth := w - 1 - leftWidth

	leftTitleStyle := styleTitleDim
	rightTitleStyle := styleTitleDim
	if m.focusRight {
		rightTitleStyle = styleTitleFocus
	} else {
		leftTitleStyle = styleTitleFocus
	}

	leftLines := m.paneLines("A", m.writerModel, m.writerBuf, leftWidth, bodyHeight, m.writerScroll)
	rightLines := m.paneLines("B", m.auditorModel, m.auditorBuf, rightWidth, bodyHeight, m.auditorScroll)

	for row := 0; row < bodyHeight; row++ {
		drawText(screen, 0, bodyTop+row, chooseStyle(row, leftTitleStyle, styleBody), fitWidth(leftLines[row], leftWidth))
		screen.SetContent(leftWidth, bodyTop+row, '│', nil, styleDivider)
		drawText(screen, leftWidth+1, bodyTop+row, chooseStyle(row, rightTitleStyle, styleBody), fitWidth(rightLines[row], rightWidth))
	}
	if h > 1 {
		drawText(screen, 0, h-1, styleStatus, fitWidth(m.helpLine(), w))
	}

	screen.Show()
}

func chooseStyle(row int, header, normal tcell.Style) tcell.Style {
	if row == 0 {
		return header
	}
	return normal
}

func (m *liveModel) statusLine() string {
	mode := "manual:off"
	if m.manualMode {
		mode = "manual:on"
	}
	wait := ""
	if m.waitingAdvance {
		wait = "  waiting:" + m.waitingAgent
	}
	return fmt.Sprintf("pass %d/%d %s  round %d/%d  %s  %s%s", m.currentPass, m.totalPasses, m.passName, m.currentRound, m.totalRounds, m.phase, mode, wait)
}

func (m *liveModel) bodyHeight() int {
	if m.height <= 2 {
		return 1
	}
	return m.height - 2
}

func (m *liveModel) helpLine() string {
	if m.waitingAdvance {
		return "left/right focus  up/down scroll  enter/space continue  m toggle manual  q quit"
	}
	return "left/right focus  up/down scroll  m toggle manual  q quit"
}

func (m *liveModel) paneLines(label, modelName, content string, width, height, scroll int) []string {
	lines := make([]string, 0, height)
	lines = append(lines, label+" "+modelName)
	bodyHeight := height - 1
	bodyLines := wrapPlain(foldForDisplay(content), width)
	if len(bodyLines) == 0 {
		bodyLines = []string{""}
	}
	scroll = clamp(scroll, 0, max(0, len(bodyLines)-bodyHeight))
	end := min(len(bodyLines), scroll+bodyHeight)
	lines = append(lines, bodyLines[scroll:end]...)
	for len(lines) < height {
		lines = append(lines, "")
	}
	if len(lines) > height {
		lines = lines[:height]
	}
	return lines
}

func (m *liveModel) scrollFocused(delta int) {
	if m.focusRight {
		m.auditorScroll = clamp(m.auditorScroll+delta, 0, m.auditorMaxScroll())
		return
	}
	m.writerScroll = clamp(m.writerScroll+delta, 0, m.writerMaxScroll())
}

func (m *liveModel) setFocusedScroll(pos int) {
	if m.focusRight {
		m.auditorScroll = clamp(pos, 0, m.auditorMaxScroll())
		return
	}
	m.writerScroll = clamp(pos, 0, m.writerMaxScroll())
}

func (m *liveModel) focusedMaxScroll() int {
	if m.focusRight {
		return m.auditorMaxScroll()
	}
	return m.writerMaxScroll()
}

func (m *liveModel) writerMaxScroll() int {
	return max(0, len(wrapPlain(foldForDisplay(m.writerBuf), max(1, (m.width-1)/2)))-(m.bodyHeight()-1))
}

func (m *liveModel) auditorMaxScroll() int {
	rightWidth := m.width - 1 - ((m.width - 1) / 2)
	return max(0, len(wrapPlain(foldForDisplay(m.auditorBuf), max(1, rightWidth)))-(m.bodyHeight()-1))
}

func drawText(screen tcell.Screen, x, y int, style tcell.Style, text string) {
	col := 0
	for _, r := range text {
		screen.SetContent(x+col, y, r, nil, style)
		col += runewidth.RuneWidth(r)
	}
}

func fitWidth(s string, width int) string {
	sw := runewidth.StringWidth(s)
	if sw > width {
		return runewidth.Truncate(s, width, "")
	}
	if sw < width {
		return s + strings.Repeat(" ", width-sw)
	}
	return s
}

func wrapPlain(s string, width int) []string {
	if width < 1 {
		return []string{""}
	}
	wrapped := wordwrap.String(s, width)
	return strings.Split(wrapped, "\n")
}

func foldForDisplay(text string) string {
	blocks := output.ParseCodeBlocks(text)
	if len(blocks) == 0 {
		return text
	}

	lines := strings.Split(text, "\n")
	var out []string
	i := 0
	for i < len(lines) {
		line := lines[i]
		if !strings.HasPrefix(line, "```") {
			out = append(out, line)
			i++
			continue
		}

		header := strings.TrimPrefix(line, "```")
		colon := strings.Index(header, ":")
		if colon < 0 {
			out = append(out, line)
			i++
			continue
		}

		filename := header[colon+1:]
		i++
		codeLines := 0
		for i < len(lines) && lines[i] != "```" {
			codeLines++
			i++
		}
		if i < len(lines) && lines[i] == "```" {
			i++
		}
		if filename == "" {
			filename = "unnamed"
		}
		out = append(out, fmt.Sprintf("[code: %s %d lines]", filename, codeLines))
	}

	return strings.TrimSpace(strings.Join(out, "\n"))
}
