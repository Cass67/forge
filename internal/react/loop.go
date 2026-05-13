package react

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"strings"
	"sync"
	"time"

	"forge/internal/agent"
	agenttools "forge/internal/agent/tools"
	"forge/internal/hooks"
	"forge/internal/llm"
	resilienceerrors "forge/internal/resilience/errors"
	"forge/internal/secscan"
)

type Config struct {
	Driver                   llm.Driver
	Tools                    *agenttools.Registry
	Renderer                 agent.RenderTarget
	SystemPrompt             func() string
	Session                  *Session
	Progress                 func(string)
	TurnComplete             func(SessionSnapshot)
	ToolExposureObserver     func(ToolExposureDecision)
	ConfigureHooks           func(*hooks.Registry)
	CompactionMaxFailures    int
	Interactive              bool
	MaxSteps                 int
	ToolThrashCircuitBreaker int
}

type ToolExposureDecision struct {
	Reason                string
	ToolNames             []string
	RequireToolCall       bool
	OutstandingAgentCount int
	PendingActionKind     string
}

type Runner struct {
	driver                   llm.Driver
	tools                    *agenttools.Registry
	renderer                 agent.RenderTarget
	systemPrompt             func() string
	session                  *Session
	hooks                    *hooks.Registry
	progress                 func(string)
	compactionManager        *CompactionManager
	compactionFailures       int
	compactionMaxFailures    int
	maxSteps                 int
	gitWorkflow              gitWorkflowState
	planWorkflow             planWorkflowState
	searchWorkflow           sameFileSearchWorkflowState
	validationWorkflow       validationWorkflowState
	repeatWorkflow           repeatToolCallState
	postDelegation           postDelegationWorkflowState
	pendingRetryPrompt       string
	turnComplete             func(SessionSnapshot)
	toolExposureObserver     func(ToolExposureDecision)
	toolThrashCircuitBreaker int
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
	streak       int
}

type postDelegationWorkflowState struct {
	pendingWrite bool
}

const planExplorationBudget = 10
const analysisExplorationBudget = 15
const previewExplorationBudget = 4
const implementExplorationBudget = 12
const inspectExplorationBudget = 8
const overviewExplorationBudget = 6
const reviewExplorationBudget = 10
const validateExplorationBudget = 6
const chatExplorationBudget = 8
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
const repeatToolCallThreshold = 6
const maxCompletionRetriesPerTurn = 3
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
		driver:       cfg.Driver,
		tools:        reg,
		renderer:     cfg.Renderer,
		systemPrompt: cfg.SystemPrompt,
		session:      session,
		hooks:        hookRegistry,
		progress:     cfg.Progress,
		compactionManager: NewCompactionManager(CompactionConfig{
			KeepTurns:            40,
			HistoryPressureTurns: 40,
			MaxFailures:          cfg.CompactionMaxFailures,
		}),
		compactionMaxFailures:    cfg.CompactionMaxFailures,
		turnComplete:             cfg.TurnComplete,
		toolExposureObserver:     cfg.ToolExposureObserver,
		maxSteps:                 maxLoopSteps(cfg.MaxSteps),
		toolThrashCircuitBreaker: cfg.ToolThrashCircuitBreaker,
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

func (r *Runner) RunWithParts(ctx context.Context, input string, parts []llm.MessageContentPart) error {
	if r == nil {
		return fmt.Errorf("react runner: runner is nil")
	}
	prompt := BuildPrompt(input)
	if prompt == "" && len(parts) == 0 {
		return nil
	}
	priorResponse := r.LastResponse()
	turn := r.session.RecordInputWithParts(prompt, parts)
	r.pendingRetryPrompt = ""
	r.syncRuntimeNote()
	r.applyCompactionDecision(ctx, r.compactionManager.Decide(r.session.Snapshot()))
	if len(parts) == 0 {
		if handled, err := r.tryDirectLastResponseMarkdownWrite(ctx, turn, prompt, priorResponse); handled {
			return err
		}
	}
	if r.driver == nil {
		err := fmt.Errorf("react runner: driver is nil")
		r.session.CompleteTurn(turn, "", nil, err)
		return err
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
	before := SessionSnapshot{}
	if r.session != nil {
		before = r.session.Snapshot()
	}
	r.dispatchCompactionHook(ctx, hooks.PointPreCompact, decision, before, false, 0)
	changed := r.compactionManager.Apply(r.session, decision)
	after := SessionSnapshot{}
	if r.session != nil {
		after = r.session.Snapshot()
	}
	if changed {
		r.compactionFailures = 0
	} else if r.compactionMaxFailures > 0 {
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
	r.postDelegation = postDelegationWorkflowState{}
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
	r.session.AppendUserMessage(text)
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
		r.session.AppendAssistantMessage(text)
	}
	if r.renderer != nil {
		r.renderer.AgentText(strings.TrimSpace(text))
	}
}

func (r *Runner) tryDirectLastResponseMarkdownWrite(ctx context.Context, turn int, input, priorResponse string) (bool, error) {
	priorResponse = strings.TrimSpace(priorResponse)
	if r == nil || r.session == nil || r.tools == nil || priorResponse == "" {
		return false, nil
	}
	path, ok := directLastResponseMarkdownWritePath(input)
	if !ok {
		return false, nil
	}
	if _, ok := r.tools.Get("write_file"); !ok {
		return false, nil
	}
	start := time.Now()
	defer r.emitStats(start)
	args, err := json.Marshal(map[string]any{"path": path, "content": priorResponse})
	if err != nil {
		return true, err
	}
	call := llm.NativeToolCall{ID: "direct_write_last_response_1", Name: "write_file", ArgsJSON: string(args)}
	r.session.AppendAssistantToolTurn("", []llm.NativeToolCall{call})
	if err := r.executeNativeToolCalls(ctx, turn, []llm.NativeToolCall{call}); err != nil {
		return true, err
	}
	final := strings.TrimSpace(toolResultForCallID(r.session.Snapshot(), call.ID))
	if final == "" {
		final = "wrote previous response to " + path
	}
	r.session.AppendAssistantMessage(final)
	r.session.CompleteTurn(turn, final, nil, nil)
	r.notifyTurnComplete()
	if r.renderer != nil {
		r.renderer.AgentText(final)
	}
	return true, nil
}

func directLastResponseMarkdownWritePath(input string) (string, bool) {
	if !directLastResponseMarkdownWriteIntent(directWriteTokens(input)) {
		return "", false
	}
	if path := extractDelegationTargetPath(input); path != "" {
		return path, true
	}
	return "docs/reports/report.md", true
}

func directWriteTokens(input string) []string {
	fields := strings.Fields(normalizeToolIntentText(input))
	tokens := make([]string, 0, len(fields))
	for _, field := range fields {
		token := strings.Trim(field, "`'\".,:;()[]{}<>!?\n\t")
		token = strings.TrimPrefix(token, "path=")
		token = strings.TrimPrefix(token, "target=")
		if token != "" {
			tokens = append(tokens, token)
		}
	}
	return tokens
}

func directLastResponseMarkdownWriteIntent(tokens []string) bool {
	verb := firstDirectWriteVerb(tokens)
	if verb < 0 {
		return false
	}
	refEnd, ok := directLastResponseReferenceEnd(tokens, verb+1)
	if !ok {
		return false
	}
	marker := firstDirectWriteTargetMarker(tokens, refEnd)
	return marker >= 0 && directWriteTargetAfterMarker(tokens[marker+1:])
}

func firstDirectWriteVerb(tokens []string) int {
	for i, token := range tokens {
		switch token {
		case "write", "save", "create":
			return i
		}
	}
	return -1
}

func directLastResponseReferenceEnd(tokens []string, start int) (int, bool) {
	if start >= len(tokens) {
		return -1, false
	}
	switch tokens[start] {
	case "it", "that", "above":
		return start + 1, true
	case "previous", "last":
		if start+1 < len(tokens) && tokens[start+1] == "response" {
			return start + 2, true
		}
	case "the":
		if directTokenPair(tokens, start+1, "previous", "response") ||
			directTokenPair(tokens, start+1, "last", "response") {
			return start + 3, true
		}
		if start+1 < len(tokens) && (tokens[start+1] == "answer" || tokens[start+1] == "above") {
			return start + 2, true
		}
	case "this":
		if start+1 < len(tokens) && tokens[start+1] == "answer" {
			return start + 2, true
		}
	}
	return -1, false
}

func firstDirectWriteTargetMarker(tokens []string, start int) int {
	for i := start; i < len(tokens); i++ {
		switch tokens[i] {
		case "to", "as", "into":
			return i
		}
	}
	return -1
}

func directWriteTargetAfterMarker(tokens []string) bool {
	for _, token := range tokens {
		if directArticleToken(token) {
			continue
		}
		if directTargetToken(token) {
			return true
		}
		return false
	}
	return false
}

func directTokenPair(tokens []string, index int, first, second string) bool {
	return index+1 < len(tokens) && tokens[index] == first && tokens[index+1] == second
}

func directArticleToken(token string) bool {
	switch token {
	case "a", "an", "the":
		return true
	default:
		return false
	}
}

func directTargetToken(token string) bool {
	if strings.HasSuffix(token, ".md") {
		return true
	}
	if directArticleToken(token) {
		return false
	}
	switch token {
	case "md", "markdown", "file", "report", "doc", "docs", "document", "memo", "note", "checklist":
		return true
	default:
		return false
	}
}

func toolResultForCallID(snapshot SessionSnapshot, id string) string {
	id = strings.TrimSpace(id)
	if id == "" {
		return ""
	}
	for i := len(snapshot.History) - 1; i >= 0; i-- {
		msg := snapshot.History[i]
		if msg.Role == llm.RoleTool && msg.ToolCallID == id {
			return msg.Content
		}
	}
	return ""
}

func (r *Runner) runLoop(ctx context.Context, turn int) error {
	start := time.Now()
	defer r.emitStats(start)

	nativeCaller, isNative := r.driver.(llm.NativeToolCaller)

	completionRetries := 0
	reactiveCompacted := false
	usedToolThisTurn := false
	for range r.maxSteps {
		if r.applyPendingInput() {
			usedToolThisTurn = false
			r.syncRuntimeNote()
		}
		snap := r.session.Snapshot()
		toolDefs, toolDecision := r.selectToolDefsWithDecision(snap)
		requireToolCall := shouldRequireToolCallForSnapshot(snap) && !usedToolThisTurn
		toolDecision.RequireToolCall = requireToolCall
		r.observeToolExposure(toolDecision)
		if len(toolDefs) > 0 && !isNative {
			err := fmt.Errorf("react runtime: driver %q does not support native tool calling", r.driver.Name())
			r.session.CompleteTurn(turn, "", nil, err)
			return err
		}
		calls, err := r.streamNativeTurn(ctx, turn, nativeCaller, toolDefs, requireToolCall)
		if err != nil {
			if !reactiveCompacted && r.reactiveCompactForContextError(ctx, err) {
				reactiveCompacted = true
				completionRetries = 0
				continue
			}
			if shouldRetryTransientStreamError(ctx, err) {
				if handled, fallbackErr := r.tryCompletedAgentResultFallback(ctx, turn); handled {
					return fallbackErr
				}
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
				if fallback := r.activeAgentFallbackText(); fallback != "" {
					r.pendingRetryPrompt = ""
					r.session.AppendAssistantMessage(fallback)
					r.session.CompleteTurn(turn, fallback, nil, nil)
					r.notifyTurnComplete()
					if r.renderer != nil {
						r.renderer.AgentText(fallback)
					}
					return nil
				}
			}
			r.session.CompleteTurn(turn, "", nil, err)
			return err
		}
		if calls == nil {
			// streamNativeTurn already recorded the final response
			if r.applyPendingInput() {
				usedToolThisTurn = false
				continue
			}
			return nil
		}
		if err := r.executeNativeToolCalls(ctx, turn, calls); err != nil {
			return err
		}
		usedToolThisTurn = true
	}

	err := fmt.Errorf("react runtime: safety step limit (%d) exceeded", r.maxSteps)
	r.session.CompleteTurn(turn, "", nil, err)
	return err
}

func isEmptyNativeResponseError(err *RetryableCompletionError) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Message), "empty native response")
}

