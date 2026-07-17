package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"forge/internal/llm"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// handlePipelineLLMEvent processes pipeline-specific LLM events.
// Returns true if the event was consumed as a pipeline event, plus an optional cmd.
func (m *ChatModel) handlePipelineLLMEvent(ev llm.Event) (bool, tea.Cmd) {
	if !m.pipelineActive {
		return false, nil
	}
	switch ev.Kind {
	case llm.EventPassStart:
		m.pipelineCurrentPass = ev.Pass
		if ev.PassName != "" {
			m.pipelinePassName = ev.PassName
		} else {
			m.pipelinePassName = llm.PassName(ev.Pass)
		}
		m.pipelinePhase = "starting"
		header := fmt.Sprintf("── Pass %d/%d: %s ──", m.pipelineCurrentPass, m.pipelineTotalPasses, m.pipelinePassName)
		m.AddMessage(ChatMessage{Kind: MsgStatus, Content: header})
		return true, nil

	case llm.EventRoundStart:
		m.pipelineCurrentPass = ev.Pass
		m.pipelineCurrentRound = ev.Round
		m.pipelinePhase = "writer"
		m.pipelineWriterTurnGap = m.pipelineWriterBuf != ""
		m.pipelineAuditorTurnGap = m.pipelineAuditorBuf != ""
		sep := fmt.Sprintf("── Round %d ──", ev.Round)
		m.AddMessage(ChatMessage{Kind: MsgStatus, Content: sep})
		return true, nil

	case llm.EventToken:
		switch ev.Agent {
		case "writer":
			m.pipelinePhase = "writer"
			m.pipelineWriterBuf = appendPipelineText(m.pipelineWriterBuf, &m.pipelineWriterTurnGap, ev.Text)
			m.pipelineWriterScroll = pipelineMaxScroll(len(m.pipelineWriterBuf), m.chatBodyHeight(), m.pipelineTotalPasses)
		case "auditor":
			m.pipelinePhase = "auditor"
			m.pipelineAuditorBuf = appendPipelineText(m.pipelineAuditorBuf, &m.pipelineAuditorTurnGap, ev.Text)
			m.pipelineAuditorScroll = pipelineMaxScroll(len(m.pipelineAuditorBuf), m.chatBodyHeight(), m.pipelineTotalPasses)
		case "summarizer":
			m.pipelinePhase = "summarizing"
		}
		return true, nil

	case llm.EventRoundEnd:
		m.pipelineCurrentPass = ev.Pass
		m.pipelineCurrentRound = ev.Round
		m.pipelinePhase = "summarizing"
		if !m.pipelineViewActive {
			m.AddMessage(ChatMessage{
				Kind:    MsgStatus,
				Content: fmt.Sprintf("Round %d complete.", ev.Round),
			})
		}
		return true, nil

	case llm.EventAgentDone:
		m.pipelineWaitingAdvance = m.pipelineManualMode
		m.pipelineWaitingAgent = ev.Agent
		if !m.pipelineViewActive {
			m.AddMessage(ChatMessage{
				Kind:    MsgStatus,
				Content: fmt.Sprintf("%s complete.", ev.Agent),
			})
		}
		return true, nil

	case llm.EventPassEnd:
		m.pipelineCurrentPass = ev.Pass
		m.pipelinePhase = "pass done"
		if !m.pipelineViewActive {
			m.AddMessage(ChatMessage{
				Kind:    MsgStatus,
				Content: fmt.Sprintf("Pass %d complete.", ev.Pass),
			})
		}
		return true, nil

	case llm.EventFeedbackRequest:
		m.pipelinePhase = "feedback_requested"
		m.AddMessage(ChatMessage{
			Kind:    MsgStatus,
			Content: "Feedback requested — switching to prompt mode",
		})
		return true, tea.Quit

	case llm.EventDone:
		m.pipelinePhase = "done"
		m.pipelineActive = false
		m.pipelineViewActive = false
		content := "Pipeline complete."
		if text := strings.TrimSpace(ev.Text); text != "" {
			content += " " + text
		}
		m.AddMessage(ChatMessage{
			Kind:    MsgStatus,
			Content: content,
		})

	case llm.EventWarning:
		if ev.Err != nil {
			m.AddMessage(ChatMessage{
				Kind:    MsgStatus,
				Content: "⚠ " + ev.Err.Error(),
			})
		}
		return true, nil

	case llm.EventAbort, llm.EventError:
		m.pipelinePhase = "aborted"
		m.pipelineActive = false
		m.pipelineViewActive = false
		errMsg := eventErrorMessage(ev)
		m.AddMessage(ChatMessage{
			Kind:    MsgStatus,
			Content: "Pipeline aborted: " + errMsg,
		})
	}

	return true, nil
}

