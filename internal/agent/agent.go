package agent

import (
	"context"
	"fmt"
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
}

func (a *Agent) Run(ctx context.Context, userMessage string) error {
	a.history = append(a.history, llm.Message{Role: llm.RoleUser, Content: userMessage})
	turnStart := time.Now()
	defer func() {
		a.renderer.Stats(time.Since(turnStart), a.getUsage())
	}()

	for turn := 0; turn < a.maxTurns; turn++ {
		// Compress history if growing too large (~100K chars ≈ ~25K tokens)
		a.compressHistory(100000)

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
					if !inCodeFence && strings.Contains(trimmed, "<tool_call>") {
						inToolCall = true
						continue
					}
					if !inCodeFence && strings.Contains(trimmed, "</tool_call>") {
						inToolCall = false
						continue
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
			if !strings.Contains(trimmed, "<tool_call>") && !strings.Contains(trimmed, "</tool_call>") {
				a.renderer.AgentToken(remaining)
			}
		}

		if err := <-errCh; err != nil {
			a.renderer.Error(err.Error())
			return err
		}

		response := sb.String()

		// Parse tool calls
		calls, _ := ParseToolCalls(response)

		// No tool calls — final answer
		if len(calls) == 0 {
			a.history = append(a.history, llm.Message{Role: llm.RoleAssistant, Content: response})
			return nil
		}

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

		// Append to history
		a.history = append(a.history, llm.Message{Role: llm.RoleAssistant, Content: response})

		toolResultContent := strings.Join(results, "\n\n")
		if len(toolResultContent) > 30*1024 {
			toolResultContent = toolResultContent[:30*1024] + "\n... (truncated)"
		}
		a.history = append(a.history, llm.Message{Role: llm.RoleUser, Content: "Tool results:\n\n" + toolResultContent})
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

// estimateTokens returns a rough token count (~4 chars per token).
func estimateTokens(text string) int {
	return (len(text) + 3) / 4
}

// compressHistory replaces old tool results with one-line summaries
// when total content exceeds the threshold.
func (a *Agent) compressHistory(charThreshold int) {
	total := 0
	for _, m := range a.history {
		total += len(m.Content)
	}
	if total <= charThreshold {
		return
	}

	// Keep the most recent 4 messages intact
	preserve := 4
	if preserve > len(a.history) {
		preserve = len(a.history)
	}
	cutoff := len(a.history) - preserve

	for i := 0; i < cutoff; i++ {
		m := &a.history[i]
		if m.Role == llm.RoleUser && strings.HasPrefix(m.Content, "Tool results:") {
			lines := strings.Split(m.Content, "\n")
			var summary []string
			for _, line := range lines {
				if strings.HasPrefix(line, "[") {
					bracket := strings.Index(line, "]")
					if bracket > 0 && len(line) > bracket+2 {
						toolName := line[1:bracket]
						summary = append(summary, fmt.Sprintf("[%s: result truncated]", toolName))
					}
				}
			}
			if len(summary) > 0 {
				m.Content = "Tool results (summarized):\n" + strings.Join(summary, "\n")
			}
		}
	}
}
