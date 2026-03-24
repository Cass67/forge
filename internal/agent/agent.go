package agent

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"forge/internal/agent/tools"
	"forge/internal/chatstate"
	"forge/internal/llm"
	"forge/internal/skills"
)

type Agent struct {
	driver            llm.Driver
	tools             *tools.Registry
	approve           tools.ApprovalFunc
	history           []llm.Message
	system            string
	systemOverride    bool // true when SetSystem was called; suppresses rebuild
	workDir           string
	maxTurns          int
	renderer          RenderTarget
	skills            []skills.Skill
	state             *chatstate.State
	isSubAgent        bool
	lastFullResponse  string
	role              string
	dispatchResults   map[string]string
	dispatchArtifacts map[string]string
	dispatchScratch   string
	mu                sync.Mutex
	activeSubCancel   context.CancelFunc
}

const targetHistoryTokens = 12000

func NewAgent(driver llm.Driver, toolReg *tools.Registry, approve tools.ApprovalFunc, workDir string, maxTurns int, renderer RenderTarget, loadedSkills []skills.Skill, state *chatstate.State) *Agent {
	if state == nil {
		state = chatstate.New()
	}
	return &Agent{
		driver:            driver,
		tools:             toolReg,
		approve:           approve,
		workDir:           workDir,
		maxTurns:          maxTurns,
		renderer:          renderer,
		system:            BuildSystemPrompt(workDir, toolReg, skills.Describe(loadedSkills)),
		skills:            loadedSkills,
		state:             state,
		dispatchResults:   make(map[string]string),
		dispatchArtifacts: make(map[string]string),
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

// SetSystem replaces the agent's system prompt with a fixed value
// that won't be rebuilt between turns (used for sub-agents with role prompts).
func (a *Agent) SetSystem(system string) {
	a.system = system
	a.systemOverride = true
}

// UseGeneratedSystem clears any fixed system override so future turns rebuild
// the system prompt from the current tool registry and loaded skills.
func (a *Agent) UseGeneratedSystem() {
	a.systemOverride = false
}

// systemPrompt returns the current system prompt, rebuilding it from the
// tool registry when no explicit override is set (picks up tool disclosure changes).
func (a *Agent) systemPrompt() string {
	if a.systemOverride {
		return a.system
	}
	return BuildSystemPrompt(a.workDir, a.tools, skills.Describe(a.skills))
}

// SetTools replaces the agent's tool registry.
func (a *Agent) SetTools(reg *tools.Registry) {
	a.tools = reg
}

func (a *Agent) SetRole(role string) {
	a.role = role
}

func (a *Agent) CancelSubAgent() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.activeSubCancel != nil {
		a.activeSubCancel()
	}
}

func (a *Agent) ClearHistory() {
	a.history = nil
	a.state.Clear()
	clear(a.dispatchResults)
	clear(a.dispatchArtifacts)
	a.dispatchScratch = ""
	if resetter, ok := a.driver.(llm.ConversationResetter); ok {
		resetter.ResetConversation()
	}
}

func (a *Agent) Run(ctx context.Context, userMessage string) error {
	a.history = append(a.history, llm.Message{Role: llm.RoleUser, Content: userMessage})
	turnStart := time.Now()
	actionPreambleRetries := 0
	subAgentMixedProseRetries := 0
	dispatchDirectAnswerRetries := 0
	scoutNoToolRetries := 0
	scoutMalformedToolRetries := 0
	sawToolCallThisRun := false
	dispatchCanStop := false
	dispatchStopAfterTurn := false
	defer func() {
		a.renderer.Stats(time.Since(turnStart), a.getUsage())
	}()

	for turn := 0; turn < a.maxTurns; turn++ {
		// Re-sent full history is expensive; compact only when the estimated request is too large.
		a.enforceHistoryBudget(targetHistoryTokens)

		messages := make([]llm.Message, 0, len(a.history)+1)
		messages = append(messages, llm.Message{Role: llm.RoleSystem, Content: a.systemPrompt()})
		messages = append(messages, a.history...)

		// Stream response
		out := make(chan llm.Token, 64)
		errCh := make(chan error, 1)
		go func() {
			errCh <- a.driver.Stream(ctx, messages, out)
		}()

		var sb strings.Builder
		for tok := range out {
			sb.WriteString(tok.Text)
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
		if a.role == "scout" && containsRawToolMarkup(response) && len(calls) == 0 {
			if turn+1 < a.maxTurns {
				scoutMalformedToolRetries++
				a.history = append(a.history, llm.Message{
					Role:    llm.RoleUser,
					Content: scoutMalformedToolMarkupNudgeMessage(scoutMalformedToolRetries),
				})
				continue
			}
			return fmt.Errorf("scout produced malformed tool markup")
		}
		mixedSubAgentToolCallProse := a.isSubAgent && len(calls) > 0 && strings.TrimSpace(visibleText) != ""
		if mixedSubAgentToolCallProse {
			subAgentMixedProseRetries++
			visibleText = ""
		} else {
			subAgentMixedProseRetries = 0
		}

		// No tool calls — final answer, or stalled narration.
		if len(calls) == 0 {
			a.lastFullResponse = response
			if a.role == "dispatch" {
				if dispatchCanStop {
					return nil
				}
				if turn+1 < a.maxTurns {
					dispatchDirectAnswerRetries++
					a.history = append(a.history, llm.Message{
						Role:    llm.RoleUser,
						Content: dispatchNudgeMessage(dispatchDirectAnswerRetries),
					})
					continue
				}
				return fmt.Errorf("dispatch produced no delegate call before answering")
			}
			if a.role == "scout" && !sawToolCallThisRun && scoutTaskRequiresEvidenceTools(userMessage) {
				if turn+1 < a.maxTurns {
					scoutNoToolRetries++
					a.history = append(a.history, llm.Message{
						Role:    llm.RoleUser,
						Content: scoutEvidenceNudgeMessage(scoutNoToolRetries),
					})
					continue
				}
				return fmt.Errorf("scout produced no evidence-gathering tool call before answering")
			}
			if a.isSubAgent && strings.TrimSpace(response) == "" {
				if turn+1 < a.maxTurns {
					a.history = append(a.history, llm.Message{
						Role:    llm.RoleUser,
						Content: subAgentNoOutputNudgeMessage(a.role),
					})
					continue
				}
				return fmt.Errorf("%s produced no final output", a.role)
			}
			isPreamble := looksLikeActionPreamble(response)
			if !a.isSubAgent && isPreamble && actionPreambleRetries < 4 && turn+1 < a.maxTurns {
				actionPreambleRetries++
				a.history = append(a.history, llm.Message{
					Role:    llm.RoleUser,
					Content: nudgeMessage(actionPreambleRetries),
				})
				continue
			}
			if strings.TrimSpace(visibleText) != "" {
				a.renderer.AgentToken(visibleText)
			}
			a.history = append(a.history, llm.Message{Role: llm.RoleAssistant, Content: response})
			return nil
		}
		actionPreambleRetries = 0
		dispatchDirectAnswerRetries = 0
		scoutNoToolRetries = 0
		scoutMalformedToolRetries = 0
		sawToolCallThisRun = true

		if a.role == "dispatch" && len(calls) > 1 {
			calls = calls[:1]
		}
		scoutFirstTurnMultipleCalls := a.role == "scout" && turn == 0 && scoutTaskRequiresEvidenceTools(userMessage) && len(calls) > 1
		if scoutFirstTurnMultipleCalls {
			calls = calls[:1]
		}

		// Execute tool calls
		var results []string
		callQueue := append([]ToolCall(nil), calls...)
		for idx := 0; idx < len(callQueue); idx++ {
			call := callQueue[idx]
			if a.isSubAgent && subAgentFiltersRuntimeArtifacts(a.role) && !subAgentAllowsRuntimeArtifactInspection(a.role, userMessage) && toolTargetsRuntimeArtifact(call) {
				msg := fmt.Sprintf("[%s] error: %s may not inspect runtime-generated conversation artifacts unless the task explicitly asks for them", call.Name, a.role)
				results = append(results, msg)
				a.renderer.Error(strings.TrimPrefix(msg, "["+call.Name+"] "))
				continue
			}
			if a.role == "dispatch" && call.Name == "delegate" {
				role, _ := call.Args["role"].(string)
				role = strings.TrimSpace(role)
				task, _ := call.Args["task"].(string)
				if enriched := enrichDispatchDelegateTask(role, task, a.dispatchResults, a.dispatchArtifacts, a.dispatchScratch); enriched != task {
					task = enriched
				}
				call.Args["task"] = task
			}
			if a.role == "dispatch" && call.Name == "scratchpad_write" {
				content, _ := call.Args["content"].(string)
				if !dispatchScratchpadWriteAllowed(content, a.dispatchResults, a.dispatchArtifacts, a.dispatchScratch) {
					msg := "[scratchpad_write] error: dispatch may only persist raw delegate or scratchpad content without rewriting it"
					results = append(results, msg)
					a.renderer.Error(strings.TrimPrefix(msg, "[scratchpad_write] "))
					continue
				}
			}
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
			if err == nil && a.isSubAgent && subAgentFiltersRuntimeArtifacts(a.role) && !subAgentAllowsRuntimeArtifactInspection(a.role, userMessage) {
				result = sanitizeRuntimeArtifactToolResult(call.Name, result)
			}
			if call.Name == "delegate" && err == nil {
				role, _ := call.Args["role"].(string)
				if normalized, malformed := normalizeDelegateResult(role, result); malformed {
					result = normalized
				}
			}
			if err != nil {
				result = fmt.Sprintf("error: %v", err)
				a.renderer.ToolResult(call.Name, result, diff, true)
			} else {
				displayResult := truncateResult(result)
				if call.Name == "delegate" {
					outcome := parseDelegateOutcome(result)
					displayResult = outcome.DisplayText()
					result = displayResult
					if a.role == "dispatch" {
						role, _ := call.Args["role"].(string)
						role = strings.TrimSpace(role)
						contextText := outcome.ContextText()
						a.dispatchResults[role] = displayResult
						if outcome.Completed() && contextText != "" {
							a.dispatchArtifacts[role] = contextText
						}
						if outcome.Structured {
							if outcome.Completed() && outcome.NextRole != "" && outcome.NextTask != "" {
								nextTask := enrichDispatchDelegateTask(outcome.NextRole, outcome.NextTask, a.dispatchResults, a.dispatchArtifacts, a.dispatchScratch)
								callQueue = append(callQueue, ToolCall{
									Name: "delegate",
									Args: map[string]any{
										"role":        outcome.NextRole,
										"task":        nextTask,
										"_auto_chain": true,
									},
								})
								dispatchCanStop = false
							} else if outcome.Completed() {
								dispatchStopAfterTurn = true
							}
						} else if outcome.Blocked() {
							dispatchCanStop = false
						} else if outcome.Completed() {
							dispatchCanStop = true
						}
					}
				}
				if a.role == "dispatch" && call.Name == "scratchpad_read" {
					a.dispatchScratch = result
				}
				a.renderer.ToolResult(call.Name, displayResult, diff, false)
			}

			results = append(results, fmt.Sprintf("[%s] %s", call.Name, result))
		}

		// Append compact history entries; preserve UI output separately via the renderer only.
		a.lastFullResponse = visibleText
		assistantText := visibleText
		if a.role == "dispatch" && len(calls) > 0 {
			assistantText = ""
		}
		if assistantSummary := compactAssistantHistory(assistantText); assistantSummary != "" {
			a.history = append(a.history, llm.Message{
				Role:    llm.RoleAssistant,
				Content: assistantSummary,
			})
		}
		a.history = append(a.history, llm.Message{
			Role:    llm.RoleUser,
			Content: compactToolResults(results),
		})
		if mixedSubAgentToolCallProse && turn+1 < a.maxTurns {
			a.history = append(a.history, llm.Message{
				Role:    llm.RoleUser,
				Content: subAgentToolCallNudgeMessage(a.role, subAgentMixedProseRetries),
			})
		}
		if scoutFirstTurnMultipleCalls && turn+1 < a.maxTurns {
			a.history = append(a.history, llm.Message{
				Role:    llm.RoleUser,
				Content: scoutFirstTurnToolCallNudgeMessage(),
			})
		}
		if a.role == "dispatch" && dispatchStopAfterTurn {
			return nil
		}
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
	if role, ok := call.Args["role"].(string); ok && call.Name == "delegate" {
		task, _ := call.Args["task"].(string)
		if len(task) > 80 {
			task = task[:80] + "..."
		}
		return fmt.Sprintf("→ %s: %s", role, task)
	}
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

func dispatchScratchpadWriteAllowed(content string, delegateResults map[string]string, delegateArtifacts map[string]string, scratchpadResult string) bool {
	content = strings.TrimSpace(content)
	if content == "" {
		return false
	}
	if strings.TrimSpace(scratchpadResult) == content {
		return true
	}
	for _, result := range delegateResults {
		if strings.TrimSpace(result) == content {
			return true
		}
	}
	for _, artifact := range delegateArtifacts {
		if strings.TrimSpace(artifact) == content {
			return true
		}
	}
	return false
}

func enrichDispatchDelegateTask(role, task string, delegateResults map[string]string, delegateArtifacts map[string]string, scratchpadResult string) string {
	role = strings.TrimSpace(role)
	var contextParts []string
	switch role {
	case "architect":
		scout := strings.TrimSpace(delegateArtifacts["scout"])
		if scout == "" {
			scout = strings.TrimSpace(delegateResults["scout"])
		}
		if scout != "" && !strings.Contains(task, scout) {
			contextParts = append(contextParts, "SCOUT FINDINGS:\n"+scout)
		}
		if scratch := strings.TrimSpace(scratchpadResult); scratch != "" && !strings.Contains(task, scratch) {
			contextParts = append(contextParts, "SCRATCHPAD CONTEXT:\n"+scratch)
		}
	case "builder":
		architect := strings.TrimSpace(delegateArtifacts["architect"])
		if architect == "" {
			architect = strings.TrimSpace(delegateResults["architect"])
		}
		if architect != "" && !strings.Contains(task, architect) {
			contextParts = append(contextParts, "ARCHITECT OUTPUT:\n"+architect)
		}
		if doctor := strings.TrimSpace(delegateArtifacts["doctor"]); doctor != "" && !strings.Contains(task, doctor) {
			contextParts = append(contextParts, "DOCTOR OUTPUT:\n"+doctor)
		}
		if scratch := strings.TrimSpace(scratchpadResult); scratch != "" && !strings.Contains(task, scratch) {
			contextParts = append(contextParts, "SCRATCHPAD CONTEXT:\n"+scratch)
		}
	default:
		return task
	}
	if len(contextParts) == 0 {
		return task
	}
	addition := strings.Join(contextParts, "\n\n")
	if strings.Contains(task, "CONTEXT:") {
		return task + "\n\n" + addition
	}
	return task + "\nCONTEXT: " + addition
}

func containsAny(text string, needles []string) bool {
	for _, needle := range needles {
		if strings.Contains(text, needle) {
			return true
		}
	}
	return false
}

func normalizeDelegateResult(role, result string) (string, bool) {
	if !containsRawToolMarkup(result) {
		return result, false
	}
	_, visible := ParseToolCalls(result)
	msg := fmt.Sprintf("AGENT ERROR (%s): delegate result contained raw tool markup; retry with a narrower task", strings.TrimSpace(role))
	if strings.TrimSpace(visible) != "" {
		msg += "\n\nVisible text:\n" + strings.TrimSpace(visible)
	}
	return msg, true
}

func containsRawToolMarkup(text string) bool {
	for _, opener := range toolCallOpeners {
		if strings.Contains(text, opener) {
			return true
		}
	}
	for _, closer := range toolCallClosers {
		if strings.Contains(text, closer) {
			return true
		}
	}
	return false
}

func normalizePromptText(text string) string {
	return strings.NewReplacer("\u2018", "'", "\u2019", "'", "\u201c", "\"", "\u201d", "\"").Replace(text)
}

func subAgentFiltersRuntimeArtifacts(role string) bool {
	switch strings.TrimSpace(role) {
	case "scout", "architect", "doctor":
		return true
	default:
		return false
	}
}

func subAgentAllowsRuntimeArtifactInspection(role, task string) bool {
	if !subAgentFiltersRuntimeArtifacts(role) {
		return true
	}
	lower := strings.ToLower(normalizePromptText(task))
	return containsAny(lower, []string{
		"debug log",
		"debug file",
		"chat debug",
		"scratchpad",
		"session history",
		"session log",
		"transcript",
		"history.jsonl",
		"jsonl log",
		"inspect the log",
		"check the log",
	})
}

func toolTargetsRuntimeArtifact(call ToolCall) bool {
	switch call.Name {
	case "read_file", "list_dir", "glob":
		if path, _ := call.Args["path"].(string); isRuntimeArtifactPath(path) {
			return true
		}
		if pattern, _ := call.Args["pattern"].(string); isRuntimeArtifactSpecifier(pattern) {
			return true
		}
	case "run_command":
		if cmd, _ := call.Args["command"].(string); isRuntimeArtifactSpecifier(cmd) {
			return true
		}
	}
	return false
}

func sanitizeRuntimeArtifactToolResult(toolName, result string) string {
	switch toolName {
	case "search", "glob", "list_dir", "run_command":
		lines := strings.Split(result, "\n")
		filtered := lines[:0]
		for _, line := range lines {
			if strings.TrimSpace(line) == "" {
				filtered = append(filtered, line)
				continue
			}
			if isRuntimeArtifactLine(line) {
				continue
			}
			filtered = append(filtered, line)
		}
		return strings.Join(filtered, "\n")
	default:
		return result
	}
}

func isRuntimeArtifactLine(line string) bool {
	path := artifactPathFromLine(line)
	if isRuntimeArtifactPath(path) {
		return true
	}
	lower := strings.ToLower(line)
	if strings.HasSuffix(strings.ToLower(filepath.ToSlash(strings.TrimSpace(path))), ".jsonl") &&
		containsAny(lower, []string{"chat.input", "llm.request", "llm.response", "\"sub_agent\"", "\"kind\":\"tool_call\"", "\"kind\":\"tool_result\""}) {
		return true
	}
	return false
}

func artifactPathFromLine(line string) string {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return ""
	}
	if idx := strings.Index(trimmed, ":"); idx >= 0 {
		return strings.TrimSpace(trimmed[:idx])
	}
	return trimmed
}

func isRuntimeArtifactSpecifier(text string) bool {
	lower := strings.ToLower(normalizePromptText(filepath.ToSlash(strings.TrimSpace(text))))
	return containsAny(lower, []string{
		".forge/scratchpad",
		"scratchpad/",
		"history.jsonl",
		"transcript.jsonl",
		"sessions/",
		"session history",
		"session log",
	})
}

func isRuntimeArtifactPath(path string) bool {
	lower := strings.ToLower(normalizePromptText(filepath.ToSlash(strings.TrimSpace(path))))
	lower = strings.TrimPrefix(lower, "./")
	if lower == "" {
		return false
	}
	if strings.HasPrefix(lower, ".forge/scratchpad/") || strings.Contains(lower, "/.forge/scratchpad/") {
		return true
	}
	if strings.HasPrefix(lower, "sessions/") || strings.Contains(lower, "/sessions/") {
		return true
	}
	base := filepath.Base(lower)
	if base == "history.jsonl" || base == "transcript.jsonl" {
		return true
	}
	return false
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
		line := clipForHistory(oneLine(result), 4000)
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
	// Normalize smart quotes/apostrophes to ASCII.
	trimmed = normalizePromptText(trimmed)
	// Phrases that indicate narration when they start the response.
	prefixes := []string{
		"i'm going to",
		"i noticed we need to",
		"next i'll",
		"i'll ",
		"let me ",
		"first,", "first i", "to accomplish", "to do this",
		"based on", "here's my plan", "here's what",
		"looking at", "the next step", "we need to", "we should",
		"i can ", "i need to", "i want to",
		"ok,", "okay,", "sure,", "alright,",
		"to start", "to begin", "my approach",
		"so,", "now,", "now i",
	}
	for _, p := range prefixes {
		if strings.HasPrefix(trimmed, p) {
			return true
		}
	}
	// Phrases that indicate narration anywhere in the response.
	contains := []string{
		"i'll start by", "i'll begin by", "let's start",
		"steps to take", "here is my plan", "here are the steps",
		"i will now", "i will first", "shall i proceed",
		"would you like me to", "should i proceed", "should i continue",
	}
	for _, c := range contains {
		if strings.Contains(trimmed, c) {
			return true
		}
	}
	return false
}

func nudgeMessage(attempt int) string {
	switch attempt {
	case 1:
		return "Continue by acting. Call the next tool now, or give the final answer."
	case 2:
		return "You must call a tool or give a final answer. Do not describe what you plan to do."
	case 3:
		return "STOP NARRATING. Either call a tool right now or say DONE if the task is complete."
	default:
		return "Call a tool now. No more text without a tool call."
	}
}

func scoutNudgeMessage() string {
	return "Scout must not mix visible prose with tool calls. Use tool calls only while gathering evidence, and never ask for pasted outputs."
}

func scoutEvidenceNudgeMessage(attempt int) string {
	switch attempt {
	case 1:
		return "Scout must gather evidence with tools before answering. Call the next search/read tool now, not a search plan."
	case 2:
		return "No blocked scout answer yet. Use the available search/read tools and return findings only after evidence is gathered."
	default:
		return "Call an evidence-gathering tool now. Do not stop with a no-evidence answer while tools are still available."
	}
}

func scoutMalformedToolMarkupNudgeMessage(attempt int) string {
	switch attempt {
	case 1:
		return "Scout emitted malformed tool markup. Return exactly one valid <tool_call>...</tool_call> block and nothing else."
	case 2:
		return "Malformed scout tool call again. Emit one valid tool call only. No prose, no second tool call, no partial wrapper."
	default:
		return "Return one valid tool call block only. No prose."
	}
}

func scoutFirstTurnToolCallNudgeMessage() string {
	return "For evidence-gathering scout tasks, the first working turn must contain exactly one valid tool call."
}

func subAgentToolCallNudgeMessage(role string, attempt int) string {
	if strings.TrimSpace(role) == "scout" {
		return scoutNudgeMessage()
	}
	switch attempt {
	case 1:
		return "Sub-agent must not mix visible prose with tool calls. Use tool calls while working; give plain-text output only when finished."
	case 2:
		return "Do not ask for pasted tool output while also calling tools. Either call the next tool or give the final answer."
	default:
		return "Stop mixing prose with tool calls. Tool calls only until the task is complete."
	}
}

func subAgentNoOutputNudgeMessage(role string) string {
	if strings.TrimSpace(role) == "scout" {
		return "Scout produced no final output after gathering evidence. Either call the next evidence tool or return findings now."
	}
	return "Sub-agent produced no final output. Either call the next tool or return the final plain-text answer now."
}

func dispatchNudgeMessage(attempt int) string {
	switch attempt {
	case 1:
		return "Dispatch must delegate. Call delegate now; do not answer directly."
	case 2:
		return "You are dispatch. Do not analyze or summarize. Emit a delegate tool call now."
	case 3:
		return "STOP. Dispatch cannot answer this itself. Call delegate immediately."
	default:
		return "Delegate now. No direct answer."
	}
}

func scoutTaskRequiresEvidenceTools(task string) bool {
	lower := strings.ToLower(normalizePromptText(task))
	return containsAny(lower, []string{
		"task:",
		"find",
		"search",
		"trace",
		"investigate",
		"identify",
		"where did",
		"where does",
		"come from",
		"origin",
		"why it was sent",
		"triggering condition",
		"repo review",
		"evidence",
		"codebase assessment",
	})
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
	total := estimateTokens(a.systemPrompt())
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