func (r *Runner) activeAgentFallbackText() string {
	if r == nil || r.session == nil {
		return ""
	}
	agents := outstandingSpawnedAgents(r.session.Snapshot())
	if len(agents) == 0 {
		return ""
	}
	parts := make([]string, 0, len(agents))
	for _, agent := range agents {
		id := strings.TrimSpace(agent.ID)
		if id == "" {
			continue
		}
		label := id
		if role := strings.TrimSpace(agent.Role); role != "" {
			label += " (" + role + ")"
		}
		status := strings.TrimSpace(string(agent.Status))
		if status == "" {
			status = string(AgentStatusRunning)
		}
		parts = append(parts, label+" is "+status)
	}
	if len(parts) == 0 {
		return ""
	}
	return "Child agent work is still in progress: " + strings.Join(parts, "; ") + ". Ask for status or tell me to continue waiting."
}

func (r *Runner) tryCompletedAgentResultFallback(ctx context.Context, turn int) (bool, error) {
	if r == nil || r.session == nil {
		return false, nil
	}
	snap := r.session.Snapshot()
	if completedAgentFallbackSettledWrite(snap) && !inputSuggestsPostDelegationAction(normalizeToolIntentText(snap.LastInput)) {
		return false, nil
	}
	content := completedAgentResultFallbackContent(snap)
	if content == "" {
		return false, nil
	}
	if path, ok := completedAgentResultMarkdownWritePathForSnapshot(snap); ok && r.tools != nil {
		if _, ok := r.tools.Get("write_file"); ok {
			writeContent := completedAgentResultMarkdownWriteContent(snap)
			if writeContent == "" {
				return false, nil
			}
			return true, r.writeCompletedAgentResultFallback(ctx, turn, path, writeContent)
		}
	}
	fallback := "Parent model connection failed while composing the final response. Showing completed child-agent result instead.\n\n" + content
	r.pendingRetryPrompt = ""
	r.session.AppendAssistantMessage(fallback)
	r.session.CompleteTurn(turn, fallback, nil, nil)
	r.notifyTurnComplete()
	if r.renderer != nil {
		r.renderer.AgentText(fallback)
	}
	return true, nil
}

func (r *Runner) writeCompletedAgentResultFallback(ctx context.Context, turn int, path, content string) error {
	args, err := json.Marshal(map[string]any{"path": path, "content": content})
	if err != nil {
		return err
	}
	call := llm.NativeToolCall{ID: "direct_write_completed_agents_1", Name: "write_file", ArgsJSON: string(args)}
	r.pendingRetryPrompt = ""
	r.session.AppendAssistantToolTurn("", []llm.NativeToolCall{call})
	if err := r.executeNativeToolCalls(ctx, turn, []llm.NativeToolCall{call}); err != nil {
		return err
	}
	final := strings.TrimSpace(toolResultForCallID(r.session.Snapshot(), call.ID))
	if final == "" {
		final = "wrote completed child-agent results to " + path
	}
	r.session.AppendAssistantMessage(final)
	r.session.CompleteTurn(turn, final, nil, nil)
	r.notifyTurnComplete()
	if r.renderer != nil {
		r.renderer.AgentText(final)
	}
	return nil
}

func completedAgentResultFallbackContent(snap SessionSnapshot) string {
	resultTurn := completedAgentResultTurn(snap)
	if sameTurnAgentStillOutstanding(snap.AgentTasks, resultTurn) {
		return ""
	}
	var parts []string
	for _, task := range snap.AgentTasks {
		if task.Status != AgentStatusCompleted || task.ParentTurn != resultTurn {
			continue
		}
		result := strings.TrimSpace(task.Result)
		if result == "" {
			continue
		}
		label := strings.TrimSpace(task.Role)
		if label == "" {
			label = strings.TrimSpace(task.ID)
		}
		if label == "" {
			label = "child agent"
		}
		parts = append(parts, "## "+label+"\n"+result)
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, "\n\n")
}

func completedAgentResultMarkdownWriteContent(snap SessionSnapshot) string {
	resultTurn := completedAgentResultTurn(snap)
	if sameTurnAgentStillOutstanding(snap.AgentTasks, resultTurn) {
		return ""
	}
	if result := completedAgentResultByRole(snap, "synthesizer"); result != "" {
		return result
	}

	var results []string
	for _, task := range snap.AgentTasks {
		if task.Status != AgentStatusCompleted || task.ParentTurn != resultTurn {
			continue
		}
		result := strings.TrimSpace(task.Result)
		if result == "" {
			continue
		}
		results = append(results, result)
	}
	if len(results) == 1 {
		return results[0]
	}
	return conciseMultiAgentMarkdownSummary(results)
}

func conciseMultiAgentMarkdownSummary(results []string) string {
	if len(results) < 2 {
		return ""
	}
	bullets := make([]string, 0, len(results))
	for _, result := range results {
		if !isConciseAgentFinding(result) {
			return ""
		}
		bullets = append(bullets, "- "+strings.ReplaceAll(strings.TrimSpace(result), "\n", " "))
	}
	return "# Consolidated Findings\n\n" + strings.Join(bullets, "\n") + "\n"
}

func isConciseAgentFinding(result string) bool {
	result = strings.TrimSpace(result)
	if result == "" || len(result) > 500 || strings.HasPrefix(result, "#") {
		return false
	}
	lines := strings.Split(result, "\n")
	if len(lines) > 3 {
		return false
	}
	for _, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			return false
		}
	}
	return true
}

func completedAgentResultTurn(snap SessionSnapshot) int {
	if agentTaskCompletedResultForTurn(snap.AgentTasks, snap.Turn) || sameTurnAgentStillOutstanding(snap.AgentTasks, snap.Turn) {
		return snap.Turn
	}
	if !pendingDelegationWriteAction(snap) && !pendingPostDelegationWriteAction(snap) {
		return snap.Turn
	}
	if snap.PendingDelegationAction != nil {
		sourceAgent := strings.TrimSpace(snap.PendingDelegationAction.SourceAgent)
		if sourceAgent != "" {
			for _, task := range snap.AgentTasks {
				if task.ID == sourceAgent && task.ParentTurn != 0 {
					return task.ParentTurn
				}
			}
		}
	}
	latest := 0
	for _, task := range snap.AgentTasks {
		if task.Status != AgentStatusCompleted || strings.TrimSpace(task.Result) == "" || task.ParentTurn == 0 {
			continue
		}
		if task.ParentTurn > latest {
			latest = task.ParentTurn
		}
	}
	if latest != 0 {
		return latest
	}
	return snap.Turn
}

func agentTaskCompletedResultForTurn(tasks []AgentTaskState, turn int) bool {
	for _, task := range tasks {
		if task.ParentTurn == turn && task.Status == AgentStatusCompleted && strings.TrimSpace(task.Result) != "" {
			return true
		}
	}
	return false
}

func completedAgentResultByRole(snap SessionSnapshot, role string) string {
	role = strings.TrimSpace(role)
	if role == "" {
		return ""
	}
	resultTurn := completedAgentResultTurn(snap)
	for i := len(snap.AgentTasks) - 1; i >= 0; i-- {
		task := snap.AgentTasks[i]
		if task.Status != AgentStatusCompleted || task.ParentTurn != resultTurn || !strings.EqualFold(strings.TrimSpace(task.Role), role) {
			continue
		}
		if result := strings.TrimSpace(task.Result); result != "" {
			return result
		}
	}
	return ""
}

func completedAgentResultMarkdownWritePath(input string) (string, bool) {
	tokens := directWriteTokens(input)
	verb := firstDirectWriteVerb(tokens)
	if verb < 0 {
		return "", false
	}
	if path := extractDelegationTargetPath(input); path != "" {
		return path, true
	}
	if completedAgentWriteTargetAfterVerb(tokens[verb+1:]) {
		return "docs/reports/report.md", true
	}
	for i := verb + 1; i < len(tokens); i++ {
		switch tokens[i] {
		case "to", "as", "into", "in":
			if completedAgentWriteTargetAfterMarker(tokens[i+1:]) {
				return "docs/reports/report.md", true
			}
		}
	}
	return "", false
}

func completedAgentResultMarkdownWritePathForSnapshot(snap SessionSnapshot) (string, bool) {
	if path, ok := completedAgentResultMarkdownWritePath(snap.LastInput); ok {
		return path, true
	}
	if snap.PendingDelegationAction != nil && snap.PendingDelegationAction.Kind == DelegationActionWriteDoc {
		if path := strings.TrimSpace(snap.PendingDelegationAction.TargetPath); path != "" {
			return path, true
		}
	}
	if path := extractDelegationTargetPath(postDelegationToolIntentText(snap)); path != "" {
		return path, true
	}
	return "", false
}

func completedAgentWriteTargetAfterVerb(tokens []string) bool {
	nonTarget := 0
	for i, token := range tokens {
		switch token {
		case "to", "as", "into", "in":
			return false
		}
		if directArticleToken(token) || token == "markup" {
			continue
		}
		if completedAgentWriteTargetAt(tokens, i) {
			return true
		}
		nonTarget++
		if nonTarget > 1 {
			return false
		}
	}
	return false
}

func completedAgentWriteTargetAfterMarker(tokens []string) bool {
	sawArticle := false
	for i, token := range tokens {
		if directArticleToken(token) {
			sawArticle = true
			continue
		}
		if token == "markup" {
			continue
		}
		if completedAgentWriteTargetAt(tokens, i) {
			return true
		}
		if !sawArticle {
			return false
		}
		next := i + 1
		if next >= len(tokens) || directArticleToken(tokens[next]) || tokens[next] == "markup" {
			return false
		}
		return completedAgentWriteTargetAt(tokens, next)
	}
	return false
}

func completedAgentWriteTargetAt(tokens []string, index int) bool {
	if index < 0 || index >= len(tokens) {
		return false
	}
	token := tokens[index]
	if !directTargetToken(token) {
		return false
	}
	for i := index + 1; i < len(tokens); i++ {
		next := tokens[i]
		if directArticleToken(next) || next == "markup" || next == "markdown" {
			continue
		}
		if completedAgentTargetRelationToken(next) {
			return true
		}
		if completedAgentCodeArtifactNoun(next) {
			return false
		}
		if directTargetToken(next) {
			continue
		}
		return true
	}
	return true
}

func completedAgentTargetRelationToken(token string) bool {
	switch token {
	case "about", "on", "for", "with", "from", "of", "containing", "including":
		return true
	default:
		return false
	}
}

func completedAgentCodeArtifactNoun(token string) bool {
	switch token {
	case "parser", "generator", "builder", "tool", "library", "package", "component", "function", "class", "module", "api", "cli", "command":
		return true
	default:
		return false
	}
}

func sameTurnAgentStillOutstanding(tasks []AgentTaskState, turn int) bool {
	for _, task := range tasks {
		if task.ParentTurn == turn && agentTaskFallbackOutstanding(task.Status) {
			return true
		}
	}
	return false
}

func agentTaskFallbackOutstanding(status AgentStatus) bool {
	if status == AgentStatusPending {
		return true
	}
	return agentStillOutstanding(status)
}

