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
	dispatchResults  map[string]string
	dispatchScratch  string
	mu               sync.Mutex
	activeSubCancel  context.CancelFunc
}

const targetHistoryTokens = 12000

type dispatchFlowKind string

const (
	dispatchFlowUnknown        dispatchFlowKind = "unknown"
	dispatchFlowSearch         dispatchFlowKind = "search"
	dispatchFlowImplement      dispatchFlowKind = "implement"
	dispatchFlowDebug          dispatchFlowKind = "debug"
	dispatchFlowPlan           dispatchFlowKind = "plan"
	dispatchFlowAssessCodebase dispatchFlowKind = "assess_codebase"
)

type dispatchFlowPhase string

const (
	dispatchPhaseIdle          dispatchFlowPhase = "idle"
	dispatchPhaseNeedContext   dispatchFlowPhase = "need_context"
	dispatchPhaseNeedDiagnosis dispatchFlowPhase = "need_diagnosis"
	dispatchPhaseNeedPlan      dispatchFlowPhase = "need_plan"
	dispatchPhaseNeedEvidence  dispatchFlowPhase = "need_evidence"
	dispatchPhaseNeedSynthesis dispatchFlowPhase = "need_synthesis"
	dispatchPhaseNeedBuild     dispatchFlowPhase = "need_build"
	dispatchPhaseDone          dispatchFlowPhase = "done"
)

