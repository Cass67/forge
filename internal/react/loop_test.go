package react

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	agenttools "forge/internal/agent/tools"
	"forge/internal/llm"
)

func TestRunnerRunInvokesDriverAndProgress(t *testing.T) {
	driver := &scriptedDriver{responses: []string{"repo overview"}}
	session := NewSession()
	var progress string
	r := NewRunner(Config{
		Driver:  driver,
		Session: session,
		Progress: func(text string) {
			progress = text
		},
	})

	if err := r.Run(context.Background(), "  inspect this file  "); err != nil {
		t.Fatal(err)
	}
	if driver.callCount != 1 {
		t.Fatalf("calls = %d, want 1", driver.callCount)
	}
	if progress == "" {
		t.Fatal("expected progress callback")
	}
	snap := session.Snapshot()
	if snap.Turn != 1 {
		t.Fatalf("turn = %d, want 1", snap.Turn)
	}
}

func TestRunnerRunReturnsErrorWhenDriverMissing(t *testing.T) {
	r := NewRunner(Config{})
	if err := r.Run(context.Background(), "inspect"); err == nil {
		t.Fatal("expected error when driver is nil")
	}
}

func TestRunnerRunSkipsEmptyInput(t *testing.T) {
	driver := &scriptedDriver{responses: []string{"ignored"}}
	r := NewRunner(Config{Driver: driver})
	if err := r.Run(context.Background(), "   "); err != nil {
		t.Fatal(err)
	}
	if driver.callCount != 0 {
		t.Fatalf("calls = %d, want 0", driver.callCount)
	}
}

func TestRunnerRunRecordsCompletedTurnDetails(t *testing.T) {
	driver := &scriptedDriver{responses: []string{"repo overview"}}
	session := NewSession()
	r := NewRunner(Config{Driver: driver, Session: session})

	if err := r.Run(context.Background(), "inspect repo"); err != nil {
		t.Fatal(err)
	}

	snap := session.Snapshot()
	if len(snap.Turns) != 1 {
		t.Fatalf("turns = %d, want 1", len(snap.Turns))
	}
	if snap.Turns[0].Input != "inspect repo" {
		t.Fatalf("turn input = %q", snap.Turns[0].Input)
	}
	if snap.Turns[0].FinalResponse != "repo overview" {
		t.Fatalf("turn final response = %q", snap.Turns[0].FinalResponse)
	}
	if len(snap.Turns[0].ToolCalls) != 0 {
		t.Fatalf("tool calls = %d, want 0", len(snap.Turns[0].ToolCalls))
	}
}

type errorDriver struct {
	err error
}

func (d *errorDriver) Name() string { return "error-driver" }

func (d *errorDriver) Stream(_ context.Context, _ []llm.Message, out chan<- llm.Token) error {
	close(out)
	return d.err
}

func TestRunnerRunRecordsTurnError(t *testing.T) {
	driver := &errorDriver{err: context.DeadlineExceeded}
	session := NewSession()
	r := NewRunner(Config{Driver: driver, Session: session})

	if err := r.Run(context.Background(), "inspect repo"); err == nil {
		t.Fatal("expected runner error")
	}

	snap := session.Snapshot()
	if len(snap.Turns) != 1 {
		t.Fatalf("turns = %d, want 1", len(snap.Turns))
	}
	if snap.Turns[0].Error == "" {
		t.Fatal("expected recorded turn error")
	}
}

type scriptedDriver struct {
	responses []string
	callCount int
}

func (d *scriptedDriver) Name() string { return "scripted" }

func (d *scriptedDriver) Stream(_ context.Context, _ []llm.Message, out chan<- llm.Token) error {
	defer close(out)
	if d.callCount >= len(d.responses) {
		return errors.New("no scripted response")
	}
	out <- llm.Token{Text: d.responses[d.callCount]}
	d.callCount++
	return nil
}

type chunkedDriver struct {
	chunks    []string
	callCount int
}

func (d *chunkedDriver) Name() string { return "chunked" }

func (d *chunkedDriver) Stream(_ context.Context, _ []llm.Message, out chan<- llm.Token) error {
	defer close(out)
	d.callCount++
	for _, chunk := range d.chunks {
		out <- llm.Token{Text: chunk}
	}
	return nil
}

type silentRenderer struct{}

