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
	f, err := os.OpenFile(resolved, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
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
	runtimeMode := string(resolveChatRuntimeMode())
	rec.log.Info("chat.debug.enabled", map[string]any{
		"path":           resolved,
		"model":          setup.ChatModel,
		"work_dir":       setup.WorkDir,
		"surface_mode":   "debug",
		"runtime_mode":   runtimeMode,
		"agents_enabled": false,
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
		"kind": kind,
		"text": text,
	})
}

func (r *chatDebugRecorder) logEvent(ev llm.Event) {
	if r == nil || r.log == nil {
		return
	}
	r.log.Debug("chat.event", map[string]any{
		"kind":      ev.Kind,
		"agent":     ev.Agent,
		"sub_agent": ev.SubAgent,
		"text":      ev.Text,
		"content":   ev.Content,
		"is_error":  ev.IsError,
		"duration":  ev.Duration.String(),
		"usage": map[string]any{
			"input_tokens":  ev.Usage.InputTokens,
			"output_tokens": ev.Usage.OutputTokens,
		},
	})
}

func (r *chatDebugRecorder) wrapDriver(inner llm.Driver) llm.Driver {
	if inner == nil {
		return nil
	}
	return &chatDebugDriver{inner: inner, rec: r}
}

func (d *chatDebugDriver) Name() string { return d.inner.Name() }

func (d *chatDebugDriver) Stream(ctx context.Context, messages []llm.Message, out chan<- llm.Token) error {
	if d.rec != nil {
		fields := map[string]any{
			"driver":   d.inner.Name(),
			"messages": messages,
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
		out <- tok
	}
	err := <-errCh
	if d.rec != nil {
		fields := map[string]any{
			"driver":   d.inner.Name(),
			"response": response.String(),
		}
		if err != nil {
			fields["error"] = err.Error()
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
			"messages":             messages,
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
				"id":        tok.ToolCall.ID,
				"name":      tok.ToolCall.Name,
				"args_json": tok.ToolCall.ArgsJSON,
			})
		}
		out <- tok
	}
	err := <-errCh
	if d.rec != nil {
		fields := map[string]any{
			"driver":   d.inner.Name(),
			"response": response.String(),
		}
		if len(toolCalls) > 0 {
			fields["tool_calls"] = toolCalls
		}
		if err != nil {
			fields["error"] = err.Error()
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
				state["objective"] = strings.TrimSpace(strings.TrimPrefix(line, "Task objective: "))
			case strings.HasPrefix(line, "Task operation: "):
				state["operation"] = strings.TrimSpace(strings.TrimPrefix(line, "Task operation: "))
			case strings.HasPrefix(line, "Task source ref: "):
				state["source_ref"] = strings.TrimSpace(strings.TrimPrefix(line, "Task source ref: "))
			case strings.HasPrefix(line, "Task target branch: "):
				state["target_branch"] = strings.TrimSpace(strings.TrimPrefix(line, "Task target branch: "))
			case strings.HasPrefix(line, "Required verification: "):
				state["required_verification"] = strings.TrimSpace(strings.TrimPrefix(line, "Required verification: "))
			}
		}
	}
	if len(state) == 0 {
		return nil
	}
	return state
}