func NewAgent(driver llm.Driver, toolReg *tools.Registry, approve tools.ApprovalFunc, workDir string, maxTurns int, renderer RenderTarget, loadedSkills []skills.Skill, state *chatstate.State) *Agent {
	if state == nil {
		state = chatstate.New()
	}
	return &Agent{
		driver:          driver,
		tools:           toolReg,
		approve:         approve,
		workDir:         workDir,
		maxTurns:        maxTurns,
		renderer:        renderer,
		system:          BuildSystemPrompt(workDir, toolReg, skills.Describe(loadedSkills)),
		skills:          loadedSkills,
		state:           state,
		dispatchResults: make(map[string]string),
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
	sawToolCallThisRun := false
	lastDispatchDelegateRole := ""
	lastDispatchDelegateBlocked := false
	dispatchFlowKind, dispatchFlowPhase := classifyDispatchFlow(userMessage)
	if dispatchFlowKind == dispatchFlowUnknown {
		if followUpKind, followUpPhase, ok := classifyDispatchFollowUp(userMessage, a.dispatchResults); ok {
			dispatchFlowKind, dispatchFlowPhase = followUpKind, followUpPhase
		}
	}
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
		sawToolCallThisRun = true

		if a.role == "dispatch" && len(calls) > 1 {
			calls = calls[:1]
		}

		// Execute tool calls
		var results []string
		for _, call := range calls {
			if a.role == "scout" && !scoutAllowsRuntimeArtifactInspection(userMessage) && scoutToolTargetsRuntimeArtifact(call) {
				msg := fmt.Sprintf("[%s] error: scout may not inspect runtime-generated conversation artifacts unless the task explicitly asks for them", call.Name)
				results = append(results, msg)
				a.renderer.Error(strings.TrimPrefix(msg, "["+call.Name+"] "))
				continue
			}
			if a.role == "dispatch" && call.Name == "delegate" {
				role, _ := call.Args["role"].(string)
				role = strings.TrimSpace(role)
				task, _ := call.Args["task"].(string)
				if dispatchFlowKind == dispatchFlowAssessCodebase {
					if canonical, ok := canonicalAssessCodebaseDelegateTask(dispatchFlowPhase, role, userMessage, a.dispatchResults, a.dispatchScratch); ok {
						task = canonical
					}
				}
				if role != "" && role == lastDispatchDelegateRole && !lastDispatchDelegateBlocked {
					msg := fmt.Sprintf("[delegate] error: dispatch cannot delegate to %s twice in a row", role)
					results = append(results, msg)
					a.renderer.Error(strings.TrimPrefix(msg, "[delegate] "))
					continue
				}
				if !dispatchRoleAllowedForFlow(dispatchFlowKind, dispatchFlowPhase, role) {
					msg := dispatchIllegalRoleMessage(dispatchFlowKind, dispatchFlowPhase, role)
					results = append(results, msg)
					a.renderer.Error(strings.TrimPrefix(msg, "[delegate] "))
					continue
				}
				if dispatchFlowKind != dispatchFlowAssessCodebase {
					if enriched := enrichDispatchDelegateTask(role, task, a.dispatchResults, a.dispatchScratch); enriched != task {
						task = enriched
					}
				}
				call.Args["task"] = task
			}
			if a.role == "dispatch" && call.Name == "scratchpad_write" {
				content, _ := call.Args["content"].(string)
				if !dispatchScratchpadWriteAllowed(content, a.dispatchResults, a.dispatchScratch) {
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
			if err == nil && a.role == "scout" && !scoutAllowsRuntimeArtifactInspection(userMessage) {
				result = sanitizeScoutToolResult(call.Name, result)
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
					displayResult = result
					if a.role == "dispatch" {
						role, _ := call.Args["role"].(string)
						lastDispatchDelegateRole = strings.TrimSpace(role)
						lastDispatchDelegateBlocked = delegateResultBlocked(result) || !delegateResultCompleted(result)
						a.dispatchResults[lastDispatchDelegateRole] = result
						if delegateResultCompleted(result) && !lastDispatchDelegateBlocked {
							task, _ := call.Args["task"].(string)
							prevPhase := dispatchFlowPhase
							dispatchFlowPhase = advanceDispatchFlowPhase(dispatchFlowKind, dispatchFlowPhase, role)
							if dispatchFlowKind == dispatchFlowAssessCodebase && role == "scout" && prevPhase == dispatchPhaseNeedEvidence && dispatchFlowPhase == dispatchPhaseNeedSynthesis {
								if persisted, ok := a.writeDispatchScratchpad(ctx, "repo_review_evidence", result); ok {
									results = append(results, persisted)
								}
							}
							if dispatchFlowKind == dispatchFlowAssessCodebase && role == "architect" && dispatchFlowPhase == dispatchPhaseDone {
								if persisted, ok := a.writeDispatchScratchpad(ctx, "repo_review_recommendations", result); ok {
									results = append(results, persisted)
								}
							}
							if shouldStopDispatchFlow(dispatchFlowKind, dispatchFlowPhase) {
								dispatchStopAfterTurn = true
							}
							_ = task
						}
					}
				}
				if a.role == "dispatch" && call.Name == "scratchpad_read" {
					a.dispatchScratch = result
					if dispatchFlowKind == dispatchFlowAssessCodebase && dispatchFlowPhase == dispatchPhaseNeedEvidence && dispatchRepoReviewEvidenceUsable(result) {
						dispatchFlowPhase = dispatchPhaseNeedSynthesis
					}
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
		if a.role == "dispatch" && dispatchStopAfterTurn {
			return nil
		}
	}

	return fmt.Errorf("max turns (%d) exceeded", a.maxTurns)
}

func (a *Agent) writeDispatchScratchpad(ctx context.Context, topic, content string) (string, bool) {
	if strings.TrimSpace(topic) == "" || strings.TrimSpace(content) == "" {
		return "", false
	}
	tool, ok := a.tools.Get("scratchpad_write")
	if !ok {
		return "", false
	}
	args := map[string]any{
		"topic":   topic,
		"content": content,
	}
	a.renderer.ToolCall("scratchpad_write", formatCallSummary(ToolCall{
		Name: "scratchpad_write",
		Args: args,
	}))
	result, err := tool.Execute(ctx, args)
	if err != nil {
		result = fmt.Sprintf("error: %v", err)
		a.renderer.ToolResult("scratchpad_write", result, "", true)
		return fmt.Sprintf("[scratchpad_write] %s", result), true
	}
	a.renderer.ToolResult("scratchpad_write", truncateResult(result), "", false)
	return fmt.Sprintf("[scratchpad_write] %s", result), true
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

func classifyDispatchFlow(userMessage string) (dispatchFlowKind, dispatchFlowPhase) {
	lower := strings.ToLower(userMessage)
	switch {
	case looksLikeAssessCodebaseRequest(lower):
		return dispatchFlowAssessCodebase, dispatchPhaseNeedEvidence
	case looksLikePlanningRequest(lower):
		return dispatchFlowPlan, dispatchPhaseNeedPlan
	case looksLikeDebugRequest(lower):
		return dispatchFlowDebug, dispatchPhaseNeedDiagnosis
	case looksLikeSearchRequest(lower):
		return dispatchFlowSearch, dispatchPhaseNeedContext
	case looksLikeImplementationRequest(lower):
		return dispatchFlowImplement, dispatchPhaseIdle
	default:
		return dispatchFlowUnknown, dispatchPhaseIdle
	}
}

func classifyDispatchFollowUp(userMessage string, delegateResults map[string]string) (dispatchFlowKind, dispatchFlowPhase, bool) {
	if !delegateResultCompleted(delegateResults["scout"]) || delegateResultBlocked(delegateResults["scout"]) {
		return dispatchFlowUnknown, dispatchPhaseIdle, false
	}
	if delegateResultCompleted(delegateResults["architect"]) && !delegateResultBlocked(delegateResults["architect"]) {
		return dispatchFlowUnknown, dispatchPhaseIdle, false
	}
	lower := strings.ToLower(normalizePromptText(userMessage))
	if !looksLikeInterpretiveFollowUp(lower) {
		return dispatchFlowUnknown, dispatchPhaseIdle, false
	}
	return dispatchFlowPlan, dispatchPhaseNeedPlan, true
}

func dispatchRoleAllowedForFlow(kind dispatchFlowKind, phase dispatchFlowPhase, role string) bool {
	role = strings.TrimSpace(role)
	switch kind {
	case dispatchFlowAssessCodebase:
		switch phase {
		case dispatchPhaseNeedEvidence:
			return role == "scout"
		case dispatchPhaseNeedSynthesis:
			return role == "architect"
		case dispatchPhaseDone:
			return false
		default:
			return true
		}
	case dispatchFlowPlan:
		if phase == dispatchPhaseNeedPlan {
			return role == "architect"
		}
		return phase != dispatchPhaseDone
	case dispatchFlowDebug:
		switch phase {
		case dispatchPhaseNeedDiagnosis:
			return role == "doctor"
		case dispatchPhaseNeedBuild:
			return role == "builder"
		case dispatchPhaseDone:
			return false
		default:
			return true
		}
	case dispatchFlowSearch:
		if phase == dispatchPhaseNeedContext {
			return role == "scout"
		}
		return phase != dispatchPhaseDone
	default:
		return true
	}
}

func dispatchIllegalRoleMessage(kind dispatchFlowKind, phase dispatchFlowPhase, role string) string {
	allowed := []string{"delegate"}
	switch kind {
	case dispatchFlowAssessCodebase:
		switch phase {
		case dispatchPhaseNeedEvidence:
			allowed = []string{"scout"}
		case dispatchPhaseNeedSynthesis:
			allowed = []string{"architect"}
		}
	case dispatchFlowPlan:
		if phase == dispatchPhaseNeedPlan {
			allowed = []string{"architect"}
		}
	case dispatchFlowDebug:
		switch phase {
		case dispatchPhaseNeedDiagnosis:
			allowed = []string{"doctor"}
		case dispatchPhaseNeedBuild:
			allowed = []string{"builder"}
		}
	case dispatchFlowSearch:
		if phase == dispatchPhaseNeedContext {
			allowed = []string{"scout"}
		}
	}
	return fmt.Sprintf("[delegate] error: %s flow in %s phase does not allow %s; allowed role(s): %s", kind, phase, role, strings.Join(allowed, ", "))
}

func advanceDispatchFlowPhase(kind dispatchFlowKind, phase dispatchFlowPhase, role string) dispatchFlowPhase {
	role = strings.TrimSpace(role)
	switch kind {
	case dispatchFlowAssessCodebase:
		if phase == dispatchPhaseNeedEvidence && role == "scout" {
			return dispatchPhaseNeedSynthesis
		}
		if phase == dispatchPhaseNeedSynthesis && role == "architect" {
			return dispatchPhaseDone
		}
	case dispatchFlowPlan:
		if phase == dispatchPhaseNeedPlan && role == "architect" {
			return dispatchPhaseDone
		}
	case dispatchFlowDebug:
		if phase == dispatchPhaseNeedDiagnosis && role == "doctor" {
			return dispatchPhaseNeedBuild
		}
		if phase == dispatchPhaseNeedBuild && role == "builder" {
			return dispatchPhaseDone
		}
	case dispatchFlowImplement:
		if role == "builder" {
			return dispatchPhaseDone
		}
	case dispatchFlowSearch:
		if phase == dispatchPhaseNeedContext && role == "scout" {
			return dispatchPhaseDone
		}
	}
	return phase
}

func shouldStopDispatchFlow(kind dispatchFlowKind, phase dispatchFlowPhase) bool {
	switch kind {
	case dispatchFlowAssessCodebase, dispatchFlowPlan, dispatchFlowDebug, dispatchFlowImplement, dispatchFlowSearch:
		return phase == dispatchPhaseDone
	default:
		return false
	}
}

func looksLikeAssessCodebaseRequest(lower string) bool {
	requestSignals := []string{
		"review",
		"take a look",
		"look at this",
		"review this",
		"assess this",
		"audit this",
		"what should i change",
		"what changes should i make",
		"changes i should make",
		"let me know if there are any changes",
	}
	targetSignals := []string{
		"repo",
		"repository",
		"directory",
		"dir",
		"project",
		"codebase",
		"here",
		"this",
	}
	return containsAny(lower, requestSignals) && containsAny(lower, targetSignals)
}

func looksLikePlanningRequest(lower string) bool {
	return containsAny(lower, []string{
		"steps you would take",
		"write up a remediation plan",
		"write a remediation plan",
		"implementation plan",
		"remediation plan",
	})
}

func looksLikeInterpretiveFollowUp(lower string) bool {
	if strings.Contains(lower, "?") {
		return true
	}
	return containsAny(lower, []string{
		"what should",
		"what do we do",
		"what now",
		"next step",
		"should we",
		"does that mean",
		"is that expected",
		"is that a problem",
		"need fixed",
		"needs fixed",
		"need changed",
		"needs changed",
	})
}

func looksLikeDebugRequest(lower string) bool {
	return containsAny(lower, []string{
		"why is this happening",
		"why does",
		"debug",
		"broken",
		"failing",
		"failure",
		"error",
		"regression",
	})
}

func looksLikeSearchRequest(lower string) bool {
	return containsAny(lower, []string{
		"where is",
		"where did",
		"where does",
		"come from",
		"came from",
		"originated from",
		"what sent",
		"what triggered",
		"find",
		"what does",
		"how does",
		"show me",
	})
}

func looksLikeImplementationRequest(lower string) bool {
	return containsAny(lower, []string{
		"fix this",
		"fix it",
		"update this",
		"update it",
		"change this",
		"change it",
		"implement",
		"write",
		"create",
		"add",
		"put that in",
		"do that now",
	})
}

func canonicalAssessCodebaseDelegateTask(phase dispatchFlowPhase, role, userMessage string, delegateResults map[string]string, scratchpadResult string) (string, bool) {
	role = strings.TrimSpace(role)
	switch {
	case phase == dispatchPhaseNeedEvidence && role == "scout":
		return fmt.Sprintf("TASK: Gather evidence for a codebase assessment of the current workspace. OUTCOME: Evidence-only findings with concrete file paths, code/config/tooling signals, and notable risks or inconsistencies that can support later recommendations. CONTEXT: User asked: %q. Repository root is the current working directory. MUST NOT: Do not provide final recommendations, prioritization, or implementation steps. Do not modify files.", userMessage), true
	case phase == dispatchPhaseNeedSynthesis && role == "architect":
		var contextParts []string
		if scout := strings.TrimSpace(delegateResults["scout"]); scout != "" {
			contextParts = append(contextParts, "SCOUT FINDINGS:\n"+scout)
		}
		if scratch := strings.TrimSpace(scratchpadResult); scratch != "" && !strings.Contains(strings.Join(contextParts, "\n\n"), scratch) {
			contextParts = append(contextParts, "SCRATCHPAD CONTEXT:\n"+scratch)
		}
		context := strings.Join(contextParts, "\n\n")
		if context == "" {
			context = "No usable evidence is currently available."
		}
		return fmt.Sprintf("TASK: Synthesize actionable codebase assessment recommendations from the latest evidence only. OUTCOME: A concise prioritized list of suggested changes with rationale and confidence notes. CONTEXT: User asked: %q.\n\n%s\n\nMUST NOT: Do not gather new repository evidence. Do not invent findings. Do not modify files.", userMessage, context), true
	default:
		return "", false
	}
}

func dispatchRepoReviewEvidenceUsable(result string) bool {
	trimmed := strings.TrimSpace(result)
	if trimmed == "" || !delegateResultCompleted(trimmed) || delegateResultBlocked(trimmed) {
		return false
	}
	lower := strings.ToLower(trimmed)
	if looksLikeActionPreamble(trimmed) {
		return false
	}
	if !containsAny(lower, []string{"findings:", "key files:", "repository evidence", "evidence-backed findings"}) {
		return false
	}
	if strings.Contains(lower, "agent error") || strings.Contains(lower, "partial output:") {
		return false
	}
	if strings.Contains(lower, "no verified evidence collected yet") {
		return false
	}
	if strings.Contains(lower, "i do not have enough codebase evidence") {
		return false
	}
	if containsAny(lower, []string{
		"i'll inspect the repository structure",
		"i'm going to inspect the repository structure",
		"summarize concrete evidence only",
	}) {
		return false
	}
	return true
}

func dispatchScratchpadWriteAllowed(content string, delegateResults map[string]string, scratchpadResult string) bool {
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
	return false
}

func enrichDispatchDelegateTask(role, task string, delegateResults map[string]string, scratchpadResult string) string {
	role = strings.TrimSpace(role)
	var contextParts []string
	switch role {
	case "architect":
		if scout := strings.TrimSpace(delegateResults["scout"]); scout != "" && !strings.Contains(task, scout) {
			contextParts = append(contextParts, "SCOUT FINDINGS:\n"+scout)
		}
		if scratch := strings.TrimSpace(scratchpadResult); scratch != "" && !strings.Contains(task, scratch) {
			contextParts = append(contextParts, "SCRATCHPAD CONTEXT:\n"+scratch)
		}
	case "builder":
		if architect := strings.TrimSpace(delegateResults["architect"]); architect != "" && !strings.Contains(task, architect) {
			contextParts = append(contextParts, "ARCHITECT OUTPUT:\n"+architect)
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

func delegateResultCompleted(result string) bool {
	trimmed := strings.TrimSpace(result)
	if trimmed == "" {
		return false
	}
	if strings.EqualFold(trimmed, "(sub-agent produced no output)") {
		return false
	}
	upper := strings.ToUpper(trimmed)
	if strings.HasPrefix(upper, "CANCELLED:") || strings.HasPrefix(upper, "AGENT ERROR") {
		return false
	}
	return true
}

func delegateResultBlocked(result string) bool {
	trimmed := strings.TrimSpace(strings.ToLower(normalizePromptText(result)))
	if trimmed == "" {
		return false
	}
	blockedSignals := []string{
		"sub-agent produced no output",
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

func scoutAllowsRuntimeArtifactInspection(task string) bool {
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

func scoutToolTargetsRuntimeArtifact(call ToolCall) bool {
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

func sanitizeScoutToolResult(toolName, result string) string {
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