func (silentRenderer) AgentToken(string)                       {}
func (silentRenderer) AgentText(string)                        {}
func (silentRenderer) ToolCall(string, string)                 {}
func (silentRenderer) ToolResult(string, string, string, bool) {}
func (silentRenderer) Stats(time.Duration, llm.Usage)          {}
func (silentRenderer) Error(string)                            {}
func (silentRenderer) Info(string)                             {}

type recordingRenderer struct {
	mu         sync.Mutex
	tokenTexts []string
	fullTexts  []string
}

func (r *recordingRenderer) AgentToken(text string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tokenTexts = append(r.tokenTexts, text)
}

func (r *recordingRenderer) AgentText(text string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.fullTexts = append(r.fullTexts, text)
}

func (r *recordingRenderer) ToolCall(string, string)                 {}
func (r *recordingRenderer) ToolResult(string, string, string, bool) {}
func (r *recordingRenderer) Stats(time.Duration, llm.Usage)          {}
func (r *recordingRenderer) Error(string)                            {}
func (r *recordingRenderer) Info(string)                             {}

type blockingToolCallDriver struct {
	mu        sync.Mutex
	callCount int
	ready     chan struct{}
	release   chan struct{}
}

func (d *blockingToolCallDriver) Name() string { return "blocking-tool-call" }

func (d *blockingToolCallDriver) Stream(ctx context.Context, _ []llm.Message, out chan<- llm.Token) error {
	d.mu.Lock()
	d.callCount++
	call := d.callCount
	d.mu.Unlock()

	defer close(out)
	switch call {
	case 1:
		out <- llm.Token{Text: "<tool_call>\n"}
		out <- llm.Token{Text: "{\"name\":\"list_dir\",\"args\":{\"path\":\".\"}}\n"}
		out <- llm.Token{Text: "</tool_call>"}
		close(d.ready)
		select {
		case <-ctx.Done():
			return nil
		case <-d.release:
			return nil
		case <-time.After(2 * time.Second):
			return errors.New("first stream was not interrupted")
		}
	case 2:
		out <- llm.Token{Text: "repo overview"}
		return nil
	default:
		return errors.New("unexpected extra stream call")
	}
}

func TestRunnerLoopExecutesToolCallAndFinishes(t *testing.T) {
	driver := &scriptedDriver{responses: []string{
		"<tool_call>\n{\"name\":\"list_dir\",\"args\":{\"path\":\".\"}}\n</tool_call>",
		"repo overview",
	}}
	reg := agenttools.NewRegistry()
	reg.Register(agenttools.Tool{
		Name:        "list_dir",
		Description: "List files",
		Execute: func(ctx context.Context, args map[string]any) (string, error) {
			return "README.md\ninternal", nil
		},
	})
	session := NewSession()
	r := NewRunner(Config{
		Driver:       driver,
		Tools:        reg,
		Renderer:     silentRenderer{},
		SystemPrompt: func() string { return "system prompt" },
		Session:      session,
	})

	if err := r.Run(context.Background(), "inspect repo"); err != nil {
		t.Fatal(err)
	}
	snap := session.Snapshot()
	if len(snap.History) < 3 {
		t.Fatalf("history length = %d, want at least 3", len(snap.History))
	}
	if snap.Turns[0].FinalResponse != "repo overview" {
		t.Fatalf("final response = %q", snap.Turns[0].FinalResponse)
	}
	if len(snap.Turns[0].ToolCalls) != 1 || snap.Turns[0].ToolCalls[0].Name != "list_dir" {
		t.Fatalf("tool calls = %#v", snap.Turns[0].ToolCalls)
	}
}

func TestRunnerLoopRetriesSlashSkillOutput(t *testing.T) {
	driver := &scriptedDriver{responses: []string{
		"/using-superpowersI’ll inspect first",
		"final answer",
	}}
	reg := agenttools.NewRegistry()
	session := NewSession()
	r := NewRunner(Config{
		Driver:       driver,
		Tools:        reg,
		Renderer:     silentRenderer{},
		SystemPrompt: func() string { return "system prompt" },
		Session:      session,
	})

	if err := r.Run(context.Background(), "inspect repo"); err != nil {
		t.Fatal(err)
	}
	snap := session.Snapshot()
	if len(snap.History) < 2 {
		t.Fatalf("history length = %d, want at least 2", len(snap.History))
	}
	if snap.Turns[0].FinalResponse != "final answer" {
		t.Fatalf("final response = %q", snap.Turns[0].FinalResponse)
	}
}

