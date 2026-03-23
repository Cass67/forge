package agent

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"forge/internal/agent/tools"
	"forge/internal/chatstate"
	"forge/internal/llm"
	"forge/internal/skills"
)

type Agent struct {
	driver   llm.Driver
	tools    *tools.Registry
	approve  tools.ApprovalFunc
	history  []llm.Message
	system   string
	workDir  string
	maxTurns int
	renderer RenderTarget
	skills   []skills.Skill
	state    *chatstate.State
}

const targetHistoryTokens = 12000

func NewAgent(driver llm.Driver, toolReg *tools.Registry, approve tools.ApprovalFunc, workDir string, maxTurns int, renderer RenderTarget, loadedSkills []skills.Skill, state *chatstate.State) *Agent {
	if state == nil {
		state = chatstate.New()
	}
	return &Agent{
		driver:   driver,
		tools:    toolReg,
		approve:  approve,
		workDir:  workDir,
		maxTurns: maxTurns,
		renderer: renderer,
		system:   BuildSystemPrompt(workDir, toolReg, skills.Describe(loadedSkills)),
		skills:   loadedSkills,
		state:    state,
	}
}

// InjectSkill prepends a skill's content into the conversation as context.
func (a *Agent) InjectSkill(s skills.Skill) {
	a.state.ActivateSkill(s.Name)
	a.history = append(a.history, llm.Message{
		Role:    llm.RoleUser,
		Content: fmt.Sprintf("[Skill: %s]\n\n%s", s.Name, s.Body),
	})
}

// Skills returns the loaded skills.
func (a *Agent) Skills() []skills.Skill {
	return a.skills
}

func (a *Agent) SetDriver(d llm.Driver) {
	a.driver = d
}

func (a *Agent) ClearHistory() {
	a.history = nil
	a.state.Clear()
	if resetter, ok := a.driver.(llm.ConversationResetter); ok {
		resetter.ResetConversation()
	}
}

