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
}

type Runner struct {
	driver          llm.Driver
	tools           *agenttools.Registry
	renderer        agent.RenderTarget
	systemPrompt    func() string
	session         *Session
	progress        func(string)
	maxSessionTurns int
	gitWorkflow     gitWorkflowState
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

func NewRunner(cfg Config) *Runner {
	session := cfg.Session
	if session == nil {
		session = NewSession()
	}
	reg := cfg.Tools
	if reg == nil {
		reg = agenttools.NewRegistry()
	}
	return &Runner{
		driver:          cfg.Driver,
		tools:           reg,
		renderer:        cfg.Renderer,
		systemPrompt:    cfg.SystemPrompt,
		session:         session,
		progress:        cfg.Progress,
		maxSessionTurns: maxSessionTurns(cfg.MaxSessionTurns),
	}
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
	r.session.Clear()
}

func (r *Runner) AppendUserMessage(text string) {
	if r == nil || r.session == nil {
		return
	}
	r.session.AppendUserMessage(text)
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

	for step := 0; step < r.maxSessionTurns; step++ {
		calls, err := r.streamNativeTurn(ctx, turn, nativeCaller, toolDefs)
		if err != nil {
			r.session.CompleteTurn(turn, "", nil, err)
			return err
		}
		if calls == nil {
			// streamNativeTurn already recorded the final response
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

	for tok := range out {
		if tok.ToolCall != nil {
			toolCalls = append(toolCalls, *tok.ToolCall)
			continue
		}
		if tok.Text != "" {
			textBuf.WriteString(tok.Text)
			current := textBuf.String()
			if r.renderer != nil && len(current) > visibleEmitted {
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
	r.session.AppendAssistantMessage(finalText)
	r.session.CompleteTurn(turn, finalText, nil, nil)
	if r.renderer != nil && visibleEmitted < len(finalText) {
		r.renderer.AgentText(finalText[visibleEmitted:])
	}
	return nil, nil
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
			continue
		}

		if r.renderer != nil {
			r.renderer.ToolCall(call.Name, reactToolSummary(agent.ToolCall{Args: args}))
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
	}
	return nil
}

func (r *Runner) currentSystemPrompt() string {
	if r.systemPrompt == nil {
		return ""
	}
	return strings.TrimSpace(r.systemPrompt())
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

func reactToolSummary(call agent.ToolCall) string {
	if path, _ := call.Args["path"].(string); strings.TrimSpace(path) != "" {
		return strings.TrimSpace(path)
	}
	if command, _ := call.Args["command"].(string); strings.TrimSpace(command) != "" {
		return strings.TrimSpace(command)
	}
	if query, _ := call.Args["query"].(string); strings.TrimSpace(query) != "" {
		return strings.TrimSpace(query)
	}
	if pattern, _ := call.Args["pattern"].(string); strings.TrimSpace(pattern) != "" {
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
		return 20
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
	case isGitConflictCheckCommand(command):
		if hasUnmergedFiles(result) {
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
	r.session.SetRuntimeNote(r.gitWorkflow.runtimeNote())
}

func (s gitWorkflowState) runtimeNote() string {
	if s.unmergedFiles {
		return "Git merge workflow active. Call git_merge_status to inspect unresolved files and next steps. Resolve each conflicted file, stage the resolutions, and only retry commit once unmerged files are gone."
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

func isGitConflictCheckCommand(command string) bool {
	return strings.Contains(command, "git diff --name-only --diff-filter=u") || strings.Contains(command, "git status --porcelain")
}

func hasMergeConflict(result string) bool {
	lower := strings.ToLower(result)
	return strings.Contains(lower, "automatic merge failed") || strings.Contains(lower, "conflict (")
}

func hasUnmergedFiles(result string) bool {
	trimmed := strings.TrimSpace(result)
	if trimmed == "" || trimmed == "exit 0" {
		return false
	}
	lines := strings.Split(trimmed, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "exit ") {
			continue
		}
		if strings.HasPrefix(line, "UU ") || strings.HasPrefix(line, "AA ") || strings.HasPrefix(line, "DD ") {
			return true
		}
		return true
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