func TestRunnerLoopRetriesMalformedToolMarkup(t *testing.T) {
	driver := &scriptedDriver{responses: []string{
		"<tool_call>git_status</tool_call><tool_call>git_log</think>",
		"final answer",
	}}
	renderer := &recordingRenderer{}
	session := NewSession()
	r := NewRunner(Config{
		Driver:       driver,
		Tools:        agenttools.NewRegistry(),
		Renderer:     renderer,
		SystemPrompt: func() string { return "system prompt" },
		Session:      session,
	})

	if err := r.Run(context.Background(), "inspect repo"); err != nil {
		t.Fatal(err)
	}

	snap := session.Snapshot()
	if got := snap.Turns[0].FinalResponse; got != "final answer" {
		t.Fatalf("final response = %q", got)
	}
	if len(snap.History) < 2 {
		t.Fatalf("history = %#v", snap.History)
	}
	foundRetry := false
	for _, msg := range snap.History {
		if msg.Role == llm.RoleUser && strings.Contains(msg.Content, "malformed tool markup") {
			foundRetry = true
		}
		if msg.Role == llm.RoleAssistant && strings.Contains(msg.Content, "<tool_call>") {
			t.Fatalf("malformed raw tool markup should not be stored as assistant output: %#v", snap.History)
		}
	}
	if !foundRetry {
		t.Fatalf("expected retry note in history, got %#v", snap.History)
	}
}

func TestRunnerLoopDoesNotDuplicateMalformedRetryNote(t *testing.T) {
	driver := &scriptedDriver{responses: []string{
		"<tool_call>git_status</tool_call><tool_call>git_log</think>",
		"<tool_call>run_command{\"command\":\"git status\"}",
		"final answer",
	}}
	session := NewSession()
	r := NewRunner(Config{
		Driver:       driver,
		Tools:        agenttools.NewRegistry(),
		Renderer:     silentRenderer{},
		SystemPrompt: func() string { return "system prompt" },
		Session:      session,
	})

	if err := r.Run(context.Background(), "inspect repo"); err != nil {
		t.Fatal(err)
	}

	snap := session.Snapshot()
	if got := snap.Turns[0].FinalResponse; got != "final answer" {
		t.Fatalf("final response = %q", got)
	}
	retryNotes := 0
	for _, msg := range snap.History {
		if msg.Role == llm.RoleUser && strings.Contains(msg.Content, "malformed tool markup") {
			retryNotes++
		}
	}
	if retryNotes != 1 {
		t.Fatalf("retry note count = %d, want 1; history=%#v", retryNotes, snap.History)
	}
}

func TestRunnerLoopFailsFastAfterRepeatedMalformedToolMarkup(t *testing.T) {
	driver := &scriptedDriver{responses: []string{
		"<tool_call>git_status</tool_call><tool_call>git_log</think>",
		"<tool_call>run_command{\"command\":\"git branch -a\"}",
		"<tool_call>run_command({\"command\":\"git status\"})",
		"final answer should not be reached",
	}}
	session := NewSession()
	r := NewRunner(Config{
		Driver:       driver,
		Tools:        agenttools.NewRegistry(),
		Renderer:     silentRenderer{},
		SystemPrompt: func() string { return "system prompt" },
		Session:      session,
	})

	err := r.Run(context.Background(), "inspect repo")
	if err == nil {
		t.Fatal("expected repeated malformed tool markup to fail")
	}
	if !strings.Contains(err.Error(), "too many invalid working responses") {
		t.Fatalf("error = %v", err)
	}
	if driver.callCount != 3 {
		t.Fatalf("driver call count = %d, want 3", driver.callCount)
	}
	snap := session.Snapshot()
	if got := snap.Turns[0].FinalResponse; got != "" {
		t.Fatalf("final response = %q, want empty", got)
	}
	if got := snap.Turns[0].Error; !strings.Contains(got, "too many invalid working responses") {
		t.Fatalf("turn error = %q", got)
	}
	retryNotes := 0
	for _, msg := range snap.History {
		if msg.Role == llm.RoleUser && strings.Contains(msg.Content, "malformed tool markup") {
			retryNotes++
		}
	}
	if retryNotes != 1 {
		t.Fatalf("retry note count = %d, want 1; history=%#v", retryNotes, snap.History)
	}
}