func (a *Agent) Run(ctx context.Context, userMessage string) error {
	a.history = append(a.history, llm.Message{Role: llm.RoleUser, Content: userMessage})
	turnStart := time.Now()
	actionPreambleRetries := 0
	defer func() {
		a.renderer.Stats(time.Since(turnStart), a.getUsage())
	}()

	for turn := 0; turn < a.maxTurns; turn++ {
		// Re-sent full history is expensive; compact only when the estimated request is too large.
		a.enforceHistoryBudget(targetHistoryTokens)

		messages := make([]llm.Message, 0, len(a.history)+1)
		messages = append(messages, llm.Message{Role: llm.RoleSystem, Content: a.system})
		messages = append(messages, a.history...)

		// Stream response
		out := make(chan llm.Token, 64)
		errCh := make(chan error, 1)
		go func() {
			errCh <- a.driver.Stream(ctx, messages, out)
		}()

		var sb strings.Builder
		var lineBuf strings.Builder
		inToolCall := false
		inCodeFence := false
		for tok := range out {
			sb.WriteString(tok.Text)

			// Filter tool_call blocks line-by-line.
			// Each complete line is emitted immediately for streaming display.
			for i := 0; i < len(tok.Text); i++ {
				ch := tok.Text[i]
				lineBuf.WriteByte(ch)

				if ch == '\n' {
					line := lineBuf.String()
					lineBuf.Reset()
					trimmed := strings.TrimSpace(line)

					if strings.HasPrefix(trimmed, "```") {
						inCodeFence = !inCodeFence
					}
					if !inCodeFence {
						if openTag, closeTag, ok := toolCallBlockTags(trimmed); ok {
							hasClose := strings.Contains(trimmed, closeTag)
							if strings.Contains(trimmed, openTag) && !hasClose {
								inToolCall = true
							} else if hasClose {
								inToolCall = false
							}
							continue
						}
						if strings.Contains(trimmed, "</tool_call>") || strings.Contains(trimmed, "</function_calls>") {
							inToolCall = false
							continue
						}
					}
					if !inToolCall {
						a.renderer.AgentToken(line)
					}
				}
			}
		}
		// Flush any remaining partial line
		remaining := lineBuf.String()
		if remaining != "" && !inToolCall {
			trimmed := strings.TrimSpace(remaining)
			if !strings.Contains(trimmed, "<tool_call>") && !strings.Contains(trimmed, "</tool_call>") &&
				!strings.Contains(trimmed, "<function_calls>") && !strings.Contains(trimmed, "</function_calls>") {
				a.renderer.AgentToken(remaining)
			}
		}

		if modeReporter, ok := a.driver.(llm.RequestModeReporter); ok {
			if mode := strings.TrimSpace(modeReporter.LastRequestMode()); mode != "" {
				a.renderer.Info("context: " + mode)
			}
		}

		if err := <-errCh; err != nil {
			return err
		}

		response := sb.String()

		// Parse tool calls
		calls, visibleText := ParseToolCalls(response)

		// No tool calls — final answer.
		if len(calls) == 0 {
			if looksLikeActionPreamble(response) && actionPreambleRetries < 2 && turn+1 < a.maxTurns {
				actionPreambleRetries++
				a.history = append(a.history, llm.Message{
					Role:    llm.RoleUser,
					Content: "Continue by acting. Call the next tool now, ask one blocker question, or give the final answer.",
				})
				continue
			}
			a.history = append(a.history, llm.Message{Role: llm.RoleAssistant, Content: response})
			return nil
		}
		actionPreambleRetries = 0

		// Execute tool calls
		var results []string
		for _, call := range calls {
			tool, ok := a.tools.Get(call.Name)
			if !ok {
				result := fmt.Sprintf("error: unknown tool %q", call.Name)
				a.renderer.Error(result)
				results = append(results, fmt.Sprintf("[%s] %s", call.Name, result))
				continue
			}

			a.renderer.ToolCall(call.Name, formatCallSummary(call))

			result, err := tool.Execute(ctx, call.Args)
			var diff string
			if tool.LastDiff != nil {
				diff = tool.LastDiff()
			}
			if err != nil {
				result = fmt.Sprintf("error: %v", err)
				a.renderer.ToolResult(call.Name, result, diff, true)
			} else {
				a.renderer.ToolResult(call.Name, truncateResult(result), diff, false)
			}

			results = append(results, fmt.Sprintf("[%s] %s", call.Name, result))
		}

		// Append compact history entries; preserve UI output separately via the renderer only.
		if assistantSummary := compactAssistantHistory(visibleText); assistantSummary != "" {
			a.history = append(a.history, llm.Message{
				Role:    llm.RoleAssistant,
				Content: assistantSummary,
			})
		}
		a.history = append(a.history, llm.Message{
			Role:    llm.RoleUser,
			Content: compactToolResults(results),
		})
	}

	return fmt.Errorf("max turns (%d) exceeded", a.maxTurns)
}

func (a *Agent) getUsage() llm.Usage {
	if ur, ok := a.driver.(llm.UsageReporter); ok {
		return ur.LastUsage()
	}
	return llm.Usage{}
}

func formatCallSummary(call ToolCall) string {
	if path, ok := call.Args["path"].(string); ok {
		return path
	}
	if cmd, ok := call.Args["command"].(string); ok {
		if len(cmd) > 60 {
			return cmd[:60] + "..."
		}
		return cmd
	}
	if pattern, ok := call.Args["pattern"].(string); ok {
		return pattern
	}
	return ""
}

func truncateResult(result string) string {
	lines := strings.Split(result, "\n")
	if len(lines) > 20 {
		return strings.Join(lines[:20], "\n") + fmt.Sprintf("\n... (%d more lines)", len(lines)-20)
	}
	return result
}

func compactAssistantHistory(visibleText string) string {
	visibleText = strings.TrimSpace(visibleText)
	if visibleText == "" {
		return ""
	}
	return clipForHistory(oneLine(visibleText), 240)
}

