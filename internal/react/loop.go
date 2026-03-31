package react

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"forge/internal/agent"
	agenttools "forge/internal/agent/tools"
	"forge/internal/llm"
)

type Config struct {
	Driver          llm.Driver
	Tools           *agenttools.Registry
	Renderer        agent.RenderTarget
	SystemPrompt    func() string
	Session         *Session
	Progress        func(string)
	MaxSessionTurns int
	CompletionCheck func(SessionSnapshot, string) error
	TurnComplete    func(SessionSnapshot)
}

type Runner struct {
	driver             llm.Driver
	tools              *agenttools.Registry
	renderer           agent.RenderTarget
	systemPrompt       func() string
	session            *Session
	progress           func(string)
	maxSessionTurns    int
	gitWorkflow        gitWorkflowState
	planWorkflow       planWorkflowState
	searchWorkflow     sameFileSearchWorkflowState
	validationWorkflow validationWorkflowState
	completionCheck    func(SessionSnapshot, string) error
	turnComplete       func(SessionSnapshot)
}

const interruptedTurnRuntimeNote = "Previous turn was interrupted. Re-check any partially completed tool or command state before continuing."

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

const planExplorationBudget = 10
const analysisExplorationBudget = 15
const sameFileSearchThrashThreshold = 5

func NewRunner(cfg Config) *Runner {
	session := cfg.Session
	if session == nil {
		session = NewSession()
	}
	reg := cfg.Tools
	if reg == nil {
		reg = agenttools.NewRegistry()
	}
	runner := &Runner{
		driver:          cfg.Driver,
		tools:           reg,
		renderer:        cfg.Renderer,
		systemPrompt:    cfg.SystemPrompt,
		session:         session,
		progress:        cfg.Progress,
		maxSessionTurns: maxSessionTurns(cfg.MaxSessionTurns),
		completionCheck: cfg.CompletionCheck,
		turnComplete:    cfg.TurnComplete,
	}
	if snap := session.Snapshot(); snap.TaskState != nil && isSynthesisGuardOperation(snap.TaskState.Operation) {
		runner.planWorkflow.active = true
		runner.planWorkflow.mode = strings.ToLower(strings.TrimSpace(snap.TaskState.Operation))
	}
	runner.syncRuntimeNote()
	return runner
}

