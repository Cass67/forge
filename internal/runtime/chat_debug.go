package runtime

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"forge/internal/llm"
	"forge/internal/logger"
	reactruntime "forge/internal/react"
	resilienceerrors "forge/internal/resilience/errors"
	"forge/internal/secscan"
	"forge/internal/version"
)

type chatDebugRecorder struct {
	log *logger.Logger
}

type chatDebugDriver struct {
	inner llm.Driver
	rec   *chatDebugRecorder
}

func EnableChatDebug(setup *ChatSetup, path string) (string, error) {
	if setup == nil {
		return "", fmt.Errorf("chat setup is nil")
	}
	resolved := strings.TrimSpace(path)
	if resolved == "" {
		resolved = defaultChatDebugPath(setup.WorkDir)
	}
	if !filepath.IsAbs(resolved) {
		resolved = filepath.Join(setup.WorkDir, resolved)
	}
	if err := os.MkdirAll(filepath.Dir(resolved), 0o755); err != nil {
		return "", err
	}
	f, err := os.OpenFile(resolved, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return "", err
	}
	if err := f.Close(); err != nil {
		return "", err
	}
	log, err := logger.NewFileLogger(resolved, logger.LevelDebug)
	if err != nil {
		return "", err
	}
	rec := &chatDebugRecorder{log: log}
	rec.log.Info("chat.debug.enabled", map[string]any{
		"path":           resolved,
		"model":          setup.ChatModel,
		"work_dir":       setup.WorkDir,
		"surface_mode":   "debug",
		"runtime_mode":   "react",
		"agents_enabled": false,
		"commit":         version.Commit,
	})
	if setup.Driver != nil {
		setup.Driver = rec.wrapDriver(setup.Driver)
	}
	if setup.MakeDriver != nil {
		orig := setup.MakeDriver
		setup.MakeDriver = func(model string) llm.Driver {
			d := orig(model)
			if d == nil {
				return nil
			}
			return rec.wrapDriver(d)
		}
	}
	setup.DebugLog = resolved
	setup.debugRec = rec
	return resolved, nil
}

func defaultChatDebugPath(workDir string) string {
	base := os.TempDir()
	if strings.TrimSpace(base) == "" {
		base = "."
	}
	return filepath.Join(base, fmt.Sprintf("forge-chat-debug-%s.jsonl", time.Now().Format("20060102-150405")))
}

func (r *chatDebugRecorder) logInput(kind, text string) {
	if r == nil || r.log == nil {
		return
	}
	r.log.Debug("chat.input", map[string]any{
		"kind":  kind,
		"chars": len(text),
		"lines": lineCount(text),
	})
}

func (r *chatDebugRecorder) logEvent(ev llm.Event) {
	if r == nil || r.log == nil {
		return
	}
	fields := map[string]any{
		"kind":      redactDebugString(string(ev.Kind)),
		"agent":     redactDebugString(ev.Agent),
		"sub_agent": redactDebugString(ev.SubAgent),
		"is_error":  ev.IsError,
	}
	if strings.TrimSpace(ev.Text) != "" {
		addDebugTextMetadata(fields, "text", ev.Text)
	}
	if strings.TrimSpace(ev.Content) != "" {
		addDebugTextMetadata(fields, "content", ev.Content)
	}
	if ev.Duration > 0 {
		fields["duration"] = ev.Duration.String()
	}
	if ev.Usage.InputTokens > 0 || ev.Usage.OutputTokens > 0 {
		fields["usage"] = map[string]any{
			"input_tokens":  ev.Usage.InputTokens,
			"output_tokens": ev.Usage.OutputTokens,
		}
	}
	r.log.Debug("chat.event", fields)
}

