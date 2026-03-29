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
	Driver             llm.Driver
	Tools              *agenttools.Registry
	Renderer           agent.RenderTarget
	SystemPrompt       func() string
	NativeSystemPrompt func() string // used when native tool calling is active; no XML tool format
	Session            *Session
	Progress           func(string)
	MaxSessionTurns    int
}

type Runner struct {
	driver             llm.Driver
	tools              *agenttools.Registry
	renderer           agent.RenderTarget
	systemPrompt       func() string
	nativeSystemPrompt func() string
	session            *Session
	progress           func(string)
	maxSessionTurns    int
}

type streamedResponse struct {
	text            string
	streamedVisible bool
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
		driver:             cfg.Driver,
		tools:              reg,
		renderer:           cfg.Renderer,
		systemPrompt:       cfg.SystemPrompt,
		nativeSystemPrompt: cfg.NativeSystemPrompt,
		session:            session,
		progress:           cfg.Progress,
		maxSessionTurns:    maxSessionTurns(cfg.MaxSessionTurns),
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

	// Determine at loop start whether to use provider-native tool calling.
	nativeCaller, isNative := r.driver.(llm.NativeToolCaller)
	var toolDefs []llm.ToolDef
	if isNative && r.tools != nil {
		toolDefs = r.tools.ToLLMToolDefs()
		if len(toolDefs) == 0 {
			isNative = false
		}
	}

	invalidResponses := 0
	for step := 0; step < r.maxSessionTurns; step++ {
		if isNative {
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
			continue
		}

		// XML text parsing path (unchanged)
		streamed, err := r.streamResponse(ctx)
		if err != nil {
			r.session.CompleteTurn(turn, "", nil, err)
			return err
		}
		response := streamed.text
		calls, visibleText := agent.ParseToolCalls(response)
		trimmedVisible := strings.TrimSpace(visibleText)
		if len(calls) == 0 {
			if retry := invalidWorkingResponseNudge(response); retry != "" {
				invalidResponses++
				if invalidResponses >= maxInvalidWorkingResponses() {
					err := fmt.Errorf("react runtime: too many invalid working responses (%d)", invalidResponses)
					r.session.CompleteTurn(turn, "", nil, err)
					return err
				}
				if step+1 < r.maxSessionTurns {
					r.session.AppendRuntimeNote(retry)
					continue
				}
			}
			if trimmedVisible == "" && strings.TrimSpace(response) == "" {
				err := fmt.Errorf("react runtime: empty final response")
				r.session.CompleteTurn(turn, "", nil, err)
				return err
			}
			if retry := invalidWorkingResponseNudge(response); retry != "" {
				err := fmt.Errorf("react runtime: invalid final response: %s", strings.TrimSpace(retry))
				r.session.CompleteTurn(turn, "", nil, err)
				return err
			}
			final := strings.TrimSpace(response)
			if trimmedVisible != "" {
				final = trimmedVisible
			}
			r.session.AppendAssistantMessage(final)
			r.session.CompleteTurn(turn, final, nil, nil)
			if r.renderer != nil && final != "" && !streamed.streamedVisible {
				r.renderer.AgentText(final)
			}
			return nil
		}

		invalidResponses = 0
		results, err := r.executeToolCalls(ctx, calls)
		r.session.AppendToolResults(compactToolResults(results))
		r.session.CompleteTurn(turn, "", calls, err)
		if err != nil {
			return err
		}
	}

	err := fmt.Errorf("react runtime: max steps (%d) exceeded", r.maxSessionTurns)
	r.session.CompleteTurn(turn, "", nil, err)
	return err
}

func (r *Runner) streamResponse(ctx context.Context) (streamedResponse, error) {
	messages := r.session.Messages(r.currentSystemPrompt())
	out := make(chan llm.Token, 64)
	streamCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	errCh := make(chan error, 1)
	go func() {
		errCh <- r.driver.Stream(streamCtx, messages, out)
	}()

	var raw strings.Builder
	visibleEmitted := 0
	streamedVisible := false
	earlyToolResponse := ""
	for tok := range out {
		raw.WriteString(tok.Text)
		current := raw.String()
		if earlyToolResponse == "" && completeToolOnlyResponse(current) {
			earlyToolResponse = current
			cancel()
			continue
		}
		safeVisible := safeVisiblePrefix(current)
		if safeLen := len(safeVisible); r.renderer != nil && safeLen > visibleEmitted {
			r.renderer.AgentToken(safeVisible[visibleEmitted:safeLen])
			visibleEmitted = safeLen
			streamedVisible = true
		}
	}
	if err := <-errCh; err != nil {
		if earlyToolResponse == "" || !errors.Is(err, context.Canceled) {
			return streamedResponse{}, err
		}
	}
	if reporter, ok := r.driver.(llm.RequestModeReporter); ok {
		if mode := strings.TrimSpace(reporter.LastRequestMode()); mode != "" && r.renderer != nil {
			r.renderer.Info("context: " + mode)
		}
	}
	if earlyToolResponse != "" {
		return streamedResponse{text: earlyToolResponse, streamedVisible: streamedVisible}, nil
	}
	return streamedResponse{text: raw.String(), streamedVisible: streamedVisible}, nil
}

