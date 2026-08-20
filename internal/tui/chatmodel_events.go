package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"forge/internal/llm"

	tea "github.com/charmbracelet/bubbletea"
)

func (m ChatModel) handleLLMEvent(ev llm.Event) (tea.Model, tea.Cmd) {
	// Sub-agent events primarily render in the tools pane, with human-readable
	// prose mirrored into the main transcript.
	if ev.SubAgent != "" {
		return m.handleSubAgentEvent(ev)
	}

	switch ev.Kind {
	case llm.EventAgentTask:
		return m.handleAgentTaskEvent(ev)
	case llm.EventToken:
		if token := sanitizeAssistantTokenForDisplay(ev.Text); token != "" {
			if m.liveStatsStartedAt.IsZero() {
				m.liveStatsStartedAt = time.Now()
			}
			m.liveStatsOutputChars += len(token)
			m.AppendToLastAgentLabeled(token, ev.Agent)
		}
	case llm.EventReasoning:
		m.AppendReasoning(ev.Text)
	case llm.EventRetry:
		m.discardPendingAssistantMessage()
		if msg := strings.TrimSpace(ev.Text); msg != "" {
			m.AddWorkingMessage(msg)
		}
	case llm.EventToolCall:
		assistantLive := m.hasLiveAssistantMessage()
		if ev.Agent != "runtime" {
			m.markLastAssistantRecordFinal()
		}
		if ev.Agent == "runtime" {
			m.AddWorkingMessage(ev.Text)
			return m, nil
		}
		if strings.TrimSpace(ev.Agent) != "" {
			m.lastToolSummary[strings.TrimSpace(ev.Agent)] = strings.TrimSpace(ev.Text)
		}
		if !assistantLive {
			if m.shouldPersistToolCallCheckpoint(ev) {
				if key, checkpoint := m.toolCallCheckpoint(ev); checkpoint != "" {
					m.emitProgressCheckpoint(key, checkpoint)
				}
			}
			if line := m.toolCallProgressLine(ev); line != "" {
				m.UpdateRecentActivity("", line)
			}
		}
		if !m.debugEnabled {
			return m, nil
		}
		sec := m.currentToolsSection("")
		if sec.buf != "" && !strings.HasSuffix(sec.buf, "\n\n") {
			sec.buf += "\n"
		}
		m.appendTools("", "────────────────────────\n")
		m.appendTools("", fmt.Sprintf("● %s\n", ev.Agent))
		m.appendTools("", fmt.Sprintf("  %s\n", ev.Text))
	case llm.EventToolResult:
		if ev.Agent == "__task_context" {
			if !ev.IsError {
				m.upsertTaskContextMessage(ev.Text)
			}
			return m, nil
		}
		if ev.Agent == "__validation" {
			if text := strings.TrimSpace(ev.Text); text != "" {
				m.AddMessage(ChatMessage{Kind: MsgStatus, Content: text})
			}
			return m, nil
		}
		if ev.Agent == "spawn_agent" && !ev.IsError {
			var spawnResult struct {
				Role string `json:"role"`
			}
			role := ""
			if json.Unmarshal([]byte(ev.Text), &spawnResult) == nil && spawnResult.Role != "" {
				role = spawnResult.Role
			} else if json.Unmarshal([]byte(ev.Content), &spawnResult) == nil && spawnResult.Role != "" {
				role = spawnResult.Role
			}
			if role != "" {
				m.AddMessage(ChatMessage{
					Kind:    MsgStatus,
					Content: fmt.Sprintf("delegating to %s", displayAgentLabel(role)),
				})
			}
			return m, nil
		}
		if ev.Agent == "update_plan" && !ev.IsError {
			m.upsertPlanMessage(ev.Text)
		}
		// Track tool call for cards display
		m.trackToolCall(ev)
		if diff := strings.TrimSpace(ev.Content); !ev.IsError && diff != "" && looksLikeDiff(diff) {
			m.AddMessage(ChatMessage{
				Kind:    MsgAgent,
				Header:  strings.TrimSpace(ev.Agent),
				Content: "```diff\n" + compactDiffForDisplay(diff, 30) + "\n```",
			})
		}
		if ev.Content != "" {
			m.lastToolResult = ev.Content
		} else if ev.Text != "" {
			m.lastToolResult = ev.Text
		}
		if key, checkpoint := m.toolResultCheckpoint(ev); checkpoint != "" && !m.hasLiveAssistantMessage() {
			m.emitProgressCheckpoint(key, checkpoint)
		}
		if m.debugEnabled {
			if line := m.toolResultProgressLine(ev); line != "" {
				m.UpdateRecentActivity("", line)
			}
		}
		if status, key := runtimeStatusMessage(ev); status != "" {
			m.upsertStatusMessage(key, status)
			m.flash = status
		}
		if ev.Agent == "delegate" {
			state := m.delegateResultState()
			label := "Agent"
			if state.role != "" {
				label = displayAgentLabel(state.role)
			}
			result := ""
			if !ev.IsError && !state.transcriptVisible {
				result = selectDelegateTranscript(ev.Text, ev.Content)
			}
			m.clearWorkingMessage()
			if !ev.IsError {
				if result := strings.TrimSpace(result); result != "" {
					stamp := time.Now().Format("15:04:05")
					m.AddMessage(ChatMessage{
						Kind:    MsgAgent,
						Header:  label + " • " + stamp,
						Content: result,
					})
				}
			} else if status := compactStatusText(ev.Text); status != "" {
				m.AddMessage(ChatMessage{
					Kind:    MsgStatus,
					Content: "status: " + status,
				})
			}
			m.pendingSubAgentSummary = nil
		}
		if !m.debugEnabled {
			if ev.IsError && !isRecoverableToolFeedback(ev) {
				m.AddMessage(ChatMessage{
					Kind:    MsgStatus,
					Content: "Error: " + compactStatusText(ev.Text),
				})
			}
			return m, nil
		}
		if ev.IsError {
			m.appendTools("", fmt.Sprintf("  status: ✗ %s\n", ev.Text))
		} else if ev.Content != "" {
			truncated := truncate(ev.Content, 200)
			m.appendTools("", fmt.Sprintf("  status: ✓\n  %s\n", truncated))
		} else {
			m.appendTools("", fmt.Sprintf("  status: ✓ %s\n", truncate(ev.Text, 200)))
		}
	case llm.EventRoundStart:
		m.appendTools("", fmt.Sprintf("\n── round %d ──\n", ev.Round))
	case llm.EventDone:
		m.busy = false
		m.activeSubAgent = ""
		m.pendingQueuedInput = nil
		m.finalizeLiveProgressRecord()
		m.markLastAssistantRecordFinal()
		m.resetProgressCheckpointState()
		m.status = "ready"
		m.syncStatusData()
		if len(m.toolsSections) > 0 {
			m.appendTools("", "status: complete\n")
		}
	case llm.EventError:
		m.busy = false
		m.pendingQueuedInput = nil
		m.finalizeLiveProgressRecord()
		m.markLastAssistantRecordFinal()
		m.resetProgressCheckpointState()
		m.status = "error"
		m.syncStatusData()
		errMsg := eventErrorMessage(ev)
		m.appendTools("", fmt.Sprintf("  ✗ %s\n", errMsg))
		m.flash = "error: " + errMsg
		m.AddMessage(ChatMessage{
			Kind:    MsgStatus,
			Content: "Error: " + errMsg,
		})
	case llm.EventAbort:
		m.busy = false
		m.activeSubAgent = ""
		m.pendingQueuedInput = nil
		m.finalizeLiveProgressRecord()
		m.markLastAssistantRecordFinal()
		m.resetProgressCheckpointState()
		m.status = "ready"
		m.syncStatusData()
	case llm.EventStats:
		m.statsDuration = ev.Duration
		m.statsUsage = ev.Usage
		if ev.ContextUsed > 0 {
			m.statusData.ContextUsed = ev.ContextUsed
			m.statusData.ContextEstimated = ev.ContextEstimated
		}
		if ev.ContextLimit > 0 {
			m.statusData.ContextLimit = ev.ContextLimit
		}
		m.liveStatsStartedAt = time.Time{}
		m.liveStatsOutputChars = 0
		m.sessionUsage.InputTokens += ev.Usage.InputTokens
		m.sessionUsage.OutputTokens += ev.Usage.OutputTokens
		m.syncStatusData()
		m.resizeChatViewport()
		if !m.debugEnabled {
			return m, m.beginProviderDiagnosticsFetch(false)
		}
		if ev.Duration > 0 {
			m.appendTools("", fmt.Sprintf("  %.1fs", ev.Duration.Seconds()))
			if ev.Usage.InputTokens > 0 {
				m.appendTools("", fmt.Sprintf(" • %d in / %d out", ev.Usage.InputTokens, ev.Usage.OutputTokens))
			}
			m.appendTools("", "\n")
		}
		return m, m.beginProviderDiagnosticsFetch(false)
	case llm.EventProgress:
		if strings.Contains(strings.ToLower(ev.Text), "applying") && strings.Contains(strings.ToLower(ev.Text), "queued input") {
			m.pendingQueuedInput = nil
		}
		if line := m.progressEventLine(ev); line != "" {
			if isCompactionProgressLine(line) {
				m.emitProgressCheckpoint("compaction:"+line, line)
			}
			m.UpdateRecentActivity(ev.Agent, line)
		}
	}
	// Auto-scroll tools pane when content is added.
	if ev.Kind == llm.EventToolCall || ev.Kind == llm.EventToolResult || ev.Kind == llm.EventStats {
		m.toolsScroll = m.toolsMaxScroll()
	}
	return m, nil
}