func (r *chatDebugRecorder) logRetryEvent(ev llm.RetryEvent) {
	if r == nil || r.log == nil {
		return
	}
	fields := map[string]any{
		"driver":              redactDebugString(ev.Driver),
		"operation":           redactDebugString(ev.Operation),
		"max_attempts":        ev.MaxAttempts,
		"timeout":             ev.Timeout.String(),
		"stream_idle_timeout": ev.StreamIdleTimeout.String(),
	}
	if ev.Attempt > 0 {
		fields["attempt"] = ev.Attempt
	}
	if ev.NextAttempt > 0 {
		fields["next_attempt"] = ev.NextAttempt
	}
	if ev.Kind == llm.RetryEventWait {
		fields["wait_duration"] = ev.Wait.String()
		fields["emitted_any"] = ev.EmittedAny
		if ev.Err != nil {
			classified := resilienceerrors.ClassifyError(ev.Err)
			fields["previous_error_class"] = classified.Class.String()
			fields["previous_error_type"] = classified.Type
			fields["previous_error_retryable"] = classified.Retryable
			fields["previous_error_chars"] = len(ev.Err.Error())
			if rules := debugSecretRules(ev.Err.Error()); rules != "" {
				fields["previous_error_secret_rules"] = rules
			}
		}
	}
	r.log.Debug("llm.retry_"+string(ev.Kind), fields)
}

func (r *chatDebugRecorder) logAgentTask(state reactruntime.AgentTaskState) {
	if r == nil || r.log == nil {
		return
	}
	fields := map[string]any{
		"id":                    redactDebugString(state.ID),
		"role":                  redactDebugString(state.Role),
		"status":                string(state.Status),
		"parent_turn":           state.ParentTurn,
		"recent_activity_count": len(state.RecentActivity),
	}
	addDebugTextMetadata(fields, "description", state.Description)
	addDebugTextMetadata(fields, "prompt", state.Prompt)
	addDebugTextMetadata(fields, "result", state.Result)
	addDebugTextMetadata(fields, "error", state.Error)
	if !state.CreatedAt.IsZero() {
		fields["created_at"] = state.CreatedAt.UTC().Format(time.RFC3339Nano)
	}
	if !state.StartedAt.IsZero() {
		fields["started_at"] = state.StartedAt.UTC().Format(time.RFC3339Nano)
	}
	if !state.CompletedAt.IsZero() {
		fields["completed_at"] = state.CompletedAt.UTC().Format(time.RFC3339Nano)
	}
	if !state.LastActivityAt.IsZero() {
		fields["last_activity_at"] = state.LastActivityAt.UTC().Format(time.RFC3339Nano)
	}
	if strings.TrimSpace(state.LastToolName) != "" {
		fields["last_tool_name"] = redactDebugString(state.LastToolName)
	}
	for _, activity := range state.RecentActivity {
		if strings.TrimSpace(activity.Summary) == "" {
			continue
		}
		if rules := debugSecretRules(activity.Summary); rules != "" {
			fields["recent_activity_secret_rules"] = rules
			break
		}
	}
	r.log.Debug("chat.agent_lifecycle", fields)
}

func (r *chatDebugRecorder) logToolExposure(decision reactruntime.ToolExposureDecision) {
	if r == nil || r.log == nil {
		return
	}
	toolNames := make([]string, 0, len(decision.ToolNames))
	for _, name := range decision.ToolNames {
		if trimmed := strings.TrimSpace(redactDebugString(name)); trimmed != "" {
			toolNames = append(toolNames, trimmed)
		}
	}
	fields := map[string]any{
		"reason":               redactDebugString(decision.Reason),
		"tool_count":           len(toolNames),
		"tool_names":           toolNames,
		"tool_choice_required": decision.RequireToolCall,
	}
	if decision.OutstandingAgentCount > 0 {
		fields["outstanding_agent_count"] = decision.OutstandingAgentCount
	}
	r.log.Debug("chat.tool_exposure", fields)
}

func (r *chatDebugRecorder) wrapDriver(inner llm.Driver) llm.Driver {
	if inner == nil {
		return nil
	}
	if setter, ok := inner.(interface{ SetRetryObserver(llm.RetryObserver) }); ok {
		setter.SetRetryObserver(r.logRetryEvent)
	}
	return &chatDebugDriver{inner: inner, rec: r}
}

func debugToolExposureObserver(setup *ChatSetup) func(reactruntime.ToolExposureDecision) {
	if setup == nil || setup.debugRec == nil {
		return nil
	}
	return setup.debugRec.logToolExposure
}

func (d *chatDebugDriver) Name() string { return d.inner.Name() }