func TestRunnerLoopFailsFastAfterRepeatedSlashResponses(t *testing.T) {
	driver := &scriptedDriver{responses: []string{
		"/using-superpowers inspect",
		"/using-superpowers search",
		"/using-superpowers summarize",
	}}
	session := NewSession()
	r := NewRunner(Config{
		Driver:       driver,
		Tools:        agenttools.NewRegistry(),
		Renderer:     silentRenderer{},
		SystemPrompt: func() string { return "system prompt" },
		Session:      session,
	})

	err := r.Run(context.Background(), "inspect repo")
	if err == nil {
		t.Fatal("expected repeated slash responses to fail")
	}
	if !strings.Contains(err.Error(), "too many invalid working responses") {
		t.Fatalf("error = %v", err)
	}
	retryNotes := 0
	for _, msg := range session.Snapshot().History {
		if msg.Role == llm.RoleUser && strings.Contains(msg.Content, "slash commands") {
			retryNotes++
		}
	}
	if retryNotes != 1 {
		t.Fatalf("retry note count = %d, want 1", retryNotes)
	}
}

func TestRunnerLoopExecutesToolCallBeforeStreamCompletes(t *testing.T) {
	driver := &blockingToolCallDriver{
		ready:   make(chan struct{}),
		release: make(chan struct{}),
	}
	toolExecuted := make(chan struct{}, 1)
	reg := agenttools.NewRegistry()
	reg.Register(agenttools.Tool{
		Name:        "list_dir",
		Description: "List files",
		Execute: func(ctx context.Context, args map[string]any) (string, error) {
			select {
			case toolExecuted <- struct{}{}:
			default:
			}
			return "README.md\ninternal", nil
		},
	})
	session := NewSession()
	r := NewRunner(Config{
		Driver:       driver,
		Tools:        reg,
		Renderer:     silentRenderer{},
		SystemPrompt: func() string { return "system prompt" },
		Session:      session,
	})

	errCh := make(chan error, 1)
	go func() {
		errCh <- r.Run(context.Background(), "inspect repo")
	}()

	select {
	case <-driver.ready:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("timed out waiting for streamed tool call")
	}

	select {
	case <-toolExecuted:
	case <-time.After(200 * time.Millisecond):
		close(driver.release)
		t.Fatal("expected tool call to execute before the first stream completed")
	}

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("runner did not finish")
	}

	if got := r.LastResponse(); got != "repo overview" {
		t.Fatalf("last response = %q", got)
	}
}

func TestRunnerStreamsPlainTextTokensIncrementally(t *testing.T) {
	driver := &chunkedDriver{chunks: []string{"repo ", "overview"}}
	renderer := &recordingRenderer{}
	session := NewSession()
	r := NewRunner(Config{
		Driver:       driver,
		Renderer:     renderer,
		SystemPrompt: func() string { return "system prompt" },
		Session:      session,
	})

	if err := r.Run(context.Background(), "inspect repo"); err != nil {
		t.Fatal(err)
	}

	renderer.mu.Lock()
	defer renderer.mu.Unlock()
	if len(renderer.tokenTexts) == 0 {
		t.Fatal("expected incremental token rendering")
	}
	if got := renderer.tokenTexts[0]; got != "repo " {
		t.Fatalf("first streamed token = %q", got)
	}
	if len(renderer.fullTexts) != 0 {
		t.Fatalf("expected no duplicate full-text render, got %#v", renderer.fullTexts)
	}
	if got := r.LastResponse(); got != "repo overview" {
		t.Fatalf("last response = %q", got)
	}
}

func TestRunnerSetDriverSwitchesSubsequentTurns(t *testing.T) {
	first := &scriptedDriver{responses: []string{"first answer"}}
	second := &scriptedDriver{responses: []string{"second answer"}}
	r := NewRunner(Config{
		Driver:       first,
		Renderer:     silentRenderer{},
		SystemPrompt: func() string { return "system prompt" },
		Session:      NewSession(),
	})

	if err := r.Run(context.Background(), "first"); err != nil {
		t.Fatal(err)
	}
	r.SetDriver(second)
	if err := r.Run(context.Background(), "second"); err != nil {
		t.Fatal(err)
	}
	if got := r.LastResponse(); got != "second answer" {
		t.Fatalf("last response = %q", got)
	}
	if first.callCount != 1 || second.callCount != 1 {
		t.Fatalf("driver calls = (%d, %d), want (1, 1)", first.callCount, second.callCount)
	}
}

