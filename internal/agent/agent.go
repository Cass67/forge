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
	driver                    llm.Driver
	tools                     *tools.Registry
	approve                   tools.ApprovalFunc
	history                   []llm.Message
	system                    string
	systemOverride            bool // true when SetSystem was called; suppresses rebuild
	workDir                   string
	maxTurns                  int
	renderer                  RenderTarget
	skills                    []skills.Skill
	state                     *chatstate.State
	isSubAgent                bool
	structuredOutputRetryMode bool
	lastFullResponse          string
	lastToolCalls             []ToolCall
	role                      string
	dispatchResults           map[string]string
	dispatchArtifacts         map[string]string
	dispatchScratch           string
	dispatchTurn              int
	latestScout               dispatchScoutEvidence
	mu                        sync.Mutex
	activeSubCancel           context.CancelFunc
}

type dispatchIntent string

const (
	dispatchIntentTrace     dispatchIntent = "trace"
	dispatchIntentInterpret dispatchIntent = "interpret"
	dispatchIntentDebug     dispatchIntent = "debug"
	dispatchIntentImplement dispatchIntent = "implement"
)

type dispatchScoutEvidence struct {
	TopicKey    string
	DisplayText string
	ContextText string
	Turn        int
}

type scoutRepoReviewEvidenceState struct {
	active      bool
	workDir     string
	topEntries  map[string]string
	sawTopLevel bool
	sawOverview bool
	sawManifest bool
	sawSource   bool
	sawHealth   bool
}

type scoutSingleFileEvidenceState struct {
	active     bool
	target     string
	matched    string
	readTarget bool
}

type scoutFocusedFilesEvidenceState struct {
	active         bool
	targetLang     string
	targetGlob     string
	minReads       int
	usedGlob       bool
	candidateOrder []string
	candidateSet   map[string]struct{}
	readPaths      map[string]struct{}
}

const targetHistoryTokens = 12000
const maxCurrentToolResultChars = 12000

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

func (a *Agent) SetSubAgentMode(enabled bool) {
	a.isSubAgent = enabled
}

func (a *Agent) Role() string {
	return a.role
}

func (a *Agent) LastResponse() string {
	return a.lastFullResponse
}

func (a *Agent) ResetTurnState() {
	a.lastFullResponse = ""
	a.lastToolCalls = nil
}

func (a *Agent) LastToolCalls() []ToolCall {
	out := make([]ToolCall, 0, len(a.lastToolCalls))
	for _, call := range a.lastToolCalls {
		out = append(out, copyToolCall(call))
	}
	return out
}

