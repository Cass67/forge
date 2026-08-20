package react

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"forge/internal/agent"
	agenttools "forge/internal/agent/tools"
	"forge/internal/hooks"
	"forge/internal/llm"
	"forge/internal/protocol"
	resilienceerrors "forge/internal/resilience/errors"
	"forge/internal/sessionstore"
	"forge/internal/workspace"
)

type Config struct {
	Driver   llm.Driver
	Tools    *agenttools.Registry
	Renderer agent.RenderTarget
	// ShowReasoning streams the model's thinking to the renderer as it
	// arrives, for renderers that can display it.
	ShowReasoning         bool
	SystemPrompt          func() string
	Session               *Session
	Progress              func(string)
	TurnComplete          func(SessionSnapshot)
	ToolExposureObserver  func(ToolExposureDecision)
	ConfigureHooks        func(*hooks.Registry)
	CompactionMaxFailures int
	// ContextWindowTokens returns the active model's context window in tokens
	// (0 if unknown). Compaction budgets scale to it instead of the fixed default.
	ContextWindowTokens       func() int
	Interactive               bool
	MaxSteps                  int
	ToolThrashCircuitBreaker  int
	OutputStore               sessionstore.OutputStore
	OutputStoreThresholdBytes int
	PostEditValidator         *PostEditValidator
	// LeanToolExposure exposes only LeanCoreToolNames schemas to the model.
	// Every other registered tool stays callable (schema via tool_help).
	LeanToolExposure bool
}

type ToolExposureDecision struct {
	Reason                string
	ToolNames             []string
	RequireToolCall       bool
	OutstandingAgentCount int
}

type Runner struct {
	driver                    llm.Driver
	tools                     *agenttools.Registry
	renderer                  agent.RenderTarget
	showReasoning             bool
	systemPrompt              func() string
	session                   *Session
	hooks                     *hooks.Registry
	progress                  func(string)
	compactionManager         *CompactionManager
	compactionFailures        int
	compactionMaxFailures     int
	lastPromptContextTokens   int
	maxSteps                  int
	gitWorkflow               gitWorkflowState
	planWorkflow              planWorkflowState
	searchWorkflow            sameFileSearchWorkflowState
	validationWorkflow        validationWorkflowState
	repeatWorkflow            repeatToolCallState
	pendingRetryPrompt        string
	turnComplete              func(SessionSnapshot)
	toolExposureObserver      func(ToolExposureDecision)
	toolThrashCircuitBreaker  int
	completionGateRejections  int
	outputStore               sessionstore.OutputStore
	outputStoreThresholdBytes int
	checkpointManager         *workspace.CheckpointManager
	checkpointedTurns         map[string]bool
	checkpointIDsByTurn       map[string]string
	postEditValidator         *PostEditValidator
	leanToolExposure          bool
}

type gitCommitBlocker int

const (
	commitBlockerNone gitCommitBlocker = iota
	commitBlockerRestage
	commitBlockerEdit
)

type gitWorkflowState struct {
	mergeActive    bool
	unmergedFiles  bool
	commitBlocker  gitCommitBlocker
	blockerSummary string
}

type planWorkflowState struct {
	mode               string
	active             bool
	explorationBatches int
	synthesisRequired  bool
	synthesisEscalated bool
}

type validationWorkflowState struct {
	ran    bool
	passed bool
	cmd    string
}

type sameFileSearchWorkflowState struct {
	toolName string
	path     string
	streak   int
	nudged   bool
}

type repeatToolCallState struct {
	lastToolName string
	lastTarget   string
	// recent holds the last repeatToolCallWindow exploration call keys since
	// the last mutating tool call, so alternating repeats (grep A / cat B /
	// grep A ...) are caught, not just consecutive identical calls.
	recent []string
	// recentResults holds a digest of what each recent call returned, in the
	// same order as recent. A repeat only counts as thrash when the result is
	// unchanged too: re-running a build while fixing errors returns something
	// different each time and is real progress, while a command answered with
	// the identical text every time is going nowhere.
	recentResults []string
	// streak is the number of occurrences of the most recent key within the
	// window (including the latest call).
	streak int
}

const planExplorationBudget = 24
const analysisExplorationBudget = 40
const previewExplorationBudget = 6
const implementExplorationBudget = 24
const inspectExplorationBudget = 24
const defaultOutputStoreThresholdBytes = 10 * 1024

const (
	postDelegationReadOutputAggregateLimitBytes = 40 * 1024
	postDelegationReadOutputMinLimitBytes       = 4 * 1024
	postDelegationReadOutputCallBudget          = 10
)

func outputStoreThresholdBytes(configured int) int {
	if configured > 0 {
		return configured
	}
	return defaultOutputStoreThresholdBytes
}

const overviewExplorationBudget = 12
const reviewExplorationBudget = 40
const validateExplorationBudget = 12
const chatExplorationBudget = 16
const maxLoopSafetySteps = 1000

func maxLoopSteps(configured int) int {
	if configured > 0 {
		return configured
	}
	return maxLoopSafetySteps
}

func toolThrashThreshold(configured, fallback int) int {
	if configured > 0 {
		return configured
	}
	return fallback
}

const sameFileSearchThrashThreshold = 5
const repeatToolCallThreshold = 3

// repeatToolCallBlockThreshold is where a repeat stops being nudged and starts
// being refused. Blocking at the first sign of repetition can cut off a model
// that was one call from resolving, so warn first and only block once the
// warning has demonstrably gone unheeded.
const repeatToolCallBlockThreshold = 6
const repeatToolCallWindow = 10

// ponytail: nudge only, no hard block — 6/10 same-file reads is a verification
// spiral, but linear paging of a big file stays under it.
const rereadSameFileThreshold = 6
const maxCompletionRetriesPerTurn = 3

// maxContextCompactionsPerTurn bounds overflow recovery so a session that
// cannot shrink fails with the provider's error instead of spinning.
const maxContextCompactionsPerTurn = 8
const maxCompletionGateRejectionsPerTurn = 2
const retryNoticeText = "Revising answer..."

var loopHookOverlayKeys = map[string]struct{}{
	"review_guidance":       {},
	"preview_workflow":      {},
	"plan_blocker":          {},
	"synthesis_guidance":    {},
	"post_delegation_write": {},
	"validation_failure":    {},
	"search_thrash":         {},
	"git_workflow":          {},
	"repeat_loop":           {},
	"agent_status":          {},
}

type promptHookPayload struct {
	Mode                     Mode
	PlanState                *PlanState
	PlanWorkflow             planWorkflowState
	ValidationWorkflow       validationWorkflowState
	SearchWorkflow           sameFileSearchWorkflowState
	GitWorkflow              gitWorkflowState
	RepeatWorkflow           repeatToolCallState
	ToolThrashCircuitBreaker int
}

type beforeToolHookPayload struct {
	ToolName    string
	Args        map[string]any
	GitWorkflow gitWorkflowState
}

type afterToolHookPayload struct {
	ToolName string
	Args     map[string]any
	IsError  bool
	Error    string
}

// maxPromptBudgetBytes caps the prompt budget at ~200k tokens regardless of
// window size: history is resent on every request, so on huge-window models an
// uncapped budget costs far more than an occasional summarize.
const maxPromptBudgetBytes = 200_000 * 4

// promptBudgetFromWindow converts a model context window (tokens) into a prompt
// byte budget: 70% of the window leaves headroom for system prompt and output,
// at ~4 bytes/token, capped at maxPromptBudgetBytes. Returns nil so the 256KB
// default applies when the window is unknown.
func promptBudgetFromWindow(windowTokens func() int) func() int {
	if windowTokens == nil {
		return nil
	}
	return func() int {
		w := windowTokens()
		if w <= 0 {
			return 0
		}
		b := w * 7 / 10 * 4
		if b > maxPromptBudgetBytes {
			b = maxPromptBudgetBytes
		}
		return b
	}
}

func NewRunner(cfg Config) *Runner {
	session := cfg.Session
	if session == nil {
		session = NewSession()
	}
	reg := cfg.Tools
	if reg == nil {
		reg = agenttools.NewRegistry()
	}
	hookRegistry := newLoopHookRegistry()
	if cfg.ConfigureHooks != nil {
		cfg.ConfigureHooks(hookRegistry)
	}
	runner := &Runner{
		driver:        cfg.Driver,
		tools:         reg,
		renderer:      cfg.Renderer,
		showReasoning: cfg.ShowReasoning,
		systemPrompt:  cfg.SystemPrompt,
		session:       session,
		hooks:         hookRegistry,
		progress:      cfg.Progress,
		compactionManager: NewCompactionManager(CompactionConfig{
			KeepTurns:            40,
			HistoryPressureTurns: 40,
			MaxFailures:          cfg.CompactionMaxFailures,
			PromptBudgetFn:       promptBudgetFromWindow(cfg.ContextWindowTokens),
		}),
		compactionMaxFailures:     cfg.CompactionMaxFailures,
		turnComplete:              cfg.TurnComplete,
		toolExposureObserver:      cfg.ToolExposureObserver,
		maxSteps:                  maxLoopSteps(cfg.MaxSteps),
		toolThrashCircuitBreaker:  cfg.ToolThrashCircuitBreaker,
		outputStore:               cfg.OutputStore,
		outputStoreThresholdBytes: outputStoreThresholdBytes(cfg.OutputStoreThresholdBytes),
		checkpointManager:         workspace.NewCheckpointManager(""),
		checkpointedTurns:         make(map[string]bool),
		checkpointIDsByTurn:       make(map[string]string),
		postEditValidator:         cfg.PostEditValidator,
		leanToolExposure:          cfg.LeanToolExposure,
	}
	if snap := session.Snapshot(); snap.TaskState != nil && isSynthesisGuardOperation(snap.TaskState.Operation) {
		runner.planWorkflow.active = true
		runner.planWorkflow.mode = strings.ToLower(strings.TrimSpace(snap.TaskState.Operation))
	} else {
		runner.planWorkflow.active = true
		runner.planWorkflow.mode = "chat"
	}
	runner.syncRuntimeNote()
	return runner
}

func (r *Runner) Run(ctx context.Context, input string) error {
	return r.RunWithParts(ctx, input, nil)
}

func (r *Runner) RunWithParts(ctx context.Context, input string, parts []llm.MessageContentPart) (err error) {
	if r == nil {
		return fmt.Errorf("react runner: runner is nil")
	}
	prompt := BuildPrompt(input)
	if prompt == "" && len(parts) == 0 {
		return nil
	}
	turnID := fmt.Sprintf("turn-%d", r.session.Snapshot().Turn+1)
	activeTurn, _, err := r.session.BeginTurn(ctx, turnID)
	if err != nil {
		return err
	}
	ctx = activeTurn.Context
	defer func() {
		reason := TurnEndReasonCompleted
		if err != nil {
			reason = TurnEndReasonFailed
		}
		if ctx.Err() != nil {
			reason = TurnEndReasonCancelled
		}
		if endErr := r.session.EndTurn(turnID, reason); endErr != nil && err == nil {
			err = endErr
		}
	}()
	r.syncRuntimeNote()
	r.applyCompactionDecision(ctx, r.compactionManager.Decide(r.session.Snapshot()))
	turn, err := r.session.RecordInputWithParts(prompt, parts)
	if err != nil {
		return err
	}
	r.pendingRetryPrompt = ""
	r.completionGateRejections = 0
	if r.driver == nil {
		err := fmt.Errorf("no model selected (LLM driver not configured) — pick a model with the model switcher")
		return r.completeTurn(turn, "", nil, err)
	}
	return r.runLoop(ctx, turn)
}