func compactToolResults(results []string) string {
	if len(results) == 0 {
		return "Tool results:\n- none"
	}
	lines := make([]string, 0, len(results)+1)
	lines = append(lines, "Tool results:")
	for _, result := range results {
		line := clipForHistory(oneLine(result), 220)
		lines = append(lines, "- "+line)
	}
	return strings.Join(lines, "\n")
}

func oneLine(s string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(s)), " ")
}

func clipForHistory(s string, max int) string {
	s = strings.TrimSpace(s)
	if len(s) <= max {
		return s
	}
	if max <= 3 {
		return s[:max]
	}
	return s[:max-3] + "..."
}

func looksLikeActionPreamble(text string) bool {
	trimmed := strings.TrimSpace(strings.ToLower(text))
	if trimmed == "" {
		return false
	}
	phrases := []string{
		"i'm going to",
		"i’m going to",
		"i noticed we need to",
		"next i'll",
		"next i’ll",
		"i'll ",
		"i’ll ",
		"let me ",
	}
	for _, phrase := range phrases {
		if strings.HasPrefix(trimmed, phrase) {
			return true
		}
	}
	return false
}

// estimateTokens returns a rough token count (~4 chars per token).
func estimateTokens(text string) int {
	return (len(text) + 3) / 4
}

// compressHistory replaces old tool results with one-line summaries
// when total content exceeds the threshold.
func (a *Agent) compressHistory(charThreshold int) {
	a.enforceHistoryBudget((charThreshold + 3) / 4)
}

func (a *Agent) enforceHistoryBudget(tokenBudget int) {
	if a.estimatedRequestTokens() <= tokenBudget {
		return
	}

	// Keep the most recent 3 messages intact.
	preserve := 3
	if preserve > len(a.history) {
		preserve = len(a.history)
	}
	cutoff := len(a.history) - preserve

	type candidate struct {
		idx       int
		current   int
		compacted string
		savings   int
	}

	var candidates []candidate
	for i := 0; i < cutoff; i++ {
		m := a.history[i]
		compacted, ok := compactOldHistoryMessage(m)
		if !ok {
			continue
		}
		current := estimateTokens(m.Content)
		compactedTokens := estimateTokens(compacted)
		if compactedTokens >= current {
			continue
		}
		candidates = append(candidates, candidate{
			idx:       i,
			current:   current,
			compacted: compacted,
			savings:   current - compactedTokens,
		})
	}

	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].savings == candidates[j].savings {
			return candidates[i].idx < candidates[j].idx
		}
		return candidates[i].savings > candidates[j].savings
	})

	for _, c := range candidates {
		if a.estimatedRequestTokens() <= tokenBudget {
			break
		}
		a.history[c.idx].Content = c.compacted
	}
}

func (a *Agent) estimatedRequestTokens() int {
	total := estimateTokens(a.system)
	for _, m := range a.history {
		total += estimateTokens(m.Content)
	}
	return total
}

func compactOldHistoryMessage(m llm.Message) (string, bool) {
	content := strings.TrimSpace(m.Content)
	if content == "" {
		return "", false
	}
	if strings.HasPrefix(content, "[Skill: ") {
		name := content
		if nl := strings.Index(name, "\n"); nl >= 0 {
			name = name[:nl]
		}
		return name, true
	}
	if m.Role == llm.RoleUser && strings.HasPrefix(content, "Tool results:") {
		lines := strings.Split(content, "\n")
		var summary []string
		for _, line := range lines {
			line = strings.TrimSpace(line)
			if line == "" || line == "Tool results:" {
				continue
			}
			summary = append(summary, clipForHistory(oneLine(line), 80))
		}
		if len(summary) == 0 {
			return "Tool results summarized.", true
		}
		if len(summary) > 4 {
			summary = append(summary[:4], "...")
		}
		return "Tool results (summarized): " + strings.Join(summary, " | "), true
	}
	if m.Role == llm.RoleAssistant || m.Role == llm.RoleUser {
		flat := oneLine(content)
		if len(flat) > 240 {
			return clipForHistory(flat, 240), true
		}
	}
	return "", false
}