func (d *chatDebugDriver) Stream(ctx context.Context, messages []llm.Message, out chan<- llm.Token) error {
	if d.rec != nil {
		fields := map[string]any{
			"driver":           d.inner.Name(),
			"message_count":    len(messages),
			"messages_summary": summarizeDebugMessages(messages),
		}
		if taskState := taskStateFromMessages(messages); len(taskState) > 0 {
			fields["task_state"] = taskState
		}
		d.rec.log.Debug("llm.request", fields)
	}

	internal := make(chan llm.Token, 64)
	errCh := make(chan error, 1)
	go func() {
		errCh <- d.inner.Stream(ctx, messages, internal)
	}()

	var response strings.Builder
	for tok := range internal {
		response.WriteString(tok.Text)
		select {
		case out <- tok:
		case <-ctx.Done():
			for range internal {
			}
			<-errCh
			close(out)
			return ctx.Err()
		}
	}
	err := <-errCh
	if d.rec != nil {
		fields := map[string]any{
			"driver":         d.inner.Name(),
			"response_chars": response.Len(),
			"response_lines": lineCount(response.String()),
		}
		if err != nil {
			fields["error"] = redactDebugString(err.Error())
			if rules := debugSecretRules(err.Error()); rules != "" {
				fields["error_secret_rules"] = rules
			}
		}
		d.rec.log.Debug("llm.response", fields)
	}
	close(out)
	return err
}

func (d *chatDebugDriver) StreamWithTools(ctx context.Context, messages []llm.Message, tools []llm.ToolDef, out chan<- llm.Token) error {
	return d.StreamWithToolsOptions(ctx, messages, tools, llm.NativeToolOptions{}, out)
}

func (d *chatDebugDriver) StreamWithToolsOptions(ctx context.Context, messages []llm.Message, tools []llm.ToolDef, opts llm.NativeToolOptions, out chan<- llm.Token) error {
	inner, ok := d.inner.(llm.NativeToolCaller)
	if !ok {
		close(out)
		return fmt.Errorf("chatDebugDriver: inner driver %q does not support native tool calling", d.inner.Name())
	}
	var advanced llm.NativeToolCallerWithOptions
	if caller, ok := d.inner.(llm.NativeToolCallerWithOptions); ok {
		advanced = caller
	}
	if d.rec != nil {
		toolNames := make([]string, 0, len(tools))
		for _, tool := range tools {
			toolNames = append(toolNames, tool.Name)
		}
		fields := map[string]any{
			"driver":               d.inner.Name(),
			"message_count":        len(messages),
			"messages_summary":     summarizeDebugMessages(messages),
			"tool_count":           len(tools),
			"tool_names":           toolNames,
			"tool_choice_required": opts.RequireToolCall,
		}
		if taskState := taskStateFromMessages(messages); len(taskState) > 0 {
			fields["task_state"] = taskState
		}
		d.rec.log.Debug("llm.request", fields)
	}

	internal := make(chan llm.Token, 64)
	errCh := make(chan error, 1)
	go func() {
		if advanced != nil {
			errCh <- advanced.StreamWithToolsOptions(ctx, messages, tools, opts, internal)
			return
		}
		errCh <- inner.StreamWithTools(ctx, messages, tools, internal)
	}()

	var response strings.Builder
	toolCalls := make([]map[string]string, 0)
	for tok := range internal {
		response.WriteString(tok.Text)
		if tok.ToolCall != nil {
			toolCalls = append(toolCalls, map[string]string{
				"id":         tok.ToolCall.ID,
				"name":       tok.ToolCall.Name,
				"args_chars": fmt.Sprintf("%d", len(tok.ToolCall.ArgsJSON)),
			})
		}
		select {
		case out <- tok:
		case <-ctx.Done():
			for range internal {
			}
			<-errCh
			close(out)
			return ctx.Err()
		}
	}
	err := <-errCh
	if d.rec != nil {
		fields := map[string]any{
			"driver":         d.inner.Name(),
			"response_chars": response.Len(),
			"response_lines": lineCount(response.String()),
		}
		if len(toolCalls) > 0 {
			fields["tool_calls"] = toolCalls
		}
		if err != nil {
			fields["error"] = redactDebugString(err.Error())
			if rules := debugSecretRules(err.Error()); rules != "" {
				fields["error_secret_rules"] = rules
			}
		}
		d.rec.log.Debug("llm.response", fields)
	}
	close(out)
	return err
}