// handlePipelineKey handles keyboard input specific to pipeline view mode.
// Returns true if the key was consumed by pipeline mode, plus an optional cmd.
func (m *ChatModel) handlePipelineKey(msg tea.KeyMsg) (bool, tea.Cmd) {
	if !m.pipelineActive {
		return false, nil
	}

	// If file preview is visible, handle those keys first
	if m.pipelineFilePreviewVisible {
		switch msg.Type {
		case tea.KeyEscape:
			m.pipelineFilePreviewVisible = false
			return true, nil
		case tea.KeyUp:
			m.pipelineFilePreviewScroll = clamp(m.pipelineFilePreviewScroll-1, 0, m.pipelineFilePreviewMaxScroll())
			return true, nil
		case tea.KeyDown:
			m.pipelineFilePreviewScroll = clamp(m.pipelineFilePreviewScroll+1, 0, m.pipelineFilePreviewMaxScroll())
			return true, nil
		case tea.KeyPgUp:
			m.pipelineFilePreviewScroll = clamp(m.pipelineFilePreviewScroll-m.pipelineFilePreviewHeight, 0, m.pipelineFilePreviewMaxScroll())
			return true, nil
		case tea.KeyPgDown:
			m.pipelineFilePreviewScroll = clamp(m.pipelineFilePreviewScroll+m.pipelineFilePreviewHeight, 0, m.pipelineFilePreviewMaxScroll())
			return true, nil
		case tea.KeyHome:
			m.pipelineFilePreviewScroll = 0
			return true, nil
		case tea.KeyEnd:
			m.pipelineFilePreviewScroll = m.pipelineFilePreviewMaxScroll()
			return true, nil
		}
	}

	switch msg.Type {
	case tea.KeyRunes:
		switch string(msg.Runes) {
		case "p":
			if m.pipelineViewActive {
				m.toggleFilePreview()
				return true, nil
			}
		}
	}

	// Pipeline view-specific key handling
	if m.pipelineViewActive {
		switch msg.Type {
		case tea.KeyEscape:
			return false, nil
		case tea.KeyLeft:
			m.pipelineFocusRight = false
			return true, nil
		case tea.KeyRight:
			m.pipelineFocusRight = true
			return true, nil
		case tea.KeyUp:
			m.pipelineScrollFocused(-1)
			return true, nil
		case tea.KeyDown:
			m.pipelineScrollFocused(1)
			return true, nil
		case tea.KeyPgUp:
			m.pipelineScrollFocused(-(m.pipelineBodyHeight() / 2))
			return true, nil
		case tea.KeyPgDown:
			m.pipelineScrollFocused(m.pipelineBodyHeight() / 2)
			return true, nil
		case tea.KeyHome:
			m.pipelineSetFocusedScroll(0)
			return true, nil
		case tea.KeyEnd:
			m.pipelineSetFocusedScroll(m.pipelineFocusedMaxScroll())
			return true, nil
		case tea.KeyEnter:
			if m.pipelineGate != nil && m.pipelineWaitingAdvance {
				m.pipelineGate.Advance()
				m.pipelineWaitingAdvance = false
				m.pipelineWaitingAgent = ""
			}
			return true, nil
		}
		switch msg.Type {
		case tea.KeyRunes:
			switch string(msg.Runes) {
			case "q":
				m.pipelineViewActive = false
				return true, nil
			case "m":
				if m.pipelineGate != nil {
					m.pipelineManualMode = m.pipelineGate.Toggle()
					if !m.pipelineManualMode {
						m.pipelineWaitingAdvance = false
						m.pipelineWaitingAgent = ""
					}
				}
				return true, nil
			case " ", "c":
				if m.pipelineGate != nil && m.pipelineWaitingAdvance {
					m.pipelineGate.Advance()
					m.pipelineWaitingAdvance = false
					m.pipelineWaitingAgent = ""
				}
				return true, nil
			case "v":
				m.pipelineViewActive = false
				return true, nil
			}
		}
	} else {
		// In chat view but pipeline active — check for toggle key
		switch msg.Type {
		case tea.KeyCtrlP:
			m.pipelineViewActive = true
			return true, nil
		case tea.KeyRunes:
			if string(msg.Runes) == "v" && strings.TrimSpace(m.inputBuf) == "" {
				m.pipelineViewActive = true
				return true, nil
			}
		}
	}
	return false, nil
}