func (r *Runner) CompactHistory(keep int) bool {
	if r == nil || r.compactionManager == nil {
		return false
	}
	return r.applyCompactionDecision(context.Background(), r.compactionManager.UserPartial(keep))
}

func (r *Runner) CompactionStatus() string {
	if r == nil || r.session == nil {
		return "compaction unavailable"
	}
	snap := r.session.Snapshot()
	if strings.TrimSpace(snap.CompactionSummary) == "" && snap.CompactedTurns == 0 {
		return "no compacted turns"
	}
	return fmt.Sprintf("%d compacted turns; summary length %d", snap.CompactedTurns, len(snap.CompactionSummary))
}

func (r *Runner) applyCompactionDecision(ctx context.Context, decision CompactionDecision) bool {
	if r == nil || r.compactionManager == nil || decision.Mode == CompactionNone {
		return false
	}
	beforeContext := 0
	before := SessionSnapshot{}
	if r.session != nil {
		before = r.session.Snapshot()
		beforeContext = estimatePromptTokens(r.session.Messages(r.currentSystemPrompt()))
	}
	r.dispatchCompactionHook(ctx, hooks.PointPreCompact, decision, before, false, 0)
	changed := r.compactionManager.Apply(r.session, decision)
	afterContext := 0
	after := SessionSnapshot{}
	if r.session != nil {
		after = r.session.Snapshot()
		afterContext = estimatePromptTokens(r.session.Messages(r.currentSystemPrompt()))
	}
	if changed {
		r.compactionFailures = 0
		if r.progress != nil && beforeContext > 0 && afterContext > 0 && afterContext < beforeContext {
			r.progress(fmt.Sprintf("react runtime: compacted context ~%d -> ~%d (%s)", beforeContext, afterContext, decision.Reason))
		}
	} else if r.compactionMaxFailures > 0 && compactionDecisionAttempted(decision) {
		r.compactionFailures++
	}
	if r.compactionCircuitOpen() {
		if r.progress != nil {
			r.progress("react runtime: compaction circuit breaker tripped")
		}
	}
	r.dispatchCompactionHook(ctx, hooks.PointPostCompact, decision, after, changed, droppedTurns(before, after))
	return changed
}

func (r *Runner) dispatchCompactionHook(ctx context.Context, point hooks.Point, decision CompactionDecision, snap SessionSnapshot, changed bool, dropped int) {
	if r == nil || r.hooks == nil {
		return
	}
	r.hooks.Dispatch(ctx, hooks.Event{
		Point: point,
		Transient: CompactionHookPayload{
			Mode:          decision.Mode,
			Reason:        decision.Reason,
			KeepTurns:     decision.KeepTurns,
			DroppedTurns:  dropped,
			SummaryLength: len(snap.CompactionSummary),
			Changed:       changed,
			CircuitOpen:   r.compactionCircuitOpen(),
		},
	})
}

func compactionDecisionAttempted(decision CompactionDecision) bool {
	switch decision.Mode {
	case CompactionSummarize, CompactionReactive, CompactionUserPartial:
		return true
	default:
		return false
	}
}

func (r *Runner) compactionCircuitOpen() bool {
	if r == nil {
		return false
	}
	if r.compactionManager != nil && r.compactionManager.CircuitOpen() {
		return true
	}
	return r.compactionMaxFailures > 0 && r.compactionFailures >= r.compactionMaxFailures
}

func droppedTurns(before, after SessionSnapshot) int {
	if dropped := after.CompactedTurns - before.CompactedTurns; dropped > 0 {
		return dropped
	}
	if dropped := len(before.RecentInputs) - len(after.RecentInputs); dropped > 0 {
		return dropped
	}
	return 0
}

func (r *Runner) SetDriver(driver llm.Driver) {
	if r == nil {
		return
	}
	r.driver = driver
}

func (r *Runner) LastResponse() string {
	if r == nil || r.session == nil {
		return ""
	}
	snap := r.session.Snapshot()
	for i := len(snap.Turns) - 1; i >= 0; i-- {
		if response := strings.TrimSpace(snap.Turns[i].FinalResponse); response != "" {
			return response
		}
	}
	return ""
}

func (r *Runner) ClearHistory() {
	if r == nil || r.session == nil {
		return
	}
	r.gitWorkflow = gitWorkflowState{}
	r.planWorkflow = planWorkflowState{}
	r.searchWorkflow = sameFileSearchWorkflowState{}
	r.validationWorkflow = validationWorkflowState{}
	r.repeatWorkflow = repeatToolCallState{}
	r.pendingRetryPrompt = ""
	r.session.Clear()
	if resetter, ok := r.driver.(llm.ConversationResetter); ok {
		resetter.ResetConversation()
	}
}

func (r *Runner) AppendUserMessage(text string) {
	if r == nil || r.session == nil {
		return
	}
	_ = r.session.AppendUserMessage(text)
}

func (r *Runner) QueuePendingInput(text string) {
	if r == nil || r.session == nil {
		return
	}
	r.session.QueuePendingInput(text)
}

func (r *Runner) DiscardPendingInput() []string {
	if r == nil || r.session == nil {
		return nil
	}
	return r.session.TakePendingInput()
}

func (r *Runner) MarkInterrupted() {
	if r == nil || r.session == nil {
		return
	}
	r.session.MarkInterrupted()
}

func (r *Runner) SetTaskState(state TaskState) {
	if r == nil || r.session == nil {
		return
	}
	r.planWorkflow = planWorkflowState{
		mode:   strings.ToLower(strings.TrimSpace(state.Operation)),
		active: isSynthesisGuardOperation(state.Operation),
	}
	r.session.SetTaskState(state)
	r.syncRuntimeNote()
	if r.renderer != nil {
		if text := formatTaskContextSummary(state); text != "" {
			r.renderer.ToolResult("__task_context", text, "", false)
		}
	}
}

func (r *Runner) TaskState() *TaskState {
	if r == nil || r.session == nil {
		return nil
	}
	return r.session.Snapshot().TaskState
}

func formatTaskContextSummary(state TaskState) string {
	var parts []string
	if obj := strings.TrimSpace(state.Objective); obj != "" {
		if len(obj) > 200 {
			obj = obj[:200] + "..."
		}
		parts = append(parts, "Objective: "+obj)
	}
	if v := strings.TrimSpace(state.RequiredVerification); v != "" {
		parts = append(parts, "Verify: "+v)
	}
	return strings.Join(parts, "\n")
}

func (r *Runner) EmitResponse(text string) {
	if r == nil || strings.TrimSpace(text) == "" {
		return
	}
	if r.session != nil {
		_ = r.session.AppendAssistantMessage(text)
	}
	if r.renderer != nil {
		r.renderer.AgentText(strings.TrimSpace(text))
	}
}

func (r *Runner) AppendSkillContext(name, body string) {
	if r == nil || r.session == nil {
		return
	}
	r.session.AppendSkillContext(name, body)
}

func (r *Runner) appendAssistantMessage(text string) error {
	if r == nil || r.session == nil {
		return nil
	}
	return r.session.AppendAssistantMessage(text)
}

func (r *Runner) appendFinalAssistantMessageAndCompleteTurn(ctx context.Context, turn int, response string, toolCalls []TurnToolCall) error {
	if err := r.ensureFinalValidationTurnCurrent(ctx, turn); err != nil {
		return err
	}
	if err := r.appendAssistantMessage(response); err != nil {
		return err
	}
	if err := r.ensureFinalValidationTurnCurrent(ctx, turn); err != nil {
		return err
	}
	return r.completeTurn(turn, response, toolCalls, nil)
}

func (r *Runner) completeTurn(turn int, response string, toolCalls []TurnToolCall, turnErr error) error {
	if r == nil || r.session == nil {
		return turnErr
	}
	if turnErr == nil && turn > 0 {
		turnID := fmt.Sprintf("turn-%d", turn)
		active, ok := r.session.ActiveTurnSnapshot()
		if !ok {
			return staleTurnError(turnID)
		}
		if active.ID == turnID && !r.session.IsActiveTurn(turnID) {
			return staleTurnError(turnID)
		}
		if active.ID != turnID {
			return staleTurnError(turnID)
		}
	}
	if err := r.session.CompleteTurn(turn, response, toolCalls, turnErr); err != nil {
		if turnErr != nil {
			return errors.Join(turnErr, err)
		}
		return err
	}
	return turnErr
}