func (m ChatModel) handleAgentTaskEvent(ev llm.Event) (tea.Model, tea.Cmd) {
	payload := strings.TrimSpace(ev.Content)
	if payload == "" {
		payload = strings.TrimSpace(ev.Text)
	}
	if payload == "" {
		return m, nil
	}
	var task chatAgentTaskState
	if err := json.Unmarshal([]byte(payload), &task); err != nil || strings.TrimSpace(task.ID) == "" {
		return m, nil
	}
	m.upsertAgentTaskState(task)
	if !m.toolsVisible && !m.agentPanelHiddenByUser {
		m.toolsVisible = true
		m.toolsWasShowing = true
		m.resizeChatViewport()
		m.viewportDirty = true
	}
	m.toolsScroll = m.toolsMaxScroll()
	return m, nil
}

func (m *ChatModel) upsertAgentTaskState(task chatAgentTaskState) {
	id := strings.TrimSpace(task.ID)
	if id == "" {
		return
	}
	task.ID = id
	for i := range m.agentTasks {
		if strings.TrimSpace(m.agentTasks[i].ID) == id {
			m.agentTasks[i] = mergeChatAgentTaskState(m.agentTasks[i], task)
			return
		}
	}
	m.agentTasks = append(m.agentTasks, task)
}

func mergeChatAgentTaskState(existing, next chatAgentTaskState) chatAgentTaskState {
	merged := existing
	merged.ID = strings.TrimSpace(next.ID)
	if role := strings.TrimSpace(next.Role); role != "" {
		merged.Role = role
	}
	if status := strings.TrimSpace(next.Status); status != "" {
		merged.Status = status
	}
	if !next.CreatedAt.IsZero() {
		merged.CreatedAt = next.CreatedAt
	}
	if !next.StartedAt.IsZero() {
		merged.StartedAt = next.StartedAt
	}
	if !next.CompletedAt.IsZero() {
		merged.CompletedAt = next.CompletedAt
	}
	if !next.LastActivityAt.IsZero() {
		merged.LastActivityAt = next.LastActivityAt
	}
	if result := strings.TrimSpace(next.Result); result != "" {
		merged.Result = result
	}
	if errText := strings.TrimSpace(next.Error); errText != "" {
		merged.Error = errText
	}
	if tool := strings.TrimSpace(next.LastToolName); tool != "" {
		merged.LastToolName = tool
	}
	if len(next.RecentActivity) > 0 {
		merged.RecentActivity = append([]chatAgentTaskActivity(nil), next.RecentActivity...)
	}
	return merged
}