func (a *Agent) EmitSyntheticResponse(text string) {
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	a.lastFullResponse = text
	a.history = append(a.history, llm.Message{Role: llm.RoleAssistant, Content: text})
	if a.renderer != nil {
		a.renderer.AgentText(text)
	}
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

func (a *Agent) ResetConversationState() {
	a.history = nil
	clear(a.dispatchResults)
	clear(a.dispatchArtifacts)
	a.dispatchScratch = ""
	a.latestScout = dispatchScoutEvidence{}
	if resetter, ok := a.driver.(llm.ConversationResetter); ok {
		resetter.ResetConversation()
	}
}

func (a *Agent) Run(ctx context.Context, userMessage string) error {
	a.lastFullResponse = ""
	a.lastToolCalls = nil
	historyUserMessage := normalizeUserMessageForHistory(userMessage)
	a.history = append(a.history, llm.Message{Role: llm.RoleUser, Content: userMessage})
	historyUserIdx := len(a.history) - 1
	defer func() {
		if historyUserIdx >= 0 && historyUserIdx < len(a.history) {
			a.history[historyUserIdx].Content = historyUserMessage
		}
	}()
	turnStart := time.Now()
	actionPreambleRetries := 0
	mainMalformedToolRetries := 0
	subAgentMixedProseRetries := 0
	subAgentMultipleToolCallRetries := 0
	dispatchDirectAnswerRetries := 0
	inspectNoToolRetries := 0
	inspectMixedToolCallRetries := 0
	scoutNoToolRetries := 0
	scoutMalformedToolRetries := 0
	subAgentMalformedToolRetries := 0
	sawToolCallThisRun := false
	dispatchCanStop := false
	dispatchStopAfterTurn := false
	currentDispatchTurn := 0
	currentDispatchIntent := dispatchIntentTrace
	currentDispatchTopic := ""
	autoChainedArchitect := false
	var currentScoutEvidence *dispatchScoutEvidence
	scoutSingleFileState := newScoutSingleFileEvidenceState(userMessage)
	scoutFocusedFilesState := newScoutFocusedFilesEvidenceState(userMessage)
	scoutRepoReviewState := newScoutRepoReviewEvidenceState(a.workDir, userMessage)
	isInspectTurn := !a.isSubAgent && isHarnessInspectTurn(userMessage)
	isAnswerTurn := !a.isSubAgent && isHarnessAnswerTurn(userMessage)
	pendingControlMsgIdxs := make([]int, 0, 4)
	var clearPendingControls func()
	clearPendingControls = func() {
		if len(pendingControlMsgIdxs) == 0 {
			return
		}
		sort.Ints(pendingControlMsgIdxs)
		for i := len(pendingControlMsgIdxs) - 1; i >= 0; i-- {
			idx := pendingControlMsgIdxs[i]
			if idx < 0 || idx >= len(a.history) {
				continue
			}
			a.history = append(a.history[:idx], a.history[idx+1:]...)
		}
		pendingControlMsgIdxs = pendingControlMsgIdxs[:0]
	}
	replacePendingControls := func(contents ...string) {
		clearPendingControls()
		for _, content := range contents {
			if strings.TrimSpace(content) == "" {
				continue
			}
			a.history = append(a.history, llm.Message{Role: llm.RoleUser, Content: content})
			pendingControlMsgIdxs = append(pendingControlMsgIdxs, len(a.history)-1)
		}
	}
	if a.role == "dispatch" {
		a.dispatchTurn++
		currentDispatchTurn = a.dispatchTurn
		currentDispatchIntent = classifyDispatchIntent(userMessage)
		if currentDispatchIntent == dispatchIntentTrace && shouldInterpretReferentialFollowUp(userMessage, currentDispatchTurn, a.latestScout, a.dispatchResults["architect"]) {
			currentDispatchIntent = dispatchIntentInterpret
		}
		if a.latestScout.Turn > 0 && a.latestScout.Turn < currentDispatchTurn-1 {
			a.latestScout = dispatchScoutEvidence{}
			delete(a.dispatchResults, "scout")
			delete(a.dispatchArtifacts, "scout")
		}
		currentDispatchTopic = resolveDispatchTopicKey(userMessage, currentDispatchIntent, currentDispatchTurn, a.latestScout)
	}
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
		if normalized, changed := normalizeStrictTurnForExecution(response, a.role, a.isSubAgent, isInspectTurn); changed {
			response = normalized
		}

		// Parse tool calls
		calls, visibleText := ParseToolCalls(response)
		if !a.isSubAgent && !isAnswerTurn && len(calls) == 0 && startsWithToolCallMarkup(response) {
			if turn+1 < a.maxTurns {
				mainMalformedToolRetries++
				replacePendingControls(mainMalformedToolMarkupNudgeMessage(mainMalformedToolRetries))
				continue
			}
			return fmt.Errorf("main agent produced malformed tool markup")
		}
		if a.role == "scout" && containsRawToolMarkup(response) && len(calls) == 0 {
			if turn+1 < a.maxTurns {
				scoutMalformedToolRetries++
				replacePendingControls(scoutMalformedToolMarkupNudgeMessage(scoutMalformedToolRetries))
				continue
			}
			return fmt.Errorf("scout produced malformed tool markup")
		}
		if a.isSubAgent && strings.TrimSpace(a.role) != "scout" && containsRawToolMarkup(response) && len(calls) == 0 {
			if turn+1 < a.maxTurns {
				subAgentMalformedToolRetries++
				replacePendingControls(subAgentMalformedToolMarkupNudgeMessage(a.role, subAgentMalformedToolRetries))
				continue
			}
			role := strings.TrimSpace(a.role)
			if role == "" {
				role = "sub-agent"
			}
			return fmt.Errorf("%s produced malformed tool markup", role)
		}
		mainMalformedToolRetries = 0
		subAgentMalformedToolRetries = 0
		inspectMixedToolCallProse := isInspectTurn && len(calls) > 0 && strings.TrimSpace(visibleText) != ""
		if inspectMixedToolCallProse {
			inspectMixedToolCallRetries++
			visibleText = ""
		} else {
			inspectMixedToolCallRetries = 0
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
					replacePendingControls(dispatchNudgeMessage(dispatchDirectAnswerRetries))
					continue
				}
				return fmt.Errorf("dispatch produced no delegate call before answering")
			}
			if a.role == "scout" && !a.structuredOutputRetryMode && !sawToolCallThisRun && scoutTaskRequiresEvidenceTools(userMessage) {
				if turn+1 < a.maxTurns {
					scoutNoToolRetries++
					replacePendingControls(scoutEvidenceNudgeMessage(scoutNoToolRetries))
					continue
				}
				return fmt.Errorf("scout produced no evidence-gathering tool call before answering")
			}
			if isInspectTurn && !sawToolCallThisRun {
				if turn+1 < a.maxTurns {
					inspectNoToolRetries++
					replacePendingControls(inspectEvidenceNudgeMessage(inspectNoToolRetries))
					continue
				}
				return fmt.Errorf("inspect turn produced no evidence-gathering tool call before answering")
			}
			if a.role == "scout" && scoutSingleFileState.NeedsMoreEvidence() {
				if turn+1 < a.maxTurns {
					replacePendingControls(scoutSingleFileState.NudgeMessage())
					continue
				}
				return fmt.Errorf("scout stopped before reading the target file")
			}
			if a.role == "scout" && scoutFocusedFilesState.NeedsMoreEvidence() {
				if turn+1 < a.maxTurns {
					replacePendingControls(scoutFocusedFilesState.NudgeMessage())
					continue
				}
				return fmt.Errorf("scout stopped before gathering enough focused-file evidence")
			}
			if a.role == "scout" && scoutRepoReviewState.NeedsMoreEvidence() {
				if turn+1 < a.maxTurns {
					replacePendingControls(scoutRepoReviewState.NudgeMessage())
					continue
				}
				return fmt.Errorf("scout stopped before gathering enough repo-review evidence")
			}
			if a.isSubAgent && strings.TrimSpace(response) == "" {
				if turn+1 < a.maxTurns {
					nudge := subAgentNoOutputNudgeMessage(a.role)
					if a.role == "scout" && scoutSingleFileState.NeedsMoreEvidence() {
						nudge = scoutSingleFileState.NudgeMessage()
					}
					if a.role == "scout" && scoutFocusedFilesState.NeedsMoreEvidence() {
						nudge = scoutFocusedFilesState.NudgeMessage()
					}
					if a.role == "scout" && scoutRepoReviewState.NeedsMoreEvidence() {
						nudge = scoutRepoReviewState.NudgeMessage()
					}
					replacePendingControls(nudge)
					continue
				}
				return fmt.Errorf("%s produced no final output", a.role)
			}
			isPreamble := looksLikeActionPreamble(response)
			if !a.isSubAgent && !isAnswerTurn && isPreamble && actionPreambleRetries < 4 && turn+1 < a.maxTurns {
				actionPreambleRetries++
				replacePendingControls(nudgeMessage(actionPreambleRetries))
				continue
			}
			clearPendingControls()
			if strings.TrimSpace(visibleText) != "" {
				a.renderer.AgentToken(visibleText)
			}
			a.history = append(a.history, llm.Message{Role: llm.RoleAssistant, Content: response})
			return nil
		}
		actionPreambleRetries = 0
		dispatchDirectAnswerRetries = 0
		inspectNoToolRetries = 0
		scoutNoToolRetries = 0
		scoutMalformedToolRetries = 0
		sawToolCallThisRun = true

		if a.role == "dispatch" && len(calls) > 1 {
			calls = calls[:1]
		}
		strictWorkerMultipleToolCalls := isStrictWorkerRole(a.role) && len(calls) > 1
		if strictWorkerMultipleToolCalls {
			subAgentMultipleToolCallRetries++
			calls = calls[:1]
		} else {
			subAgentMultipleToolCallRetries = 0
		}
		inspectMultipleToolCalls := isInspectTurn && len(calls) > 1
		if inspectMultipleToolCalls {
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
				if shouldReuseLatestScoutEvidence(role, task, currentDispatchIntent, currentDispatchTurn, currentDispatchTopic, a.latestScout) {
					role = "architect"
					task = synthesizeInterpretiveArchitectTask(userMessage)
					call.Args["role"] = role
					call.Args["_auto_chain"] = true
				}
				task = rewriteDispatchDelegateTaskForUser(role, task, userMessage)
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
			a.lastToolCalls = append(a.lastToolCalls, copyToolCall(call))

			a.renderer.ToolCall(call.Name, formatCallSummary(call))

			result, err := tool.Execute(ctx, call.Args)
			var diff string
			if tool.LastDiff != nil {
				diff = tool.LastDiff()
			}
			if err == nil && a.isSubAgent && subAgentFiltersRuntimeArtifacts(a.role) && !subAgentAllowsRuntimeArtifactInspection(a.role, userMessage) {
				result = sanitizeRuntimeArtifactToolResult(call.Name, result)
			}
			scoutSingleFileState.Observe(call, result)
			scoutFocusedFilesState.Observe(call, result)
			scoutRepoReviewState.Observe(call, result)
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
				renderToolResult := true
				if call.Name == "delegate" {
					role, _ := call.Args["role"].(string)
					role = strings.TrimSpace(role)
					task, _ := call.Args["task"].(string)
					autoChain, _ := call.Args["_auto_chain"].(bool)
					outcome := parseDelegateOutcomeForRole(role, result)
					artifactContextText := outcome.ContextText()
					carryContextText := outcome.CarryContextText()
					displayContextText := artifactContextText
					if strings.TrimSpace(displayContextText) == "" {
						displayContextText = carryContextText
					}
					displayResult = outcome.DisplayText()
					usedInterpretFallback := false
					if a.role == "dispatch" && role == "architect" && autoChain && outcome.Blocked() {
						if fallback := resolveScoutFallbackEvidence(currentDispatchTurn, currentScoutEvidence, a.latestScout); fallback != nil {
							displayResult = fallback.DisplayText + "\n" + labelInterpretationUnavailable(outcome.DisplayText())
							result = displayResult
							dispatchCanStop = true
							dispatchStopAfterTurn = true
							usedInterpretFallback = true
							carryContextText = fallback.ContextText
							displayContextText = fallback.ContextText
						}
					}
					if strings.TrimSpace(displayContextText) != "" && strings.TrimSpace(displayContextText) != strings.TrimSpace(displayResult) {
						diff = displayContextText
					}
					result = displayResult
					if a.role == "dispatch" {
						a.dispatchResults[role] = displayResult
						if outcome.Completed() && carryContextText != "" {
							a.dispatchArtifacts[role] = carryContextText
						}
						if role == "scout" && outcome.Blocked() && !usedInterpretFallback {
							renderToolResult = false
						}
						if role == "scout" && outcome.Completed() {
							topicContext := artifactContextText
							if strings.TrimSpace(topicContext) == "" {
								topicContext = carryContextText
							}
							topicKey := deriveScoutTopicKey(topicContext, task)
							currentScoutEvidence = &dispatchScoutEvidence{
								TopicKey:    topicKey,
								DisplayText: displayResult,
								ContextText: carryContextText,
								Turn:        currentDispatchTurn,
							}
							if strings.TrimSpace(carryContextText) != "" {
								a.latestScout = *currentScoutEvidence
							}
						}
						if outcome.Structured {
							if outcome.Completed() && outcome.NextRole != "" && outcome.NextTask != "" {
								callQueue = append(callQueue, ToolCall{
									Name: "delegate",
									Args: map[string]any{
										"role":        outcome.NextRole,
										"task":        outcome.NextTask,
										"_auto_chain": true,
									},
								})
								dispatchCanStop = false
							} else if outcome.Completed() && role == "scout" && shouldAutoChainRepoReviewArchitect(task) && !autoChainedArchitect {
								autoChainedArchitect = true
								callQueue = append(callQueue, ToolCall{
									Name: "delegate",
									Args: map[string]any{
										"role":        "architect",
										"task":        synthesizeRepoReviewArchitectTask(userMessage),
										"_auto_chain": true,
									},
								})
								dispatchCanStop = false
							} else if outcome.Completed() && role == "scout" && currentDispatchIntent == dispatchIntentInterpret && !autoChainedArchitect {
								autoChainedArchitect = true
								callQueue = append(callQueue, ToolCall{
									Name: "delegate",
									Args: map[string]any{
										"role":        "architect",
										"task":        synthesizeInterpretiveArchitectTask(userMessage),
										"_auto_chain": true,
									},
								})
								dispatchCanStop = false
							} else if outcome.Completed() {
								dispatchStopAfterTurn = true
							}
						} else if outcome.Blocked() && !usedInterpretFallback {
							dispatchCanStop = false
						} else if outcome.Completed() {
							dispatchCanStop = true
							dispatchStopAfterTurn = true
						}
					}
				}
				if a.role == "dispatch" && call.Name == "scratchpad_read" {
					a.dispatchScratch = result
				}
				if renderToolResult {
					a.renderer.ToolResult(call.Name, displayResult, diff, false)
				}
			}

			results = append(results, fmt.Sprintf("[%s] %s", call.Name, result))
		}

		// Append compact history entries; preserve UI output separately via the renderer only.
		clearPendingControls()
		a.lastFullResponse = visibleText
		assistantText := visibleText
		if len(calls) > 0 {
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
		nextTurnControls := make([]string, 0, 2)
		if mixedSubAgentToolCallProse && turn+1 < a.maxTurns {
			nextTurnControls = append(nextTurnControls, subAgentToolCallNudgeMessage(a.role, subAgentMixedProseRetries))
		}
		if strictWorkerMultipleToolCalls && turn+1 < a.maxTurns {
			nextTurnControls = append(nextTurnControls, subAgentSingleToolCallNudgeMessage(a.role, subAgentMultipleToolCallRetries))
		}
		if inspectMixedToolCallProse && turn+1 < a.maxTurns {
			nextTurnControls = append(nextTurnControls, inspectToolCallNudgeMessage(inspectMixedToolCallRetries))
		}
		if inspectMultipleToolCalls && turn+1 < a.maxTurns {
			nextTurnControls = append(nextTurnControls, inspectSingleToolCallNudgeMessage())
		}
		if scoutFirstTurnMultipleCalls && turn+1 < a.maxTurns {
			nextTurnControls = append(nextTurnControls, scoutFirstTurnToolCallNudgeMessage())
		}
		if len(nextTurnControls) > 0 {
			replacePendingControls(nextTurnControls...)
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

func copyToolCall(call ToolCall) ToolCall {
	cloned := ToolCall{Name: call.Name}
	if len(call.Args) > 0 {
		cloned.Args = make(map[string]any, len(call.Args))
		for key, value := range call.Args {
			cloned.Args[key] = value
		}
	}
	return cloned
}

func isStrictWorkerRole(role string) bool {
	switch strings.TrimSpace(role) {
	case "reader", "editor", "verifier", "researcher":
		return true
	default:
		return false
	}
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

func classifyDispatchIntent(task string) dispatchIntent {
	lower := strings.ToLower(normalizePromptText(task))
	switch {
	case containsAny(lower, []string{
		"fix",
		"implement",
		"edit",
		"write",
		"update",
		"remove",
		"add",
		"change",
		"refactor",
	}):
		return dispatchIntentImplement
	case containsAny(lower, []string{
		"root cause",
		"bug",
		"broken",
		"not working",
		"failing",
		"failure",
		"error",
		"debug",
	}):
		return dispatchIntentDebug
	case containsAny(lower, []string{
		"worry",
		"urgent",
		"severity",
		"risk",
		"safe",
		"ignore",
		"what does that mean",
		"what should",
		"what do i do",
		"next step",
		"next check",
		"recommend",
		"recommendation",
		"actionable",
		"should i",
	}):
		return dispatchIntentInterpret
	default:
		return dispatchIntentTrace
	}
}

func rewriteDispatchDelegateTask(role, task string) string {
	return rewriteDispatchDelegateTaskForUser(role, task, "")
}

func rewriteDispatchDelegateTaskForUser(role, task, userMessage string) string {
	switch strings.TrimSpace(role) {
	case "scout":
		return rewriteDispatchScoutTaskForUser(task, userMessage)
	case "builder", "doctor", "architect":
		return ensureTaskProfileSections(task, classifyDelegatedTaskProfile(userMessage, task), false)
	default:
		return task
	}
}

func rewriteDispatchScoutTask(task string) string {
	return rewriteDispatchScoutTaskForUser(task, "")
}

func rewriteDispatchScoutTaskForUser(task, userMessage string) string {
	profile := classifyDelegatedTaskProfile(userMessage, task)
	task = ensureTaskProfileSections(task, profile, true)

	switch profile.Scope {
	case taskScopeSingleFile:
		return task
	case taskScopeFocusedFiles:
		return ensureMustNotConstraints(
			task,
			"Do not broaden this into a repo-wide review; stay within the matching files and nearby context only.",
			"Do not stop after listings or globs alone; read representative matching files before concluding.",
			"Do not start with a recursive root listing when TARGET_GLOB already identifies the scope.",
		)
	}

	if target := extractSingleFileTaskTarget(task); target != "" {
		if strings.TrimSpace(taskSection(task, "SCOPE:")) == "" {
			task = strings.TrimRight(task, "\n") + "\nSCOPE: single-file"
		}
		if strings.TrimSpace(taskSection(task, "TARGET:")) == "" {
			task = strings.TrimRight(task, "\n") + "\nTARGET: " + target
		}
		return task
	}

	lower := strings.ToLower(normalizePromptText(task))
	if task == "" || strings.Contains(lower, "do not provide final recommendations") {
		return task
	}
	if !dispatchScoutTaskNeedsEvidenceOnly(task) {
		return task
	}

	task = replaceLabeledTaskSection(
		task,
		"OUTCOME:",
		"OUTCOME: Evidence-backed findings only. Gather repository purpose, structure, tech stack, key modules, and concrete maintenance signals with file/path references so an architect can synthesize recommendations.",
		taskSectionStopLabels("OUTCOME:")...,
	)

	const recommendationConstraint = "Do not provide final recommendations, cleanup actions, prioritization, or user-facing advice."
	const evidenceDepthConstraint = "Read at least one relevant file or search result before concluding; do not base a repository review on directory listings alone."

	switch {
	case strings.Contains(task, "MUST NOT:"):
		if !strings.Contains(strings.ToLower(normalizePromptText(task)), strings.ToLower(recommendationConstraint)) {
			task += " " + recommendationConstraint
		}
		if !strings.Contains(strings.ToLower(normalizePromptText(task)), strings.ToLower(evidenceDepthConstraint)) {
			task += " " + evidenceDepthConstraint
		}
	default:
		task += "\nMUST NOT: " + recommendationConstraint + " " + evidenceDepthConstraint
	}
	return task
}

func dispatchScoutTaskNeedsEvidenceOnly(task string) bool {
	lower := strings.ToLower(normalizePromptText(task))
	if containsAny(lower, []string{"evidence-backed findings only", "do not provide final recommendations"}) {
		return false
	}
	repoReference := containsAny(lower, []string{"repo", "repository"})
	repoReviewTopic := repoReference && containsAny(lower, []string{
		"review",
		"inspect",
		"explain this repo",
		"repo tour",
		"repo overview",
		"repo structure",
		"repository structure",
		"key packages",
		"key modules",
		"packages",
		"entrypoint",
		"entrypoints",
		"tests",
		"tooling",
		"config",
		"tech stack",
		"maintenance smells",
	})
	recommendationAsk := containsAny(lower, []string{
		"recommend",
		"cleanup",
		"improvement",
		"changes i should make",
		"what to change",
		"priorit",
		"actionable",
		"maintenance smells",
	})
	return repoReviewTopic && recommendationAsk
}

func replaceLabeledTaskSection(task, label, replacement string, stopLabels ...string) string {
	start, end, ok := taskSectionBounds(task, label, stopLabels...)
	if !ok {
		if strings.TrimSpace(task) == "" {
			return replacement
		}
		return strings.TrimRight(task, "\n") + "\n" + replacement
	}
	prefix := strings.TrimRight(task[:start], "\n")
	suffix := strings.TrimLeft(task[end:], "\n")
	switch {
	case prefix == "" && suffix == "":
		return replacement
	case prefix == "":
		return replacement + "\n" + suffix
	case suffix == "":
		return prefix + "\n" + replacement
	default:
		return prefix + "\n" + replacement + "\n" + suffix
	}
}

func resolveDispatchTopicKey(userMessage string, intent dispatchIntent, currentTurn int, latest dispatchScoutEvidence) string {
	if key := deriveTopicKeyFromText(userMessage); key != "" {
		return key
	}
	if intent == dispatchIntentInterpret && latest.Turn == currentTurn-1 {
		return latest.TopicKey
	}
	return ""
}

func shouldReuseLatestScoutEvidence(role, task string, intent dispatchIntent, currentTurn int, currentTopic string, latest dispatchScoutEvidence) bool {
	if strings.TrimSpace(role) != "scout" || intent != dispatchIntentInterpret {
		return false
	}
	if latest.Turn != currentTurn-1 || strings.TrimSpace(latest.TopicKey) == "" || strings.TrimSpace(latest.ContextText) == "" {
		return false
	}
	if taskTopic := deriveTopicKeyFromText(task); taskTopic != "" && taskTopic != latest.TopicKey {
		return false
	}
	if currentTopic != "" && currentTopic != latest.TopicKey {
		return false
	}
	return true
}

func shouldInterpretReferentialFollowUp(userMessage string, currentTurn int, latest dispatchScoutEvidence, priorArchitectResult string) bool {
	if latest.Turn != currentTurn-1 || strings.TrimSpace(latest.TopicKey) == "" || strings.TrimSpace(latest.ContextText) == "" {
		return false
	}
	normalized := strings.ToLower(normalizePromptText(strings.TrimSpace(userMessage)))
	if normalized == "" || deriveTopicKeyFromText(normalized) != "" {
		return false
	}
	if containsAny(normalized, []string{
		"where",
		"which file",
		"what file",
		"what line",
		"show me",
		"trace",
		"find",
		"search",
		"fix",
		"implement",
		"change",
		"update",
		"edit",
		"write",
		"debug",
		"root cause",
	}) {
		return false
	}
	fields := strings.Fields(normalized)
	if len(fields) == 0 || len(fields) > 6 {
		return false
	}
	if containsAny(normalized, []string{
		"is it",
		"well is it",
		"means",
		"mean",
		"what does",
		"what now",
		"so what",
		"should i",
		"worry",
		"urgent",
		"serious",
		"safe",
		"ok",
		"okay",
		"how bad",
	}) {
		return true
	}
	if strings.TrimSpace(priorArchitectResult) != "" && strings.Contains(normalized, "?") {
		return true
	}
	return false
}

func synthesizeInterpretiveArchitectTask(userMessage string) string {
	return strings.TrimSpace("TASK: Interpret the existing scout findings for the user's question.\n" +
		"OUTCOME: Concise severity, actionability, and the next check the user should make.\n" +
		"CONTEXT: USER QUESTION:\n" + strings.TrimSpace(userMessage) + "\n" +
		"MUST NOT: Do not gather new evidence unless the scout findings are unusable.")
}

func synthesizeRepoReviewArchitectTask(userMessage string) string {
	return strings.TrimSpace("TASK: Synthesize the existing repo-review evidence into prioritized findings, risks, and next actions for the user.\n" +
		"OUTCOME: Concise repo-review synthesis that highlights the most important maintenance risks and the most actionable next steps.\n" +
		"CONTEXT: USER QUESTION:\n" + strings.TrimSpace(userMessage) + "\n" +
		"SCOPE: repo-review\n" +
		"MUST NOT: Do not gather new evidence unless the scout findings are unusable.")
}

func resolveScoutFallbackEvidence(currentTurn int, current *dispatchScoutEvidence, latest dispatchScoutEvidence) *dispatchScoutEvidence {
	if current != nil && strings.TrimSpace(current.DisplayText) != "" {
		return current
	}
	if latest.Turn == currentTurn-1 && strings.TrimSpace(latest.DisplayText) != "" {
		return &latest
	}
	return nil
}

func normalizeStrictTurnForExecution(response, role string, isSubAgent, isInspectTurn bool) (string, bool) {
	strictTurn := isInspectTurn || strings.TrimSpace(role) == "strictlocal" || (isSubAgent && isStrictWorkerRole(role))
	if !strictTurn {
		return response, false
	}
	normalized, changed := NormalizeStrictToolTurn(response)
	if !changed {
		return response, false
	}
	if calls, _ := ParseToolCalls(normalized); len(calls) > 0 {
		return normalized, true
	}
	return response, false
}

func labelInterpretationUnavailable(message string) string {
	message = strings.TrimSpace(message)
	if message == "" {
		return "Interpretation unavailable."
	}
	return "Interpretation unavailable: " + trimSentencePunctuation(message) + "."
}

func shouldAutoChainRepoReviewArchitect(task string) bool {
	return classifyTaskProfile(task).Scope == taskScopeRepoReview
}

func deriveScoutTopicKey(contextText, task string) string {
	if object, ok := parseDelegateArtifactObject(contextText); ok {
		if path := scoutSourceLocation(object); path != "" {
			return normalizeDispatchTopicKey(path)
		}
		if source := firstNonEmptyString(object, "source"); source != "" {
			return normalizeDispatchTopicKey(source)
		}
	}
	if key := deriveTopicKeyFromText(task); key != "" {
		return key
	}
	return normalizeDispatchTopicKey(task)
}

func deriveTopicKeyFromText(text string) string {
	text = normalizePromptText(filepath.ToSlash(text))
	for _, field := range strings.Fields(text) {
		token := strings.Trim(field, "\"'`()[]{}.,;!?")
		if token == "" {
			continue
		}
		if strings.Contains(token, "/") {
			return normalizeDispatchTopicKey(token)
		}
	}
	return ""
}

func normalizeDispatchTopicKey(text string) string {
	text = strings.ToLower(normalizePromptText(filepath.ToSlash(strings.TrimSpace(text))))
	if text == "" {
		return ""
	}
	return strings.Join(strings.Fields(text), " ")
}

func trimSentencePunctuation(text string) string {
	return strings.TrimRight(strings.TrimSpace(text), ".!?")
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

func startsWithToolCallMarkup(text string) bool {
	trimmed := strings.TrimSpace(text)
	for _, opener := range toolCallOpeners {
		if strings.HasPrefix(trimmed, opener) {
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
		block := clipMultilineForHistory(strings.TrimSpace(result), maxCurrentToolResultChars)
		if block == "" {
			lines = append(lines, "- (empty)")
			continue
		}
		blockLines := strings.Split(block, "\n")
		lines = append(lines, "- "+blockLines[0])
		for _, line := range blockLines[1:] {
			lines = append(lines, "  "+line)
		}
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

func clipMultilineForHistory(s string, max int) string {
	s = strings.TrimSpace(s)
	if len(s) <= max {
		return s
	}
	const suffix = "\n... (truncated in history)"
	if max <= len(suffix) {
		return s[:max]
	}
	return strings.TrimSpace(s[:max-len(suffix)]) + suffix
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

func isHarnessInspectTurn(userMessage string) bool {
	return strings.HasPrefix(strings.TrimSpace(userMessage), "HARNESS MODE: inspect")
}

func isHarnessAnswerTurn(userMessage string) bool {
	return strings.HasPrefix(strings.TrimSpace(userMessage), "HARNESS MODE: answer")
}

func normalizeUserMessageForHistory(userMessage string) string {
	if extracted, ok := extractHarnessUserRequest(userMessage); ok {
		return extracted
	}
	return userMessage
}

func extractHarnessUserRequest(userMessage string) (string, bool) {
	trimmed := strings.TrimSpace(strings.ReplaceAll(userMessage, "\r\n", "\n"))
	if !strings.HasPrefix(trimmed, "HARNESS MODE:") {
		return "", false
	}
	const marker = "\nUSER REQUEST:\n"
	idx := strings.Index(trimmed, marker)
	if idx < 0 {
		return "", false
	}
	request := strings.TrimSpace(trimmed[idx+len(marker):])
	if request == "" {
		return "", false
	}
	return request, true
}

func inspectToolCallNudgeMessage(attempt int) string {
	switch attempt {
	case 1:
		return "Inspect turns must not mix visible prose with tool calls. Use tool calls only while gathering evidence."
	case 2:
		return "Stop mixing prose with inspect tool calls. Emit one tool call only until you are ready to answer."
	default:
		return "Inspect turns are tool calls only until evidence gathering is complete."
	}
}

func inspectEvidenceNudgeMessage(attempt int) string {
	switch attempt {
	case 1:
		return "Inspect turns must inspect the workspace with a tool before answering. Call the next read/list/search tool now."
	case 2:
		return "No direct inspect answer yet. Use one inspection tool call now, not a summary."
	default:
		return "Call one inspection tool now. Do not answer before collecting evidence."
	}
}

func inspectSingleToolCallNudgeMessage() string {
	return "Inspect turns must emit exactly one tool call per working turn."
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

func mainMalformedToolMarkupNudgeMessage(attempt int) string {
	switch attempt {
	case 1:
		return "Malformed tool markup. Return exactly one valid <tool_call>...</tool_call> block and nothing else while you are still working."
	case 2:
		return "Malformed tool markup again. Emit one valid tool call block only. No prose, no partial wrapper."
	default:
		return "Return one valid tool call block only. No prose."
	}
}

func scoutFirstTurnToolCallNudgeMessage() string {
	return "For evidence-gathering scout tasks, the first working turn must contain exactly one valid tool call."
}

func subAgentMalformedToolMarkupNudgeMessage(role string, attempt int) string {
	if strings.TrimSpace(role) == "scout" {
		return scoutMalformedToolMarkupNudgeMessage(attempt)
	}
	switch attempt {
	case 1:
		return "Sub-agent emitted malformed tool markup. Return exactly one valid <tool_call>...</tool_call> block and nothing else."
	case 2:
		return "Malformed tool markup again. Emit one valid tool call block only. No prose, no JSON result yet."
	default:
		return "Return one valid tool call block only. No prose."
	}
}

func subAgentToolCallNudgeMessage(role string, attempt int) string {
	if strings.TrimSpace(role) == "scout" {
		return scoutNudgeMessage()
	}
	switch attempt {
	case 1:
		if isStrictWorkerRole(role) {
			return "Hidden worker must not mix visible text with tool calls. Use exactly one tool call while working; emit the final JSON object only when finished."
		}
		return "Sub-agent must not mix visible prose with tool calls. Use tool calls while working; emit the required final output only when finished."
	case 2:
		if isStrictWorkerRole(role) {
			return "Do not combine tool calls with JSON or status text. Either call the next tool or emit the final JSON object."
		}
		return "Do not ask for pasted tool output while also calling tools. Either call the next tool or emit the final output."
	default:
		if isStrictWorkerRole(role) {
			return "Stop mixing tool calls with visible text. Tool calls only until the final JSON object is ready."
		}
		return "Stop mixing prose with tool calls. Tool calls only until the task is complete."
	}
}

func subAgentSingleToolCallNudgeMessage(role string, attempt int) string {
	if !isStrictWorkerRole(role) {
		return "Sub-agent must emit exactly one tool call per working turn."
	}
	switch attempt {
	case 1:
		return "Hidden worker must emit exactly one tool call per working turn."
	case 2:
		return "Still too many tool calls in one worker turn. Emit one tool call only, wait for results, then continue."
	default:
		return "One tool call only per worker turn until the final JSON object is ready."
	}
}

func subAgentNoOutputNudgeMessage(role string) string {
	if strings.TrimSpace(role) == "scout" {
		return "Scout produced no final output after gathering evidence. Either call the next evidence tool or return findings now."
	}
	if isStrictWorkerRole(role) {
		return "Hidden worker produced no final output. Either call the next tool or return the final JSON object now."
	}
	return "Sub-agent produced no final output. Either call the next tool or return the final output now."
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
	switch classifyTaskProfile(task).Scope {
	case taskScopeSingleFile, taskScopeFocusedFiles, taskScopeRepoReview:
		return true
	}
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

func newScoutSingleFileEvidenceState(task string) scoutSingleFileEvidenceState {
	profile := classifyTaskProfile(task)
	target := profile.Target
	if profile.Scope != taskScopeSingleFile {
		target = ""
	}
	return scoutSingleFileEvidenceState{
		active: target != "",
		target: target,
	}
}

func (s *scoutSingleFileEvidenceState) Observe(call ToolCall, result string) {
	if !s.active {
		return
	}

	switch call.Name {
	case "read_file":
		if path, _ := call.Args["path"].(string); s.matches(path) {
			s.readTarget = true
			s.matched = normalizeFileReference(path)
		}
	case "glob", "list_dir", "search":
		s.observeCandidateLines(result)
	}
}

func (s *scoutSingleFileEvidenceState) observeCandidateLines(result string) {
	for _, rawLine := range strings.Split(result, "\n") {
		line := strings.TrimSpace(rawLine)
		if line == "" || strings.HasPrefix(line, "...") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		candidate := normalizeFileReference(fields[0])
		if candidate == "" {
			continue
		}
		if s.matches(candidate) {
			s.matched = candidate
			return
		}
	}
}

func (s scoutSingleFileEvidenceState) matches(candidate string) bool {
	target := normalizeFileReference(s.target)
	candidate = normalizeFileReference(candidate)
	if target == "" || candidate == "" {
		return false
	}
	if candidate == target {
		return true
	}
	return filepath.Base(candidate) == filepath.Base(target)
}

func (s scoutSingleFileEvidenceState) NeedsMoreEvidence() bool {
	return s.active && !s.readTarget
}

func (s scoutSingleFileEvidenceState) NudgeMessage() string {
	if s.matched != "" {
		return "Single-file evidence is still incomplete. Read the matched file now before answering: " + s.matched + "."
	}
	if s.target != "" {
		return "Single-file evidence is still incomplete. Locate and read the target file before answering: " + s.target + "."
	}
	return "Single-file evidence is still incomplete. Read the target file before answering."
}

func newScoutFocusedFilesEvidenceState(task string) scoutFocusedFilesEvidenceState {
	profile := classifyTaskProfile(task)
	if profile.Scope != taskScopeFocusedFiles {
		return scoutFocusedFilesEvidenceState{}
	}
	return scoutFocusedFilesEvidenceState{
		active:       true,
		targetLang:   profile.TargetLang,
		targetGlob:   profile.TargetGlob,
		minReads:     profile.EvidenceMinReads,
		candidateSet: make(map[string]struct{}),
		readPaths:    make(map[string]struct{}),
	}
}

func (s *scoutFocusedFilesEvidenceState) Observe(call ToolCall, result string) {
	if !s.active {
		return
	}

	switch call.Name {
	case "read_file":
		if path, _ := call.Args["path"].(string); s.matches(path) {
			s.recordRead(path)
		}
	case "glob":
		pattern, _ := call.Args["pattern"].(string)
		before := len(s.candidateOrder)
		s.observeCandidateLines(result)
		if s.patternCouldMatch(pattern) || len(s.candidateOrder) > before {
			s.usedGlob = true
		}
	case "search", "list_dir":
		s.observeCandidateLines(result)
	}
}

func (s *scoutFocusedFilesEvidenceState) observeCandidateLines(result string) {
	for _, rawLine := range strings.Split(result, "\n") {
		line := strings.TrimSpace(rawLine)
		if line == "" || strings.HasPrefix(line, "...") {
			continue
		}
		candidate := line
		if idx := strings.Index(candidate, ":"); idx > 0 {
			candidate = candidate[:idx]
		}
		fields := strings.Fields(candidate)
		if len(fields) == 0 {
			continue
		}
		s.recordCandidate(fields[0])
	}
}

func (s *scoutFocusedFilesEvidenceState) recordCandidate(path string) {
	path = normalizeFileReference(path)
	if !s.matches(path) {
		return
	}
	if _, ok := s.candidateSet[path]; ok {
		return
	}
	s.candidateSet[path] = struct{}{}
	s.candidateOrder = append(s.candidateOrder, path)
}

func (s *scoutFocusedFilesEvidenceState) recordRead(path string) {
	path = normalizeFileReference(path)
	if path == "" {
		return
	}
	s.readPaths[path] = struct{}{}
	s.recordCandidate(path)
}

func (s scoutFocusedFilesEvidenceState) patternCouldMatch(pattern string) bool {
	pattern = strings.TrimSpace(filepath.ToSlash(pattern))
	if pattern == "" {
		return false
	}
	if pattern == s.targetGlob {
		return true
	}
	if strings.Contains(pattern, "*.") {
		return s.matches("placeholder" + pattern[strings.LastIndex(pattern, "."):])
	}
	return strings.Contains(pattern, "*")
}

func (s scoutFocusedFilesEvidenceState) matches(candidate string) bool {
	candidate = normalizeFileReference(candidate)
	if candidate == "" {
		return false
	}
	candidateExt := strings.ToLower(filepath.Ext(candidate))
	for _, ext := range targetGlobExtensions(s.targetGlob) {
		if candidateExt == ext {
			return true
		}
	}
	if s.targetLang != "" {
		return candidateExt == filepath.Ext(languageGlob(s.targetLang))
	}
	if s.targetGlob == "" {
		return candidateExt != ""
	}
	return false
}

func (s scoutFocusedFilesEvidenceState) requiredReads() int {
	required := s.minReads
	if required <= 0 {
		required = 3
	}
	if count := len(s.candidateOrder); s.usedGlob && count > 0 && required > count {
		required = count
	}
	if required < 1 {
		required = 1
	}
	return required
}

func (s scoutFocusedFilesEvidenceState) NeedsMoreEvidence() bool {
	if !s.active {
		return false
	}
	if len(s.readPaths) >= s.requiredReads() {
		return false
	}
	if s.usedGlob && len(s.candidateOrder) == 0 {
		return false
	}
	return true
}

func (s scoutFocusedFilesEvidenceState) NudgeMessage() string {
	remaining := s.requiredReads() - len(s.readPaths)
	if remaining < 1 {
		remaining = 1
	}
	nextTargets := make([]string, 0, 3)
	for _, candidate := range s.candidateOrder {
		if _, ok := s.readPaths[candidate]; ok {
			continue
		}
		nextTargets = append(nextTargets, candidate)
		if len(nextTargets) == 3 {
			break
		}
	}
	if len(nextTargets) > 0 {
		return fmt.Sprintf("Focused-file evidence is still incomplete. Stay within the declared scope and read %d more matching file(s) before answering: %s.", remaining, strings.Join(nextTargets, ", "))
	}
	if s.targetGlob != "" {
		return "Focused-file evidence is still incomplete. Stay within the declared scope. Use TARGET_GLOB to locate representative matching files and read them before answering: " + s.targetGlob + "."
	}
	if s.targetLang != "" {
		return "Focused-file evidence is still incomplete. Stay within the declared scope and read more " + s.targetLang + " files before answering."
	}
	return "Focused-file evidence is still incomplete. Read more matching files before answering."
}

func newScoutRepoReviewEvidenceState(workDir, task string) scoutRepoReviewEvidenceState {
	return scoutRepoReviewEvidenceState{
		active:     scoutTaskIsRepoReview(task),
		workDir:    workDir,
		topEntries: make(map[string]string),
	}
}

func (s *scoutRepoReviewEvidenceState) Observe(call ToolCall, result string) {
	if !s.active {
		return
	}

	switch call.Name {
	case "read_file":
		if path, _ := call.Args["path"].(string); path != "" {
			s.observePath(path)
		}
	case "list_dir":
		path, _ := call.Args["path"].(string)
		if strings.TrimSpace(path) == "" {
			path = "."
		}
		s.observePath(path)
		if isRootRepoPath(s.workDir, path) {
			s.sawTopLevel = true
			s.observeTopEntries(result)
		}
	case "search":
		if path, _ := call.Args["path"].(string); path != "" {
			s.observePath(path)
		}
		s.observeResultPaths(result)
	case "glob":
		if path, _ := call.Args["path"].(string); path != "" {
			s.observePath(path)
		}
		if pattern, _ := call.Args["pattern"].(string); pattern != "" {
			s.observePath(pattern)
		}
	}
}

func (s *scoutRepoReviewEvidenceState) observeTopEntries(result string) {
	for _, rawLine := range strings.Split(result, "\n") {
		line := strings.TrimSpace(rawLine)
		if line == "" || strings.HasPrefix(line, "...") {
			continue
		}
		entry := strings.Fields(line)[0]
		if entry == "" || entry == "." || entry == ".." {
			continue
		}
		s.recordTopEntry(entry)
	}
}

func (s *scoutRepoReviewEvidenceState) observeResultPaths(result string) {
	for _, rawLine := range strings.Split(result, "\n") {
		line := strings.TrimSpace(rawLine)
		if line == "" {
			continue
		}
		candidate := line
		if idx := strings.Index(candidate, ":"); idx > 0 {
			candidate = candidate[:idx]
		}
		candidate = strings.Fields(candidate)[0]
		if strings.Contains(candidate, "/") || strings.Contains(candidate, "\\") {
			s.observePath(candidate)
		}
	}
}

func (s *scoutRepoReviewEvidenceState) observePath(path string) {
	rel := normalizeRepoReviewPath(s.workDir, path)
	if rel == "" {
		return
	}
	s.recordTopEntry(rel)
	lower := strings.ToLower(rel)
	base := strings.ToLower(filepath.Base(lower))

	switch {
	case isRepoOverviewPath(lower, base):
		s.sawOverview = true
	case isRepoManifestPath(base):
		s.sawManifest = true
	case isRepoSourcePath(lower, base):
		s.sawSource = true
	case isRepoHealthPath(lower, base):
		s.sawHealth = true
	}

	if isRepoManifestPath(base) {
		s.sawManifest = true
	}
	if isRepoSourcePath(lower, base) {
		s.sawSource = true
	}
	if isRepoHealthPath(lower, base) {
		s.sawHealth = true
	}
}

func (s *scoutRepoReviewEvidenceState) recordTopEntry(path string) {
	rel := normalizeRepoReviewPath(s.workDir, path)
	if rel == "" {
		return
	}
	first := rel
	if idx := strings.Index(first, "/"); idx >= 0 {
		first = first[:idx+1]
	}
	key := normalizeRepoReviewEntryKey(first)
	if _, exists := s.topEntries[key]; !exists {
		s.topEntries[key] = first
	}
}

func (s scoutRepoReviewEvidenceState) NeedsMoreEvidence() bool {
	if !s.active {
		return false
	}
	if !s.sawOverview && !s.sawManifest {
		return true
	}
	if !s.sawTopLevel {
		return !(s.sawManifest && s.sawSource && s.sawHealth)
	}
	if s.hasTopEntry(repoReviewManifestCandidates...) && !s.sawManifest {
		return true
	}
	if s.hasTopEntry(repoReviewSourceCandidates...) && !s.sawSource {
		return true
	}
	if s.hasTopEntry(repoReviewHealthCandidates...) && !s.sawHealth {
		return true
	}
	return false
}

func (s scoutRepoReviewEvidenceState) NudgeMessage() string {
	if !s.sawTopLevel {
		return "Repo-review evidence is still incomplete. Determine the repo shape first: use list_dir on . (non-recursive) or inspect a likely manifest/source/health target next. Do not stop until manifest/source/health coverage is established or ruled out."
	}
	targets := make([]string, 0, 3)
	if s.hasTopEntry(repoReviewManifestCandidates...) && !s.sawManifest {
		targets = append(targets, s.firstTopEntry(repoReviewManifestCandidates...))
	}
	if s.hasTopEntry(repoReviewSourceCandidates...) && !s.sawSource {
		targets = append(targets, s.firstTopEntry(repoReviewSourceCandidates...))
	}
	if s.hasTopEntry(repoReviewHealthCandidates...) && !s.sawHealth {
		targets = append(targets, s.firstTopEntry(repoReviewHealthCandidates...))
	}
	if !s.sawOverview && !s.sawManifest {
		if overview := s.firstTopEntry(repoReviewOverviewCandidates...); overview != "" {
			targets = append([]string{overview}, targets...)
		}
	}
	if len(targets) == 0 {
		return "Repo-review evidence is still incomplete. Inspect the next concrete manifest, source, or build/test target now instead of stopping."
	}
	return "Repo-review evidence is still incomplete. Inspect the next concrete targets now instead of stopping or running a recursive root listing: " + strings.Join(targets, ", ") + ". Use read_file or list_dir on those targets, then return findings only after the missing categories are covered."
}

func (s scoutRepoReviewEvidenceState) hasTopEntry(candidates ...string) bool {
	for _, candidate := range candidates {
		if _, ok := s.topEntries[normalizeRepoReviewEntryKey(candidate)]; ok {
			return true
		}
	}
	return false
}

func (s scoutRepoReviewEvidenceState) firstTopEntry(candidates ...string) string {
	for _, candidate := range candidates {
		if value, ok := s.topEntries[normalizeRepoReviewEntryKey(candidate)]; ok {
			return value
		}
	}
	return ""
}

var taskSectionLabels = []string{
	"TASK:",
	"OUTCOME:",
	"CONTEXT:",
	"MUST NOT:",
	"SCOPE:",
	"TARGET:",
	"TARGET_LANG:",
	"TARGET_GLOB:",
	"TOPIC:",
	"EVIDENCE_MIN_READS:",
}

func taskSection(task, label string) string {
	start, end, ok := taskSectionBounds(task, label, taskSectionStopLabels(label)...)
	if !ok {
		return ""
	}
	sectionStart := start + len(label)
	return strings.TrimSpace(task[sectionStart:end])
}

func taskSectionBounds(task, label string, stopLabels ...string) (start, end int, ok bool) {
	start = strings.Index(task, label)
	if start < 0 {
		return 0, 0, false
	}
	sectionStart := start + len(label)
	end = len(task)
	for _, stop := range stopLabels {
		if stop == "" || stop == label {
			continue
		}
		if idx := strings.Index(task[sectionStart:], stop); idx >= 0 {
			candidate := sectionStart + idx
			if candidate < end {
				end = candidate
			}
		}
	}
	return start, end, true
}

func extractSingleFileTaskTarget(task string) string {
	if target := normalizeFileReference(taskSection(task, "TARGET:")); target != "" {
		return target
	}
	taskText := taskSection(task, "TASK:")
	if taskText == "" {
		taskText = task
	}
	targets := extractFileReferenceTokens(taskText)
	if len(targets) != 1 {
		return ""
	}
	return targets[0]
}

func extractFileReferenceTokens(text string) []string {
	seen := make(map[string]struct{})
	var refs []string
	for _, raw := range strings.Fields(normalizePromptText(text)) {
		token := normalizeFileReference(raw)
		if token == "" {
			continue
		}
		if _, ok := seen[token]; ok {
			continue
		}
		seen[token] = struct{}{}
		refs = append(refs, token)
	}
	return refs
}

func normalizeFileReference(token string) string {
	token = strings.TrimSpace(token)
	token = strings.Trim(token, "\"'`()[]{}.,;!?")
	token = strings.TrimPrefix(token, "@")
	token = filepath.ToSlash(token)
	token = strings.TrimPrefix(token, "./")
	if token == "" || strings.Contains(token, "://") {
		return ""
	}
	if strings.ContainsAny(token, "*?") || strings.HasSuffix(token, "/") {
		return ""
	}
	base := filepath.Base(token)
	ext := filepath.Ext(base)
	if ext == "" || len(ext) == 1 || len(ext) > 10 {
		return ""
	}
	return filepath.Clean(token)
}

var repoReviewOverviewCandidates = []string{
	"README.md",
	"README",
	"ARCHITECTURE.md",
	"docs/",
}

var repoReviewManifestCandidates = []string{
	"go.mod",
	"package.json",
	"pyproject.toml",
	"Cargo.toml",
	"pom.xml",
	"build.gradle",
	"build.gradle.kts",
	"composer.json",
	"Gemfile",
	"Pipfile",
	"requirements.txt",
}

var repoReviewSourceCandidates = []string{
	"cmd/",
	"internal/",
	"pkg/",
	"src/",
	"app/",
	"lib/",
	"main.go",
}

var repoReviewHealthCandidates = []string{
	"BUILD.md",
	"Makefile",
	".golangci.yml",
	".golangci.yaml",
	".github/",
	"ci/",
	"test/",
	"tests/",
	"CONTRIBUTING.md",
}

func scoutTaskIsRepoReview(task string) bool {
	return classifyTaskProfile(task).Scope == taskScopeRepoReview
}

func normalizeRepoReviewPath(workDir, path string) string {
	path = strings.TrimSpace(filepath.ToSlash(path))
	if path == "" || path == "." {
		return "."
	}
	if filepath.IsAbs(path) {
		if rel, err := filepath.Rel(workDir, path); err == nil {
			path = filepath.ToSlash(rel)
		}
	}
	path = strings.TrimPrefix(path, "./")
	return filepath.ToSlash(filepath.Clean(path))
}

func normalizeRepoReviewEntryKey(path string) string {
	path = strings.TrimSpace(filepath.ToSlash(path))
	path = strings.TrimPrefix(path, "./")
	path = strings.TrimSuffix(path, "/")
	return strings.ToLower(path)
}

func isRootRepoPath(workDir, path string) bool {
	rel := normalizeRepoReviewPath(workDir, path)
	return rel == "."
}

func isRepoOverviewPath(lower, base string) bool {
	switch {
	case strings.HasPrefix(base, "readme"):
		return true
	case base == "architecture.md":
		return true
	case base == "docs", strings.HasPrefix(lower, "docs/"):
		return true
	default:
		return false
	}
}

func isRepoManifestPath(base string) bool {
	return containsAny(base, []string{
		"go.mod",
		"package.json",
		"pyproject.toml",
		"cargo.toml",
		"pom.xml",
		"build.gradle",
		"build.gradle.kts",
		"composer.json",
		"gemfile",
		"pipfile",
		"requirements.txt",
	})
}

func isRepoSourcePath(lower, base string) bool {
	switch {
	case base == "main.go":
		return true
	case base == "cmd",
		base == "internal",
		base == "pkg",
		base == "src",
		base == "app",
		base == "lib":
		return true
	case strings.HasPrefix(lower, "cmd/"),
		strings.HasPrefix(lower, "internal/"),
		strings.HasPrefix(lower, "pkg/"),
		strings.HasPrefix(lower, "src/"),
		strings.HasPrefix(lower, "app/"),
		strings.HasPrefix(lower, "lib/"):
		return true
	default:
		return false
	}
}

func isRepoHealthPath(lower, base string) bool {
	switch {
	case strings.HasSuffix(base, "_test.go"):
		return true
	case base == "build.md",
		base == "makefile",
		base == ".golangci.yml",
		base == ".golangci.yaml",
		base == "contributing.md",
		base == ".github",
		base == "ci",
		base == "test",
		base == "tests":
		return true
	case strings.HasPrefix(lower, ".github/"),
		strings.HasPrefix(lower, "ci/"),
		strings.HasPrefix(lower, "test/"),
		strings.HasPrefix(lower, "tests/"):
		return true
	default:
		return false
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
			return "Earlier tool outputs summarized.", true
		}
		if len(summary) > 4 {
			summary = append(summary[:4], "...")
		}
		return "Earlier tool outputs summary: " + strings.Join(summary, " | "), true
	}
	if m.Role == llm.RoleAssistant || m.Role == llm.RoleUser {
		flat := oneLine(content)
		if len(flat) > 240 {
			return clipForHistory(flat, 240), true
		}
	}
	return "", false
}