func (d *chatDebugDriver) SetParams(p llm.Params) {
	if c, ok := d.inner.(llm.Configurable); ok {
		c.SetParams(p)
	}
}

func (d *chatDebugDriver) LastUsage() llm.Usage {
	if reporter, ok := d.inner.(llm.UsageReporter); ok {
		return reporter.LastUsage()
	}
	return llm.Usage{}
}

func (d *chatDebugDriver) LastRequestMode() string {
	if reporter, ok := d.inner.(llm.RequestModeReporter); ok {
		return reporter.LastRequestMode()
	}
	return ""
}

func (d *chatDebugDriver) ResetConversation() {
	if resetter, ok := d.inner.(llm.ConversationResetter); ok {
		resetter.ResetConversation()
	}
}

func taskStateFromMessages(messages []llm.Message) map[string]any {
	state := map[string]any{}
	for _, msg := range messages {
		if msg.Role != llm.RoleSystem {
			continue
		}
		for _, line := range strings.Split(msg.Content, "\n") {
			line = strings.TrimSpace(line)
			switch {
			case strings.HasPrefix(line, "Task objective: "):
				state["objective"] = redactDebugString(strings.TrimSpace(strings.TrimPrefix(line, "Task objective: ")))
			case strings.HasPrefix(line, "Task operation: "):
				state["operation"] = redactDebugString(strings.TrimSpace(strings.TrimPrefix(line, "Task operation: ")))
			case strings.HasPrefix(line, "Task source ref: "):
				state["source_ref"] = redactDebugString(strings.TrimSpace(strings.TrimPrefix(line, "Task source ref: ")))
			case strings.HasPrefix(line, "Task target branch: "):
				state["target_branch"] = redactDebugString(strings.TrimSpace(strings.TrimPrefix(line, "Task target branch: ")))
			case strings.HasPrefix(line, "Required verification: "):
				state["required_verification"] = redactDebugString(strings.TrimSpace(strings.TrimPrefix(line, "Required verification: ")))
			}
		}
	}
	if len(state) == 0 {
		return nil
	}
	return state
}

func summarizeDebugMessages(messages []llm.Message) []map[string]any {
	summary := make([]map[string]any, 0, len(messages))
	for _, msg := range messages {
		entry := map[string]any{
			"role": string(msg.Role),
		}
		if strings.TrimSpace(msg.Content) != "" {
			entry["chars"] = len(msg.Content)
			entry["lines"] = lineCount(msg.Content)
		}
		if len(msg.ToolCalls) > 0 {
			entry["tool_calls"] = summarizeDebugToolCalls(msg.ToolCalls)
		}
		if strings.TrimSpace(msg.ToolCallID) != "" {
			entry["tool_call_id"] = msg.ToolCallID
		}
		summary = append(summary, entry)
	}
	return summary
}

func summarizeDebugToolCalls(calls []llm.NativeToolCall) []map[string]any {
	summary := make([]map[string]any, 0, len(calls))
	for _, call := range calls {
		entry := map[string]any{
			"id":   redactDebugString(call.ID),
			"name": redactDebugString(call.Name),
		}
		if strings.TrimSpace(call.ArgsJSON) != "" {
			entry["args_chars"] = len(call.ArgsJSON)
			if rules := debugSecretRules(call.ArgsJSON); rules != "" {
				entry["args_secret_rules"] = rules
			}
		}
		summary = append(summary, entry)
	}
	return summary
}

func addDebugTextMetadata(fields map[string]any, prefix, text string) {
	if fields == nil || strings.TrimSpace(prefix) == "" || strings.TrimSpace(text) == "" {
		return
	}
	fields[prefix+"_chars"] = len(text)
	fields[prefix+"_lines"] = lineCount(text)
	if rules := debugSecretRules(text); rules != "" {
		fields[prefix+"_secret_rules"] = rules
	}
}

func redactDebugString(text string) string {
	if text == "" {
		return ""
	}
	scanner := secscan.NewDefaultScanner()
	return secscan.Redact(text, scanner.Scan(text))
}

func debugSecretRules(text string) string {
	if text == "" {
		return ""
	}
	scanner := secscan.NewDefaultScanner()
	return secscan.Summary(scanner.Scan(text))
}

func lineCount(text string) int {
	if text == "" {
		return 0
	}
	return strings.Count(text, "\n") + 1
}