func shouldRetryTransientStreamError(ctx context.Context, err error) bool {
	if err == nil {
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
	return r.applyCompactionDecision(ctx, r.compactionManager.Reactive(20, "context window exceeded"))
}

// streamNativeTurn runs one native tool calling step.
// Returns nil calls (+ nil error) when a final text answer was received.
// Returns non-nil calls when the model requested tool executions.
func (r *Runner) streamNativeTurn(ctx context.Context, turn int, caller llm.NativeToolCaller, toolDefs []llm.ToolDef, requireToolCall bool) ([]llm.NativeToolCall, error) {
	messages := r.session.Messages(r.currentSystemPrompt())
	if prompt := strings.TrimSpace(r.pendingRetryPrompt); prompt != "" {
		messages = injectSystemMessageBeforeHistory(messages, "Runtime correction for the previous attempt:\n"+prompt)
	}
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
	hasTools := len(toolDefs) > 0

	for tok := range out {
		if tok.ReasoningContent != "" {
			reasoningBuf.WriteString(tok.ReasoningContent)
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
		if !hasTools {
			current := textBuf.String()
			if streamVisible && r.renderer != nil && len(current) > visibleEmitted {
				r.renderer.AgentToken(current[visibleEmitted:])
				visibleEmitted = len(current)
			}
		}
	}
	if err := <-errCh; err != nil {
		return nil, err
	}

	if len(toolCalls) > 0 {
		r.pendingRetryPrompt = ""
		preamble := stripXMLToolCallMarkup(strings.TrimSpace(textBuf.String()))
		safePreamble := redactRuntimeText(preamble)
		reasoning := strings.TrimSpace(reasoningBuf.String())
		r.session.AppendAssistantToolTurn(safePreamble, toolCalls)
		if reasoning != "" {
			r.session.SetLastAssistantReasoning(reasoning)
		}
		if streamVisible && r.renderer != nil && hasTools && safePreamble != "" {
			r.renderer.AgentText(safePreamble)
		} else if safePreamble != "" && r.renderer != nil && visibleEmitted < len(safePreamble) {
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
	if looksLikeLegacyXMLToolCall(finalText) {
		return nil, NewRetryableCompletionError(
			"react runtime: provider returned deprecated XML tool-call markup",
			"Use the provider's native tool-calling interface only. Do not emit prose, XML, or example markup in place of a tool call.",
		)
	}
	if requireToolCall {
		return nil, NewRetryableCompletionError(
			"react runtime: required tool call missing",
			"A tool call is required for this step. Use one of the available tools instead of answering with prose.",
		)
	}
	reasoning := strings.TrimSpace(reasoningBuf.String())
	r.session.AppendAssistantMessage(finalText)
	if reasoning != "" {
		r.session.SetLastAssistantReasoning(reasoning)
	}
	r.session.CompleteTurn(turn, finalText, nil, nil)
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
	visibleEmitted := 0
	streamVisible := r.renderer != nil

	for tok := range out {
		if tok.Text == "" {
			continue
		}
		textBuf.WriteString(tok.Text)
		current := textBuf.String()
		if streamVisible && r.renderer != nil && len(current) > visibleEmitted {
			r.renderer.AgentToken(current[visibleEmitted:])
			visibleEmitted = len(current)
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
	if looksLikeLegacyXMLToolCall(finalText) {
		return nil, NewRetryableCompletionError(
			"react runtime: provider returned deprecated XML tool-call markup",
			"Use the provider's native tool-calling interface only. Do not emit prose, XML, or example markup in place of a tool call.",
		)
	}
	r.session.AppendAssistantMessage(finalText)
	r.session.CompleteTurn(turn, finalText, nil, nil)
	r.notifyTurnComplete()
	if r.renderer != nil && visibleEmitted < len(finalText) {
		r.renderer.AgentText(finalText[visibleEmitted:])
	}
	return nil, nil
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
// to the session. Unknown tools are returned to the model as tool errors so it can recover;
// execution errors still abort the turn after recording the failed result.
// Tool executions are dispatched in parallel (matching Codex's FuturesOrdered model)
// to reduce total wall-clock time when multiple independent tools are requested.
func (r *Runner) executeNativeToolCalls(ctx context.Context, turn int, calls []llm.NativeToolCall) error {
	type toolExec struct {
		call       llm.NativeToolCall
		tool       agenttools.Tool
		args       map[string]any
		beforeTool hooks.ExecutionOutput
		execute    func() (result string, diff string, execErr error)
	}

	// Phase 1: resolve tools, parse args, run pre-hooks (sequential, may have side effects)
	execs := make([]toolExec, 0, len(calls))
	for _, call := range calls {
		tool, ok := r.tools.Get(call.Name)
		if !ok {
			errMsg := redactRuntimeText(fmt.Sprintf("error: unknown tool %q. Use one of the tools provided for this turn.", call.Name))
			execs = append(execs, toolExec{
				call: call,
				execute: func() (string, string, error) {
					return errMsg, "", nil
				},
			})
			continue
		}

		var args map[string]any
		if err := json.Unmarshal([]byte(call.ArgsJSON), &args); err != nil {
			parseErr := fmt.Sprintf("error: malformed tool call arguments for %q: %v", call.Name, err)
			if r.renderer != nil {
				r.renderer.Error(parseErr)
			}
			r.session.AppendNativeToolResult(call.ID, parseErr)
			r.session.CompleteTurn(turn, "", nil, errors.New(parseErr))
			return errors.New(parseErr)
		}

		beforeTool := r.beforeToolHookOutput(ctx, call.Name, args)
		if beforeTool.Block != nil {
			blocked := strings.TrimSpace(beforeTool.Block.Message)
			if r.renderer != nil {
				r.renderer.ToolResult(call.Name, blocked, "", true)
			}
			r.session.AppendNativeToolResult(call.ID, blocked)
			r.updatePlanWorkflow(call.Name, args, "", true)
			r.updateSameFileSearchWorkflow(call.Name, args, true)
			continue
		}

		toolRef := tool // capture for closure
		execs = append(execs, toolExec{
			call:       call,
			tool:       toolRef,
			args:       args,
			beforeTool: beforeTool,
			execute: func() (string, string, error) {
				res, execErr := toolRef.Execute(ctx, args)
				diff := ""
				if toolRef.LastDiff != nil && execErr == nil {
					diff = toolRef.LastDiff()
				}
				return res, diff, execErr
			},
		})
	}

	// Phase 2: dispatch tool executions (parallel for safe tools, sequential for pool-mutating)
	// Pool-related tools (spawn_agent, wait_agent, kill_agent) must run sequentially
	// to avoid lifecycle ordering races. All other tools can run in parallel.
	type execResult struct {
		index  int
		result string
		diff   string
		err    error
	}
	results := make([]execResult, 0, len(execs))
	hasPoolTool := false
	for _, exec := range execs {
		if toolMutatesPool(exec.call.Name) {
			hasPoolTool = true
			break
		}
	}
	if hasPoolTool {
		// Sequential: execute tools one at a time, in order
		for _, exec := range execs {
			result, diff, err := exec.execute()
			results = append(results, execResult{result: result, diff: diff, err: err})
		}
	} else {
		// Parallel: dispatch all tools in goroutines, collect in order
		type indexedResult struct {
			index  int
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
				result, diff, err := exec.execute()
				ch <- indexedResult{index: i, result: result, diff: diff, err: err}
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
	for i, res := range results {
		exec := execs[i]
		call := exec.call
		args := exec.args
		beforeTool := exec.beforeTool

		if r.renderer != nil {
			r.renderer.ToolCall(call.Name, reactToolSummary(args))
		}

		if res.err != nil {
			errResult := fmt.Sprintf("error: %v", res.err)
			r.applyHookOutput(beforeTool)
			r.applyHookOutput(r.afterToolHookOutput(ctx, call.Name, args, true, errResult))
			if r.renderer != nil {
				r.renderer.ToolResult(call.Name, errResult, res.diff, true)
			}
			r.session.AppendNativeToolResult(call.ID, errResult)
			r.updateGitWorkflow(call.Name, args, errResult)
			r.updatePostDelegationWorkflow(call.Name, errResult, true)
			r.session.CompleteTurn(turn, "", nil, res.err)
			return res.err
		}

		display := truncateToolResult(res.result)
		if r.renderer != nil {
			r.renderer.ToolResult(call.Name, display, res.diff, false)
		}
		r.session.AppendNativeToolResult(call.ID, res.result)
		r.updateGitWorkflow(call.Name, args, res.result)
		r.updatePlanWorkflow(call.Name, args, res.result, false)
		r.updateSameFileSearchWorkflow(call.Name, args, false)
		r.updateValidationWorkflow(call.Name, args, res.result)
		r.updateRepeatToolCallWorkflow(call.Name, args, res.result)
		r.updatePostDelegationWorkflow(call.Name, res.result, false)
		r.applyHookOutput(beforeTool)
		r.applyHookOutput(r.afterToolHookOutput(ctx, call.Name, args, false, ""))
	}
	return nil
}

// toolMutatesPool returns true for tools that modify agent pool state and must run
// sequentially to avoid lifecycle ordering races.
func toolMutatesPool(name string) bool {
	switch name {
	case "spawn_agent", "wait_agent", "get_agent_output", "kill_agent":
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
	if r == nil || r.renderer == nil {
		return
	}
	var usage llm.Usage
	if reporter, ok := r.driver.(llm.UsageReporter); ok {
		usage = reporter.LastUsage()
	}
	r.renderer.Stats(time.Since(start), usage)
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

var (
	readOnlyToolNames = []string{
		"read_file", "list_dir", "search", "code_search", "glob", "view_image",
		"lsp_definition", "lsp_references", "lsp_hover", "lsp_document_symbols",
	}
	writeToolNames   = []string{"write_file", "edit_file", "apply_patch"}
	commandToolNames = []string{
		"run_command", "exec_session_start", "exec_session_status", "exec_session_write",
		"exec_session_resize", "exec_session_stop", "command_status", "command_write_stdin",
	}
	gitReadToolNames     = []string{"git_status", "git_diff", "git_log", "git_branch_state", "git_merge_status"}
	previewToolNames     = []string{"artifact_write", "artifact_read", "preview_server_ensure", "preview_server_status"}
	planningToolNames    = []string{"think", "update_plan", "enter_plan_mode", "exit_plan_mode", "ask_user_question"}
	webToolNames         = []string{"web_fetch", "web_search"}
	delegateToolNames    = []string{"spawn_agent", "wait_agent"}
	activeAgentToolNames = []string{"wait_agent", "get_agent_output", "agent_status", "kill_agent"}
	agentStatusToolNames = []string{"agent_status"}
)

func (r *Runner) selectToolDefs(snapshot SessionSnapshot) []llm.ToolDef {
	defs, _ := r.selectToolDefsWithDecision(snapshot)
	return defs
}

func (r *Runner) selectToolDefsWithDecision(snapshot SessionSnapshot) ([]llm.ToolDef, ToolExposureDecision) {
	decision := newToolExposureDecision(snapshot)
	if r == nil || r.tools == nil {
		return nil, decision
	}
	if len(outstandingSpawnedAgents(snapshot)) > 0 {
		defs := r.tools.Filter(activeAgentToolNames).ToLLMToolDefs()
		return defs, decision.withTools("active_agents", defs)
	}
	delegationComplete := historyIncludesCompletedToolCall(snapshot, "wait_agent")
	currentPostDelegationAction := inputSuggestsPostDelegationAction(normalizeToolIntentText(snapshot.LastInput))
	fallbackSettledWrite := completedAgentFallbackSettledWrite(snapshot) && !currentPostDelegationAction
	pendingWorkflowWrite := r.postDelegation.pendingWrite || pendingDelegationWriteAction(snapshot)
	if fallbackSettledWrite {
		pendingWorkflowWrite = false
	}
	pendingPostDelegationWrite := pendingWorkflowWrite || pendingPostDelegationWriteAction(snapshot)
	if delegationComplete {
		postDelegationText := snapshot.LastInput
		if !fallbackSettledWrite {
			postDelegationText = postDelegationToolIntentText(snapshot)
		}
		if !pendingPostDelegationWrite && !inputSuggestsPostDelegationAction(normalizeToolIntentText(postDelegationText)) {
			return nil, decision
		}
		snapshot.LastInput = postDelegationText
		goto selectParentTools
	}
	if shouldRouteParentThroughDelegation(snapshot) {
		if defs := r.tools.Filter(delegateToolNames).ToLLMToolDefs(); len(defs) > 0 {
			return defs, decision.withTools("delegation_intent", defs)
		}
	}
selectParentTools:
	allowed := allowedToolNamesForSnapshot(snapshot)
	reason := "parent_intent"
	if len(allowed) == 0 {
		reason = "none"
	}
	if delegationComplete {
		allowed = withoutToolNames(allowed, delegateToolNames...)
	}
	if pendingPostDelegationWrite {
		reason = "post_delegation_pending_action"
		allowed = append(allowed, readOnlyToolNames...)
		allowed = append(allowed, writeToolNames...)
		allowed = append(allowed, gitReadToolNames...)
		allowed = append(allowed, "tool_help")
		if postDelegationNeedsSynthesisAgent(snapshot) {
			allowed = append(allowed, delegateToolNames...)
		}
	}
	pluginNames := r.pluginToolNames()
	pluginIntent := inputSuggestsPluginTool(snapshot.LastInput, pluginNames)
	if len(allowed) == 0 && !pluginIntent && len(snapshot.AgentTasks) > 0 {
		if defs := r.tools.Filter(agentStatusToolNames).ToLLMToolDefs(); len(defs) > 0 {
			return defs, decision.withTools("agent_status_state", defs)
		}
	}
	if len(allowed) == 0 && !pluginIntent {
		reason = "fallback_delegate"
		allowed = append(allowed, delegateToolNames...)
	}
	if pluginIntent {
		if reason == "none" || reason == "fallback_delegate" {
			reason = "plugin_intent"
		} else {
			reason = reason + "+plugin_intent"
		}
		allowed = append(allowed, pluginNames...)
	}
	if !delegationComplete {
		allowed = append(allowed, delegateToolNames...)
	}
	defs := r.tools.Filter(allowed).ToLLMToolDefs()
	return defs, decision.withTools(reason, defs)
}

func newToolExposureDecision(snapshot SessionSnapshot) ToolExposureDecision {
	decision := ToolExposureDecision{
		Reason:                "none",
		OutstandingAgentCount: len(outstandingSpawnedAgents(snapshot)),
	}
	if snapshot.PendingDelegationAction != nil {
		decision.PendingActionKind = string(snapshot.PendingDelegationAction.Kind)
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

func withoutToolNames(names []string, excluded ...string) []string {
	if len(names) == 0 || len(excluded) == 0 {
		return names
	}
	exclude := make(map[string]struct{}, len(excluded))
	for _, name := range excluded {
		exclude[name] = struct{}{}
	}
	out := names[:0]
	for _, name := range names {
		if _, ok := exclude[name]; ok {
			continue
		}
		out = append(out, name)
	}
	return out
}

func (r *Runner) pluginToolNames() []string {
	if r == nil || r.tools == nil {
		return nil
	}
	var names []string
	for _, tool := range r.tools.All() {
		if strings.HasPrefix(strings.TrimSpace(tool.Name), "plugin__") {
			names = append(names, tool.Name)
		}
	}
	return names
}

func inputSuggestsPluginTool(input string, pluginNames []string) bool {
	if len(pluginNames) == 0 {
		return false
	}
	text := normalizeToolIntentText(input)
	if text == "" {
		return false
	}
	if strings.Contains(text, "plugin") || strings.Contains(text, "plugin__") {
		return true
	}
	for _, name := range pluginNames {
		for _, token := range pluginIntentTokens(name) {
			if token != "" && strings.Contains(text, token) {
				return true
			}
		}
	}
	return false
}

func pluginIntentTokens(namespacedName string) []string {
	parts := strings.Split(strings.TrimSpace(namespacedName), "__")
	if len(parts) != 3 || parts[0] != "plugin" {
		return nil
	}
	tokens := []string{
		normalizePluginIntentToken(parts[1]),
		normalizePluginIntentToken(parts[1] + " " + parts[2]),
	}
	if token := normalizePluginIntentToken(parts[2]); !isGenericPluginIntentToken(token) {
		tokens = append(tokens, token)
	}
	return tokens
}

func isGenericPluginIntentToken(token string) bool {
	switch token {
	case "", "task", "skill", "grep", "glob", "look at":
		return true
	default:
		return false
	}
}

func normalizePluginIntentToken(token string) string {
	token = strings.NewReplacer("_", " ", "-", " ").Replace(strings.ToLower(strings.TrimSpace(token)))
	return strings.Join(strings.Fields(token), " ")
}

func allowedToolNamesForSnapshot(snapshot SessionSnapshot) []string {
	text := normalizeToolIntentText(snapshot.LastInput)
	repoContext := inputSuggestsRepoContext(text)
	allowed := make(map[string]struct{})

	add := func(names ...string) {
		for _, name := range names {
			name = strings.TrimSpace(name)
			if name == "" {
				continue
			}
			allowed[name] = struct{}{}
		}
	}
	addAll := func(groups ...[]string) {
		for _, group := range groups {
			add(group...)
		}
	}

	operation := ""
	if snapshot.TaskState != nil {
		operation = strings.ToLower(strings.TrimSpace(snapshot.TaskState.Operation))
	}

	switch operation {
	case "overview":
		addAll(readOnlyToolNames, gitReadToolNames, planningToolNames)
	case "inspect", "analysis", "review", "plan":
		addAll(readOnlyToolNames, writeToolNames, gitReadToolNames, planningToolNames)
		if inputSuggestsCommandWork(text) {
			addAll(commandToolNames)
		}
		if inputSuggestsWebResearch(text) {
			addAll(webToolNames)
		}
	case "implement":
		addAll(readOnlyToolNames, writeToolNames, gitReadToolNames, commandToolNames, planningToolNames)
		if inputSuggestsPreviewWork(text) {
			addAll(previewToolNames)
		}
	case "validate":
		addAll(readOnlyToolNames, gitReadToolNames, commandToolNames, planningToolNames)
	case "preview":
		addAll(readOnlyToolNames, writeToolNames, previewToolNames, planningToolNames)
	case "merge":
		addAll(readOnlyToolNames, writeToolNames, gitReadToolNames, commandToolNames, planningToolNames)
		add("git_commit")
	default:
		if inputSuggestsPreviewWork(text) {
			addAll(readOnlyToolNames, writeToolNames, previewToolNames)
		}
		if inputSuggestsFileInspection(text) {
			addAll(readOnlyToolNames)
			if repoContext {
				addAll(gitReadToolNames)
			}
		}
		if inputSuggestsFileWrites(text) {
			addAll(writeToolNames)
		}
		if inputSuggestsCommandWork(text) {
			addAll(commandToolNames)
		}
		if inputSuggestsWebResearch(text) {
			addAll(webToolNames)
		}
		if inputSuggestsGitCommit(text) {
			addAll(gitReadToolNames)
			add("git_commit")
		}
	}

	if inputSuggestsDelegation(text) {
		addAll(delegateToolNames)
	}
	if inputSuggestsActionFollowUp(text) {
		addAll(readOnlyToolNames, writeToolNames, gitReadToolNames, commandToolNames, planningToolNames)
	}
	if inputSuggestsBugFixWork(text) {
		addAll(readOnlyToolNames, writeToolNames, commandToolNames, planningToolNames)
	}
	if inputSuggestsGitPush(text) {
		addAll(gitReadToolNames, commandToolNames)
	}
	if inputAsksGitPushStatus(text) {
		addAll(gitReadToolNames)
	}
	if inputSuggestsGitCommit(text) {
		add("git_commit")
	}
	if historyIncludesToolHelp(snapshot) && len(allowed) > 0 {
		addAll(writeToolNames, commandToolNames)
	}
	if len(allowed) > 0 {
		add("tool_help")
	}

	names := make([]string, 0, len(allowed))
	for name := range allowed {
		names = append(names, name)
	}
	return names
}

func historyIncludesToolHelp(snapshot SessionSnapshot) bool {
	return historyIncludesToolCall(snapshot, "tool_help")
}

func historyIncludesToolCall(snapshot SessionSnapshot, toolName string) bool {
	for _, msg := range snapshot.History {
		if msg.Role != llm.RoleAssistant {
			continue
		}
		for _, tc := range msg.ToolCalls {
			if tc.Name == toolName {
				return true
			}
		}
	}
	return false
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

func postDelegationToolIntentText(snapshot SessionSnapshot) string {
	parts := []string{snapshot.LastInput}
	if hints := postDelegationActionHints(completedToolCallResults(snapshot, "wait_agent")); hints != "" {
		parts = append(parts, hints)
	}
	return strings.Join(parts, "\n")
}

func postDelegationActionHints(results []string) string {
	var hints []string
	for _, result := range results {
		text := normalizeToolIntentText(result)
		if text == "" {
			continue
		}
		if inputMentionsPathLikeText(text) && containsToolPhrase(text,
			"report path", "intended report path", "file creation", "target path",
			"docs/reports", "docs/findings", "docs/superpowers", ".md",
		) {
			hints = append(hints, "write report path docs/reports/report.md")
		}
		if !containsToolPhrase(text, "do not commit", "don't commit") && inputSuggestsGitCommit(text) {
			hints = append(hints, "commit it")
		}
	}
	return strings.Join(hints, "\n")
}

func (r *Runner) updatePostDelegationWorkflow(toolName, result string, isError bool) {
	if r == nil {
		return
	}
	toolName = strings.TrimSpace(toolName)
	switch toolName {
	case "spawn_agent":
		if isError || r.session == nil {
			return
		}
		snapshot := r.session.Snapshot()
		if !inputSuggestsFileWrites(normalizeToolIntentText(snapshot.LastInput)) {
			return
		}
		action := DelegationActionState{
			Kind:        DelegationActionWriteDoc,
			TargetPath:  extractDelegationTargetPath(snapshot.LastInput),
			Description: "write delegated output",
		}
		if agentResult, ok := decodeAgentToolResult(result); ok {
			action.SourceAgent = strings.TrimSpace(agentResult.ID)
		}
		r.postDelegation.pendingWrite = true
		r.session.SetPendingDelegationAction(action)
	case "wait_agent":
		if isError {
			return
		}
		snapshot := SessionSnapshot{}
		if r.session != nil {
			snapshot = r.session.Snapshot()
		}
		if inputSuggestsFileWrites(normalizeToolIntentText(snapshot.LastInput)) || inputSuggestsFileWrites(normalizeToolIntentText(result)) {
			r.postDelegation.pendingWrite = true
			if r.session != nil {
				action := DelegationActionState{
					Kind:        DelegationActionWriteDoc,
					TargetPath:  extractDelegationTargetPath(snapshot.LastInput + "\n" + result),
					Description: "write delegated output",
				}
				if agentResult, ok := decodeAgentToolResult(result); ok {
					action.SourceAgent = strings.TrimSpace(agentResult.ID)
				}
				r.session.SetPendingDelegationAction(action)
			}
		}
	case "write_file", "edit_file", "apply_patch":
		if !isError {
			r.postDelegation.pendingWrite = false
			if r.session != nil {
				r.session.ClearPendingDelegationAction()
			}
		}
	}
}

func extractDelegationTargetPath(text string) string {
	for _, field := range strings.Fields(text) {
		candidate := strings.Trim(field, "`'\".,:;()[]{}<>")
		candidate = strings.TrimPrefix(candidate, "path=")
		candidate = strings.TrimPrefix(candidate, "target=")
		if strings.HasSuffix(strings.ToLower(candidate), ".md") {
			return candidate
		}
	}
	return ""
}

func pendingPostDelegationWriteAction(snapshot SessionSnapshot) bool {
	waitResultIndex := lastCompletedToolResultIndex(snapshot, "wait_agent")
	if waitResultIndex < 0 {
		return false
	}
	currentPostDelegationAction := inputSuggestsPostDelegationAction(normalizeToolIntentText(snapshot.LastInput))
	if completedAgentFallbackAfterIndex(snapshot, waitResultIndex) && !currentPostDelegationAction {
		return false
	}
	if successfulToolResultAfterIndex(snapshot, waitResultIndex, writeToolNames) {
		return false
	}
	if historyBeforeIndexSuggestsFileWrite(snapshot, waitResultIndex) {
		return true
	}
	for _, result := range completedToolCallResults(snapshot, "wait_agent") {
		if inputSuggestsFileWrites(normalizeToolIntentText(result)) {
			return true
		}
	}
	return false
}

func completedAgentFallbackSettledWrite(snapshot SessionSnapshot) bool {
	waitResultIndex := lastCompletedToolResultIndex(snapshot, "wait_agent")
	return waitResultIndex >= 0 && completedAgentFallbackAfterIndex(snapshot, waitResultIndex)
}

func completedAgentFallbackAfterIndex(snapshot SessionSnapshot, index int) bool {
	const fallbackPrefix = "Parent model connection failed while composing the final response. Showing completed child-agent result instead."
	for i, msg := range snapshot.History {
		if i <= index || msg.Role != llm.RoleAssistant {
			continue
		}
		if strings.HasPrefix(strings.TrimSpace(msg.Content), fallbackPrefix) {
			return true
		}
	}
	return false
}

func pendingDelegationWriteAction(snapshot SessionSnapshot) bool {
	return snapshot.PendingDelegationAction != nil && snapshot.PendingDelegationAction.Kind == DelegationActionWriteDoc
}

func lastCompletedToolResultIndex(snapshot SessionSnapshot, toolName string) int {
	ids := make(map[string]struct{})
	last := -1
	for i, msg := range snapshot.History {
		switch msg.Role {
		case llm.RoleAssistant:
			for _, tc := range msg.ToolCalls {
				if tc.Name == toolName && strings.TrimSpace(tc.ID) != "" {
					ids[tc.ID] = struct{}{}
				}
			}
		case llm.RoleTool:
			if _, ok := ids[msg.ToolCallID]; ok {
				last = i
			}
		}
	}
	return last
}

func historyBeforeIndexSuggestsFileWrite(snapshot SessionSnapshot, index int) bool {
	if index < 0 {
		return false
	}
	for i, msg := range snapshot.History {
		if i > index {
			break
		}
		if msg.Role != llm.RoleUser {
			continue
		}
		if inputSuggestsFileWrites(normalizeToolIntentText(msg.Content)) {
			return true
		}
	}
	return false
}

func successfulToolResultAfterIndex(snapshot SessionSnapshot, index int, toolNames []string) bool {
	if index < 0 || len(toolNames) == 0 {
		return false
	}
	ids := make(map[string]struct{})
	for i, msg := range snapshot.History {
		if i <= index {
			continue
		}
		switch msg.Role {
		case llm.RoleAssistant:
			for _, tc := range msg.ToolCalls {
				if toolNameIn(tc.Name, toolNames) && strings.TrimSpace(tc.ID) != "" {
					ids[tc.ID] = struct{}{}
				}
			}
		case llm.RoleTool:
			if _, ok := ids[msg.ToolCallID]; ok && !strings.HasPrefix(normalizeToolIntentText(msg.Content), "error:") {
				return true
			}
		}
	}
	return false
}

func toolNameIn(name string, names []string) bool {
	name = strings.TrimSpace(name)
	for _, candidate := range names {
		if name == strings.TrimSpace(candidate) {
			return true
		}
	}
	return false
}

func shouldRequireToolCallForSnapshot(snapshot SessionSnapshot) bool {
	if len(outstandingSpawnedAgents(snapshot)) > 0 {
		text := normalizeToolIntentText(snapshot.LastInput)
		return shouldRouteParentThroughDelegation(snapshot) || inputSuggestsPostDelegationAction(text)
	}
	return shouldRouteParentThroughDelegation(snapshot) && !historyIncludesCompletedToolCall(snapshot, "wait_agent")
}

func shouldRouteParentThroughDelegation(snapshot SessionSnapshot) bool {
	if snapshot.TaskState != nil {
		return false
	}
	text := normalizeToolIntentText(snapshot.LastInput)
	return inputSuggestsDelegation(text)
}

func normalizeToolIntentText(text string) string {
	text = strings.ToLower(strings.TrimSpace(text))
	if text == "" {
		return ""
	}
	return strings.Join(strings.Fields(text), " ")
}

func inputSuggestsPreviewWork(text string) bool {
	return containsToolPhrase(text,
		"preview", "mock up", "mockup", "web page", "webpage", "landing page",
		"show in browser", "show me in browser", "html preview", "still up",
		"pick 3 others", "pick three others", "no neon", "show me them on the screen",
		"show it on the web page", "put that on the web page", "refresh the preview",
		"open the preview again",
	)
}

func inputSuggestsFileInspection(text string) bool {
	if text == "" {
		return false
	}
	if inputMentionsPathLikeText(text) {
		return true
	}
	return containsToolPhrase(text,
		"read ", "open ", "inspect ", "examine ", "check ", "look at ", "show ",
		"file", "files", "log", "logs", "trace", "debug", "readme", "config", "output", "image",
		"repo", "repository", "project", "codebase", "workspace", "directory", "folder",
		"what do you think", "tell me what you think", "anything i need change",
		"anything i need to change", "improve", "improvement", "review",
	)
}

func inputSuggestsRepoContext(text string) bool {
	return containsToolPhrase(text, "repo", "repository", "project", "codebase", "workspace")
}

func inputSuggestsFileWrites(text string) bool {
	if !containsToolPhrase(text,
		"write ", "save ", "create ", "update ", "edit ", "patch ", "append ",
		"rewrite ", "modify ",
	) {
		return inputSuggestsReportFileTarget(text)
	}
	return inputMentionsPathLikeText(text) || containsToolPhrase(text,
		" file", " files", "markdown", ".md", "to a file", "into a file",
		"readme", "config", "artifact", "html", "script",
		" doc", " docs", "document", "spec", "report", "findings", "memo", "note",
	)
}

func inputSuggestsReportFileTarget(text string) bool {
	return inputMentionsPathLikeText(text) && containsToolPhrase(text,
		"report path", "intended report path", "file creation", "target path", "save as", "write as",
		"docs/reports", "docs/findings", "docs/superpowers", "conformance.md", "audit.md",
	)
}

func inputSuggestsCommandWork(text string) bool {
	return containsToolPhrase(text,
		"run ", "command", "shell", "terminal", "test", "tests", "build", "lint",
		"install", "compile", "benchmark", "start server", "restart server",
		"dev server", "keep it running", "terminal session",
	)
}

func inputSuggestsWebResearch(text string) bool {
	if inputSuggestsPreviewWork(text) {
		return false
	}
	return containsToolPhrase(text,
		"latest", "look up", "lookup", "search the web", "browse", "online",
		"internet", "website", "url", "fetch", "news",
	)
}

func inputSuggestsGitCommit(text string) bool {
	return containsToolPhrase(text,
		"git commit", "commit it", "commit this", "create a commit",
		"make a commit", "commit the changes",
	)
}

func inputSuggestsBugFixWork(text string) bool {
	return containsToolPhrase(text,
		"bug", "broken", "does not work", "doesn't work", "not working",
		"fix this", "fix it", "patch this", "issue", "error", "failing",
		"wrong", "regression", "cursor", "input pane", "input panel",
	)
}

func inputSuggestsActionFollowUp(text string) bool {
	return containsToolPhrase(text,
		"do it", "continue", "use what you need", "go ahead", "implement it", "build it",
		"make the change", "make changes", "fix it", "apply it", "ship it", "finish it",
	)
}

func inputSuggestsGitPush(text string) bool {
	return containsToolPhrase(text,
		"git push", "push it", "push this", "push main", "push to remote",
		"push the branch", "push the changes", "publish local commits", "publish commits",
	)
}

func inputAsksGitPushStatus(text string) bool {
	return containsToolPhrase(text,
		"did you push", "did it push", "is it pushed", "was it pushed",
		"have you pushed", "has it been pushed",
	)
}

func inputSuggestsDelegation(text string) bool {
	if inputSuggestsMultiRootDelegatedReport(text) {
		return true
	}
	return containsToolPhrase(text,
		"sub-agent", "sub agent", "delegate", "parallel agent", "parallel agents", "spawn agent",
		"ask agent", "ask agents", "use agent", "use agents", "agent to", "agents to",
		"multiple agents", "three agents", "3 agents", "omo agent", "omo agents", "openagent", "oh my openagent",
		"audit this repo", "audit the repo", "audit repository", "audit this codebase", "audit the codebase",
		"compare this repo", "compare the repo", "fall down compared to",
	)
}

func inputSuggestsMultiRootDelegatedReport(text string) bool {
	return inputSuggestsFileWrites(text) && len(absoluteWorkspaceRoots(text)) > 1
}

func absoluteWorkspaceRoots(text string) map[string]struct{} {
	roots := make(map[string]struct{})
	for _, field := range strings.Fields(text) {
		path := strings.Trim(field, "`'\".,;:()[]{}<>")
		if !strings.HasPrefix(path, "/") && !strings.HasPrefix(path, "~/") {
			continue
		}
		root := absoluteWorkspaceRoot(path)
		if root != "" {
			roots[root] = struct{}{}
		}
	}
	return roots
}

func absoluteWorkspaceRoot(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	if strings.HasPrefix(path, "~/") {
		parts := strings.Split(strings.Trim(path, "/"), "/")
		if len(parts) < 3 {
			return path
		}
		return strings.Join(parts[:3], "/")
	}
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) < 2 {
		return path
	}
	if len(parts) >= 4 && strings.EqualFold(parts[0], "users") {
		return "/" + strings.Join(parts[:4], "/")
	}
	return "/" + strings.Join(parts[:2], "/")
}

func inputSuggestsPostDelegationAction(text string) bool {
	return inputSuggestsFileWrites(text) ||
		inputSuggestsCommandWork(text) ||
		inputSuggestsPreviewWork(text) ||
		inputSuggestsGitCommit(text) ||
		inputSuggestsGitPush(text) ||
		inputSuggestsActionFollowUp(text) ||
		inputSuggestsBugFixWork(text)
}

func inputMentionsPathLikeText(text string) bool {
	if strings.Contains(text, "/") || strings.Contains(text, "\\") {
		return true
	}
	return containsToolPhrase(text,
		".go", ".md", ".txt", ".log", ".json", ".yaml", ".yml", ".toml",
		".png", ".jpg", ".jpeg", ".gif", ".svg",
	)
}

func containsToolPhrase(text string, phrases ...string) bool {
	for _, phrase := range phrases {
		if strings.Contains(text, phrase) {
			return true
		}
	}
	return false
}

func (r *Runner) blockedToolResult(toolName string, args map[string]any) string {
	output := r.beforeToolHookOutput(context.Background(), toolName, args)
	if output.Block == nil {
		return ""
	}
	return strings.TrimSpace(output.Block.Message)
}

func (r *Runner) updateGitWorkflow(toolName string, args map[string]any, result string) {
	switch toolName {
	case "run_command":
		r.updateGitWorkflowForCommand(strings.ToLower(strings.TrimSpace(stringArg(args, "command"))), result)
	case "git_commit":
		r.updateGitWorkflowForCommitResult(result)
	case "edit_file", "write_file":
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
	case strings.Contains(command, "git add"):
		if r.gitWorkflow.commitBlocker == commitBlockerRestage {
			r.gitWorkflow.commitBlocker = commitBlockerNone
			r.gitWorkflow.blockerSummary = ""
		}
	}
}

func (r *Runner) updateGitWorkflowForCommitResult(result string) {
	lower := strings.ToLower(result)
	switch {
	case isSuccessfulGitCommit(result):
		r.gitWorkflow = gitWorkflowState{}
	case strings.Contains(lower, "files were modified by this hook"):
		r.gitWorkflow.mergeActive = true
		r.gitWorkflow.commitBlocker = commitBlockerRestage
		r.gitWorkflow.blockerSummary = "pre-commit modified files; re-stage them before retrying commit"
	case strings.Contains(lower, "hook id:") || strings.Contains(lower, "line too long") || strings.Contains(lower, "error committing:"):
		r.gitWorkflow.mergeActive = true
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

func newLoopHookRegistry() *hooks.Registry {
	registry := hooks.NewRegistry()
	registry.Register(hooks.PointPromptContext, "inspect_first_action", inspectFirstActionPromptHook)
	registry.Register(hooks.PointPromptContext, "review_guidance", reviewPromptHook)
	registry.Register(hooks.PointPromptContext, "preview_workflow", previewWorkflowPromptHook)
	registry.Register(hooks.PointPromptContext, "plan_blocker", blockedPlanPromptHook)
	registry.Register(hooks.PointPromptContext, "post_delegation_write", postDelegationWritePromptHook)
	registry.Register(hooks.PointPromptContext, "synthesis_guidance", synthesisPromptHook)
	registry.Register(hooks.PointPromptContext, "validation_failure", validationPromptHook)
	registry.Register(hooks.PointPromptContext, "search_thrash", searchThrashPromptHook)
	registry.Register(hooks.PointPromptContext, "git_workflow", gitWorkflowPromptHook)
	registry.Register(hooks.PointPromptContext, "repeat_loop", repeatLoopPromptHook)
	registry.Register(hooks.PointPromptContext, "agent_status", agentStatusPromptHook)
	registry.Register(hooks.PointBeforeTool, "git_commit_blocker", beforeToolGitCommitBlockHook)
	return registry
}

func inspectFirstActionPromptHook(_ context.Context, event hooks.Event) []hooks.Result {
	payload, ok := event.Transient.(promptHookPayload)
	if !ok || payload.Mode != ModeInspect {
		return nil
	}
	snap, ok := event.Snapshot.(SessionSnapshot)
	if ok && snapshotHasRepoReadEvidence(snap) {
		if !isRepoOverviewTask(snap) {
			return nil
		}
		return []hooks.Result{hooks.OverlayResult{
			Key:        "inspect_first_action",
			Content:    "Repo overview workflow active. If you already have the repo root listing and one high-signal file such as README.md, stop exploring and answer briefly in 2-4 bullets or a short paragraph. Keep it conversational and do not turn it into a full repo audit.",
			Priority:   hooks.PriorityHigh,
			Provenance: "runtime",
		}}
	}
	content := "Repo inspection workflow active. Start with a short natural sentence explaining what you are checking, then call the repo read/search tool. For a general overview, list_dir(.) or read_file(README.md) is usually enough to begin."
	if ok && isRepoOverviewTask(snap) {
		content = "Repo overview workflow active. Start with a short natural sentence explaining what you are checking, then call the repo read/search tool. Usually list_dir(.) plus README.md or one other high-signal file is enough; once you have that, stop exploring and answer briefly."
	}
	return []hooks.Result{hooks.OverlayResult{
		Key:        "inspect_first_action",
		Content:    content,
		Priority:   hooks.PriorityHigh,
		Provenance: "runtime",
	}}
}

func reviewPromptHook(_ context.Context, event hooks.Event) []hooks.Result {
	payload, ok := event.Transient.(promptHookPayload)
	if !ok || payload.Mode != ModeReview {
		return nil
	}
	return []hooks.Result{hooks.OverlayResult{
		Key:        "review_guidance",
		Content:    "Review workflow active. Lead with findings before summary, keep findings grounded in repo evidence, and call out regressions, risks, or missing tests explicitly.",
		Priority:   hooks.PriorityHigh,
		Provenance: "runtime",
	}}
}

func previewWorkflowPromptHook(_ context.Context, event hooks.Event) []hooks.Result {
	payload, ok := event.Transient.(promptHookPayload)
	if !ok || payload.Mode != ModePreview {
		return nil
	}
	snap, ok := event.Snapshot.(SessionSnapshot)
	if !ok || snapshotHasPreviewVerificationEvidence(snap) {
		return nil
	}

	var content string
	switch {
	case snapshotHasWriteEvidence(snap):
		content = "Preview workflow active. The preview content is already written. Call preview_server_ensure now, then answer with the verified URL."
	case payload.PlanWorkflow.active && strings.EqualFold(payload.PlanWorkflow.mode, "preview") && payload.PlanWorkflow.synthesisRequired:
		content = "Preview workflow active. You have enough repo evidence for the mockup. Stop exploring. Write the preview artifact or target file now, then call preview_server_ensure and answer with the verified URL. If the user asked for multiple concepts, present them together on one preview page instead of researching more."
	case !snapshotHasAnyToolEvidence(snap):
		content = "Preview workflow active. Start with the most likely directory or named file from the request rather than a repo-wide survey. Prefer list_dir on the likely folder, read_file on the candidate file, or code_search with one literal identifier. Avoid shotgun alternation searches like foo|bar|baz."
	case snapshotHasRepoReadEvidence(snap):
		content = "Preview workflow active. Keep research tight: after 1-3 high-signal reads, stop exploring, write the mockup artifact or target file, and call preview_server_ensure."
	default:
		return nil
	}

	return []hooks.Result{hooks.OverlayResult{
		Key:        "preview_workflow",
		Content:    content,
		Priority:   hooks.PriorityHigh,
		Provenance: "runtime",
	}}
}

func snapshotHasRepoReadEvidence(snapshot SessionSnapshot) bool {
	for _, msg := range snapshot.History {
		if msg.Role != llm.RoleAssistant {
			continue
		}
		for _, call := range msg.ToolCalls {
			switch strings.TrimSpace(call.Name) {
			case "read_file", "search", "code_search", "list_dir", "glob",
				"git_status", "git_diff", "git_log", "git_branch_state", "git_merge_status",
				"artifact_read", "preview_server_status",
				"lsp_definition", "lsp_references", "lsp_hover", "lsp_document_symbols":
				return true
			}
		}
	}
	return false
}

func isRepoOverviewTask(snapshot SessionSnapshot) bool {
	if snapshot.TaskState == nil {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(snapshot.TaskState.Operation), "overview")
}

func snapshotHasAnyToolEvidence(snapshot SessionSnapshot) bool {
	for _, msg := range snapshot.History {
		switch msg.Role {
		case llm.RoleAssistant:
			if len(msg.ToolCalls) > 0 {
				return true
			}
		case llm.RoleTool:
			return true
		}
	}
	return false
}

func snapshotHasWriteEvidence(snapshot SessionSnapshot) bool {
	for _, msg := range snapshot.History {
		if msg.Role != llm.RoleAssistant {
			continue
		}
		for _, call := range msg.ToolCalls {
			switch strings.TrimSpace(call.Name) {
			case "edit_file", "write_file", "apply_patch", "artifact_write":
				return true
			}
		}
	}
	return false
}

func snapshotHasPreviewVerificationEvidence(snapshot SessionSnapshot) bool {
	previewCalls := map[string]struct{}{}
	for _, msg := range snapshot.History {
		switch msg.Role {
		case llm.RoleAssistant:
			for _, call := range msg.ToolCalls {
				switch strings.TrimSpace(call.Name) {
				case "preview_server_ensure", "preview_server_status":
					previewCalls[call.ID] = struct{}{}
				}
			}
		case llm.RoleTool:
			if _, ok := previewCalls[msg.ToolCallID]; !ok {
				continue
			}
			if previewVerificationResultLooksLive(msg.Content) {
				return true
			}
		}
	}
	return false
}

func previewVerificationResultLooksLive(result string) bool {
	lower := strings.ToLower(strings.TrimSpace(result))
	if lower == "" {
		return false
	}
	if !strings.Contains(lower, "http://127.0.0.1:") && !strings.Contains(lower, `"url"`) {
		return false
	}
	return strings.Contains(lower, `"status":"live"`) || strings.Contains(lower, `"status":"running"`)
}

func looksLikeLegacyXMLToolCall(text string) bool {
	if _, ok := parseLegacyXMLToolCall(text); ok {
		return true
	}
	if _, ok := parseXMLToolCallsWrapper(text); ok {
		return true
	}
	return containsXMLToolCallMarkup(text)
}

func containsXMLToolCallMarkup(text string) bool {
	lower := strings.ToLower(strings.TrimSpace(text))
	return strings.Contains(lower, "<tool_call") ||
		strings.Contains(lower, "</tool_call") ||
		strings.Contains(lower, "<tool_calls") ||
		strings.Contains(lower, "</tool_calls") ||
		strings.Contains(lower, "<function_calls") ||
		strings.Contains(lower, "</function_calls")
}

// parseXMLToolCallsWrapper detects the <tool_calls>...</tool_calls> XML wrapper
// format used by some providers.
func parseXMLToolCallsWrapper(text string) (llm.NativeToolCall, bool) {
	const open = "<tool_calls>"
	const close = "</tool_calls>"
	start := strings.Index(text, open)
	if start < 0 {
		return llm.NativeToolCall{}, false
	}
	end := strings.LastIndex(text, close)
	if end < 0 || end <= start {
		return llm.NativeToolCall{}, false
	}
	inner := strings.TrimSpace(text[start+len(open) : end])
	if inner == "" {
		return llm.NativeToolCall{}, false
	}
	return llm.NativeToolCall{ID: "legacy_xml_wrapper_1", Name: "", ArgsJSON: "{}"}, true
}

// stripXMLToolCallMarkup removes <tool_calls>...</tool_calls> and
// <tool_call>...</tool_call> XML markup from text. This prevents models
// from leaking tool call syntax into the chat history and display when
// they emit XML alongside native tool calls.
func stripXMLToolCallMarkup(text string) string {
	text = stripXMLBlock(text, "<tool_calls>", "</tool_calls>")
	text = stripXMLBlock(text, "<tool_call>", "</tool_call>")
	text = stripSelfClosingXMLTag(text, "<tool_call")
	return strings.TrimSpace(text)
}

func stripSelfClosingXMLTag(text, open string) string {
	for {
		lower := strings.ToLower(text)
		start := strings.Index(lower, open)
		if start < 0 {
			return text
		}
		end := strings.Index(lower[start:], "/>")
		if end < 0 {
			return text
		}
		end += start
		text = strings.TrimSpace(text[:start] + text[end+len("/>"):])
	}
}

func stripXMLBlock(text, open, close string) string {
	for {
		start := strings.Index(text, open)
		if start < 0 {
			return text
		}
		end := strings.Index(text[start+len(open):], close)
		if end < 0 {
			return text
		}
		end += start + len(open)
		text = strings.TrimSpace(text[:start] + text[end+len(close):])
	}
}

func blockedPlanPromptHook(_ context.Context, event hooks.Event) []hooks.Result {
	payload, ok := event.Transient.(promptHookPayload)
	if !ok {
		return nil
	}
	blocker := currentPlanBlocker(payload.PlanState)
	if payload.Mode != ModePlan || blocker == "" {
		return nil
	}
	return []hooks.Result{hooks.OverlayResult{
		Key:        "plan_blocker",
		Content:    "Current plan is blocked: " + blocker + ". Resolve the blocker directly if you can, otherwise use ask_user_question to get the missing decision before continuing broad work.",
		Priority:   hooks.PriorityHigh,
		Provenance: "runtime",
	}}
}

func postDelegationWritePromptHook(_ context.Context, event hooks.Event) []hooks.Result {
	snap, ok := event.Snapshot.(SessionSnapshot)
	if !ok || !historyIncludesCompletedToolCall(snap, "wait_agent") {
		return nil
	}
	if !pendingDelegationWriteAction(snap) && !pendingPostDelegationWriteAction(snap) {
		return nil
	}
	target := "the requested document path"
	if snap.PendingDelegationAction != nil && strings.TrimSpace(snap.PendingDelegationAction.TargetPath) != "" {
		target = strings.TrimSpace(snap.PendingDelegationAction.TargetPath)
	}
	content := "Post-delegation document write active. Use completed child-agent results as source material, but synthesize the requested document instead of concatenating reports. Do not paste raw child-agent outputs or role headings such as `## explorer`. Write the final, user-facing document with write_file to " + target + ". Include prioritized findings, evidence paths, and concrete next steps when the user requested a gaps/findings report."
	if postDelegationNeedsSynthesisAgent(snap) {
		content += " Multiple completed child-agent results are available and no synthesizer result is recorded. Spawn a read-only synthesizer agent and include the full completed child-agent results in its task_description. The synthesizer has no filesystem or search tools; do not ask it to read files, search repositories, or inspect paths. Wait for it, then write_file the synthesized result. Do not write any single child-agent report."
	}
	return []hooks.Result{hooks.OverlayResult{
		Key:        "post_delegation_write",
		Content:    content,
		Priority:   hooks.PriorityHigh,
		Provenance: "runtime",
	}}
}

func postDelegationNeedsSynthesisAgent(snap SessionSnapshot) bool {
	if !pendingDelegationWriteAction(snap) && !pendingPostDelegationWriteAction(snap) {
		return false
	}
	resultTurn := completedAgentResultTurn(snap)
	completed := 0
	for _, task := range snap.AgentTasks {
		if task.Status != AgentStatusCompleted || task.ParentTurn != resultTurn || strings.TrimSpace(task.Result) == "" {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(task.Role), "synthesizer") {
			return false
		}
		completed++
	}
	return completed > 1
}

func synthesisPromptHook(_ context.Context, event hooks.Event) []hooks.Result {
	payload, ok := event.Transient.(promptHookPayload)
	if !ok {
		return nil
	}
	content := strings.TrimSpace(payload.PlanWorkflow.overlayContent())
	if content == "" {
		return nil
	}
	return []hooks.Result{hooks.OverlayResult{
		Key:        "synthesis_guidance",
		Content:    content,
		Priority:   hooks.PriorityHigh,
		Provenance: "runtime",
	}}
}

func validationPromptHook(_ context.Context, event hooks.Event) []hooks.Result {
	payload, ok := event.Transient.(promptHookPayload)
	if !ok {
		return nil
	}
	content := strings.TrimSpace(payload.ValidationWorkflow.overlayContent())
	if content == "" {
		return nil
	}
	return []hooks.Result{hooks.OverlayResult{
		Key:        "validation_failure",
		Content:    content,
		Priority:   hooks.PriorityHigh,
		Provenance: "runtime",
	}}
}

func searchThrashPromptHook(_ context.Context, event hooks.Event) []hooks.Result {
	payload, ok := event.Transient.(promptHookPayload)
	if !ok {
		return nil
	}
	content := strings.TrimSpace(payload.SearchWorkflow.overlayContent())
	if content == "" {
		return nil
	}
	return []hooks.Result{hooks.OverlayResult{
		Key:        "search_thrash",
		Content:    content,
		Priority:   hooks.PriorityHigh,
		Provenance: "runtime",
	}}
}

func gitWorkflowPromptHook(_ context.Context, event hooks.Event) []hooks.Result {
	payload, ok := event.Transient.(promptHookPayload)
	if !ok {
		return nil
	}
	content := strings.TrimSpace(payload.GitWorkflow.overlayContent())
	if content == "" {
		return nil
	}
	return []hooks.Result{hooks.OverlayResult{
		Key:        "git_workflow",
		Content:    content,
		Priority:   hooks.PriorityHigh,
		Provenance: "runtime",
	}}
}

func repeatLoopPromptHook(_ context.Context, event hooks.Event) []hooks.Result {
	payload, ok := event.Transient.(promptHookPayload)
	if !ok {
		return nil
	}
	content := strings.TrimSpace(payload.RepeatWorkflow.overlayContent(payload.ToolThrashCircuitBreaker))
	if content == "" {
		return nil
	}
	return []hooks.Result{hooks.OverlayResult{
		Key:        "repeat_loop",
		Content:    content,
		Priority:   hooks.PriorityHigh,
		Provenance: "runtime",
	}}
}

func agentStatusPromptHook(_ context.Context, event hooks.Event) []hooks.Result {
	snap, ok := event.Snapshot.(SessionSnapshot)
	if !ok {
		return nil
	}
	agents := outstandingSpawnedAgents(snap)
	if len(agents) == 0 {
		return nil
	}
	var b strings.Builder
	b.WriteString("Outstanding child agents are still unresolved:\n")
	tasksByID := make(map[string]AgentTaskState, len(snap.AgentTasks))
	for _, task := range snap.AgentTasks {
		if id := strings.TrimSpace(task.ID); id != "" {
			tasksByID[id] = task
		}
	}
	for _, agent := range agents {
		if task, ok := tasksByID[strings.TrimSpace(agent.ID)]; ok && agentStillOutstanding(task.Status) {
			fmt.Fprintf(&b, "- %s\n", formatAgentTaskPromptLine(task))
			continue
		}
		role := strings.TrimSpace(agent.Role)
		if role == "" {
			role = "unknown-role"
		}
		status := strings.TrimSpace(string(agent.Status))
		if status == "" {
			status = string(AgentStatusRunning)
		}
		fmt.Fprintf(&b, "- %s (%s): %s\n", strings.TrimSpace(agent.ID), role, status)
	}
	b.WriteString("Do not say no agents are running while this list is non-empty. If the user asks about agents, report this state; use wait_agent with the agent id before continuing delegated work.")
	return []hooks.Result{hooks.OverlayResult{
		Key:        "agent_status",
		Content:    strings.TrimSpace(b.String()),
		Priority:   hooks.PriorityHigh,
		Provenance: "runtime",
	}}
}

func formatAgentTaskPromptLine(task AgentTaskState) string {
	role := strings.TrimSpace(task.Role)
	if role == "" {
		role = "unknown-role"
	}
	status := strings.TrimSpace(string(task.Status))
	if status == "" {
		status = string(AgentStatusRunning)
	}
	line := fmt.Sprintf("%s (%s): %s", strings.TrimSpace(task.ID), role, status)
	if tool := strings.TrimSpace(task.LastToolName); tool != "" {
		line += "; last: " + tool
		if len(task.RecentActivity) > 0 {
			last := task.RecentActivity[len(task.RecentActivity)-1]
			if summary := strings.TrimSpace(last.Summary); summary != "" {
				line += " " + redactRuntimeText(summary)
			}
		}
	}
	return line
}

func redactRuntimeText(text string) string {
	if text == "" {
		return ""
	}
	scanner := secscan.NewDefaultScanner()
	return secscan.Redact(text, scanner.Scan(text))
}

func beforeToolGitCommitBlockHook(_ context.Context, event hooks.Event) []hooks.Result {
	payload, ok := event.Transient.(beforeToolHookPayload)
	if !ok || !isCommitToolCall(payload.ToolName, payload.Args) {
		return nil
	}
	switch {
	case payload.GitWorkflow.unmergedFiles:
		return []hooks.Result{hooks.BlockResult{
			Message:    "blocked: unmerged git conflicts remain. Resolve conflicted files, stage them, and call git_merge_status before retrying commit.",
			Provenance: "runtime",
		}}
	case payload.GitWorkflow.commitBlocker == commitBlockerRestage:
		if payload.ToolName == "run_command" && strings.Contains(strings.ToLower(stringArg(payload.Args, "command")), "git add") {
			return nil
		}
		return []hooks.Result{hooks.BlockResult{
			Message:    "blocked: the previous commit attempt modified files via hooks. Re-stage those files and call git_merge_status before retrying commit.",
			Provenance: "runtime",
		}}
	case payload.GitWorkflow.commitBlocker == commitBlockerEdit:
		return []hooks.Result{hooks.BlockResult{
			Message:    "blocked: the previous commit attempt already failed and nothing has changed since then. Fix the reported hook issues and call git_merge_status before retrying commit.",
			Provenance: "runtime",
		}}
	default:
		return nil
	}
}

func mergePromptHookOutput(base, runtime hooks.ExecutionOutput) hooks.ExecutionOutput {
	merged := hooks.ExecutionOutput{
		Overlays: filterPromptHookOverlays(base.Overlays, runtime.Overlays),
		Failures: append([]hooks.Failure(nil), runtime.Failures...),
	}
	if base.Note != nil {
		note := *base.Note
		merged.Note = &note
	}
	if runtime.Note != nil && (merged.Note == nil || runtime.Note.Priority > merged.Note.Priority) {
		note := *runtime.Note
		merged.Note = &note
	}
	merged.Overlays = append(merged.Overlays, runtime.Overlays...)
	return merged
}

func filterPromptHookOverlays(overlays []hooks.OverlayResult, runtimeOverlays []hooks.OverlayResult) []hooks.OverlayResult {
	runtimePluginKeys := make(map[string]struct{}, len(runtimeOverlays))
	for _, overlay := range runtimeOverlays {
		if isPluginOverlay(overlay) {
			runtimePluginKeys[strings.ToLower(strings.TrimSpace(overlay.Key))] = struct{}{}
		}
	}
	filtered := make([]hooks.OverlayResult, 0, len(overlays))
	for _, overlay := range overlays {
		key := strings.TrimSpace(overlay.Key)
		if _, ok := loopHookOverlayKeys[key]; ok {
			continue
		}
		if _, ok := runtimePluginKeys[strings.ToLower(key)]; ok {
			continue
		}
		filtered = append(filtered, overlay)
	}
	return filtered
}

func isPluginOverlay(overlay hooks.OverlayResult) bool {
	return strings.HasPrefix(strings.TrimSpace(overlay.Provenance), "plugin:") ||
		strings.HasPrefix(strings.TrimSpace(overlay.Key), "plugin_")
}

func hasHookOutputContent(output hooks.ExecutionOutput) bool {
	return len(output.Overlays) > 0 || output.Note != nil || output.Block != nil || len(output.Failures) > 0
}

func cloneArgs(args map[string]any) map[string]any {
	if len(args) == 0 {
		return nil
	}
	cloned := make(map[string]any, len(args))
	maps.Copy(cloned, args)
	return cloned
}

func currentPlanBlocker(state *PlanState) string {
	if state == nil {
		return ""
	}
	if step, ok := state.BlockedStep(); ok {
		if blocker := strings.TrimSpace(step.Blocker); blocker != "" {
			return blocker
		}
		return strings.TrimSpace(step.Step)
	}
	return ""
}

func (r *Runner) updateSameFileSearchWorkflow(toolName string, args map[string]any, blocked bool) {
	if blocked {
		r.syncRuntimeNote()
		return
	}
	path := strings.TrimSpace(stringArg(args, "path"))
	switch toolName {
	case "code_search", "search":
		if path == "" {
			r.searchWorkflow = sameFileSearchWorkflowState{}
			r.syncRuntimeNote()
			return
		}
		if r.searchWorkflow.toolName == toolName && r.searchWorkflow.path == path {
			r.searchWorkflow.streak++
		} else {
			r.searchWorkflow = sameFileSearchWorkflowState{
				toolName: toolName,
				path:     path,
				streak:   1,
			}
		}
		r.searchWorkflow.nudged = r.searchWorkflow.streak >= toolThrashThreshold(r.toolThrashCircuitBreaker, sameFileSearchThrashThreshold)
	default:
		r.searchWorkflow = sameFileSearchWorkflowState{}
	}
	r.syncRuntimeNote()
}

func (r *Runner) updatePlanWorkflow(toolName string, args map[string]any, _ string, blocked bool) {
	if !r.planWorkflow.active {
		return
	}
	if blocked {
		r.syncRuntimeNote()
		return
	}
	if allowsPlanSynthesis(toolName) {
		// update_plan or think: reset exploration counter so the model can keep working
		r.planWorkflow.explorationBatches = 0
		r.planWorkflow.synthesisRequired = false
		r.planWorkflow.synthesisEscalated = false
		r.syncRuntimeNote()
		return
	}
	if !isExplorationToolCall(toolName, args) {
		// write mutation: the model made progress — reset exploration state
		r.planWorkflow.explorationBatches = 0
		r.planWorkflow.synthesisRequired = false
		r.planWorkflow.synthesisEscalated = false
		r.syncRuntimeNote()
		return
	}
	r.planWorkflow.explorationBatches++
	budget := synthesisGuardBudget(r.planWorkflow.mode)
	if r.planWorkflow.explorationBatches >= budget*2 {
		r.planWorkflow.synthesisEscalated = true
	}
	if r.planWorkflow.explorationBatches >= budget {
		r.planWorkflow.synthesisRequired = true
	}
	r.syncRuntimeNote()
}

func (s planWorkflowState) overlayContent() string {
	if !s.active || !s.synthesisRequired {
		return ""
	}
	if s.synthesisEscalated {
		return "URGENT: you have explored far too much without acting. Stop all exploration immediately. You must either make an edit, run a command, or provide a concrete text answer RIGHT NOW in this exact message. No more reading, searching, or listing."
	}
	switch s.mode {
	case "analysis":
		return "Analysis guidance: you have enough evidence to answer. Avoid exhaustive repo-wide searches, stop exploring and summarize findings or recommendations now. Put any uncertainty into open questions instead of doing more low-yield research."
	case "preview":
		return ""
	case "implement":
		return "Implementation guidance: you have gathered enough context. Stop exploring and either make an edit with the edit tools or provide a concrete text summary of what you found and what needs to change."
	case "inspect":
		return "Inspection guidance: you have gathered enough evidence. Stop searching and provide a concise summary grounded in the paths you already inspected."
	case "overview":
		return "Overview guidance: you have enough context. Stop exploring and give a brief, conversational overview grounded in the paths you already inspected."
	case "review":
		return "Review guidance: you have enough evidence. Stop searching and deliver your findings first, ordered by severity, with specific references to the code you inspected."
	case "validate":
		return "Validation guidance: you have enough context. Stop exploring and run the relevant verification command, or summarize what you found if no verification is needed."
	case "chat":
		return "Guidance: you have gathered enough context. Stop exploring and either act (edit, run, write) or provide a concrete answer grounded in what you already inspected."
	default:
		return "Planning task guidance: you have enough evidence to write the plan. Avoid exhaustive repo-wide searches, stop exploring and synthesize the next actionable plan now. Use update_plan to capture the steps, and put any uncertainty into open questions instead of doing more broad research."
	}
}

func (s gitWorkflowState) overlayContent() string {
	if s.unmergedFiles {
		return "Git merge workflow active. Call git_merge_status to inspect unresolved files, conflict previews, and next steps. Resolve each conflicted file, stage the resolutions, and only retry commit once unmerged files are gone."
	}
	if s.commitBlocker != commitBlockerNone {
		summary := strings.TrimSpace(s.blockerSummary)
		if summary == "" {
			summary = "commit blockers remain"
		}
		return "Git merge workflow active. " + summary + ". Call git_merge_status after each fix and do not retry the same commit until the blockers are cleared."
	}
	if s.mergeActive {
		return "Git merge workflow active. Call git_merge_status to inspect the current merge state, then keep resolving and validating the merge until commit succeeds."
	}
	return ""
}

func isCommitToolCall(toolName string, args map[string]any) bool {
	if toolName == "git_commit" {
		return true
	}
	if toolName != "run_command" {
		return false
	}
	return isGitCommitLike(strings.ToLower(strings.TrimSpace(stringArg(args, "command"))))
}

func stringArg(args map[string]any, key string) string {
	value, _ := args[key].(string)
	return value
}

func isGitCommitLike(command string) bool {
	return strings.Contains(command, "git commit")
}

func isGitMergeLike(command string) bool {
	return strings.HasPrefix(command, "git merge ") || strings.Contains(command, " git merge ")
}

// isGitUnmergedListCommand matches commands whose output consists exclusively of
// unmerged-file entries, so any non-empty line is a conflict indicator.
func isGitUnmergedListCommand(command string) bool {
	return strings.Contains(command, "git diff --name-only --diff-filter=u") ||
		strings.Contains(command, "git diff --name-only --diff-filter=U") ||
		strings.Contains(command, "git ls-files -u") ||
		strings.Contains(command, "git ls-files --unmerged")
}

// isGitStatusPorcelainCommand matches git status --porcelain, whose output mixes
// all change types; only specific XY codes indicate unmerged files.
func isGitStatusPorcelainCommand(command string) bool {
	return strings.Contains(command, "git status --porcelain")
}

func hasMergeConflict(result string) bool {
	lower := strings.ToLower(result)
	return strings.Contains(lower, "automatic merge failed") || strings.Contains(lower, "conflict (")
}

// hasNonEmptyOutput returns true when result contains at least one non-blank,
// non-exit-code line. Used for commands that only emit unmerged-file paths.
func hasNonEmptyOutput(result string) bool {
	trimmed := strings.TrimSpace(result)
	if trimmed == "" || trimmed == "exit 0" {
		return false
	}
	for line := range strings.SplitSeq(trimmed, "\n") {
		line = strings.TrimSpace(line)
		if line != "" && !strings.HasPrefix(line, "exit ") {
			return true
		}
	}
	return false
}

// hasPorcelainConflicts checks git status --porcelain output for lines whose XY
// status code indicates an unmerged (conflicted) file. Normal staged/unstaged
// changes (e.g. "M  README.md") are not treated as conflicts.
func hasPorcelainConflicts(result string) bool {
	conflictPrefixes := []string{"UU ", "AA ", "DD ", "AU ", "UA ", "DU ", "UD "}
	for line := range strings.SplitSeq(result, "\n") {
		line = strings.TrimSpace(line)
		for _, prefix := range conflictPrefixes {
			if strings.HasPrefix(line, prefix) {
				return true
			}
		}
	}
	return false
}

func isSuccessfulGitCommit(result string) bool {
	lower := strings.ToLower(result)
	return !strings.Contains(lower, "error committing:") &&
		!strings.Contains(lower, "exit 1") &&
		(strings.Contains(lower, "file changed") || strings.Contains(lower, "files changed") ||
			strings.Contains(lower, "nothing to commit") || strings.Contains(lower, "create mode"))
}

func summarizeCommitFailure(result string) string {
	lower := strings.ToLower(result)
	switch {
	case strings.Contains(lower, "yamllint"):
		return "commit blocked by yamllint/pre-commit failures"
	case strings.Contains(lower, "prettier"):
		return "commit blocked by prettier/pre-commit failures"
	case strings.Contains(lower, "hook id:"):
		return "commit blocked by pre-commit hook failures"
	default:
		return "commit blockers remain"
	}
}

func (r *Runner) updateValidationWorkflow(toolName string, args map[string]any, result string) {
	if toolName != "run_command" {
		return
	}
	command := strings.TrimSpace(stringArg(args, "command"))
	if !isValidationCommand(strings.ToLower(command)) {
		return
	}
	passed := isValidationPass(result)
	r.validationWorkflow.ran = true
	r.validationWorkflow.passed = passed
	r.validationWorkflow.cmd = command
	if r.renderer != nil {
		r.renderer.ToolResult("__validation", formatValidationResult(command, passed), "", !passed)
	}
	r.syncRuntimeNote()
}

func isValidationCommand(command string) bool {
	for _, prefix := range []string{
		"go test", "go build", "go vet",
		"npm test", "npm run build",
		"bun test", "bun run build",
		"yarn test", "yarn build",
		"pnpm test", "pnpm build",
		"pytest", "cargo test", "cargo build", "cargo check",
		"golangci-lint",
	} {
		if strings.HasPrefix(command, prefix) {
			return true
		}
	}
	return false
}

func isValidationPass(result string) bool {
	if idx := strings.LastIndex(result, "\nexit "); idx >= 0 {
		code := strings.TrimSpace(result[idx+len("\nexit "):])
		return code == "0"
	}
	lower := strings.ToLower(result)
	return !strings.Contains(lower, "\nfail\t")
}

func formatValidationResult(cmd string, passed bool) string {
	if passed {
		return "validation passed: " + cmd
	}
	return "validation failed: " + cmd
}

func (s validationWorkflowState) overlayContent() string {
	if !s.ran || s.passed {
		return ""
	}
	return "Last validation failed: " + s.cmd + " — fix the reported errors before finishing."
}

func (s sameFileSearchWorkflowState) overlayContent() string {
	if !s.nudged || s.path == "" {
		return ""
	}
	return "Search thrash guidance: you have repeatedly searched the same file without switching to a direct read. Stop trying more patterns on " + s.path + ". Read that file now, inspect the relevant function or block directly, then continue editing."
}

func (r *Runner) updateRepeatToolCallWorkflow(toolName string, args map[string]any, _ string) {
	target := repeatToolCallTarget(toolName, args)
	if target == "" {
		r.repeatWorkflow = repeatToolCallState{}
		r.syncRuntimeNote()
		return
	}
	key := toolName + ":" + target
	lastKey := r.repeatWorkflow.lastToolName + ":" + r.repeatWorkflow.lastTarget
	if key == lastKey {
		r.repeatWorkflow.streak++
	} else {
		r.repeatWorkflow = repeatToolCallState{
			lastToolName: toolName,
			lastTarget:   target,
			streak:       1,
		}
	}
	r.syncRuntimeNote()
}

func repeatToolCallTarget(toolName string, args map[string]any) string {
	switch toolName {
	case "read_file":
		return strings.TrimSpace(stringArg(args, "path"))
	case "list_dir":
		return strings.TrimSpace(stringArg(args, "path"))
	case "code_search":
		return strings.TrimSpace(stringArg(args, "query"))
	case "search":
		return strings.TrimSpace(stringArg(args, "pattern"))
	case "run_command":
		return strings.TrimSpace(stringArg(args, "command"))
	case "glob":
		return strings.TrimSpace(stringArg(args, "pattern"))
	case "edit_file":
		path := strings.TrimSpace(stringArg(args, "path"))
		oldText := strings.TrimSpace(stringArg(args, "old_text"))
		newText := strings.TrimSpace(stringArg(args, "new_text"))
		if path == "" || oldText != newText {
			return ""
		}
		return path + ":" + oldText + "->" + newText
	case "wait_agent":
		return strings.TrimSpace(stringArg(args, "id"))
	default:
		return ""
	}
}

func (s repeatToolCallState) overlayContent(threshold int) string {
	if s.streak < toolThrashThreshold(threshold, repeatToolCallThreshold) {
		return ""
	}
	return fmt.Sprintf("Loop detection: you have called %s on the same target %q %d times in a row without making progress. Stop repeating this action. Either the approach is wrong or you already have the information you need. Switch to a different tool or synthesize your findings now.", s.lastToolName, s.lastTarget, s.streak)
}

func isExplorationToolCall(toolName string, args map[string]any) bool {
	switch toolName {
	case "read_file", "list_dir", "search", "glob", "code_search", "tool_help", "view_image",
		"lsp_definition", "lsp_references", "lsp_hover", "lsp_document_symbols",
		"web_fetch", "web_search", "git_status", "git_diff", "git_log", "git_branch_state", "git_merge_status":
		return true
	case "run_command":
		return isReadOnlyCommand(strings.ToLower(strings.TrimSpace(stringArg(args, "command"))))
	default:
		return false
	}
}

func allowsPlanSynthesis(toolName string) bool {
	switch toolName {
	case "update_plan", "think":
		return true
	default:
		return false
	}
}

func isSynthesisGuardOperation(operation string) bool {
	switch strings.ToLower(strings.TrimSpace(operation)) {
	case "plan", "analysis", "preview", "implement", "inspect", "overview", "review", "validate":
		return true
	default:
		return false
	}
}

func synthesisGuardBudget(mode string) int {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "analysis":
		return analysisExplorationBudget
	case "preview":
		return previewExplorationBudget
	case "implement":
		return implementExplorationBudget
	case "inspect":
		return inspectExplorationBudget
	case "overview":
		return overviewExplorationBudget
	case "review":
		return reviewExplorationBudget
	case "validate":
		return validateExplorationBudget
	case "chat":
		return chatExplorationBudget
	default:
		return planExplorationBudget
	}
}

func isReadOnlyCommand(command string) bool {
	if command == "" {
		return false
	}
	for _, prefix := range []string{
		"rg ", "grep ", "sed ", "cat ", "ls", "git status", "git diff", "git log", "git show", "git branch", "git grep", "go test", "npm test", "pnpm test", "yarn test",
	} {
		if strings.HasPrefix(command, prefix) {
			return true
		}
	}
	return false
}