func (r *Runner) executeToolCalls(ctx context.Context, calls []agent.ToolCall) ([]string, error) {
	results := make([]string, 0, len(calls))
	for _, call := range calls {
		tool, ok := r.tools.Get(call.Name)
		if !ok {
			msg := fmt.Sprintf("[%s] error: unknown tool %q", call.Name, call.Name)
			if r.renderer != nil {
				r.renderer.Error(strings.TrimPrefix(msg, "["+call.Name+"] "))
			}
			results = append(results, msg)
			continue
		}
		if r.renderer != nil {
			r.renderer.ToolCall(call.Name, reactToolSummary(call))
		}
		result, err := tool.Execute(ctx, call.Args)
		diff := ""
		if tool.LastDiff != nil {
			diff = tool.LastDiff()
		}
		if err != nil {
			errResult := fmt.Sprintf("error: %v", err)
			if r.renderer != nil {
				r.renderer.ToolResult(call.Name, errResult, diff, true)
			}
			results = append(results, fmt.Sprintf("[%s] %s", call.Name, errResult))
			return results, err
		}
		display := truncateToolResult(result)
		if r.renderer != nil {
			r.renderer.ToolResult(call.Name, display, diff, false)
		}
		results = append(results, fmt.Sprintf("[%s] %s", call.Name, result))
	}
	return results, nil
}

// streamNativeTurn runs one native tool calling step.
// Returns nil calls (+ nil error) when a final text answer was received.
// Returns non-nil calls when the model requested tool executions.
func (r *Runner) streamNativeTurn(ctx context.Context, turn int, caller llm.NativeToolCaller, toolDefs []llm.ToolDef) ([]llm.NativeToolCall, error) {
	messages := r.session.Messages(r.nativeCurrentSystemPrompt())
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
			safe := safeVisiblePrefix(current)
			if r.renderer != nil && len(safe) > visibleEmitted {
				r.renderer.AgentToken(safe[visibleEmitted:])
				visibleEmitted = len(safe)
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
			// CompleteTurn is called here (not in runLoop's caller) because runLoop
			// does not call CompleteTurn after executeNativeToolCalls returns an error.
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
			// CompleteTurn is called here (not in runLoop's caller) because runLoop
			// does not call CompleteTurn after executeNativeToolCalls returns an error.
			r.session.CompleteTurn(turn, "", nil, err)
			return err
		}

		display := truncateToolResult(result)
		if r.renderer != nil {
			r.renderer.ToolResult(call.Name, display, diff, false)
		}
		r.session.AppendNativeToolResult(call.ID, result)
	}
	return nil
}

func (r *Runner) currentSystemPrompt() string {
	if r.systemPrompt == nil {
		return ""
	}
	return strings.TrimSpace(r.systemPrompt())
}

func (r *Runner) nativeCurrentSystemPrompt() string {
	if r.nativeSystemPrompt != nil {
		return strings.TrimSpace(r.nativeSystemPrompt())
	}
	return r.currentSystemPrompt()
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

func invalidWorkingResponseNudge(response string) string {
	trimmed := strings.TrimSpace(response)
	switch {
	case trimmed == "":
		return "Runtime note: your previous response was empty. Call a tool if you still need to inspect or act; otherwise provide the final answer."
	case strings.HasPrefix(trimmed, "/"):
		return "Runtime note: do not emit /skill invocations or slash commands. Either call a tool or provide the final answer."
	case containsAnyToolMarkup(trimmed):
		return "Runtime note: malformed tool markup. Return exactly valid <tool_call>...</tool_call> blocks or a final answer."
	default:
		return ""
	}
}

func compactToolResults(results []string) string {
	return strings.TrimSpace(strings.Join(results, "\n"))
}

func completeToolOnlyResponse(text string) bool {
	if !hasCompleteToolCallBlock(text) {
		return false
	}
	calls, visible := agent.ParseToolCalls(text)
	return len(calls) > 0 && strings.TrimSpace(visible) == ""
}

func hasCompleteToolCallBlock(text string) bool {
	for i, opener := range toolCallOpeners() {
		openIdx := strings.Index(text, opener)
		if openIdx < 0 {
			continue
		}
		closeIdx := strings.Index(text[openIdx+len(opener):], toolCallClosers()[i])
		if closeIdx >= 0 {
			return true
		}
	}
	return false
}

func safeVisiblePrefix(text string) string {
	cutoff := len(text)
	if idx := earliestMarkupIndex(text); idx >= 0 && idx < cutoff {
		cutoff = idx
	}
	if idx := trailingMarkupPrefixIndex(text); idx >= 0 && idx < cutoff {
		cutoff = idx
	}
	return text[:cutoff]
}

func earliestMarkupIndex(text string) int {
	best := -1
	for _, tag := range append(toolCallOpeners(), toolCallClosers()...) {
		if idx := strings.Index(text, tag); idx >= 0 && (best == -1 || idx < best) {
			best = idx
		}
	}
	return best
}

func trailingMarkupPrefixIndex(text string) int {
	maxLen := 0
	tags := append(toolCallOpeners(), toolCallClosers()...)
	for _, tag := range tags {
		if len(tag) > maxLen {
			maxLen = len(tag)
		}
	}
	start := len(text) - maxLen + 1
	if start < 0 {
		start = 0
	}
	for i := start; i < len(text); i++ {
		suffix := text[i:]
		if !strings.HasPrefix(suffix, "<") {
			continue
		}
		for _, tag := range tags {
			if suffix != tag && strings.HasPrefix(tag, suffix) {
				return i
			}
		}
	}
	return -1
}

func toolCallOpeners() []string {
	return []string{"<tool_call>", "<function_calls>", "<tool_calls>"}
}

func toolCallClosers() []string {
	return []string{"</tool_call>", "</function_calls>", "</tool_calls>"}
}

func containsAnyToolMarkup(text string) bool {
	for _, tag := range append(toolCallOpeners(), toolCallClosers()...) {
		if strings.Contains(text, tag) {
			return true
		}
	}
	return false
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

func maxInvalidWorkingResponses() int {
	return 3
}