// handleSubAgentEvent routes all sub-agent activity to the tools pane with
// the agent role as a visible header. Human-readable prose is also mirrored
// into the main chat transcript for a transcript-first experience.
func (m ChatModel) handleSubAgentEvent(ev llm.Event) (tea.Model, tea.Cmd) {
	label := ev.SubAgent
	m.activeSubAgent = label
	if strings.TrimSpace(label) != "" && !m.toolsVisible && !m.agentPanelHiddenByUser {
		m.toolsVisible = true
		m.toolsWasShowing = true
		m.resizeChatViewport()
		m.viewportDirty = true
	}

	// Detect start/done/cancelled lifecycle messages from the sub-agent renderer.
	if ev.Kind == llm.EventToolCall && ev.Agent == "runtime" {
		if strings.Contains(ev.Text, "] starting") {
			m.toolsSections = append(m.toolsSections, toolsSection{role: label})
			sec := &m.toolsSections[len(m.toolsSections)-1]
			sec.buf = fmt.Sprintf("┌─ %s ─────────────────\n", label)
			m.status = label
			m.toolsScroll = m.toolsMaxScroll()
			return m, nil
		}
		if strings.Contains(ev.Text, "] done") || strings.Contains(ev.Text, "] cancelled") {
			for i := len(m.toolsSections) - 1; i >= 0; i-- {
				if m.toolsSections[i].role == label {
					sec := &m.toolsSections[i]
					status := "complete"
					if strings.Contains(ev.Text, "cancelled") {
						status = "cancelled"
					}
					sec.buf += fmt.Sprintf("└─ %s %s ────────\n\n", label, status)
					sec.summary = fmt.Sprintf("─ %s (%d turns, %d tools) %s ─\n", label, sec.turnCount, sec.toolCount, status)
					sec.collapsed = true
					m.pendingSubAgentSummary = &subAgentSummary{
						role:              label,
						turns:             sec.turnCount,
						tools:             sec.toolCount,
						transcriptVisible: sec.transcriptVisible,
					}
					break
				}
			}
			m.activeSubAgent = ""
			m.status = "running"
			m.toolsScroll = m.toolsMaxScroll()
			return m, nil
		}
	}

	switch ev.Kind {
	case llm.EventToken:
		sec := m.currentToolsSection(label)
		sec.tokenRun += ev.Text
		m.appendTools(label, ev.Text)
		if strings.TrimSpace(sec.tokenRun) != "" && !looksLikeStructuredTranscript(sec.tokenRun) {
			m.AppendToLastAgentLabeled(ev.Text, label)
			sec.transcriptVisible = true
		}
	case llm.EventToolCall:
		sec := m.currentToolsSection(label)
		sec.tokenRun = ""
		if sec.buf != "" && !strings.HasSuffix(sec.buf, "\n\n") {
			sec.buf += "\n"
		}
		m.appendTools(label, fmt.Sprintf("  │ %s › %s\n", label, ev.Agent))
		m.appendTools(label, fmt.Sprintf("  │   %s\n", ev.Text))
		sec.toolCount++
	case llm.EventToolResult:
		sec := m.currentToolsSection(label)
		sec.tokenRun = ""
		if ev.IsError {
			m.appendTools(label, fmt.Sprintf("  │   ✗ %s\n", truncate(ev.Text, 200)))
		} else {
			m.appendTools(label, fmt.Sprintf("  │   ✓ %s\n", truncate(ev.Text, 200)))
		}
	case llm.EventProgress:
		if line := strings.TrimSpace(ev.Text); line != "" {
			m.UpdateRecentActivity(label, line)
		}
	case llm.EventStats:
		sec := m.currentToolsSection(label)
		sec.tokenRun = ""
		m.sessionUsage.InputTokens += ev.Usage.InputTokens
		m.sessionUsage.OutputTokens += ev.Usage.OutputTokens
		if ev.Duration > 0 {
			m.appendTools(label, fmt.Sprintf("  │ %.1fs", ev.Duration.Seconds()))
			if ev.Usage.InputTokens > 0 {
				m.appendTools(label, fmt.Sprintf(" • %d in / %d out", ev.Usage.InputTokens, ev.Usage.OutputTokens))
			}
			m.appendTools(label, "\n")
		}
		for i := len(m.toolsSections) - 1; i >= 0; i-- {
			if m.toolsSections[i].role == label {
				m.toolsSections[i].turnCount++
				break
			}
		}
	case llm.EventError:
		sec := m.currentToolsSection(label)
		sec.tokenRun = ""
		m.appendTools(label, fmt.Sprintf("  │ ✗ [%s] %s\n", label, ev.Text))
	}
	// Auto-scroll tools pane to follow new output.
	m.toolsScroll = m.toolsMaxScroll()
	return m, nil
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "…"
}