// toggleFilePreview toggles the file preview pane on the currently focused file.
func (m *ChatModel) toggleFilePreview() {
	if m.pipelineFilePreviewVisible {
		m.pipelineFilePreviewVisible = false
		return
	}
	// Find the most recently modified file
	changes := m.fileChanges
	var target string
	if len(changes.Modified) > 0 {
		target = changes.Modified[len(changes.Modified)-1]
	} else if len(changes.Added) > 0 {
		target = changes.Added[len(changes.Added)-1]
	}
	if target == "" {
		return
	}
	m.loadFilePreview(target)
}

// loadFilePreview reads and stores the content of the given file for preview.
func (m *ChatModel) loadFilePreview(path string) {
	absPath := path
	if !filepath.IsAbs(path) {
		absPath = filepath.Join(m.workDir, path)
	}
	// Refuse files larger than 1 MB to avoid OOM
	info, err := os.Stat(absPath)
	if err != nil {
		m.pipelineFilePreviewContent = fmt.Sprintf("error reading %s: %v", path, err)
		return
	}
	if info.Size() > 1*1024*1024 {
		m.pipelineFilePreviewContent = fmt.Sprintf("file too large for preview (%.1f MB)", float64(info.Size())/(1024*1024))
		return
	}
	data, err := os.ReadFile(absPath)
	if err != nil {
		m.pipelineFilePreviewContent = fmt.Sprintf("error reading %s: %v", path, err)
		return
	}
	// Detect binary files via null byte
	if len(data) > 0 && containsNullByte(data) {
		m.pipelineFilePreviewContent = fmt.Sprintf("binary file: %s (cannot preview)", path)
		return
	}
	m.pipelineFilePreviewPath = path
	m.pipelineFilePreviewContent = string(data)
	m.pipelineFilePreviewScroll = 0
	m.pipelineFilePreviewVisible = true
}

// pipelineFilePreviewMaxScroll returns the max scroll position for the file preview.
func (m *ChatModel) pipelineFilePreviewMaxScroll() int {
	lines := strings.Split(m.pipelineFilePreviewContent, "\n")
	height := m.pipelineFilePreviewHeight
	if height <= 0 {
		height = 10
	}
	return max(0, len(lines)-height)
}

// pipelineRenderView renders the entire pipeline view.
// If pipelineViewActive is true, it shows the two-pane pipeline view.
// Otherwise it falls through to the normal chat view.
func (m ChatModel) pipelineRenderView(theme chatTheme, header string) string {

	if m.pipelineViewActive {
		return m.pipelineRenderTwoPane(theme, header)
	}
	return m.pipelineRenderChatView(theme, header)
}

