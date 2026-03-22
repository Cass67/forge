package tui

import (
	"fmt"
	"strings"
	"time"

	"forge/internal/llm"
)

func (m *chatLiveModel) handleSlashCommand(input string) {
	switch {
	case input == "/help":
		m.overlays.helpVisible = true
		m.display.flash = "shortcuts help opened"
	case input == "/find":
		m.openSearchOverlay()
	case strings.HasPrefix(input, "/find "):
		m.openSearchOverlay()
		m.overlays.search.query = strings.TrimSpace(strings.TrimPrefix(input, "/find "))
		m.overlays.search.pos = len([]rune(m.overlays.search.query))
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
		resolved := resolveModelName(m.overlays.models.list, arg)
		if resolved == "" {
			m.display.flash = fmt.Sprintf("unknown model %q — try /models", arg)
			return
		}
		if m.switchModelFn != nil {
			newModel, err := m.switchModelFn(resolved)
			if err != nil {
				m.display.flash = fmt.Sprintf("error: %v", err)
				return
			}
			m.model = newModel
			m.display.flash = fmt.Sprintf("switched to %s", newModel)
		}
	case input == "/expand":
		if m.display.lastExpandable != "" {
			m.panes.tools.buf += "\n" + m.display.lastExpandable + "\n"
			m.panes.tools.scroll = m.toolsMaxScroll()
			m.display.lastExpandable = ""
			m.display.flash = "expanded"
		} else {
			m.display.flash = "nothing to expand"
		}
	case input == "/toggle tools":
		m.panes.layout.toolsVisible = !m.panes.layout.toolsVisible
		if !m.panes.layout.toolsVisible {
			m.panes.focusR = false
			m.display.flash = "tools pane hidden"
		} else {
			m.display.flash = "tools pane shown"
		}
	case input == "/toggle tools on":
		m.panes.layout.toolsVisible = true
		m.display.flash = "tools pane shown"
	case input == "/toggle tools off":
		m.panes.layout.toolsVisible = false
		m.panes.focusR = false
		m.display.flash = "tools pane hidden"
	case input == "/theme":
		m.themeLowContrast = !m.themeLowContrast
		if m.themeLowContrast {
			m.display.flash = "theme: low contrast"
		} else {
			m.display.flash = "theme: default"
		}
	case input == "/theme low":
		m.themeLowContrast = true
		m.display.flash = "theme: low contrast"
	case input == "/theme default":
		m.themeLowContrast = false
		m.display.flash = "theme: default"
	case input == "/copy agent":
		if err := m.copyBufferToFile("agent", m.panes.agent.buf); err != nil {
			m.display.flash = fmt.Sprintf("copy failed: %v", err)
		} else {
			m.display.flash = "agent pane exported"
		}
	case input == "/copy tools":
		if err := m.copyBufferToFile("tools", m.panes.tools.buf); err != nil {
			m.display.flash = fmt.Sprintf("copy failed: %v", err)
		} else {
			m.display.flash = "tools pane exported"
		}
	case input == "/copy code":
		if strings.TrimSpace(m.display.lastCodeBlock) == "" {
			m.display.flash = "copy failed: no code block yet"
		} else if err := m.copyBufferToFile("code", m.display.lastCodeBlock); err != nil {
			m.display.flash = fmt.Sprintf("copy failed: %v", err)
		} else {
			m.display.flash = "latest code block exported"
		}
	case input == "/copy result":
		if strings.TrimSpace(m.display.lastToolResult) == "" {
			m.display.flash = "copy failed: no tool result yet"
		} else if err := m.copyBufferToFile("result", m.display.lastToolResult); err != nil {
			m.display.flash = fmt.Sprintf("copy failed: %v", err)
		} else {
			m.display.flash = "latest tool result exported"
		}
	case input == "/save":
		name, err := defaultChatSessionName()
		if err != nil {
			m.display.flash = fmt.Sprintf("save failed: %v", err)
			return
		}
		if err := m.saveSession(name); err != nil {
			m.display.flash = fmt.Sprintf("save failed: %v", err)
			return
		}
		m.display.flash = fmt.Sprintf("session saved: %s", name)
	case strings.HasPrefix(input, "/save "):
		name := sanitizeChatSessionName(strings.TrimSpace(strings.TrimPrefix(input, "/save ")))
		if name == "" {
			m.display.flash = "save failed: missing session name"
			return
		}
		if err := m.saveSession(name); err != nil {
			m.display.flash = fmt.Sprintf("save failed: %v", err)
			return
		}
		m.display.flash = fmt.Sprintf("session saved: %s", name)
	case input == "/restore":
		name, err := latestChatSessionName()
		if err != nil {
			m.display.flash = fmt.Sprintf("restore failed: %v", err)
			return
		}
		if err := m.restoreSession(name); err != nil {
			m.display.flash = fmt.Sprintf("restore failed: %v", err)
			return
		}
		m.display.flash = fmt.Sprintf("session restored: %s", name)
	case strings.HasPrefix(input, "/restore "):
		name := sanitizeChatSessionName(strings.TrimSpace(strings.TrimPrefix(input, "/restore ")))
		if name == "" {
			m.display.flash = "restore failed: missing session name"
			return
		}
		if err := m.restoreSession(name); err != nil {
			m.display.flash = fmt.Sprintf("restore failed: %v", err)
			return
		}
		m.display.flash = fmt.Sprintf("session restored: %s", name)
	case input == "/sessions":
		m.openSessionsPicker()
	case input == "/clear", input == "/clear all":
		if m.clearHistFn != nil {
			m.clearHistFn()
		}
		m.panes.agent.buf = ""
		m.panes.tools.buf = ""
		m.panes.agent.scroll = 0
		m.panes.tools.scroll = 0
		m.overlays.search.matches = nil
		m.overlays.search.current = -1
		m.overlays.search.lineStarts = nil
		m.display.flash = "conversation cleared"
	case input == "/clear agent":
		m.panes.agent.buf = ""
		m.panes.agent.scroll = 0
		if m.overlays.search.pane == "left" {
			m.overlays.search.matches = nil
			m.overlays.search.current = -1
			m.overlays.search.lineStarts = nil
		}
		m.display.flash = "agent pane cleared"
	case input == "/clear tools":
		m.panes.tools.buf = ""
		m.panes.tools.scroll = 0
		if m.overlays.search.pane == "right" {
			m.overlays.search.matches = nil
			m.overlays.search.current = -1
			m.overlays.search.lineStarts = nil
		}
		m.display.flash = "tools pane cleared"
	default:
		m.display.flash = fmt.Sprintf("unknown command: %s (try /help)", input)
	}
}

