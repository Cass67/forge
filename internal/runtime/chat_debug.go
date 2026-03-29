package runtime

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"forge/internal/agent"
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
		d.rec.log.Debug("llm.request", map[string]any{
			"driver":   d.inner.Name(),
			"messages": messages,
		})
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
		responseText := response.String()
		normalized := false
		if isStrictTurnRequest(messages) {
			if normalizedText, changed := agent.NormalizeStrictWorkerTurnForLogging(responseText); changed {
				responseText = normalizedText
				normalized = true
			}
		}
		fields := map[string]any{
			"driver":   d.inner.Name(),
			"response": responseText,
		}
		if normalized {
			fields["response_normalized"] = true
		}
		if err != nil {
			fields["error"] = err.Error()
			d.rec.log.Debug("llm.response", fields)
		} else {
			d.rec.log.Debug("llm.response", fields)
		}
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

func isStrictTurnRequest(messages []llm.Message) bool {
	for _, msg := range messages {
		if msg.Role == llm.RoleSystem && strings.Contains(msg.Content, "Only respond with plain text (no tool calls) when you have a complete final answer.") {
			return true
		}
	}
	return false
}
