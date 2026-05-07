package react

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"strings"
	"time"

	"forge/internal/agent"
	agenttools "forge/internal/agent/tools"
	"forge/internal/hooks"
	"forge/internal/llm"
)

type Config struct {
	Driver                llm.Driver
	Tools                 *agenttools.Registry
	Renderer              agent.RenderTarget
	SystemPrompt          func() string
	Session               *Session
	Progress              func(string)
	TurnComplete          func(SessionSnapshot)
	ConfigureHooks        func(*hooks.Registry)
	CompactionMaxFailures int
	Interactive           bool
	MaxSteps              int
}

type Runner struct {
	driver                llm.Driver
	tools                 *agenttools.Registry
	renderer              agent.RenderTarget
	systemPrompt          func() string
	session               *Session
	hooks                 *hooks.Registry
	progress              func(string)
	compactionManager     *CompactionManager
	compactionFailures    int
	compactionMaxFailures int
	maxSteps              int
	gitWorkflow           gitWorkflowState
	planWorkflow          planWorkflowState
	searchWorkflow        sameFileSearchWorkflowState
	validationWorkflow    validationWorkflowState
	repeatWorkflow        repeatToolCallState
	pendingRetryPrompt    string
	turnComplete          func(SessionSnapshot)
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

const sameFileSearchThrashThreshold = 5
const repeatToolCallThreshold = 6
const maxCompletionRetriesPerTurn = 3
const retryNoticeText = "Revising answer..."

var loopHookOverlayKeys = map[string]struct{}{
	"review_guidance":    {},
	"preview_workflow":   {},
	"plan_blocker":       {},
	"synthesis_guidance": {},
	"validation_failure": {},
	"search_thrash":      {},
	"git_workflow":       {},
	"repeat_loop":        {},
}

type promptHookPayload struct {
	Mode               Mode
	PlanState          *PlanState
	PlanWorkflow       planWorkflowState
	ValidationWorkflow validationWorkflowState
	SearchWorkflow     sameFileSearchWorkflowState
	GitWorkflow        gitWorkflowState
	RepeatWorkflow     repeatToolCallState
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
		compactionMaxFailures: cfg.CompactionMaxFailures,
		turnComplete:          cfg.TurnComplete,
		maxSteps:              maxLoopSteps(cfg.MaxSteps),
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
	turn := r.session.RecordInputWithParts(prompt, parts)
	r.pendingRetryPrompt = ""
	r.syncRuntimeNote()
	r.applyCompactionDecision(ctx, r.compactionManager.Decide(r.session.Snapshot()))
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
	if !changed && r.compactionMaxFailures > 0 {
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

func (r *Runner) runLoop(ctx context.Context, turn int) error {
	start := time.Now()
	defer r.emitStats(start)

	nativeCaller, isNative := r.driver.(llm.NativeToolCaller)

	completionRetries := 0
	for range r.maxSteps {
		if r.applyPendingInput() {
			r.syncRuntimeNote()
		}
		snap := r.session.Snapshot()
		toolDefs := r.selectToolDefs(snap)
		requireToolCall := shouldRequireToolCallForSnapshot(snap)
		if len(toolDefs) > 0 && !isNative {
			err := fmt.Errorf("react runtime: driver %q does not support native tool calling", r.driver.Name())
			r.session.CompleteTurn(turn, "", nil, err)
			return err
		}
		calls, err := r.streamNativeTurn(ctx, turn, nativeCaller, toolDefs, requireToolCall)
		if err != nil {
			var retryable *RetryableCompletionError
			if errors.As(err, &retryable) && completionRetries < maxCompletionRetriesPerTurn {
				completionRetries++
				if prompt := strings.TrimSpace(retryable.Prompt); prompt != "" {
					r.pendingRetryPrompt = prompt
				}
				r.emitRetryNotice(retryNoticeText)
				continue
			}
			r.session.CompleteTurn(turn, "", nil, err)
			return err
		}
		if calls == nil {
			// streamNativeTurn already recorded the final response
			if r.applyPendingInput() {
				continue
			}
			return nil
		}
		if err := r.executeNativeToolCalls(ctx, turn, calls); err != nil {
			return err
		}
	}

	err := fmt.Errorf("react runtime: safety step limit (%d) exceeded", r.maxSteps)
	r.session.CompleteTurn(turn, "", nil, err)
	return err
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
		reasoning := strings.TrimSpace(reasoningBuf.String())
		r.session.AppendAssistantToolTurn(preamble, toolCalls)
		if reasoning != "" {
			r.session.SetLastAssistantReasoning(reasoning)
		}
		if streamVisible && r.renderer != nil && hasTools && preamble != "" {
			r.renderer.AgentText(preamble)
		} else if preamble != "" && r.renderer != nil && visibleEmitted < len(preamble) {
			r.renderer.AgentText(preamble[visibleEmitted:])
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
// to the session. On unknown tool or execution error the call is recorded as a failed result and the loop aborts.
func (r *Runner) executeNativeToolCalls(ctx context.Context, turn int, calls []llm.NativeToolCall) error {
	for _, call := range calls {
		tool, ok := r.tools.Get(call.Name)
		if !ok {
			errMsg := fmt.Sprintf("error: unknown tool %q", call.Name)
			if r.renderer != nil {
				r.renderer.Error(fmt.Sprintf("unknown tool %q", call.Name))
			}
			r.session.AppendNativeToolResult(call.ID, errMsg)
			r.session.CompleteTurn(turn, "", nil, errors.New(errMsg))
			return errors.New(errMsg)
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

		if r.renderer != nil {
			r.renderer.ToolCall(call.Name, reactToolSummary(args))
		}

		result, err := tool.Execute(ctx, args)
		diff := ""
		if tool.LastDiff != nil {
			diff = tool.LastDiff()
		}
		if err != nil {
			errResult := fmt.Sprintf("error: %v", err)
			r.applyHookOutput(beforeTool)
			r.applyHookOutput(r.afterToolHookOutput(ctx, call.Name, args, true, errResult))
			if r.renderer != nil {
				r.renderer.ToolResult(call.Name, errResult, diff, true)
			}
			r.session.AppendNativeToolResult(call.ID, errResult)
			r.updateGitWorkflow(call.Name, args, errResult)
			r.session.CompleteTurn(turn, "", nil, err)
			return err
		}

		display := truncateToolResult(result)
		if r.renderer != nil {
			r.renderer.ToolResult(call.Name, display, diff, false)
		}
		r.session.AppendNativeToolResult(call.ID, result)
		r.updateGitWorkflow(call.Name, args, result)
		r.updatePlanWorkflow(call.Name, args, result, false)
		r.updateSameFileSearchWorkflow(call.Name, args, false)
		r.updateValidationWorkflow(call.Name, args, result)
		r.updateRepeatToolCallWorkflow(call.Name, args, result)
		r.applyHookOutput(beforeTool)
		r.applyHookOutput(r.afterToolHookOutput(ctx, call.Name, args, false, ""))
	}
	return nil
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
		r.session.AppendUserMessage(text)
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
		return strings.TrimSpace(path)
	}
	if command, _ := args["command"].(string); strings.TrimSpace(command) != "" {
		return strings.TrimSpace(command)
	}
	if query, _ := args["query"].(string); strings.TrimSpace(query) != "" {
		return strings.TrimSpace(query)
	}
	if task, _ := args["task_description"].(string); strings.TrimSpace(task) != "" {
		return strings.TrimSpace(task)
	}
	if role, _ := args["role"].(string); strings.TrimSpace(role) != "" {
		return strings.TrimSpace(role)
	}
	if id, _ := args["id"].(string); strings.TrimSpace(id) != "" {
		return strings.TrimSpace(id)
	}
	if pattern, _ := args["pattern"].(string); strings.TrimSpace(pattern) != "" {
		return strings.TrimSpace(pattern)
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
	gitReadToolNames  = []string{"git_status", "git_diff", "git_log", "git_branch_state", "git_merge_status"}
	previewToolNames  = []string{"artifact_write", "artifact_read", "preview_server_ensure", "preview_server_status"}
	planningToolNames = []string{"think", "update_plan", "enter_plan_mode", "exit_plan_mode", "ask_user_question"}
	webToolNames      = []string{"web_fetch", "web_search"}
	delegateToolNames = []string{"spawn_agent", "wait_agent"}
)

func (r *Runner) selectToolDefs(snapshot SessionSnapshot) []llm.ToolDef {
	if r == nil || r.tools == nil {
		return nil
	}
	delegationComplete := false
	if shouldRouteParentThroughDelegation(snapshot) {
		delegationComplete = historyIncludesCompletedToolCall(snapshot, "wait_agent")
		if delegationComplete && !inputSuggestsPostDelegationAction(normalizeToolIntentText(snapshot.LastInput)) {
			return nil
		}
		if delegationComplete {
			goto selectParentTools
		}
		return r.tools.Filter(delegateToolNames).ToLLMToolDefs()
	}
selectParentTools:
	allowed := allowedToolNamesForSnapshot(snapshot)
	if delegationComplete {
		allowed = withoutToolNames(allowed, delegateToolNames...)
	}
	pluginNames := r.pluginToolNames()
	pluginIntent := inputSuggestsPluginTool(snapshot.LastInput, pluginNames)
	if len(allowed) == 0 && !pluginIntent {
		allowed = append(allowed, delegateToolNames...)
	}
	if pluginIntent {
		allowed = append(allowed, pluginNames...)
	}
	if !delegationComplete {
		allowed = append(allowed, delegateToolNames...)
	}
	return r.tools.Filter(allowed).ToLLMToolDefs()
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

func shouldRequireToolCallForSnapshot(snapshot SessionSnapshot) bool {
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
		return false
	}
	return inputMentionsPathLikeText(text) || containsToolPhrase(text,
		" file", " files", "markdown", ".md", "to a file", "into a file",
		"readme", "config", "artifact", "html", "script",
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
	return containsToolPhrase(text,
		"sub-agent", "sub agent", "delegate", "parallel agent", "parallel agents", "spawn agent",
		"ask agent", "ask agents", "use agent", "use agents", "agent to", "agents to",
		"multiple agents", "three agents", "3 agents", "omo agent", "omo agents", "openagent", "oh my openagent",
		"audit this repo", "audit the repo", "audit repository", "audit this codebase", "audit the codebase",
		"compare this repo", "compare the repo", "fall down compared to",
	)
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
			Mode:               snap.Mode,
			PlanState:          snap.PlanState,
			PlanWorkflow:       r.planWorkflow,
			ValidationWorkflow: r.validationWorkflow,
			SearchWorkflow:     r.searchWorkflow,
			GitWorkflow:        r.gitWorkflow,
			RepeatWorkflow:     r.repeatWorkflow,
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
	registry.Register(hooks.PointPromptContext, "synthesis_guidance", synthesisPromptHook)
	registry.Register(hooks.PointPromptContext, "validation_failure", validationPromptHook)
	registry.Register(hooks.PointPromptContext, "search_thrash", searchThrashPromptHook)
	registry.Register(hooks.PointPromptContext, "git_workflow", gitWorkflowPromptHook)
	registry.Register(hooks.PointPromptContext, "repeat_loop", repeatLoopPromptHook)
	registry.Register(hooks.PointBeforeTool, "search_pattern_guard", beforeToolSearchPatternGuardHook)
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
	content := strings.TrimSpace(payload.RepeatWorkflow.overlayContent())
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

func beforeToolSearchPatternGuardHook(_ context.Context, event hooks.Event) []hooks.Result {
	payload, ok := event.Transient.(beforeToolHookPayload)
	if !ok || payload.ToolName != "search" {
		return nil
	}
	pattern := strings.TrimSpace(stringArg(payload.Args, "pattern"))
	if !looksLikeShotgunAlternationSearch(pattern) {
		return nil
	}
	return []hooks.Result{hooks.BlockResult{
		Message:    "blocked: avoid shotgun alternation regex searches like foo|bar|baz. Search one likely term at a time, use code_search for a literal identifier, or list_dir/read_file on the likely path instead.",
		Provenance: "runtime",
	}}
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
		r.searchWorkflow.nudged = r.searchWorkflow.streak >= sameFileSearchThrashThreshold
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
	default:
		return ""
	}
}

func (s repeatToolCallState) overlayContent() string {
	if s.streak < repeatToolCallThreshold {
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

func looksLikeShotgunAlternationSearch(pattern string) bool {
	pattern = strings.TrimSpace(pattern)
	if len(pattern) < 24 || strings.Count(pattern, "|") < 2 {
		return false
	}
	parts := strings.Split(pattern, "|")
	nonEmpty := 0
	for _, part := range parts {
		if strings.TrimSpace(part) != "" {
			nonEmpty++
		}
	}
	return nonEmpty >= 3
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