func (r *Runner) Run(ctx context.Context, input string) error {
	if r == nil {
		return fmt.Errorf("react runner: runner is nil")
	}
	prompt := BuildPrompt(input)
	if prompt == "" {
		return nil
	}
	turn := r.session.RecordInput(prompt)
	if r.progress != nil {
		r.progress(fmt.Sprintf("react runtime: executing turn %d", turn))
	}
	if CompactSessionHistory(r.session, r.maxSessionTurns) && r.progress != nil {
		r.progress("react runtime: compacted session context")
	}
	if r.driver == nil {
		err := fmt.Errorf("react runner: driver is nil")
		r.session.CompleteTurn(turn, "", nil, err)
		return err
	}
	return r.runLoop(ctx, turn)
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
	r.session.Clear()
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

func (r *Runner) MarkInterrupted() {
	if r == nil || r.session == nil {
		return
	}
	r.session.MarkInterrupted()
	r.session.SetRuntimeNote(strings.TrimSpace(strings.Join([]string{strings.TrimSpace(r.session.Snapshot().RuntimeNote), interruptedTurnRuntimeNote}, "\n\n")))
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
	if !isNative {
		err := fmt.Errorf("react runtime: driver %q does not support native tool calling", r.driver.Name())
		r.session.CompleteTurn(turn, "", nil, err)
		return err
	}

	toolDefs := r.tools.ToLLMToolDefs()

	emptyRetried := false
	completionRetried := false
	budgetWarnAt := r.maxSessionTurns * 2 / 3
	budgetWarnSent := false
	for step := 0; step < r.maxSessionTurns; step++ {
		if !budgetWarnSent && step >= budgetWarnAt {
			budgetWarnSent = true
			remaining := r.maxSessionTurns - step
			r.session.AppendUserMessage(fmt.Sprintf(
				"[budget] %d steps remaining. Stop gathering context. Complete any pending edits, run verification, and deliver your final answer.",
				remaining,
			))
		}
		if r.applyPendingInput() {
			r.syncRuntimeNote()
		}
		calls, err := r.streamNativeTurn(ctx, turn, nativeCaller, toolDefs)
		if err != nil {
			if !emptyRetried && strings.Contains(err.Error(), "empty native response") {
				emptyRetried = true
				r.session.AppendUserMessage("Please provide a response summarizing what you've done and what the result is.")
				continue
			}
			var retryable *RetryableCompletionError
			if errors.As(err, &retryable) && !completionRetried {
				completionRetried = true
				if prompt := strings.TrimSpace(retryable.Prompt); prompt != "" {
					r.session.AppendUserMessage(prompt)
				}
				if r.progress != nil {
					r.progress("react runtime: retrying after non-compliant completion")
				}
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

	err := fmt.Errorf("react runtime: max steps (%d) exceeded", r.maxSessionTurns)
	r.session.CompleteTurn(turn, "", nil, err)
	return err
}

// streamNativeTurn runs one native tool calling step.
// Returns nil calls (+ nil error) when a final text answer was received.
// Returns non-nil calls when the model requested tool executions.
func (r *Runner) streamNativeTurn(ctx context.Context, turn int, caller llm.NativeToolCaller, toolDefs []llm.ToolDef) ([]llm.NativeToolCall, error) {
	messages := r.session.Messages(r.currentSystemPrompt())
	out := make(chan llm.Token, 64)
	streamCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	errCh := make(chan error, 1)
	go func() {
		errCh <- caller.StreamWithTools(streamCtx, messages, toolDefs, out)
	}()

	var textBuf strings.Builder
	var toolCalls []llm.NativeToolCall
	visibleEmitted := 0
	streamVisible := r.completionCheck == nil

	for tok := range out {
		if tok.ToolCall != nil {
			toolCalls = append(toolCalls, *tok.ToolCall)
			continue
		}
		if tok.Text != "" {
			textBuf.WriteString(tok.Text)
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
		r.session.AppendAssistantWithToolCalls(toolCalls)
		return toolCalls, nil
	}

	// Final text answer
	finalText := strings.TrimSpace(textBuf.String())
	if finalText == "" {
		return nil, fmt.Errorf("react runtime: empty native response")
	}
	if r.completionCheck != nil {
		if err := r.completionCheck(r.session.Snapshot(), finalText); err != nil {
			return nil, err
		}
	}
	r.session.AppendAssistantMessage(finalText)
	r.session.CompleteTurn(turn, finalText, nil, nil)
	r.notifyTurnComplete()
	if r.renderer != nil && visibleEmitted < len(finalText) {
		r.renderer.AgentText(finalText[visibleEmitted:])
	}
	return nil, nil
}

func (r *Runner) notifyTurnComplete() {
	if r == nil || r.session == nil || r.turnComplete == nil {
		return
	}
	r.turnComplete(r.session.Snapshot())
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

		if blocked := r.blockedToolResult(call.Name, args); blocked != "" {
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
	}
	return nil
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

func maxSessionTurns(value int) int {
	if value < 1 {
		return 50
	}
	return value
}

func (r *Runner) blockedToolResult(toolName string, args map[string]any) string {
	if !isCommitToolCall(toolName, args) {
		return ""
	}
	switch {
	case r.gitWorkflow.unmergedFiles:
		return "blocked: unmerged git conflicts remain. Resolve conflicted files, stage them, and call git_merge_status before retrying commit."
	case r.gitWorkflow.commitBlocker == commitBlockerRestage:
		if toolName == "run_command" && strings.Contains(strings.ToLower(stringArg(args, "command")), "git add") {
			return ""
		}
		return "blocked: the previous commit attempt modified files via hooks. Re-stage those files and call git_merge_status before retrying commit."
	case r.gitWorkflow.commitBlocker == commitBlockerEdit:
		return "blocked: the previous commit attempt already failed and nothing has changed since then. Fix the reported hook issues and call git_merge_status before retrying commit."
	default:
		return ""
	}
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
	r.syncRuntimeOverlays()
	notes := make([]string, 0, 4)
	if note := strings.TrimSpace(r.modeRuntimeNote()); note != "" {
		notes = append(notes, note)
	}
	if note := strings.TrimSpace(r.planWorkflow.runtimeNote()); note != "" {
		notes = append(notes, note)
	}
	if note := strings.TrimSpace(r.gitWorkflow.runtimeNote()); note != "" {
		notes = append(notes, note)
	}
	if note := strings.TrimSpace(r.searchWorkflow.runtimeNote()); note != "" {
		notes = append(notes, note)
	}
	if note := strings.TrimSpace(r.validationWorkflow.runtimeNote()); note != "" {
		notes = append(notes, note)
	}
	r.session.SetRuntimeNote(strings.Join(notes, "\n\n"))
}

func (r *Runner) syncRuntimeOverlays() {
	if r == nil || r.session == nil {
		return
	}
	snap := r.session.Snapshot()
	if snap.Mode == ModeReview {
		r.session.SetHookOverlay(HookOverlay{
			Key:        "review_guidance",
			Content:    "Review workflow active. Lead with findings before summary, keep findings grounded in repo evidence, and call out regressions, risks, or missing tests explicitly.",
			Priority:   HookPriorityHigh,
			Provenance: "runtime",
		})
	} else {
		r.session.ClearHookOverlay("review_guidance")
	}
	if blocker := currentPlanBlocker(snap.PlanState); blocker != "" && snap.Mode == ModePlan {
		r.session.SetHookOverlay(HookOverlay{
			Key:        "plan_blocker",
			Content:    "Current plan is blocked: " + blocker + ". Resolve the blocker directly if you can, otherwise use ask_user_question to get the missing decision before continuing broad work.",
			Priority:   HookPriorityHigh,
			Provenance: "runtime",
		})
	} else {
		r.session.ClearHookOverlay("plan_blocker")
	}
	if content := strings.TrimSpace(r.planWorkflow.overlayContent()); content != "" {
		r.session.SetHookOverlay(HookOverlay{
			Key:        "synthesis_guidance",
			Content:    content,
			Priority:   HookPriorityHigh,
			Provenance: "runtime",
		})
	} else {
		r.session.ClearHookOverlay("synthesis_guidance")
	}
	if content := strings.TrimSpace(r.validationWorkflow.overlayContent()); content != "" {
		r.session.SetHookOverlay(HookOverlay{
			Key:        "validation_failure",
			Content:    content,
			Priority:   HookPriorityHigh,
			Provenance: "runtime",
		})
	} else {
		r.session.ClearHookOverlay("validation_failure")
	}
	if content := strings.TrimSpace(r.searchWorkflow.overlayContent()); content != "" {
		r.session.SetHookOverlay(HookOverlay{
			Key:        "search_thrash",
			Content:    content,
			Priority:   HookPriorityHigh,
			Provenance: "runtime",
		})
	} else {
		r.session.ClearHookOverlay("search_thrash")
	}
}

func (r *Runner) modeRuntimeNote() string {
	if r == nil || r.session == nil {
		return ""
	}
	return ""
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
		r.syncRuntimeNote()
		return
	}
	if !isExplorationToolCall(toolName, args) {
		// write mutation: the model made progress — reset exploration state
		r.planWorkflow.explorationBatches = 0
		r.planWorkflow.synthesisRequired = false
		r.syncRuntimeNote()
		return
	}
	r.planWorkflow.explorationBatches++
	if r.planWorkflow.explorationBatches >= synthesisGuardBudget(r.planWorkflow.mode) {
		r.planWorkflow.synthesisRequired = true
	}
	r.syncRuntimeNote()
}

func (s planWorkflowState) overlayContent() string {
	if !s.active || !s.synthesisRequired {
		return ""
	}
	switch s.mode {
	case "analysis":
		return "Analysis guidance: you have enough evidence to answer. Avoid exhaustive repo-wide searches, stop exploring and summarize findings or recommendations now. Put any uncertainty into open questions instead of doing more low-yield research."
	default:
		return "Planning task guidance: you have enough evidence to write the plan. Avoid exhaustive repo-wide searches, stop exploring and synthesize the next actionable plan now. Use update_plan to capture the steps, and put any uncertainty into open questions instead of doing more broad research."
	}
}

func (s planWorkflowState) runtimeNote() string {
	return ""
}

func (s gitWorkflowState) runtimeNote() string {
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
	for _, line := range strings.Split(trimmed, "\n") {
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
	for _, line := range strings.Split(result, "\n") {
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
		(strings.Contains(lower, "files changed") || strings.Contains(lower, "nothing to commit") || strings.Contains(lower, "create mode"))
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
		"go test", "go build",
		"npm test", "npm run build",
		"bun test", "bun run build",
		"yarn test",
		"pnpm test",
		"pytest", "cargo test", "cargo build",
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

func (s validationWorkflowState) runtimeNote() string {
	return ""
}

func (s sameFileSearchWorkflowState) overlayContent() string {
	if !s.nudged || s.path == "" {
		return ""
	}
	return "Search thrash guidance: you have repeatedly searched the same file without switching to a direct read. Stop trying more patterns on " + s.path + ". Read that file now, inspect the relevant function or block directly, then continue editing."
}

func (s sameFileSearchWorkflowState) runtimeNote() string {
	return ""
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
	case "plan", "analysis":
		return true
	default:
		return false
	}
}

func synthesisGuardBudget(mode string) int {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "analysis":
		return analysisExplorationBudget
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