func (m *chatLiveModel) appendSteeringInput(input string) {
	stamp := time.Now().Format("15:04:05")
	m.panes.agent.buf += fmt.Sprintf("\nSteer • %s\n→ %s\n", stamp, input)
	if m.panes.tools.buf != "" && !strings.HasSuffix(m.panes.tools.buf, "\n\n") {
		m.panes.tools.buf += "\n"
	}
	m.panes.tools.buf += fmt.Sprintf("────────────────────────\n● steering\n  queued while busy • %s\n  → %s\n", stamp, input)
	m.pushTimeline(fmt.Sprintf("steer %s", input))
	m.panes.agent.follow = true
	m.panes.tools.follow = true
	m.panes.agent.scroll = m.agentMaxScroll()
	m.panes.tools.scroll = m.toolsMaxScroll()
	m.display.flash = "steering sent"
}

func (m *chatLiveModel) appendTurnStart(input string) {
	started := m.display.turnStartedAt
	if started.IsZero() {
		started = time.Now()
	}
	stamp := started.Format("15:04:05")
	sep := fmt.Sprintf("\n%s\n", strings.Repeat("─", 28))
	if strings.TrimSpace(m.panes.agent.buf) == "" {
		sep = ""
	}
	m.panes.agent.buf += fmt.Sprintf("%sYou • %s\n%s\n", sep, stamp, input)
	if strings.TrimSpace(m.panes.tools.buf) != "" {
		m.panes.tools.buf += fmt.Sprintf("\n%s\n", strings.Repeat("─", 28))
	}
	m.panes.agent.follow = true
	m.panes.tools.follow = true
	m.panes.agent.scroll = m.agentMaxScroll()
	m.panes.tools.scroll = m.toolsMaxScroll()
}