// pipelineRenderChatView renders the chat-oriented view of pipeline progress.
func (m ChatModel) pipelineRenderChatView(theme chatTheme, header string) string {
	headerGap := lipgloss.NewStyle().Width(m.width).Render("")
	budget := m.normalChatLayoutBudget()
	chatBodyHeight := budget.Chat
	chatContentWidth := max(1, m.chatContentWidth())
	chatView := m.chatViewport.View()
	chatLines := strings.Split(chatView, "\n")
	chatTotalLines := len(strings.Split(m.chatVisible, "\n"))
	if strings.TrimSpace(m.chatVisible) == "" {
		empty := []string{
			"  Pipeline running...",
			keyLabel("  Press Ctrl+P for pipeline view, P for file preview."),
		}
		chatLines = empty
		chatTotalLines = len(empty)
	}

	if len(chatLines) < chatBodyHeight {
		padding := make([]string, chatBodyHeight-len(chatLines))
		for i := range padding {
			padding[i] = ""
		}
		chatLines = append(chatLines, padding...)
	} else if len(chatLines) > chatBodyHeight {
		chatLines = chatLines[:chatBodyHeight]
	}

	chatScrollbar := scrollbarColumn(chatTotalLines, m.chatViewport.Height, m.chatViewport.YOffset, chatBodyHeight)
	chatBody := joinWithScrollbar(chatLines, chatScrollbar, chatContentWidth, chatBodyHeight)
	chatPane := lipgloss.NewStyle().
		Foreground(theme.Text).
		Width(m.chatPaneWidth()).
		Height(chatBodyHeight).
		Render(chatBody)

	liveRegion := m.renderLiveProgressSlot(theme)

	// Pipeline status line
	statusLine := m.pipelineStatusLine()
	statusStyle := lipgloss.NewStyle().
		Background(lipgloss.Color("#161b22")).
		Foreground(lipgloss.Color("#8b949e")).
		Width(m.width).
		Render(statusLine)

	parts := []string{header, headerGap, chatPane}
	if panel := m.renderAgentTaskPanel(theme); panel != "" {
		parts = append(parts, panel)
	}
	if cards := m.renderToolCardsPanel(theme); cards != "" {
		parts = append(parts, cards)
	}
	if changes := m.renderFileChangesPanel(theme); changes != "" {
		parts = append(parts, changes)
	}
	parts = append(parts, liveRegion)
	parts = append(parts, statusStyle)

	return lipgloss.JoinVertical(lipgloss.Left, parts...)
}

// pipelineRenderTwoPane renders the two-pane split view for pipeline mode.
func (m ChatModel) pipelineRenderTwoPane(theme chatTheme, header string) string {
	headerGap := lipgloss.NewStyle().Width(m.width).Render("")

	// When no pipeline is active, show an informational idle state
	if !m.pipelineActive {
		bodyHeight := m.pipelineBodyHeight()
		bodyWidth := max(20, m.width-2)
		lines := []string{
			"",
			"  ╔══════════════════════════════════════════════╗",
			"  ║                                              ║",
			"  ║     No pipeline running                      ║",
			"  ║     Type /make to start a pipeline session   ║",
			"  ║     Press Ctrl+P to return to chat view      ║",
			"  ║                                              ║",
			"  ╚══════════════════════════════════════════════╝",
			"",
		}
		if len(lines) < bodyHeight {
			padding := make([]string, bodyHeight-len(lines))
			for i := range padding {
				padding[i] = ""
			}
			lines = append(lines, padding...)
		}
		idle := lipgloss.NewStyle().
			Background(lipgloss.Color("#0d1117")).
			Foreground(lipgloss.Color("#8b949e")).
			Width(bodyWidth).Height(bodyHeight).
			Render(strings.Join(lines, "\n"))
		body := lipgloss.JoinHorizontal(lipgloss.Top, idle)
		return lipgloss.JoinVertical(lipgloss.Left, header, headerGap, body, m.pipelineRenderFooter())
	}

	leftWidth := max(20, (m.width-1)/2)
	rightWidth := max(20, m.width-leftWidth)
	bodyHeight := m.pipelineBodyHeight()

	// If file preview is visible, split the right pane
	if m.pipelineFilePreviewVisible {
		previewHeight := max(5, bodyHeight/3)
		m.pipelineFilePreviewHeight = previewHeight
		codeHeight := bodyHeight - previewHeight
		leftPane := m.pipelineRenderPane("A", m.model, m.pipelineWriterBuf, leftWidth, codeHeight, m.pipelineWriterScroll, !m.pipelineFocusRight)
		rightPane := m.pipelineRenderPane("B", m.pipelineAuditorLabel(), m.pipelineAuditorBuf, rightWidth, codeHeight, m.pipelineAuditorScroll, m.pipelineFocusRight)
		filePreview := m.pipelineRenderFilePreviewPane(rightWidth, previewHeight)
		body := lipgloss.JoinHorizontal(lipgloss.Top, leftPane, rightPane)
		body = lipgloss.JoinVertical(lipgloss.Top, body, filePreview)
		return lipgloss.JoinVertical(lipgloss.Left, header, headerGap, body, m.pipelineRenderFooter())
	}

	leftPane := m.pipelineRenderPane("A", m.model, m.pipelineWriterBuf, leftWidth, bodyHeight, m.pipelineWriterScroll, !m.pipelineFocusRight)
	rightPane := m.pipelineRenderPane("B", m.pipelineAuditorLabel(), m.pipelineAuditorBuf, rightWidth, bodyHeight, m.pipelineAuditorScroll, m.pipelineFocusRight)
	body := lipgloss.JoinHorizontal(lipgloss.Top, leftPane, rightPane)

	return lipgloss.JoinVertical(lipgloss.Left, header, headerGap, body, m.pipelineRenderFooter())
}