func (r *Runner) runLoop(ctx context.Context, turn int) error {
	start := time.Now()
	turnID := fmt.Sprintf("turn-%d", turn)

	nativeCaller, isNative := r.driver.(llm.NativeToolCaller)

	completionRetries := 0
	compactionAttempts := 0
	for range r.maxSteps {
		if r.applyPendingInput() {
			r.syncRuntimeNote()
		}
		r.applyProactivePromptCompaction(ctx)
		snap := r.session.Snapshot()
		toolDefs, toolDecision := r.selectToolDefsWithDecision(snap)
		// Never force a tool call: prose from the model is its answer.
		const requireToolCall = false
		r.observeToolExposure(toolDecision)
		if len(toolDefs) > 0 && !isNative {
			err := fmt.Errorf("react runtime: driver %q does not support native tool calling", r.driver.Name())
			return r.completeTurn(turn, "", nil, err)
		}
		_ = r.session.SetActiveTurnPhase(turnID, TurnPhaseRunningModel)
		calls, err := r.streamNativeTurn(ctx, turn, nativeCaller, toolDefs, requireToolCall)
		if err != nil {
			// Recover from overflow as many times as compaction keeps freeing
			// room. A single attempt per turn is not enough for a long
			// autonomous run, where one pass often lands still over the limit.
			// Compaction reporting no change is what ends the loop, not a latch.
			if compactionAttempts < maxContextCompactionsPerTurn && r.reactiveCompactForContextError(ctx, err) {
				compactionAttempts++
				completionRetries = 0
				continue
			}
			if shouldRetryTransientStreamError(ctx, err) {
				if completionRetries < maxCompletionRetriesPerTurn {
					completionRetries++
					r.pendingRetryPrompt = ""
					r.emitRetryNotice(transientStreamRetryNotice(err))
					continue
				}
			}
			var retryable *RetryableCompletionError
			if errors.As(err, &retryable) && completionRetries < maxCompletionRetriesPerTurn {
				completionRetries++
				if prompt := strings.TrimSpace(retryable.Prompt); prompt != "" {
					r.pendingRetryPrompt = prompt
				}
				r.emitRetryNotice(retryNoticeText)
				continue
			}
			if errors.As(err, &retryable) && isEmptyNativeResponseError(retryable) {
				if failed := r.failedAgentFallbackErrorText(); failed != "" {
					return r.completeTurn(turn, "", nil, errors.New(failed))
				}
				if fallback := r.activeAgentFallbackText(); fallback != "" {
					r.pendingRetryPrompt = ""
					if ok, err := r.validateFinalCompletion(ctx, turn, fallback, false); !ok || err != nil {
						return err
					}
					if err := r.appendFinalAssistantMessageAndCompleteTurn(ctx, turn, fallback, nil); err != nil {
						return err
					}
					r.notifyTurnComplete()
					if r.renderer != nil {
						r.renderer.AgentText(fallback)
					}
					return nil
				}
			}
			if errors.As(err, &retryable) {
				if retryableCompletionAllowsCompletedAgentFallback(retryable) && ctx.Err() == nil {
					if handled, fallbackErr := r.tryCompletedAgentResultFallbackAfterError(ctx, turn); handled {
						return fallbackErr
					}
				}
				return r.completeTurn(turn, "", nil, err)
			}
			if ctx.Err() == nil {
				if handled, fallbackErr := r.tryCompletedAgentResultFallbackAfterError(ctx, turn); handled {
					return fallbackErr
				}
			}
			return r.completeTurn(turn, "", nil, err)
		}
		r.emitStats(start)
		if calls == nil {
			// streamNativeTurn already recorded the final response
			if r.applyPendingInput() {
				continue
			}
			return nil
		}
		_ = r.session.SetActiveTurnPhase(turnID, TurnPhaseRunningTools)
		if err := r.executeNativeToolCalls(ctx, turn, calls, toolDecision.ToolNames); err != nil {
			if ctx.Err() == nil {
				if handled, fallbackErr := r.tryCompletedAgentResultFallbackAfterError(ctx, turn); handled {
					return fallbackErr
				}
			}
			return err
		}
		completionRetries = 0
	}

	err := fmt.Errorf("react runtime: safety step limit (%d) exceeded", r.maxSteps)
	return r.completeTurn(turn, "", nil, err)
}

func (r *Runner) applyProactivePromptCompaction(ctx context.Context) bool {
	if r == nil || r.session == nil || r.compactionManager == nil {
		return false
	}
	changed := false
	for range 3 {
		messages := r.session.Messages(r.currentSystemPrompt())
		decision := r.compactionManager.DecidePromptPressure(messages)
		if decision.Mode == CompactionNone {
			return changed
		}
		before := estimatePromptBytes(messages)
		if !r.applyCompactionDecision(ctx, decision) {
			return changed
		}
		after := estimatePromptBytes(r.session.Messages(r.currentSystemPrompt()))
		if after >= before {
			// The pass mutated history without shrinking the prompt (e.g. the
			// oversized content survives prompt-side truncation). Stop instead
			// of thrashing on decisions that cannot converge.
			return changed
		}
		if r.progress != nil {
			r.progress("react runtime: compacting prompt before provider call")
		}
		changed = true
	}
	return changed
}

func isEmptyNativeResponseError(err *RetryableCompletionError) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Message), "empty native response")
}

func shouldRetryTransientStreamError(ctx context.Context, err error) bool {
	if err == nil {
		return false
	}
	if retryDriverExhaustedTransientError(err) {
		return false
	}
	if ctx != nil && ctx.Err() != nil {
		return false
	}
	classified := resilienceerrors.ClassifyError(err)
	if !classified.Retryable {
		return false
	}
	switch classified.Class {
	case resilienceerrors.ErrorClassRetryable, resilienceerrors.ErrorClassServer, resilienceerrors.ErrorClassCapacity:
		return true
	default:
		return false
	}
}

func retryDriverExhaustedTransientError(err error) bool {
	var exhausted *llm.RetryAttemptsExhaustedError
	if !errors.As(err, &exhausted) {
		return false
	}
	classified := resilienceerrors.ClassifyError(exhausted.Err)
	return classified.Class == resilienceerrors.ErrorClassRetryable
}

func transientStreamRetryNotice(err error) string {
	classified := resilienceerrors.ClassifyError(err)
	message := strings.TrimSpace(classified.UserMessage)
	if message == "" {
		return retryNoticeText
	}
	return message
}

func (r *Runner) reactiveCompactForContextError(ctx context.Context, err error) bool {
	if r == nil || r.compactionManager == nil || err == nil {
		return false
	}
	classified := resilienceerrors.ClassifyError(err)
	if classified.Class != resilienceerrors.ErrorClassContext {
		return false
	}
	if r.progress != nil {
		r.progress("react runtime: compacting after context window error")
	}
	if r.applyCompactionDecision(ctx, r.compactionManager.Reactive(20, "context window exceeded")) {
		return true
	}
	return r.applyCompactionDecision(ctx, CompactionDecision{Mode: CompactionMicro, Reason: "context window exceeded", KeepTurns: r.compactionManager.cfg.KeepTurns})
}

// streamNativeTurn runs one native tool calling step.
// Returns nil calls (+ nil error) when a final text answer was received.
// Returns non-nil calls when the model requested tool executions.
// secretTriggerPattern marks where a secret could still be forming. Holding
// back a fixed window instead would stop short answers streaming at all, and
// ordinary prose contains no trigger, so it streams the moment it arrives.
var secretTriggerPattern = regexp.MustCompile(`(?i)(-----BEGIN |bearer\s|sk-|gh[pousr]_|AKIA|ASIA|TOKEN|API[_-]?KEY|PASSWORD|SECRET)`)

// streamableLen returns how much of an in-flight response may be displayed:
// everything, unless a secret trigger has appeared, in which case the text
// from that trigger onwards waits until the response is complete and the
// scanner can match it whole.
func streamableLen(raw string) int {
	matches := secretTriggerPattern.FindAllStringIndex(raw, -1)
	if len(matches) == 0 {
		return len(raw)
	}
	return matches[len(matches)-1][0]
}

// streamRedactedPrefix emits whatever of an accumulating response has become
// both markup-safe and redaction-stable, and returns the new emitted length.
// Streaming raw tokens would put a secret on screen that the stored copy
// redacts, so display and storage are redacted by the same rule.
//
// Secret patterns span whitespace and a private key block is unbounded, so
// text is held back from the point a secret could still be forming rather
// than by a fixed window.
func streamRedactedPrefix(emit func(string), raw string, emitted int) int {
	cut := streamableLen(raw)
	if cut <= 0 {
		return emitted
	}
	safe := safeRawMarkupStreamingPrefixLen(raw[:cut])
	redacted := redactRuntimeText(raw[:safe])
	if len(redacted) <= emitted {
		return emitted
	}
	emit(redacted[emitted:])
	return len(redacted)
}

// reasoningTarget reports the renderer that can display thinking, when the
// renderer supports it and the session has not turned it off.
func (r *Runner) reasoningTarget() (agent.ReasoningTarget, bool) {
	if r == nil || r.renderer == nil || !r.showReasoning {
		return nil, false
	}
	target, ok := r.renderer.(agent.ReasoningTarget)
	return target, ok
}

func (r *Runner) streamNativeTurn(ctx context.Context, turn int, caller llm.NativeToolCaller, toolDefs []llm.ToolDef, requireToolCall bool) ([]llm.NativeToolCall, error) {
	messages := r.session.Messages(r.currentSystemPrompt())
	if prompt := strings.TrimSpace(r.pendingRetryPrompt); prompt != "" {
		messages = injectSystemMessageBeforeHistory(messages, "Runtime correction for the previous attempt:\n"+prompt)
	}
	r.lastPromptContextTokens = estimatePromptTokens(messages)
	opts := llm.NativeToolOptions{RequireToolCall: requireToolCall}
	if len(toolDefs) == 0 {
		return r.streamPlainTurn(ctx, turn, messages)
	}
	out := make(chan llm.Token, 64)
	streamCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	errCh := make(chan error, 1)
	go func() {
		if advanced, ok := caller.(llm.NativeToolCallerWithOptions); ok {
			errCh <- advanced.StreamWithToolsOptions(streamCtx, messages, toolDefs, opts, out)
			return
		}
		errCh <- caller.StreamWithTools(streamCtx, messages, toolDefs, out)
	}()

	var textBuf strings.Builder
	var reasoningBuf strings.Builder
	var toolCalls []llm.NativeToolCall
	visibleEmitted := 0
	streamVisible := r.renderer != nil
	suppressFinalStreaming := r.finalPlanGateMayBlock()
	reasoningTarget, showReasoning := r.reasoningTarget()
	reasoningEmitted := 0

	for tok := range out {
		if tok.ReasoningContent != "" {
			reasoningBuf.WriteString(tok.ReasoningContent)
			// Thinking was captured and stored but never surfaced, so a
			// working turn showed nothing but tool cards. Stream it as it
			// arrives, kept separate from the answer.
			if showReasoning {
				reasoningEmitted = streamRedactedPrefix(reasoningTarget.AgentReasoning, reasoningBuf.String(), reasoningEmitted)
			}
			continue
		}
		if tok.ToolCall != nil {
			toolCalls = append(toolCalls, *tok.ToolCall)
			continue
		}
		if tok.Text == "" {
			continue
		}
		textBuf.WriteString(tok.Text)
		// Stream the model's prose whether or not this step ends in tool
		// calls. Withholding it until the step finished meant every working
		// turn arrived as a block after the fact, which read as silence.
		if !suppressFinalStreaming && streamVisible && r.renderer != nil {
			visibleEmitted = streamRedactedPrefix(r.renderer.AgentToken, textBuf.String(), visibleEmitted)
		}
	}
	if err := <-errCh; err != nil {
		return nil, err
	}

	if len(toolCalls) > 0 {
		preamble := stripXMLToolCallMarkup(strings.TrimSpace(textBuf.String()))
		safePreamble := redactRuntimeText(preamble)
		reasoning := strings.TrimSpace(reasoningBuf.String())
		if err := r.rejectUnknownNativeToolCalls(ctx, turn, toolCalls, toolDefs); err != nil {
			return nil, err
		}
		r.pendingRetryPrompt = ""
		r.ensurePreMutationCheckpointForCalls(ctx, turn, toolCalls)
		if err := r.session.AppendAssistantToolTurn(safePreamble, toolCalls); err != nil {
			return nil, err
		}
		if reasoning != "" {
			r.session.SetLastAssistantReasoning(reasoning)
		}
		if safePreamble != "" && r.renderer != nil && visibleEmitted < len(safePreamble) {
			r.renderer.AgentText(safePreamble[visibleEmitted:])
		}
		return toolCalls, nil
	}

	// Final text answer
	finalText := strings.TrimSpace(textBuf.String())
	if finalText == "" {
		return nil, NewRetryableCompletionError(
			"react runtime: empty native response",
			"Your response was empty. Please provide a text response or use tool calls.",
		)
	}
	r.pendingRetryPrompt = ""
	if ok, err := r.validateFinalCompletion(ctx, turn, finalText, requireToolCall); !ok || err != nil {
		return []llm.NativeToolCall{}, err
	}
	reasoning := strings.TrimSpace(reasoningBuf.String())
	if err := r.ensureFinalValidationTurnCurrent(ctx, turn); err != nil {
		return nil, err
	}
	if reasoning != "" {
		r.session.SetLastAssistantReasoning(reasoning)
	}
	if err := r.appendFinalAssistantMessageAndCompleteTurn(ctx, turn, finalText, nil); err != nil {
		return nil, err
	}
	r.notifyTurnComplete()
	if r.renderer != nil && visibleEmitted < len(finalText) {
		r.renderer.AgentText(finalText[visibleEmitted:])
	}
	return nil, nil
}