func runtimeStatusMessage(ev llm.Event) (string, string) {
	if status := previewStatusMessage(ev); status != "" {
		return status, ""
	}
	return commandSessionStatusMessage(ev)
}

func previewStatusMessage(ev llm.Event) string {
	if ev.IsError {
		return ""
	}
	switch ev.Agent {
	case "preview_server_ensure", "preview_server_status":
	default:
		return ""
	}

	var payload struct {
		Status string `json:"status"`
		URL    string `json:"url"`
		Reused bool   `json:"reused"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(ev.Text)), &payload); err != nil {
		return ""
	}
	if strings.TrimSpace(payload.URL) == "" {
		return ""
	}
	switch ev.Agent {
	case "preview_server_ensure":
		if payload.Reused {
			return "preview still live at " + payload.URL
		}
		return "preview live at " + payload.URL
	case "preview_server_status":
		return "preview status: " + payload.URL
	default:
		return ""
	}
}

func commandSessionStatusMessage(ev llm.Event) (string, string) {
	if ev.IsError {
		return "", ""
	}
	switch ev.Agent {
	case "run_command", "command_status", "exec_session_start", "exec_session_status", "exec_session_write", "exec_session_resize", "exec_session_stop":
	default:
		return "", ""
	}

	var payload struct {
		Status    string `json:"status"`
		SessionID int    `json:"session_id"`
		Command   string `json:"command"`
		Output    string `json:"output"`
		ExitCode  int    `json:"exit_code"`
		PTY       bool   `json:"pty"`
		Cols      int    `json:"cols"`
		Rows      int    `json:"rows"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(ev.Text)), &payload); err != nil {
		return "", ""
	}
	if payload.SessionID == 0 || strings.TrimSpace(payload.Status) == "" {
		return "", ""
	}
	key := fmt.Sprintf("cmd-session-%d", payload.SessionID)
	command := strings.TrimSpace(payload.Command)
	sessionLabel := "command session"
	if payload.PTY || strings.HasPrefix(strings.TrimSpace(ev.Agent), "exec_session_") {
		sessionLabel = "terminal session"
	}
	sizeLabel := ""
	if payload.Cols > 0 && payload.Rows > 0 {
		sizeLabel = fmt.Sprintf("%dx%d", payload.Cols, payload.Rows)
	}
	switch payload.Status {
	case "running":
		if ev.Agent == "exec_session_resize" && sizeLabel != "" {
			if command == "" {
				return fmt.Sprintf("%s %d resized to %s", sessionLabel, payload.SessionID, sizeLabel), key
			}
			return fmt.Sprintf("%s %d resized to %s: %s", sessionLabel, payload.SessionID, sizeLabel, command), key
		}
		output := summarizeCommandSessionOutput(payload.Output)
		if command == "" {
			if output == "" {
				return fmt.Sprintf("%s %d running", sessionLabel, payload.SessionID), key
			}
			return fmt.Sprintf("%s %d running\n  └ %s", sessionLabel, payload.SessionID, output), key
		}
		if output == "" {
			return fmt.Sprintf("%s %d running: %s", sessionLabel, payload.SessionID, command), key
		}
		return fmt.Sprintf("%s %d running: %s\n  └ %s", sessionLabel, payload.SessionID, command, output), key
	case "exited":
		output := summarizeCommandSessionOutput(payload.Output)
		if command == "" {
			if output == "" {
				return fmt.Sprintf("%s %d exited with code %d", sessionLabel, payload.SessionID, payload.ExitCode), key
			}
			return fmt.Sprintf("%s %d exited with code %d\n  └ %s", sessionLabel, payload.SessionID, payload.ExitCode, output), key
		}
		if output == "" {
			return fmt.Sprintf("%s %d exited with code %d: %s", sessionLabel, payload.SessionID, payload.ExitCode, command), key
		}
		return fmt.Sprintf("%s %d exited with code %d: %s\n  └ %s", sessionLabel, payload.SessionID, payload.ExitCode, command, output), key
	default:
		return "", ""
	}
}