// pipelineAuditorLabel returns the auditor model label.
func (m ChatModel) pipelineAuditorLabel() string {
	// If we have a pipelineGate, show it
	return "auditor"
}

// pipelineRenderPane renders a single pipeline pane with title, content, scrollbar.
func (m ChatModel) pipelineRenderPane(label, modelName, content string, width, height, scroll int, focused bool) string {
	titleColor := lipgloss.Color("#8b949e")
	borderColor := lipgloss.Color("#30363d")
	if focused {
		titleColor = lipgloss.Color("#56d364")
		borderColor = lipgloss.Color("#56d364")
	}

	title := lipgloss.NewStyle().Foreground(titleColor).Bold(true).Render(label + " " + modelName)
	bodyWidth := max(1, width-4)
	lines := wrapPlain(foldForDisplay(content), bodyWidth)
	if len(lines) == 0 {
		lines = []string{""}
	}
	scroll = clamp(scroll, 0, max(0, len(lines)-height))
	end := min(len(lines), scroll+height)
	visible := append([]string(nil), lines[scroll:end]...)
	for i, line := range visible {
		visible[i] = RenderSemanticPlain(line, profileStatus, defaultSemanticTheme())
	}
	scrollbar := scrollbarColumn(len(lines), height, scroll, height)
	contentBody := joinWithScrollbar(visible, scrollbar, bodyWidth, height)

	inner := lipgloss.JoinVertical(
		lipgloss.Left,
		title,
		lipgloss.NewStyle().Width(max(1, width-2)).Height(height).Render(contentBody),
	)
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(borderColor).
		Background(lipgloss.Color("#0d1117")).
		Foreground(lipgloss.Color("#f0f6fc")).
		Width(max(1, width-2)).
		Height(max(1, height+1)).
		Render(inner)
}

// pipelineRenderFilePreviewPane renders a file preview pane.
func (m ChatModel) pipelineRenderFilePreviewPane(width, height int) string {
	if !m.pipelineFilePreviewVisible || m.pipelineFilePreviewContent == "" {
		return ""
	}
	titleColor := lipgloss.Color("#58a6ff")
	borderColor := lipgloss.Color("#1f6feb")

	title := lipgloss.NewStyle().Foreground(titleColor).Bold(true).Render("Preview: " + m.pipelineFilePreviewPath)
	bodyWidth := max(1, width-4)
	lines := strings.Split(m.pipelineFilePreviewContent, "\n")
	scroll := clamp(m.pipelineFilePreviewScroll, 0, max(0, len(lines)-height))
	end := min(len(lines), scroll+height)
	visible := lines[scroll:end]
	if len(visible) < height {
		padding := make([]string, height-len(visible))
		for i := range padding {
			padding[i] = ""
		}
		visible = append(visible, padding...)
	}
	scrollbar := scrollbarColumn(len(lines), height, m.pipelineFilePreviewScroll, height)
	contentBody := joinWithScrollbar(visible, scrollbar, bodyWidth, height)

	inner := lipgloss.JoinVertical(
		lipgloss.Left,
		title,
		lipgloss.NewStyle().Width(max(1, width-2)).Height(height).Render(contentBody),
	)
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(borderColor).
		Background(lipgloss.Color("#0d1117")).
		Foreground(lipgloss.Color("#f0f6fc")).
		Width(max(1, width-2)).
		Height(max(1, height+1)).
		Render(inner)
}

// pipelineStatusLine returns the status line showing pass/round info.
func (m ChatModel) pipelineStatusLine() string {
	mode := "manual:off"
	if m.pipelineManualMode {
		mode = "manual:on"
	}
	wait := ""
	if m.pipelineWaitingAdvance {
		wait = "  waiting:" + m.pipelineWaitingAgent
	}
	return fmt.Sprintf("pass %d/%d %s  round %d/%d  %s  %s%s",
		m.pipelineCurrentPass, m.pipelineTotalPasses, m.pipelinePassName,
		m.pipelineCurrentRound, m.pipelineTotalRounds, m.pipelinePhase,
		mode, wait)
}