func (m *chatLiveModel) handleEvent(ev llm.Event) {
	switch ev.Kind {
	case llm.EventToken:
		wasAtBottom := m.panes.agent.follow || m.panes.agent.scroll >= m.agentMaxScroll()
		lines := strings.Split(ev.Text, "\n")
		for i, line := range lines {
			if i < len(lines)-1 {
				m.panes.agent.buf += " │ " + line + "\n"
			} else if line != "" {
				m.panes.agent.buf += " │ " + line
			}
		}
		if wasAtBottom {
			m.panes.agent.scroll = m.agentMaxScroll()
		}

	case llm.EventToolCall:
		m.pushTimeline(fmt.Sprintf("tool %s", ev.Agent))
		wasAtBottom := m.panes.tools.follow || m.panes.tools.scroll >= m.toolsMaxScroll()
		if m.panes.tools.buf != "" && !strings.HasSuffix(m.panes.tools.buf, "\n\n") {
			m.panes.tools.buf += "\n"
		}
		m.panes.tools.buf += fmt.Sprintf("────────────────────────\n")
		m.panes.tools.buf += fmt.Sprintf("● %s\n", ev.Agent)
		m.panes.tools.buf += fmt.Sprintf("  %s\n", ev.Text)
		if wasAtBottom {
			m.panes.tools.scroll = m.toolsMaxScroll()
		}

	case llm.EventToolResult:
		if ev.IsError {
			m.pushTimeline("tool error")
		} else {
			m.pushTimeline("tool result")
		}
		wasAtBottom := m.panes.tools.follow || m.panes.tools.scroll >= m.toolsMaxScroll()
		if ev.Content != "" {
			m.display.lastToolResult = ev.Content
		} else if ev.Text != "" {
			m.display.lastToolResult = ev.Text
		}
		if ev.IsError {
			m.panes.tools.buf += fmt.Sprintf("  status: ✗ %s\n", ev.Text)
		} else {
			if ev.Content != "" {
				diffLines := strings.Split(ev.Content, "\n")
				shown := 0
				m.panes.tools.buf += "  result:\n"
				for _, dl := range diffLines {
					if dl == "" {
						continue
					}
					if shown >= 10 {
						remaining := len(diffLines) - shown
						m.panes.tools.buf += fmt.Sprintf("  ... (%d more, /expand)\n", remaining)
						m.display.lastExpandable = ev.Content
						break
					}
					m.panes.tools.buf += fmt.Sprintf("  %s\n", dl)
					shown++
				}
			}
			m.panes.tools.buf += fmt.Sprintf("  status: ✓ %s\n", ev.Text)
		}
		if wasAtBottom {
			m.panes.tools.scroll = m.toolsMaxScroll()
		}

	case llm.EventError:
		m.pushTimeline("error")
		wasAtBottom := m.panes.tools.follow || m.panes.tools.scroll >= m.toolsMaxScroll()
		m.panes.tools.buf += fmt.Sprintf("  ✗ %s\n", ev.Text)
		if wasAtBottom {
			m.panes.tools.scroll = m.toolsMaxScroll()
		}

	case llm.EventStats:
		m.display.statsDuration = ev.Duration
		m.display.statsUsage = ev.Usage

	case llm.EventDone:
		m.pushTimeline("done")
		m.busy = false
		m.status = "ready"
		finished := time.Now()
		stamp := finished.Format("15:04:05")
		if strings.TrimSpace(m.panes.agent.buf) != "" {
			m.panes.agent.buf += fmt.Sprintf("\nAgent complete • %s\n", stamp)
		}
		if strings.TrimSpace(m.panes.tools.buf) != "" {
			statusLine := fmt.Sprintf("status: complete • %s", stamp)
			if m.display.statsDuration > 0 {
				statusLine += fmt.Sprintf(" • %.1fs", m.display.statsDuration.Seconds())
			}
			m.panes.tools.buf += statusLine + "\n"
		}
	}
}