func summarizeCommandSessionOutput(output string) string {
	output = strings.TrimSpace(output)
	if output == "" {
		return ""
	}
	lines := strings.Split(output, "\n")
	parts := make([]string, 0, min(2, len(lines)))
	for _, line := range lines {
		line = compactStatusText(line)
		if line == "" {
			continue
		}
		parts = append(parts, truncate(line, 120))
		if len(parts) == 2 {
			break
		}
	}
	if len(parts) == 0 {
		return ""
	}
	summary := strings.Join(parts, " | ")
	if len(lines) > len(parts) {
		summary += fmt.Sprintf(" | … +%d lines", len(lines)-len(parts))
	}
	return summary
}

func latestFencedCodeBlock(content string) string {
	parts := strings.Split(content, "```")
	if len(parts) < 3 {
		return ""
	}
	for i := len(parts) - 2; i >= 1; i -= 2 {
		block := strings.TrimLeft(parts[i], "\n")
		if newline := strings.Index(block, "\n"); newline >= 0 {
			block = block[newline+1:]
		}
		block = strings.TrimSpace(block)
		if block != "" {
			return block
		}
	}
	return ""
}

func (m *ChatModel) resetProviderDiagnostics() {
	m.statusData.CopilotLive = nil
	m.statusData.CodexUsage = nil
	m.statsCopilotLoading = false
	m.statsCodexLoading = false
	m.statsCopilotErr = ""
	m.statsCodexErr = ""
}