func (r *Runner) streamPlainTurn(ctx context.Context, turn int, messages []llm.Message) ([]llm.NativeToolCall, error) {
	out := make(chan llm.Token, 64)
	errCh := make(chan error, 1)
	go func() {
		errCh <- r.driver.Stream(ctx, messages, out)
	}()

	var textBuf strings.Builder
	var reasoningBuf strings.Builder
	visibleEmitted := 0
	streamVisible := r.renderer != nil
	suppressFinalStreaming := r.finalPlanGateMayBlock()
	reasoningTarget, showReasoning := r.reasoningTarget()
	reasoningEmitted := 0

	for tok := range out {
		// Reasoning was dropped entirely on this path: not shown, not even
		// stored, so a plain answer lost the thinking behind it.
		if tok.ReasoningContent != "" {
			reasoningBuf.WriteString(tok.ReasoningContent)
			if showReasoning {
				reasoningEmitted = streamRedactedPrefix(reasoningTarget.AgentReasoning, reasoningBuf.String(), reasoningEmitted)
			}
			continue
		}
		if tok.Text == "" {
			continue
		}
		textBuf.WriteString(tok.Text)
		if !suppressFinalStreaming && streamVisible && r.renderer != nil {
			visibleEmitted = streamRedactedPrefix(r.renderer.AgentToken, textBuf.String(), visibleEmitted)
		}
	}
	if err := <-errCh; err != nil {
		return nil, err
	}

	finalText := strings.TrimSpace(textBuf.String())
	if finalText == "" {
		return nil, NewRetryableCompletionError(
			"react runtime: empty native response",
			"Your response was empty. Please provide a text response or use tool calls.",
		)
	}
	r.pendingRetryPrompt = ""
	if ok, err := r.validateFinalCompletion(ctx, turn, finalText, false); !ok || err != nil {
		return []llm.NativeToolCall{}, err
	}
	if reasoning := strings.TrimSpace(reasoningBuf.String()); reasoning != "" {
		r.session.SetLastAssistantReasoning(reasoning)
	}
	if err := r.appendFinalAssistantMessageAndCompleteTurn(ctx, turn, finalText, nil); err != nil {
		return nil, err
	}
	r.notifyTurnComplete()
	if r.renderer != nil && visibleEmitted < len(finalText) {
		r.renderer.AgentText(finalText[visibleEmitted:])
	}
	return nil, nil
}

func (r *Runner) rejectUnknownNativeToolCalls(ctx context.Context, turn int, calls []llm.NativeToolCall, toolDefs []llm.ToolDef) error {
	available := make(map[string]struct{}, len(toolDefs))
	availableNames := make([]string, 0, len(toolDefs))
	for _, def := range toolDefs {
		name := strings.TrimSpace(def.Name)
		if name == "" {
			continue
		}
		if _, ok := available[name]; ok {
			continue
		}
		available[name] = struct{}{}
		availableNames = append(availableNames, name)
	}
	for _, call := range calls {
		name := strings.TrimSpace(call.Name)
		if _, ok := available[name]; ok {
			continue
		}
		if r.leanToolExposure {
			if _, registered := r.tools.Get(name); registered {
				// Deferred tool under lean exposure: registered but its
				// schema wasn't attached this turn. Execute it anyway.
				continue
			}
		}
		if err := r.ensureTurnCanMutate(ctx, turn); err != nil {
			return err
		}
		message := fmt.Sprintf("react runtime: tool %q is not available this turn", name)
		if _, registered := r.tools.Get(name); !registered {
			message = fmt.Sprintf("react runtime: unknown tool %q", name)
		}
		availableText := strings.Join(availableNames, ", ")
		if availableText == "" {
			availableText = "none"
		}
		return NewRetryableCompletionError(
			message,
			fmt.Sprintf("The tool %q is not available. Available tools this turn: %s. Emit a valid native tool call only, or explain why no tool is needed.", name, availableText),
		)
	}
	return nil
}

func retryableCompletionAllowsCompletedAgentFallback(err *RetryableCompletionError) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(strings.TrimSpace(err.Message))
	return strings.Contains(message, `tool "wait_agent" is not available this turn`)
}

func (r *Runner) rejectRawToolMarkupFinalText(ctx context.Context, turn int, finalText string) error {
	_, ok := rawToolMarkupDetail(finalText)
	if !ok {
		return nil
	}
	if err := r.ensureTurnCanMutate(ctx, turn); err != nil {
		return err
	}
	return NewRetryableCompletionError(
		"react runtime: provider returned raw tool-call markup or deprecated XML tool-call markup",
		"Use the provider's native tool-calling interface only. Do not emit DSML, XML, JSON tool-call objects, or example markup as final assistant text.",
	)
}

func safeRawMarkupStreamingPrefixLen(text string) int {
	unsafeAt := -1
	for _, marker := range []string{"<", "{", "["} {
		if idx := strings.Index(text, marker); idx >= 0 && (unsafeAt < 0 || idx < unsafeAt) {
			unsafeAt = idx
		}
	}
	if unsafeAt >= 0 {
		return unsafeAt
	}
	return len(text)
}

func rawToolMarkupDetail(text string) (string, bool) {
	trimmed := strings.TrimSpace(text)
	lower := strings.ToLower(trimmed)
	if strings.Contains(lower, "<｜｜dsml｜｜") || strings.Contains(lower, "<||dsml||") {
		return "DSML", true
	}
	if looksLikeLegacyXMLToolCall(trimmed) {
		return "tool_call", true
	}
	if detail, ok := jsonToolCallDetail([]byte(trimmed)); ok {
		return detail, true
	}
	if detail, ok := embeddedJSONToolCallDetail(trimmed); ok {
		return detail, true
	}
	return "", false
}

func embeddedJSONToolCallDetail(text string) (string, bool) {
	text = textOutsideFencedCodeBlocks(text)
	for i := 0; i < len(text); i++ {
		if text[i] != '{' && text[i] != '[' {
			continue
		}
		raw, ok := balancedJSONValueAt(text, i)
		if !ok {
			continue
		}
		if detail, ok := jsonToolCallDetail(raw); ok {
			return detail, true
		}
	}
	return "", false
}

func textOutsideFencedCodeBlocks(text string) string {
	var out strings.Builder
	inFence := false
	for _, line := range strings.SplitAfter(text, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			inFence = !inFence
			continue
		}
		if !inFence {
			out.WriteString(line)
		}
	}
	return out.String()
}

func balancedJSONValueAt(text string, start int) ([]byte, bool) {
	if start < 0 || start >= len(text) || (text[start] != '{' && text[start] != '[') {
		return nil, false
	}
	stack := []byte{text[start]}
	inString := false
	escaped := false
	for i := start + 1; i < len(text); i++ {
		c := text[i]
		if inString {
			if escaped {
				escaped = false
				continue
			}
			switch c {
			case '\\':
				escaped = true
			case '"':
				inString = false
			}
			continue
		}
		switch c {
		case '"':
			inString = true
		case '{', '[':
			stack = append(stack, c)
		case '}', ']':
			if len(stack) == 0 || !jsonBracketsMatch(stack[len(stack)-1], c) {
				return nil, false
			}
			stack = stack[:len(stack)-1]
			if len(stack) == 0 {
				raw := []byte(text[start : i+1])
				if json.Valid(raw) {
					return raw, true
				}
				return nil, false
			}
		}
	}
	return nil, false
}

func jsonBracketsMatch(open, close byte) bool {
	return (open == '{' && close == '}') || (open == '[' && close == ']')
}

func jsonToolCallDetail(raw json.RawMessage) (string, bool) {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || (!strings.HasPrefix(trimmed, "{") && !strings.HasPrefix(trimmed, "[")) {
		return "", false
	}
	if strings.HasPrefix(trimmed, "[") {
		var items []json.RawMessage
		if err := json.Unmarshal(raw, &items); err != nil {
			return "", false
		}
		for _, item := range items {
			if detail, ok := jsonToolCallDetail(item); ok {
				return detail, true
			}
		}
		return "", false
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		return "", false
	}
	if detail, ok := directJSONToolCallDetail(obj); ok {
		return detail, true
	}
	if function, ok := obj["function"]; ok {
		if detail, ok := jsonToolCallDetail(function); ok {
			return detail, true
		}
	}
	if calls, ok := obj["tool_calls"]; ok {
		if detail, ok := jsonToolCallDetail(calls); ok {
			return detail, true
		}
	}
	return "", false
}

func directJSONToolCallDetail(obj map[string]json.RawMessage) (string, bool) {
	name := jsonStringField(obj, "name")
	if name == "" {
		name = jsonStringField(obj, "tool_name")
	}
	if name == "" || !jsonObjectHasAnyField(obj, "arguments", "args") {
		return "", false
	}
	return name, true
}

func jsonObjectHasAnyField(obj map[string]json.RawMessage, fields ...string) bool {
	for _, field := range fields {
		if _, ok := obj[field]; ok {
			return true
		}
	}
	return false
}

func jsonStringField(obj map[string]json.RawMessage, field string) string {
	raw, ok := obj[field]
	if !ok {
		return ""
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return ""
	}
	return strings.TrimSpace(value)
}

func parseLegacyXMLToolCall(text string) (llm.NativeToolCall, bool) {
	const open = "<tool_call>"
	const close = "</tool_call>"
	_, rest, ok := strings.Cut(text, open)
	if !ok {
		return llm.NativeToolCall{}, false
	}
	inner, _, ok := strings.Cut(rest, close)
	if !ok {
		return llm.NativeToolCall{}, false
	}
	inner = strings.TrimSpace(inner)
	var parsed struct {
		Name string          `json:"name"`
		Args json.RawMessage `json:"args"`
	}
	if err := json.Unmarshal([]byte(inner), &parsed); err != nil {
		return llm.NativeToolCall{}, false
	}
	name := strings.TrimSpace(parsed.Name)
	if name == "" {
		return llm.NativeToolCall{}, false
	}
	args := strings.TrimSpace(string(parsed.Args))
	if args == "" {
		args = "{}"
	}
	return llm.NativeToolCall{
		ID:       "legacy_xml_call_1",
		Name:     name,
		ArgsJSON: args,
	}, true
}