// pipelineRenderFooter renders the help footer for pipeline mode.
func (m ChatModel) pipelineRenderFooter() string {
	helpText := "↑↓ scroll  ←→ focus  v chat view  p file preview"
	if m.pipelineManualMode {
		helpText += "  space continue  esc cancel"
	}
	if m.pipelinePhase == "done" || m.pipelinePhase == "aborted" {
		helpText = "q quit  v chat view" + helpText
	}
	if m.pipelineFilePreviewVisible {
		helpText = "↑↓ scroll  p close preview"
	}
	// Debug: show buffer sizes so we can see if data is flowing
	helpText = fmt.Sprintf("buf:%d/%d  %s", len(m.pipelineWriterBuf), len(m.pipelineAuditorBuf), helpText)
	return lipgloss.NewStyle().
		Background(lipgloss.Color("#161b22")).
		Foreground(lipgloss.Color("#8b949e")).
		Width(m.width).
		Render(helpText)
}

// pipelineBodyHeight returns the height available for the two-pane body.
func (m *ChatModel) pipelineBodyHeight() int {
	if m.height <= 2 {
		return 3
	}
	return max(3, m.height-4)
}

// chatBodyHeight returns the height of the chat body area.
func (m *ChatModel) chatBodyHeight() int {
	if m.height <= 10 {
		return 3
	}
	return max(3, m.height-10)
}

// pipelineScrollFocused scrolls the currently focused pane.
func (m *ChatModel) pipelineScrollFocused(delta int) {
	if m.pipelineFocusRight {
		m.pipelineAuditorScroll = clamp(m.pipelineAuditorScroll+delta, 0, m.pipelineAuditorMaxScroll())
		return
	}
	m.pipelineWriterScroll = clamp(m.pipelineWriterScroll+delta, 0, m.pipelineWriterMaxScroll())
}

// pipelineSetFocusedScroll sets the scroll position of the focused pane.
func (m *ChatModel) pipelineSetFocusedScroll(pos int) {
	if m.pipelineFocusRight {
		m.pipelineAuditorScroll = clamp(pos, 0, m.pipelineAuditorMaxScroll())
		return
	}
	m.pipelineWriterScroll = clamp(pos, 0, m.pipelineWriterMaxScroll())
}

// pipelineFocusedMaxScroll returns max scroll for the focused pane.
func (m *ChatModel) pipelineFocusedMaxScroll() int {
	if m.pipelineFocusRight {
		return m.pipelineAuditorMaxScroll()
	}
	return m.pipelineWriterMaxScroll()
}

// pipelineWriterMaxScroll returns max scroll for the writer pane.
func (m *ChatModel) pipelineWriterMaxScroll() int {
	width := max(20, (m.width-1)/2) - 4
	return max(0, len(wrapPlain(foldForDisplay(m.pipelineWriterBuf), max(1, width)))-m.pipelineBodyHeight())
}

// pipelineAuditorMaxScroll returns max scroll for the auditor pane.
func (m *ChatModel) pipelineAuditorMaxScroll() int {
	leftWidth := max(20, (m.width-1)/2)
	rightWidth := max(20, m.width-leftWidth) - 4
	return max(0, len(wrapPlain(foldForDisplay(m.pipelineAuditorBuf), max(1, rightWidth)))-m.pipelineBodyHeight())
}

// appendPipelineText appends text to a pipeline buffer with a turn gap.
func appendPipelineText(buf string, turnGap *bool, text string) string {
	if *turnGap {
		buf += "\n<─────────────────────────>\n"
		*turnGap = false
	}
	return buf + text
}

// containsNullByte returns true if data contains a null byte (binary detection).
func containsNullByte(data []byte) bool {
	for _, b := range data {
		if b == 0 {
			return true
		}
	}
	return false
}

// pipelineMaxScroll returns max scroll offset for a content buffer.
// The bodyHeight accounts for viewport size. For the pipeline view we let
// content scroll freely.
func pipelineMaxScroll(contentLen, bodyHeight, totalPasses int) int {
	_ = totalPasses
	// If content fits in viewport, no scrolling needed.
	if contentLen <= bodyHeight {
		return 0
	}
	return contentLen - bodyHeight
}