func (m *ChatModel) beginProviderDiagnosticsFetch(force bool) tea.Cmd {
	provider := providerFromModel(m.model)
	var cmds []tea.Cmd

	if provider == "copilot" && m.config.FetchLiveCopilotQuota != nil && (force || (m.statusData.CopilotLive == nil && !m.statsCopilotLoading)) {
		m.statsCopilotLoading = true
		if force {
			m.statsCopilotErr = ""
		}
		model := m.model
		fetch := m.config.FetchLiveCopilotQuota
		cmds = append(cmds, func() tea.Msg {
			quota, err := fetch(context.Background())
			return statsCopilotQuotaMsg{model: model, quota: quota, err: err}
		})
	}

	if (provider == "chatgpt" || provider == "openai" || provider == "codex") && m.config.FetchCodexUsage != nil && (force || (m.statusData.CodexUsage == nil && !m.statsCodexLoading)) {
		m.statsCodexLoading = true
		if force {
			m.statsCodexErr = ""
		}
		model := m.model
		fetch := m.config.FetchCodexUsage
		cmds = append(cmds, func() tea.Msg {
			snapshot, err := fetch(context.Background())
			return statsCodexUsageMsg{model: model, snapshot: snapshot, err: err}
		})
	}

	if len(cmds) == 0 {
		return nil
	}
	return tea.Batch(cmds...)
}