func (r *Runner) notifyTurnComplete() {
	if r == nil || r.session == nil || r.turnComplete == nil {
		return
	}
	r.turnComplete(r.session.Snapshot())
}

func (r *Runner) emitRetryNotice(msg string) {
	if r == nil {
		return
	}
	msg = strings.TrimSpace(msg)
	if msg == "" {
		return
	}
	if notifier, ok := r.renderer.(agent.RetryNotifier); ok {
		notifier.Retry(msg)
		return
	}
	if r.progress != nil {
		r.progress(msg)
	}
}

// executeNativeToolCalls executes a batch of native tool calls and appends results
// to the session. Unknown or unavailable tools are model-output failures and do
// not append synthetic successful tool results; execution errors still abort the
// turn after recording the failed result.
// Tool executions are dispatched in parallel (matching Codex's FuturesOrdered model)
// to reduce total wall-clock time when multiple independent tools are requested.
func (r *Runner) executeNativeToolCalls(ctx context.Context, turn int, calls []llm.NativeToolCall, allowedToolNames ...[]string) error {
	type toolExec struct {
		call       llm.NativeToolCall
		tool       agenttools.Tool
		args       map[string]any
		beforeTool hooks.ExecutionOutput
		execute    func() ToolRunResult
	}
	var allowed map[string]struct{}
	var allowedList []string
	if len(allowedToolNames) > 0 {
		allowedList = uniqueStrings(allowedToolNames[0])
		if len(allowedList) > 0 {
			allowed = make(map[string]struct{}, len(allowedList))
			for _, name := range allowedList {
				allowed[name] = struct{}{}
			}
		}
	}

	// Phase 1: resolve tools, parse args, run pre-hooks (sequential, may have side effects)
	execs := make([]toolExec, 0, len(calls))
	for _, call := range calls {
		tool, ok := r.tools.Get(call.Name)
		if !ok {
			if err := r.ensureTurnCanMutate(ctx, turn); err != nil {
				return err
			}
			return fmt.Errorf("react runtime: unknown tool %q", call.Name)
		}
		if len(allowed) > 0 {
			if _, ok := allowed[strings.TrimSpace(call.Name)]; !ok {
				// Under lean exposure the allowed list is the reduced core
				// schema set, but registered-but-deferred tools (tool==ok
				// above) stay callable — mirror rejectUnknownNativeToolCalls.
				if !r.leanToolExposure {
					if err := r.ensureTurnCanMutate(ctx, turn); err != nil {
						return err
					}
					return fmt.Errorf("react runtime: tool %q is not available for this turn. Available tools this turn: %s", call.Name, strings.Join(allowedList, ", "))
				}
			}
		}
		if err := r.ensureTurnCanMutate(ctx, turn); err != nil {
			return err
		}

		var args map[string]any
		if err := json.Unmarshal([]byte(call.ArgsJSON), &args); err != nil {
			parseErr := fmt.Sprintf("error: malformed tool call arguments for %q: %v", call.Name, err)
			if err := r.appendFailureAndToolResultForTurn(ctx, turn,
				protocol.FailureItem{Decision: protocol.ClassifyToolArgFailure(parseErr)},
				protocol.ToolCallItem{ToolName: call.Name, ToolCallID: call.ID},
				protocol.ToolResultItem{ToolCallID: call.ID, Text: parseErr},
			); err != nil {
				return err
			}
			if r.renderer != nil {
				r.renderer.ToolCall(call.Name, "malformed arguments")
				r.renderer.ToolResult(call.Name, parseErr, "", true)
			}
			r.updatePlanWorkflow(call.Name, nil, "", true)
			r.updateSameFileSearchWorkflow(call.Name, nil, true)
			continue
		}
		if validationErr := validateToolArgs(tool, args); validationErr != "" {
			if err := r.appendFailureAndToolResultForTurn(ctx, turn,
				protocol.FailureItem{Decision: protocol.ClassifyToolArgFailure(validationErr)},
				protocol.ToolCallItem{ToolName: call.Name, ToolCallID: call.ID, Args: args},
				protocol.ToolResultItem{ToolCallID: call.ID, Text: validationErr},
			); err != nil {
				return err
			}
			if r.renderer != nil {
				r.renderer.ToolCall(call.Name, reactToolSummary(args))
				r.renderer.ToolResult(call.Name, validationErr, "", true)
			}
			r.updatePlanWorkflow(call.Name, args, "", true)
			r.updateSameFileSearchWorkflow(call.Name, args, true)
			r.updateRepeatToolCallWorkflow(call.Name, args, validationErr)
			continue
		}
		if blocked, ok := r.blockRepeatedExplorationToolCall(call.Name, args); ok {
			if err := r.appendFailureAndToolResultForTurn(ctx, turn,
				protocol.FailureItem{Decision: protocol.ClassifyPolicyBlocked(blocked)},
				protocol.ToolCallItem{ToolName: call.Name, ToolCallID: call.ID, Args: args},
				protocol.ToolResultItem{ToolCallID: call.ID, Text: blocked},
			); err != nil {
				return err
			}
			if r.renderer != nil {
				r.renderer.ToolCall(call.Name, reactToolSummary(args))
				r.renderer.ToolResult(call.Name, blocked, "", true)
			}
			r.updatePlanWorkflow(call.Name, args, "", true)
			r.updateSameFileSearchWorkflow(call.Name, args, true)
			r.recordBlockedRepeat(call.Name, args)
			continue
		}

		if isCheckpointMutatingTool(call.Name) {
			r.ensurePreMutationCheckpoint(ctx, turn)
		}
		beforeTool := r.beforeToolHookOutput(ctx, call.Name, args)
		if beforeTool.Block != nil {
			blocked := strings.TrimSpace(beforeTool.Block.Message)
			if err := r.appendFailureAndToolResultForTurn(ctx, turn,
				protocol.FailureItem{Decision: protocol.ClassifyPolicyBlocked(blocked)},
				protocol.ToolCallItem{ToolName: call.Name, ToolCallID: call.ID, Args: args},
				protocol.ToolResultItem{ToolCallID: call.ID, Text: blocked},
			); err != nil {
				return err
			}
			if r.renderer != nil {
				r.renderer.ToolCall(call.Name, reactToolSummary(args))
				r.renderer.ToolResult(call.Name, blocked, "", true)
			}
			r.updatePlanWorkflow(call.Name, args, "", true)
			r.updateSameFileSearchWorkflow(call.Name, args, true)
			continue
		}

		toolRef := tool // capture for closure
		orchestrator := ToolOrchestrator{}
		execs = append(execs, toolExec{
			call:       call,
			tool:       toolRef,
			args:       args,
			beforeTool: beforeTool,
			execute: func() ToolRunResult {
				return orchestrator.Run(ctx, ToolRunRequest{
					TurnID: turn,
					CallID: call.ID,
					Tool:   toolRef,
					Args:   args,
				})
			},
		})
		if err := r.appendToolCallForTurn(ctx, turn, protocol.ToolCallItem{ToolName: call.Name, ToolCallID: call.ID, Args: args}); err != nil {
			return err
		}
		if r.renderer != nil {
			r.renderer.ToolCall(call.Name, reactToolSummary(args))
		}
	}

	// Phase 2: dispatch tool executions (parallel for safe tools, sequential when
	// any tool declares serial-only concurrency).
	type execResult struct {
		index  int
		status ToolRunStatus
		result string
		diff   string
		err    error
	}
	results := make([]execResult, 0, len(execs))
	hasSerialTool := false
	for _, exec := range execs {
		if !exec.tool.ParallelSafe() {
			hasSerialTool = true
			break
		}
	}
	if hasSerialTool {
		// Sequential: execute tools one at a time, in order
		for _, exec := range execs {
			run := exec.execute()
			results = append(results, execResult{status: run.Status, result: run.Result, diff: run.Diff, err: run.Error})
		}
	} else {
		// Parallel: dispatch all tools in goroutines, collect in order
		type indexedResult struct {
			index  int
			status ToolRunStatus
			result string
			diff   string
			err    error
		}
		ch := make(chan indexedResult, len(execs))
		var wg sync.WaitGroup
		for i, exec := range execs {
			i, exec := i, exec
			wg.Add(1)
			go func() {
				defer wg.Done()
				run := exec.execute()
				ch <- indexedResult{index: i, status: run.Status, result: run.Result, diff: run.Diff, err: run.Error}
			}()
		}
		wg.Wait()
		close(ch)

		ordered := make([]execResult, len(execs))
		for r := range ch {
			ordered[r.index] = execResult(r)
		}
		results = append(results, ordered...)
	}

	// Phase 3: apply results in original call order (preserving history ordering)
	mutated := false
	var changedFiles []string
	for i, res := range results {
		exec := execs[i]
		call := exec.call
		args := exec.args
		beforeTool := exec.beforeTool

		if res.err == nil && res.status != "" && res.status != ToolRunSucceeded {
			res.err = context.Canceled
			if res.status == ToolRunTimedOut {
				res.err = context.DeadlineExceeded
			}
		}
		if res.err != nil {
			errResult := fmt.Sprintf("error: %v", res.err)
			appended, appendErr := r.appendNativeToolResultForTurn(ctx, turn, call.ID, errResult)
			if appendErr != nil {
				return appendErr
			}
			if !appended {
				if r.hasTurnSnapshot(turn) {
					return res.err
				}
				return nil
			}
			r.applyHookOutput(beforeTool)
			r.applyHookOutput(r.afterToolHookOutput(ctx, call.Name, args, true, errResult))
			if r.renderer != nil {
				r.renderer.ToolResult(call.Name, errResult, res.diff, true)
			}
			r.updateGitWorkflow(call.Name, args, errResult)
			if isModelCorrectableToolExecutionError(call.Name, res.err) {
				continue
			}
			return r.completeTurn(turn, "", nil, res.err)
		}

		toolResult, appendErr := r.toolResultItem(ctx, turn, call.Name, call.ID, res.result)
		if appendErr != nil {
			return appendErr
		}
		display := renderableToolResultText(toolResult)
		appended, appendErr := r.appendNativeToolResultItemForTurn(ctx, turn, toolResult)
		if appendErr != nil {
			return appendErr
		}
		if !appended {
			return nil
		}
		if r.renderer != nil {
			r.renderer.ToolResult(call.Name, display, res.diff, false)
		}
		r.updateGitWorkflow(call.Name, args, res.result)
		r.clearBlockingHandoffsAfterWrite(call.Name, args)
		r.updatePlanWorkflow(call.Name, args, res.result, false)
		r.updateSameFileSearchWorkflow(call.Name, args, false)
		r.updateValidationWorkflow(call.Name, args, res.result)
		r.updateRepeatToolCallWorkflow(call.Name, args, res.result)
		r.recordCheckpointScope(ctx, turn, call.Name, args)
		if exec.tool.MutatesWorkspace && strings.TrimSpace(res.diff) != "" {
			mutated = true
			changedFiles = append(changedFiles, checkpointScopePaths(call.Name, args)...)
		}
		r.applyHookOutput(beforeTool)
		r.applyHookOutput(r.afterToolHookOutput(ctx, call.Name, args, false, ""))
	}
	r.runPostEditValidator(ctx, changedFiles, mutated)
	return nil
}