func TestRunnerNativeCurrentSystemPromptFallsBackToSystemPrompt(t *testing.T) {
	r := NewRunner(Config{
		SystemPrompt: func() string { return "  base prompt  " },
	})
	if got := r.nativeCurrentSystemPrompt(); got != "base prompt" {
		t.Fatalf("got %q, want %q", got, "base prompt")
	}
}

func TestRunnerNativeCurrentSystemPromptUsesNativePromptWhenSet(t *testing.T) {
	r := NewRunner(Config{
		SystemPrompt:       func() string { return "base" },
		NativeSystemPrompt: func() string { return "  native  " },
	})
	if got := r.nativeCurrentSystemPrompt(); got != "native" {
		t.Fatalf("got %q, want %q", got, "native")
	}
}

// nativeToolCallDriver simulates a provider that returns a native tool call
// on the first invocation and a plain text response on subsequent invocations.
type nativeToolCallDriver struct {
	callCount int
	lastTools []llm.ToolDef
	lastMsgs  []llm.Message
}

func (d *nativeToolCallDriver) Name() string { return "native-tool-driver" }

func (d *nativeToolCallDriver) Stream(_ context.Context, _ []llm.Message, out chan<- llm.Token) error {
	close(out)
	return errors.New("Stream should not be called on a NativeToolCaller driver")
}

func (d *nativeToolCallDriver) StreamWithTools(_ context.Context, msgs []llm.Message, tools []llm.ToolDef, out chan<- llm.Token) error {
	defer close(out)
	d.callCount++
	d.lastTools = tools
	d.lastMsgs = msgs
	switch d.callCount {
	case 1:
		out <- llm.Token{ToolCall: &llm.NativeToolCall{ID: "c1", Name: "git_status", ArgsJSON: `{}`}}
	default:
		out <- llm.Token{Text: "No changes detected."}
	}
	return nil
}

func TestRunnerNativeToolCallingPath(t *testing.T) {
	driver := &nativeToolCallDriver{}
	reg := agenttools.NewRegistry()
	called := false
	reg.Register(agenttools.Tool{
		Name:        "git_status",
		Description: "git status",
		AutoApprove: true,
		Execute: func(_ context.Context, _ map[string]any) (string, error) {
			called = true
			return "nothing to commit", nil
		},
	})
	session := NewSession()
	r := NewRunner(Config{
		Driver:  driver,
		Tools:   reg,
		Session: session,
	})

	if err := r.Run(context.Background(), "check the repo"); err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("git_status tool should have been called")
	}
	if driver.callCount != 2 {
		t.Fatalf("driver calls = %d, want 2 (tool call turn + final answer turn)", driver.callCount)
	}

	// Session history: user + assistant(tool_calls) + tool(result) + assistant(final)
	snap := session.Snapshot()
	roles := make([]llm.Role, 0, len(snap.History))
	for _, m := range snap.History {
		roles = append(roles, m.Role)
	}
	want := []llm.Role{llm.RoleUser, llm.RoleAssistant, llm.RoleTool, llm.RoleAssistant}
	if len(roles) != len(want) {
		t.Fatalf("history roles = %v, want %v", roles, want)
	}
	for i, r := range want {
		if roles[i] != r {
			t.Fatalf("history[%d] role = %q, want %q", i, roles[i], r)
		}
	}
}

func TestRunnerNativePathUsesNativeSystemPrompt(t *testing.T) {
	driver := &nativeToolCallDriver{}
	reg := agenttools.NewRegistry()
	reg.Register(agenttools.Tool{
		Name: "git_status", AutoApprove: true,
		Execute: func(_ context.Context, _ map[string]any) (string, error) { return "ok", nil },
	})
	nativePromptCalled := false
	r := NewRunner(Config{
		Driver:       driver,
		Tools:        reg,
		SystemPrompt: func() string { return "xml-prompt-with-tool-format" },
		NativeSystemPrompt: func() string {
			nativePromptCalled = true
			return "native-prompt"
		},
	})
	_ = r.Run(context.Background(), "check")
	if !nativePromptCalled {
		t.Fatal("native system prompt should be used when driver implements NativeToolCaller")
	}
	// Also verify the native prompt was sent to the driver, not the XML prompt
	for _, msg := range driver.lastMsgs {
		if msg.Role == llm.RoleSystem && msg.Content == "xml-prompt-with-tool-format" {
			t.Fatal("XML prompt should NOT be sent to a NativeToolCaller driver")
		}
	}
}

