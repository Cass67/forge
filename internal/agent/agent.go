package agent

import (
	"context"
	"fmt"
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
	driver           llm.Driver
	tools            *tools.Registry
	approve          tools.ApprovalFunc
	history          []llm.Message
	system           string
	systemOverride   bool // true when SetSystem was called; suppresses rebuild
	workDir          string
	maxTurns         int
	renderer         RenderTarget
	skills           []skills.Skill
	state            *chatstate.State
	isSubAgent       bool
	lastFullResponse string
	role             string
	mu               sync.Mutex
	activeSubCancel  context.CancelFunc
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
	if resetter, ok := a.driver.(llm.ConversationResetter); ok {
		resetter.ResetConversation()
	}
}

func (a *Agent) Run(ctx context.Context, userMessage string) error {
	a.history = append(a.history, llm.Message{Role: llm.RoleUser, Content: userMessage})
	turnStart := time.Now()
	actionPreambleRetries := 0
	dispatchDirectAnswerRetries := 0
	sawToolCallThisRun := false
	lastDispatchDelegateRole := ""
	lastDispatchDelegateBlocked := false
	dispatchReadOnlyRolesSinceBuilder := make(map[string]bool)
	dispatchLastDelegateResult := make(map[string]string)
	dispatchLastScratchpadRead := ""
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
		if a.role == "scout" && len(calls) > 0 && strings.TrimSpace(visibleText) != "" {
			if turn+1 < a.maxTurns {
				a.history = append(a.history, llm.Message{
					Role:    llm.RoleUser,
					Content: scoutNudgeMessage(),
				})
				continue
			}
		}

		// No tool calls — final answer, or stalled narration.
		if len(calls) == 0 {
			a.lastFullResponse = response
			if a.role == "dispatch" && !sawToolCallThisRun {
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
			if a.role == "dispatch" {
				return nil
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
		sawToolCallThisRun = true

		// Execute tool calls
		var results []string
		for _, call := range calls {
			if a.role == "dispatch" && call.Name == "delegate" {
				role, _ := call.Args["role"].(string)
				role = strings.TrimSpace(role)
				task, _ := call.Args["task"].(string)
				if role != "" && role == lastDispatchDelegateRole && !lastDispatchDelegateBlocked {
					results = append(results, fmt.Sprintf("[delegate] error: dispatch cannot delegate to %s twice in a row", role))
					continue
				}
				if dispatchRoleRequiresFreshState(role) && dispatchReadOnlyRolesSinceBuilder[role] {
					results = append(results, fmt.Sprintf("[delegate] error: dispatch already delegated to %s in this pass; use that result or delegate to builder", role))
					continue
				}
				if dispatchScoutMustStayEvidenceOnly(role, task) {
					results = append(results, "[delegate] error: repo-review and improvement requests must use scout for evidence gathering only, then architect for recommendations")
					continue
				}
				if dispatchBuilderPresentationShimForbidden(role, task, dispatchLastDelegateResult) {
					results = append(results, "[delegate] error: repo-review presentation must end after architect synthesis; do not delegate to builder just to format the answer")
					continue
				}
				if enriched := enrichDispatchDelegateTask(role, task, dispatchLastDelegateResult, dispatchLastScratchpadRead); enriched != task {
					task = enriched
					call.Args["task"] = task
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
			if err != nil {
				result = fmt.Sprintf("error: %v", err)
				a.renderer.ToolResult(call.Name, result, diff, true)
			} else {
				displayResult := truncateResult(result)
				if call.Name == "delegate" {
					displayResult = result
					if a.role == "dispatch" {
						role, _ := call.Args["role"].(string)
						lastDispatchDelegateRole = strings.TrimSpace(role)
						lastDispatchDelegateBlocked = delegateResultBlocked(result)
						dispatchLastDelegateResult[lastDispatchDelegateRole] = result
						if delegateResultCompleted(result) && !lastDispatchDelegateBlocked {
							task, _ := call.Args["task"].(string)
							if dispatchRepoReviewArchitectSynthesisTask(task) {
								dispatchStopAfterTurn = true
							}
							if role == "builder" {
								clear(dispatchReadOnlyRolesSinceBuilder)
							} else if dispatchRoleRequiresFreshState(role) {
								dispatchReadOnlyRolesSinceBuilder[role] = true
							}
						}
					}
				}
				if a.role == "dispatch" && call.Name == "scratchpad_read" {
					dispatchLastScratchpadRead = result
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

func dispatchRoleRequiresFreshState(role string) bool {
	switch strings.TrimSpace(role) {
	case "scout", "architect", "doctor":
		return true
	default:
		return false
	}
}

func dispatchScoutMustStayEvidenceOnly(role, task string) bool {
	if strings.TrimSpace(role) != "scout" {
		return false
	}
	lower := strings.ToLower(task)
	repoSignals := []string{
		"repo",
		"repository",
		"codebase",
		"project structure",
		"maintainability",
		"code quality",
		"documentation",
		"testing",
	}
	recommendationSignals := []string{
		"improvement opportunit",
		"recommended improvement",
		"recommend improvements",
		"suggest improvements",
		"prioritized recommendations",
		"recommended changes",
		"improvement list",
	}
	return containsAny(lower, repoSignals) && containsAny(lower, recommendationSignals)
}

func dispatchBuilderPresentationShimForbidden(role, task string, delegateResults map[string]string) bool {
	if strings.TrimSpace(role) != "builder" {
		return false
	}
	if strings.TrimSpace(delegateResults["architect"]) == "" {
		return false
	}
	lower := strings.ToLower(task)
	presentationSignals := []string{
		"present the architect",
		"user-facing response",
		"final concise review answer",
		"format the review",
		"package the response",
		"present recommendations",
	}
	repoReviewSignals := []string{
		"repo review",
		"repository improvement",
		"improvement recommendations",
	}
	return containsAny(lower, presentationSignals) && containsAny(lower, repoReviewSignals)
}

func dispatchRepoReviewArchitectSynthesisTask(task string) bool {
	lower := strings.ToLower(task)
	repoReviewSignals := []string{
		"repo review",
		"repository review",
		"repository improvement recommendations",
		"improvement recommendations for the user",
		"improvement recommendations",
		"prioritized recommendations",
		"synthesize the repo review",
		"synthesize repository review",
	}
	return containsAny(lower, repoReviewSignals)
}

func enrichDispatchDelegateTask(role, task string, delegateResults map[string]string, scratchpadResult string) string {
	if strings.TrimSpace(role) != "architect" {
		return task
	}
	var contextParts []string
	if scout := strings.TrimSpace(delegateResults["scout"]); scout != "" && !strings.Contains(task, scout) {
		contextParts = append(contextParts, "SCOUT FINDINGS:\n"+scout)
	}
	if scratch := strings.TrimSpace(scratchpadResult); scratch != "" && !strings.Contains(task, scratch) {
		contextParts = append(contextParts, "SCRATCHPAD CONTEXT:\n"+scratch)
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

func delegateResultCompleted(result string) bool {
	trimmed := strings.TrimSpace(result)
	if trimmed == "" {
		return false
	}
	upper := strings.ToUpper(trimmed)
	if strings.HasPrefix(upper, "CANCELLED:") || strings.HasPrefix(upper, "AGENT ERROR") {
		return false
	}
	return true
}

func delegateResultBlocked(result string) bool {
	trimmed := strings.TrimSpace(strings.ToLower(result))
	if trimmed == "" {
		return false
	}
	blockedSignals := []string{
		"i don't have",
		"i dont have",
		"do not have access",
		"don't have access",
		"missing context",
		"need more context",
		"paste the contents",
		"paste the evidence",
		"constrained not to gather new evidence",
		"blocked",
		"incomplete",
	}
	return containsAny(trimmed, blockedSignals)
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
	// Normalize smart quotes/apostrophes to ASCII.
	trimmed = strings.NewReplacer("\u2018", "'", "\u2019", "'", "\u201c", "\"", "\u201d", "\"").Replace(trimmed)
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