func runCommandResultExitZero(result string) bool {
	lines := strings.Split(result, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		return line == "exit 0"
	}
	return false
}

func (r *Runner) runPostEditValidator(ctx context.Context, changedFiles []string, mutated bool) {
	if r == nil || r.postEditValidator == nil || !mutated {
		return
	}
	result := r.postEditValidator.Validate(ctx, PostEditValidationRequest{ChangedFiles: uniqueStrings(changedFiles)})
	if result.Err == nil {
		return
	}
	output := strings.TrimSpace(result.Output)
	message := fmt.Sprintf("Runtime diagnostic feedback (not a user instruction): Post-edit validation failed after %s: %v", result.Duration.Round(time.Millisecond), result.Err)
	if output != "" {
		message += "\n\n" + output
	}
	if err := r.session.AppendUserMessage(message); err != nil && r.progress != nil {
		r.progress("post-edit validation feedback was not persisted: " + err.Error())
	}
}

func (r *Runner) ensurePreMutationCheckpoint(ctx context.Context, turn int) {
	if r == nil || r.session == nil || r.checkpointManager == nil {
		return
	}
	turnID := fmt.Sprintf("turn-%d", turn)
	if r.checkpointedTurns == nil {
		r.checkpointedTurns = make(map[string]bool)
	}
	if r.checkpointedTurns[turnID] {
		return
	}
	r.checkpointedTurns[turnID] = true
	cp, err := r.checkpointManager.Create(ctx, turnID)
	if err != nil {
		if r.progress != nil {
			r.progress("workspace checkpoint skipped: " + err.Error())
		}
		return
	}
	if r.checkpointIDsByTurn == nil {
		r.checkpointIDsByTurn = make(map[string]string)
	}
	r.checkpointIDsByTurn[turnID] = cp.ID
	if err := r.session.AppendItem(protocol.Item{
		Kind:   protocol.ItemCheckpoint,
		TurnID: turnID,
		Checkpoint: &protocol.CheckpointItem{
			ID:           cp.ID,
			Phase:        "created",
			ChangedFiles: cp.ChangedFiles,
		},
	}); err != nil && r.progress != nil {
		r.progress("workspace checkpoint item was not persisted: " + err.Error())
	}
}

func (r *Runner) ensurePreMutationCheckpointForCalls(ctx context.Context, turn int, calls []llm.NativeToolCall) {
	if r == nil || r.tools == nil || len(calls) == 0 {
		return
	}
	for _, call := range calls {
		if !isCheckpointMutatingTool(call.Name) {
			continue
		}
		tool, ok := r.tools.Get(call.Name)
		if !ok {
			continue
		}
		var args map[string]any
		if err := json.Unmarshal([]byte(call.ArgsJSON), &args); err != nil {
			continue
		}
		if validateToolArgs(tool, args) != "" {
			continue
		}
		r.ensurePreMutationCheckpoint(ctx, turn)
		return
	}
}

func (r *Runner) recordCheckpointScope(ctx context.Context, turn int, toolName string, args map[string]any) {
	if r == nil || r.checkpointManager == nil || r.checkpointIDsByTurn == nil {
		return
	}
	checkpointID := r.checkpointIDsByTurn[fmt.Sprintf("turn-%d", turn)]
	if checkpointID == "" {
		return
	}
	paths := checkpointScopePaths(toolName, args)
	if len(paths) == 0 {
		return
	}
	if err := r.checkpointManager.RecordChangedFiles(ctx, checkpointID, paths); err != nil && r.progress != nil {
		r.progress("workspace checkpoint scope skipped: " + err.Error())
	}
}

func (r *Runner) blockRepeatedExplorationToolCall(toolName string, args map[string]any) (string, bool) {
	toolName = strings.TrimSpace(toolName)
	// Any run_command qualifies, not only read-only ones. A command that keeps
	// returning the identical result is making no progress whatever it does,
	// and requiring it to look read-only let a rejected command repeat forever.
	if !isExplorationToolCall(toolName, args) && toolName != "run_command" {
		return "", false
	}
	target := repeatToolCallTarget(toolName, args)
	if target == "" {
		return "", false
	}
	count := repeatToolCallStalledOccurrences(r.repeatWorkflow, toolName+":"+target)
	if count < toolThrashThreshold(r.toolThrashCircuitBreaker, repeatToolCallBlockThreshold) {
		return "", false
	}
	return fmt.Sprintf("blocked: identical %s on %q already ran %d times in your recent calls and returned the same result. Do not repeat it. Use the evidence already gathered, target a different range or pattern, or synthesize the answer now.", toolName, target, count), true
}

func repeatToolCallOccurrences(recent []string, key string) int {
	count := 0
	for _, k := range recent {
		if k == key {
			count++
		}
	}
	return count
}

// repeatToolCallStalledOccurrences counts how many times the key recurred with
// one unchanged result, which is the signal that repeating it is pointless.
func repeatToolCallStalledOccurrences(state repeatToolCallState, key string) int {
	byResult := map[string]int{}
	best := 0
	for i, k := range state.recent {
		if k != key || i >= len(state.recentResults) {
			continue
		}
		byResult[state.recentResults[i]]++
		if n := byResult[state.recentResults[i]]; n > best {
			best = n
		}
	}
	return best
}

// repeatToolCallStalledDigest returns the digest that recurred most for a key,
// which is the result the repeats are stuck on.
func repeatToolCallStalledDigest(state repeatToolCallState, key string) string {
	byResult := map[string]int{}
	best, digest := 0, ""
	for i, k := range state.recent {
		if k != key || i >= len(state.recentResults) {
			continue
		}
		byResult[state.recentResults[i]]++
		if n := byResult[state.recentResults[i]]; n > best {
			best, digest = n, state.recentResults[i]
		}
	}
	return digest
}

// repeatToolCallResultDigest bounds how much of a result is retained; only
// equality matters, never the content.
func repeatToolCallResultDigest(result string) string {
	sum := fnv.New64a()
	_, _ = sum.Write([]byte(result))
	return strconv.FormatUint(sum.Sum64(), 16)
}

func uniqueStrings(values []string) []string {
	seen := make(map[string]bool, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		result = append(result, value)
	}
	return result
}

func isCheckpointMutatingTool(name string) bool {
	switch name {
	case "write_file", "edit_file", "apply_patch", "artifact_write", "git_commit", "run_command", "exec_session_write", "command_write_stdin":
		return true
	default:
		return false
	}
}

func (r *Runner) ensureTurnCanMutate(ctx context.Context, turn int) error {
	if ctx != nil && ctx.Err() != nil {
		return ctx.Err()
	}
	if r == nil || r.session == nil {
		return nil
	}
	turnID := fmt.Sprintf("turn-%d", turn)
	if !r.session.IsActiveTurn(turnID) {
		return staleTurnError(turnID)
	}
	return nil
}

func (r *Runner) appendToolCallForTurn(ctx context.Context, turn int, toolCall protocol.ToolCallItem) error {
	if err := r.ensureTurnCanMutate(ctx, turn); err != nil {
		return err
	}
	if r == nil || r.session == nil {
		return nil
	}
	return r.session.AppendToolCallForTurn(fmt.Sprintf("turn-%d", turn), toolCall)
}

func (r *Runner) appendFailureAndToolResultForTurn(ctx context.Context, turn int, failure protocol.FailureItem, toolCall protocol.ToolCallItem, result protocol.ToolResultItem) error {
	if err := r.ensureTurnCanMutate(ctx, turn); err != nil {
		return err
	}
	if r == nil || r.session == nil {
		return nil
	}
	return r.session.AppendFailureAndToolResultForTurn(fmt.Sprintf("turn-%d", turn), failure, toolCall, result)
}

func (r *Runner) hasTurnSnapshot(turn int) bool {
	if r.session == nil {
		return false
	}
	active, ok := r.session.ActiveTurnSnapshot()
	return ok && active.ID == fmt.Sprintf("turn-%d", turn)
}

func (r *Runner) appendNativeToolResultForTurn(ctx context.Context, turn int, toolCallID, result string) (bool, error) {
	return r.appendNativeToolResultItemForTurn(ctx, turn, protocol.ToolResultItem{ToolCallID: toolCallID, Text: result})
}

func (r *Runner) appendNativeToolResultItemForTurn(ctx context.Context, turn int, result protocol.ToolResultItem) (bool, error) {
	if r.session == nil {
		return false, nil
	}
	turnID := fmt.Sprintf("turn-%d", turn)
	if r.session.IsActiveTurn(turnID) {
		err := r.session.AppendToolResultForTurn(turnID, result)
		if errors.Is(err, ErrStaleTurn) {
			return false, nil
		}
		return err == nil, err
	}
	if ctx != nil && ctx.Err() != nil {
		return false, nil
	}
	return false, staleTurnError(turnID)
}

func readOutputLimitArg(value any) (int64, bool) {
	switch v := value.(type) {
	case int:
		return int64(v), true
	case int64:
		return v, true
	case int32:
		return int64(v), true
	case float64:
		return int64(v), true
	case json.Number:
		n, err := v.Int64()
		return n, err == nil
	}
	return 0, false
}

func (r *Runner) toolResultItem(ctx context.Context, turn int, toolName, toolCallID, result string) (protocol.ToolResultItem, error) {
	item := protocol.ToolResultItem{ToolCallID: toolCallID, Text: result}
	if strings.TrimSpace(toolName) == "read_output" {
		return item, nil
	}
	if r == nil || r.outputStore == nil || len(result) <= r.outputStoreThresholdBytes {
		return item, nil
	}
	handle, err := r.outputStore.Put(ctx, "session", []byte(result))
	if err != nil {
		return protocol.ToolResultItem{}, err
	}
	item.Handle = handle.ID
	item.OriginalBytes = handle.Bytes
	item.SHA256 = handle.SHA256
	// Tell the model how to read it, now. Saying retrieval "will be available
	// later" made models treat large output as permanently unreachable and
	// abandon the task, when read_output serves it immediately.
	item.Text = fmt.Sprintf(
		"Tool output was large (%d bytes) and is stored under handle %s (sha256 %s). "+
			"Read it now with read_output(handle=%q), optionally with offset and limit to page through it.",
		handle.Bytes, handle.ID, handle.SHA256, handle.ID)
	return item, nil
}