func TestRunnerNativePathPassesToolDefs(t *testing.T) {
	driver := &nativeToolCallDriver{}
	reg := agenttools.NewRegistry()
	reg.Register(agenttools.Tool{
		Name:        "read_file",
		Description: "read a file",
		Parameters:  []agenttools.ParameterDef{{Name: "path", Type: "string", Required: true}},
		AutoApprove: true,
		Execute:     func(_ context.Context, _ map[string]any) (string, error) { return "content", nil },
	})
	r := NewRunner(Config{Driver: driver, Tools: reg})
	_ = r.Run(context.Background(), "read something")
	if len(driver.lastTools) == 0 {
		t.Fatal("tool defs should be passed to StreamWithTools")
	}
	if driver.lastTools[0].Name != "read_file" {
		t.Fatalf("first tool def name = %q, want read_file", driver.lastTools[0].Name)
	}
}

func TestRunnerFallsBackToXMLWhenNoNativeToolCaller(t *testing.T) {
	// A plain scriptedDriver does NOT implement NativeToolCaller.
	// The runner should use the XML text parsing path.
	driver := &scriptedDriver{responses: []string{
		"<tool_call>\n{\"name\":\"git_status\",\"args\":{}}\n</tool_call>",
		"Clean.",
	}}
	reg := agenttools.NewRegistry()
	called := false
	reg.Register(agenttools.Tool{
		Name: "git_status", AutoApprove: true,
		Execute: func(_ context.Context, _ map[string]any) (string, error) {
			called = true
			return "nothing to commit", nil
		},
	})
	r := NewRunner(Config{Driver: driver, Tools: reg})
	if err := r.Run(context.Background(), "check"); err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("git_status should be called via XML fallback path")
	}
}

// malformedArgsDriver returns a tool call with invalid JSON args on the first call.
type malformedArgsDriver struct{ callCount int }

func (d *malformedArgsDriver) Name() string { return "malformed-args-driver" }

func (d *malformedArgsDriver) Stream(_ context.Context, _ []llm.Message, out chan<- llm.Token) error {
	close(out)
	return errors.New("Stream should not be called on a NativeToolCaller driver")
}

func (d *malformedArgsDriver) StreamWithTools(_ context.Context, _ []llm.Message, _ []llm.ToolDef, out chan<- llm.Token) error {
	defer close(out)
	d.callCount++
	out <- llm.Token{ToolCall: &llm.NativeToolCall{ID: "c1", Name: "git_status", ArgsJSON: `{bad json`}}
	return nil
}

func TestRunnerNativePathHandlesMalformedArgsJSON(t *testing.T) {
	driver := &malformedArgsDriver{}
	reg := agenttools.NewRegistry()
	reg.Register(agenttools.Tool{
		Name:        "git_status",
		Description: "git status",
		AutoApprove: true,
		Execute:     func(_ context.Context, _ map[string]any) (string, error) { return "ok", nil },
	})
	session := NewSession()
	r := NewRunner(Config{
		Driver:  driver,
		Tools:   reg,
		Session: session,
	})

	err := r.Run(context.Background(), "check")
	if err == nil {
		t.Fatal("expected error for malformed args JSON")
	}
	if !strings.Contains(err.Error(), "malformed tool call arguments") {
		t.Fatalf("error = %v, want mention of malformed tool call arguments", err)
	}

	snap := session.Snapshot()
	if len(snap.Turns) != 1 {
		t.Fatalf("turns = %d, want 1", len(snap.Turns))
	}
	if snap.Turns[0].Error == "" {
		t.Fatal("expected recorded turn error for malformed args")
	}
}

func TestRunnerClearHistoryResetsSessionState(t *testing.T) {
	r := NewRunner(Config{
		Driver:       &scriptedDriver{responses: []string{"done"}},
		Renderer:     silentRenderer{},
		SystemPrompt: func() string { return "system prompt" },
		Session:      NewSession(),
	})

	if err := r.Run(context.Background(), "inspect repo"); err != nil {
		t.Fatal(err)
	}
	if got := r.LastResponse(); got != "done" {
		t.Fatalf("last response = %q", got)
	}

	r.ClearHistory()
	if got := r.LastResponse(); got != "" {
		t.Fatalf("last response after clear = %q", got)
	}
	if snap := r.session.Snapshot(); snap.Turn != 0 || len(snap.History) != 0 || len(snap.Turns) != 0 {
		t.Fatalf("snapshot after clear = %#v", snap)
	}
}