func validateToolArgs(tool agenttools.Tool, args map[string]any) string {
	if tool.Schema != nil {
		return validateToolSchema(tool.Name, tool.Schema, args)
	}
	for _, param := range tool.Parameters {
		value, ok := args[param.Name]
		if !ok || value == nil {
			if param.Required {
				return fmt.Sprintf("error: %s.%s is required", tool.Name, param.Name)
			}
			continue
		}
		switch param.Type {
		case "string":
			text, ok := value.(string)
			if !ok {
				return fmt.Sprintf("error: %s.%s must be a string", tool.Name, param.Name)
			}
			if param.Required && strings.TrimSpace(text) == "" {
				return fmt.Sprintf("error: %s.%s is required", tool.Name, param.Name)
			}
			if containsOmittedPlaceholder(text) {
				return fmt.Sprintf("error: %s.%s contains the literal marker \"<omitted N chars>\". That marker is a truncation placeholder from your conversation history, not real content — do not copy earlier tool calls. Regenerate the complete value from scratch.", tool.Name, param.Name)
			}
		case "int":
			number, ok := value.(float64)
			if !ok || number != float64(int(number)) {
				return fmt.Sprintf("error: %s.%s must be an integer", tool.Name, param.Name)
			}
		case "bool":
			if _, ok := value.(bool); !ok {
				return fmt.Sprintf("error: %s.%s must be a boolean", tool.Name, param.Name)
			}
		}
	}
	return ""
}

func validateToolSchema(path string, schema *llm.ToolSchema, value any) string {
	if schema == nil {
		return ""
	}
	switch schema.Type {
	case "object":
		obj, ok := value.(map[string]any)
		if !ok {
			return fmt.Sprintf("error: %s must be an object", path)
		}
		for _, required := range schema.Required {
			v, ok := obj[required]
			if !ok || v == nil {
				return fmt.Sprintf("error: %s.%s is required", path, required)
			}
		}
		if schema.AdditionalProperties != nil && !*schema.AdditionalProperties {
			for name := range obj {
				if _, ok := schema.Properties[name]; !ok {
					return fmt.Sprintf("error: %s.%s is not allowed", path, name)
				}
			}
		}
		for name, prop := range schema.Properties {
			v, ok := obj[name]
			if !ok || v == nil {
				continue
			}
			if err := validateToolSchema(path+"."+name, prop, v); err != "" {
				return err
			}
		}
	case "array":
		items, ok := value.([]any)
		if !ok {
			return fmt.Sprintf("error: %s must be an array", path)
		}
		for i, item := range items {
			if err := validateToolSchema(fmt.Sprintf("%s[%d]", path, i), schema.Items, item); err != "" {
				return err
			}
		}
	case "string":
		text, ok := value.(string)
		if !ok {
			return fmt.Sprintf("error: %s must be a string", path)
		}
		if strings.TrimSpace(text) == "" {
			return fmt.Sprintf("error: %s is required", path)
		}
		if containsOmittedPlaceholder(text) {
			return fmt.Sprintf("error: %s contains the literal marker \"<omitted N chars>\". That marker is a truncation placeholder from your conversation history, not real content — do not copy earlier tool calls. Regenerate the complete value from scratch.", path)
		}
		if len(schema.Enum) > 0 {
			for _, allowed := range schema.Enum {
				if text == allowed {
					return ""
				}
			}
			return fmt.Sprintf("error: %s must be one of: %s", path, strings.Join(schema.Enum, ", "))
		}
	case "integer":
		number, ok := value.(float64)
		if !ok || number != float64(int(number)) {
			return fmt.Sprintf("error: %s must be an integer", path)
		}
	case "boolean":
		if _, ok := value.(bool); !ok {
			return fmt.Sprintf("error: %s must be a boolean", path)
		}
	}
	return ""
}

func containsOmittedPlaceholder(text string) bool {
	text = strings.TrimSpace(text)
	start := strings.Index(text, "<omitted ")
	for start >= 0 {
		rest := text[start+len("<omitted "):]
		end := strings.Index(rest, " chars>")
		if end > 0 {
			digits := rest[:end]
			allDigits := true
			for _, r := range digits {
				if r < '0' || r > '9' {
					allDigits = false
					break
				}
			}
			if allDigits {
				return true
			}
		}
		next := strings.Index(text[start+1:], "<omitted ")
		if next < 0 {
			return false
		}
		start += next + 1
	}
	return false
}

func isModelCorrectableToolExecutionError(name string, err error) bool {
	if err == nil {
		return false
	}
	if strings.Contains(err.Error(), "escapes working directory") {
		switch name {
		case "write_file", "edit_file", "apply_patch":
			return true
		}
	}
	if name == "read_output" && strings.Contains(err.Error(), "read output handle") {
		return true
	}
	switch name {
	case "ask_user_question":
		return true
	default:
		return false
	}
}

func injectSystemMessageBeforeHistory(messages []llm.Message, content string) []llm.Message {
	content = strings.TrimSpace(content)
	if content == "" {
		return messages
	}
	insertAt := len(messages)
	for i, msg := range messages {
		if msg.Role != llm.RoleSystem {
			insertAt = i
			break
		}
	}
	injected := llm.Message{Role: llm.RoleSystem, Content: content}
	messages = append(messages, llm.Message{})
	copy(messages[insertAt+1:], messages[insertAt:])
	messages[insertAt] = injected
	return messages
}

func (r *Runner) currentSystemPrompt() string {
	if r.systemPrompt == nil {
		return ""
	}
	return strings.TrimSpace(r.systemPrompt())
}

func (r *Runner) applyPendingInput() bool {
	if r == nil || r.session == nil {
		return false
	}
	pending := r.session.TakePendingInput()
	if len(pending) == 0 {
		return false
	}
	for _, text := range pending {
		r.session.appendQueuedUserInput(text)
	}
	if r.progress != nil {
		r.progress(fmt.Sprintf("react runtime: applying %d queued input message(s)", len(pending)))
	}
	return true
}

func (r *Runner) emitStats(start time.Time) {
	if r == nil {
		return
	}
	var usage llm.Usage
	if reporter, ok := r.driver.(llm.UsageReporter); ok {
		usage = reporter.LastUsage()
	}
	duration := time.Since(start)
	if r.session != nil {
		r.session.AppendStats(duration, usage)
	}
	if r.renderer != nil {
		contextUsed := r.lastPromptContextTokens
		r.lastPromptContextTokens = 0
		if contextUsed > 0 {
			if renderer, ok := r.renderer.(agent.ContextStatsTarget); ok {
				renderer.StatsWithContext(duration, usage, contextUsed)
				return
			}
		}
		r.renderer.Stats(duration, usage)
	}
}

func reactToolSummary(args map[string]any) string {
	if path, _ := args["path"].(string); strings.TrimSpace(path) != "" {
		return redactRuntimeText(strings.TrimSpace(path))
	}
	if command, _ := args["command"].(string); strings.TrimSpace(command) != "" {
		return redactRuntimeText(strings.TrimSpace(command))
	}
	if query, _ := args["query"].(string); strings.TrimSpace(query) != "" {
		return redactRuntimeText(strings.TrimSpace(query))
	}
	if task, _ := args["task_description"].(string); strings.TrimSpace(task) != "" {
		return redactRuntimeText(strings.TrimSpace(task))
	}
	if role, _ := args["role"].(string); strings.TrimSpace(role) != "" {
		return redactRuntimeText(strings.TrimSpace(role))
	}
	if id, _ := args["id"].(string); strings.TrimSpace(id) != "" {
		return redactRuntimeText(strings.TrimSpace(id))
	}
	if pattern, _ := args["pattern"].(string); strings.TrimSpace(pattern) != "" {
		return redactRuntimeText(strings.TrimSpace(pattern))
	}
	return ""
}

func truncateToolResult(result string) string {
	lines := strings.Split(result, "\n")
	if len(lines) > 20 {
		return strings.Join(lines[:20], "\n") + fmt.Sprintf("\n... (%d more lines)", len(lines)-20)
	}
	return result
}

func renderableToolResultText(result protocol.ToolResultItem) string {
	if strings.TrimSpace(result.Handle) != "" {
		return fmt.Sprintf("output stored (%d bytes); full content available to the model via read_output", result.OriginalBytes)
	}
	return truncateToolResult(result.Text)
}

// LeanCoreToolNames is the reduced schema set exposed under lean tool
// exposure (weak/local models). Everything else stays registered and
// callable; deferred tools are advertised by name in the system prompt
// with schemas available via tool_help.
var LeanCoreToolNames = []string{
	"read_file", "write_file", "edit_file", "list_dir", "glob", "search",
	"run_command", "read_output",
	"git_status", "git_diff", "git_log", "git_commit", "git_push",
	"update_plan", "ask_user_question", "tool_help",
}

func (r *Runner) selectToolDefsWithDecision(snapshot SessionSnapshot) ([]llm.ToolDef, ToolExposureDecision) {
	decision := newToolExposureDecision(snapshot)
	if r == nil || r.tools == nil {
		return nil, decision
	}
	// The model always sees every registered tool. Which tools a turn needs
	// is the model's call; keyword-routing tool exposure from the user's
	// phrasing caused most of the runtime's unreliability.
	defs := r.tools.ToLLMToolDefs()
	if r.leanToolExposure {
		// Static per-session profile, not per-turn keyword routing: the
		// exposed set never changes between turns, so no flip-flopping.
		core := make(map[string]bool, len(LeanCoreToolNames))
		for _, name := range LeanCoreToolNames {
			core[name] = true
		}
		lean := make([]llm.ToolDef, 0, len(LeanCoreToolNames))
		for _, def := range defs {
			if core[def.Name] {
				lean = append(lean, def)
			}
		}
		if len(lean) > 0 {
			return lean, decision.withTools("lean", lean)
		}
	}
	return defs, decision.withTools("all", defs)
}
func newToolExposureDecision(snapshot SessionSnapshot) ToolExposureDecision {
	decision := ToolExposureDecision{
		Reason:                "none",
		OutstandingAgentCount: len(outstandingSpawnedAgents(snapshot)),
	}
	return decision
}

func (d ToolExposureDecision) withTools(reason string, defs []llm.ToolDef) ToolExposureDecision {
	if strings.TrimSpace(reason) != "" {
		d.Reason = reason
	}
	d.ToolNames = toolNamesFromDefs(defs)
	if len(d.ToolNames) == 0 && d.Reason != "none" {
		d.Reason = "none"
	}
	return d
}

func toolNamesFromDefs(defs []llm.ToolDef) []string {
	if len(defs) == 0 {
		return nil
	}
	names := make([]string, 0, len(defs))
	for _, def := range defs {
		if name := strings.TrimSpace(def.Name); name != "" {
			names = append(names, name)
		}
	}
	return names
}

func (r *Runner) observeToolExposure(decision ToolExposureDecision) {
	if r == nil || r.toolExposureObserver == nil {
		return
	}
	decision.ToolNames = append([]string(nil), decision.ToolNames...)
	r.toolExposureObserver(decision)
}

func historyIncludesCompletedToolCall(snapshot SessionSnapshot, toolName string) bool {
	ids := make(map[string]struct{})
	for _, msg := range snapshot.History {
		switch msg.Role {
		case llm.RoleAssistant:
			for _, tc := range msg.ToolCalls {
				if tc.Name == toolName && strings.TrimSpace(tc.ID) != "" {
					ids[tc.ID] = struct{}{}
				}
			}
		case llm.RoleTool:
			if _, ok := ids[msg.ToolCallID]; ok {
				return true
			}
		}
	}
	return false
}

func completedToolCallResults(snapshot SessionSnapshot, toolName string) []string {
	ids := make(map[string]struct{})
	var results []string
	for _, msg := range snapshot.History {
		switch msg.Role {
		case llm.RoleAssistant:
			for _, tc := range msg.ToolCalls {
				if tc.Name == toolName && strings.TrimSpace(tc.ID) != "" {
					ids[tc.ID] = struct{}{}
				}
			}
		case llm.RoleTool:
			if _, ok := ids[msg.ToolCallID]; ok && strings.TrimSpace(msg.Content) != "" {
				results = append(results, msg.Content)
			}
		}
	}
	return results
}

func outstandingSpawnedAgents(snapshot SessionSnapshot) []AgentResult {
	transcriptAgents := outstandingTranscriptAgents(snapshot)
	if len(snapshot.AgentTasks) > 0 {
		stateIDs := make(map[string]struct{}, len(snapshot.AgentTasks))
		out := outstandingAgentTasks(snapshot.AgentTasks)
		for _, task := range snapshot.AgentTasks {
			if id := strings.TrimSpace(task.ID); id != "" {
				stateIDs[id] = struct{}{}
			}
		}
		for _, agent := range transcriptAgents {
			if _, exists := stateIDs[strings.TrimSpace(agent.ID)]; exists {
				continue
			}
			out = append(out, agent)
		}
		return out
	}
	return transcriptAgents
}

func outstandingTranscriptAgents(snapshot SessionSnapshot) []AgentResult {
	toolNamesByCallID := make(map[string]string)
	outstanding := make(map[string]AgentResult)
	var order []string

	for _, msg := range snapshot.History {
		switch msg.Role {
		case llm.RoleAssistant:
			for _, call := range msg.ToolCalls {
				if id := strings.TrimSpace(call.ID); id != "" {
					toolNamesByCallID[id] = strings.TrimSpace(call.Name)
				}
			}
		case llm.RoleTool:
			toolName := toolNamesByCallID[msg.ToolCallID]
			if toolName != "spawn_agent" && toolName != "wait_agent" {
				continue
			}
			result, ok := decodeAgentToolResult(msg.Content)
			if !ok || strings.TrimSpace(result.ID) == "" {
				continue
			}
			switch toolName {
			case "spawn_agent":
				if agentStillOutstanding(result.Status) {
					if _, exists := outstanding[result.ID]; !exists {
						order = append(order, result.ID)
					}
					outstanding[result.ID] = result
				}
			case "wait_agent":
				if agentStillOutstanding(result.Status) {
					if _, exists := outstanding[result.ID]; !exists {
						order = append(order, result.ID)
					}
					outstanding[result.ID] = result
				} else {
					delete(outstanding, result.ID)
				}
			}
		}
	}

	agents := make([]AgentResult, 0, len(outstanding))
	for _, id := range order {
		if result, ok := outstanding[id]; ok {
			agents = append(agents, result)
		}
	}
	return agents
}

func outstandingAgentTasks(tasks []AgentTaskState) []AgentResult {
	agents := make([]AgentResult, 0, len(tasks))
	for _, task := range tasks {
		id := strings.TrimSpace(task.ID)
		if id == "" || !agentStillOutstanding(task.Status) {
			continue
		}
		status := task.Status
		if status == "" {
			status = AgentStatusRunning
		}
		agents = append(agents, AgentResult{
			ID:     id,
			Role:   strings.TrimSpace(task.Role),
			Status: status,
			Result: strings.TrimSpace(task.Result),
			Error:  strings.TrimSpace(task.Error),
		})
	}
	return agents
}

func decodeAgentToolResult(content string) (AgentResult, bool) {
	content = strings.TrimSpace(content)
	if content == "" || strings.HasPrefix(normalizeToolIntentText(content), "error:") {
		return AgentResult{}, false
	}
	var result AgentResult
	if err := json.Unmarshal([]byte(content), &result); err != nil {
		return AgentResult{}, false
	}
	if strings.TrimSpace(result.ID) == "" {
		return AgentResult{}, false
	}
	return result, true
}

func agentStillOutstanding(status AgentStatus) bool {
	switch status {
	case "", AgentStatusRunning, AgentStatusTimeout:
		return true
	default:
		return false
	}
}

func normalizeToolIntentText(text string) string {
	text = strings.ToLower(strings.TrimSpace(text))
	if text == "" {
		return ""
	}
	return strings.Join(strings.Fields(text), " ")
}

func (r *Runner) updateGitWorkflow(toolName string, args map[string]any, result string) {
	switch toolName {
	case "run_command":
		r.updateGitWorkflowForCommand(strings.ToLower(strings.TrimSpace(stringArg(args, "command"))), result)
	case "git_commit":
		r.updateGitWorkflowForCommitResult(result)
	case "edit_file", "write_file", "apply_patch":
		r.gitWorkflow.commitBlocker = commitBlockerNone
		r.gitWorkflow.blockerSummary = ""
	}
	r.syncRuntimeNote()
}

func (r *Runner) updateGitWorkflowForCommand(command, result string) {
	switch {
	case command == "":
		return
	case isGitUnmergedListCommand(command):
		// Commands that only output unmerged files: any non-empty line = conflict.
		if hasNonEmptyOutput(result) {
			r.gitWorkflow.mergeActive = true
			r.gitWorkflow.unmergedFiles = true
			return
		}
		r.gitWorkflow.unmergedFiles = false
		if r.gitWorkflow.commitBlocker == commitBlockerNone {
			r.gitWorkflow.blockerSummary = ""
		}
	case isGitStatusPorcelainCommand(command):
		// git status --porcelain mixes staged/unstaged changes with conflict markers.
		// Only lines with explicit conflict XY codes indicate unmerged files.
		if hasPorcelainConflicts(result) {
			r.gitWorkflow.mergeActive = true
			r.gitWorkflow.unmergedFiles = true
			return
		}
		r.gitWorkflow.unmergedFiles = false
		if r.gitWorkflow.commitBlocker == commitBlockerNone {
			r.gitWorkflow.blockerSummary = ""
		}
	case isGitMergeLike(command) && hasMergeConflict(result):
		r.gitWorkflow.mergeActive = true
		r.gitWorkflow.unmergedFiles = true
		r.gitWorkflow.commitBlocker = commitBlockerNone
		r.gitWorkflow.blockerSummary = ""
	case isGitCommitLike(command):
		r.updateGitWorkflowForCommitResult(result)
	case strings.Contains(command, "git add") && isValidationPass(result):
		r.gitWorkflow.commitBlocker = commitBlockerNone
		r.gitWorkflow.blockerSummary = ""
	}
}

func (r *Runner) updateGitWorkflowForCommitResult(result string) {
	lower := strings.ToLower(result)
	switch {
	case isSuccessfulGitCommit(result):
		r.gitWorkflow = gitWorkflowState{}
	case strings.Contains(lower, "files were modified by this hook"):
		r.gitWorkflow.commitBlocker = commitBlockerRestage
		r.gitWorkflow.blockerSummary = "pre-commit modified files; re-stage them before retrying commit"
	case strings.Contains(lower, "hook id:") || strings.Contains(lower, "line too long") || strings.Contains(lower, "error committing:"):
		r.gitWorkflow.commitBlocker = commitBlockerEdit
		r.gitWorkflow.blockerSummary = summarizeCommitFailure(result)
	}
}

func (r *Runner) syncRuntimeNote() {
	if r == nil || r.session == nil {
		return
	}
	base := promptHookOutput(r.session.Snapshot())
	output := mergePromptHookOutput(base, r.promptHookOutput(context.Background()))
	if !r.session.Snapshot().HookOutputSet && !hasHookOutputContent(output) {
		return
	}
	r.session.SetHookOutput(output)
}

func (r *Runner) promptHookOutput(ctx context.Context) hooks.ExecutionOutput {
	if r == nil || r.hooks == nil {
		return hooks.ExecutionOutput{}
	}
	snap := SessionSnapshot{}
	if r.session != nil {
		snap = r.session.Snapshot()
	}
	return r.hooks.Dispatch(ctx, hooks.Event{
		Point:    hooks.PointPromptContext,
		Snapshot: snap,
		Transient: promptHookPayload{
			Mode:                     snap.Mode,
			PlanState:                snap.PlanState,
			PlanWorkflow:             r.planWorkflow,
			ValidationWorkflow:       r.validationWorkflow,
			SearchWorkflow:           r.searchWorkflow,
			GitWorkflow:              r.gitWorkflow,
			RepeatWorkflow:           r.repeatWorkflow,
			ToolThrashCircuitBreaker: r.toolThrashCircuitBreaker,
		},
	})
}

func (r *Runner) beforeToolHookOutput(ctx context.Context, toolName string, args map[string]any) hooks.ExecutionOutput {
	if r == nil || r.hooks == nil {
		return hooks.ExecutionOutput{}
	}
	snap := SessionSnapshot{}
	if r.session != nil {
		snap = r.session.Snapshot()
	}
	return r.hooks.Dispatch(ctx, hooks.Event{
		Point:    hooks.PointBeforeTool,
		Snapshot: snap,
		Transient: beforeToolHookPayload{
			ToolName:    toolName,
			Args:        cloneArgs(args),
			GitWorkflow: r.gitWorkflow,
		},
	})
}

func (r *Runner) afterToolHookOutput(ctx context.Context, toolName string, args map[string]any, isError bool, errorText string) hooks.ExecutionOutput {
	if r == nil || r.hooks == nil {
		return hooks.ExecutionOutput{}
	}
	snap := SessionSnapshot{}
	if r.session != nil {
		snap = r.session.Snapshot()
	}
	return r.hooks.Dispatch(ctx, hooks.Event{
		Point:    hooks.PointAfterTool,
		Snapshot: snap,
		Transient: afterToolHookPayload{
			ToolName: strings.TrimSpace(toolName),
			Args:     cloneArgs(args),
			IsError:  isError,
			Error:    strings.TrimSpace(errorText),
		},
	})
}

func (r *Runner) applyHookOutput(output hooks.ExecutionOutput) {
	if r == nil || r.session == nil || !hasHookOutputContent(output) {
		return
	}
	base := promptHookOutput(r.session.Snapshot())
	r.session.SetHookOutput(mergePromptHookOutput(base, output))
}
