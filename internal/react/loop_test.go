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

// nativeScriptedDriver is a minimal NativeToolCaller driver for tests.
// It responds with a plain text answer to every StreamWithTools call.
type nativeScriptedDriver struct {
	responses []string
	callCount int
}

func (d *nativeScriptedDriver) Name() string { return "native-scripted" }

func (d *nativeScriptedDriver) Stream(_ context.Context, _ []llm.Message, out chan<- llm.Token) error {
	close(out)
	return errors.New("Stream should not be called on a NativeToolCaller driver")
}

func (d *nativeScriptedDriver) StreamWithTools(_ context.Context, _ []llm.Message, _ []llm.ToolDef, out chan<- llm.Token) error {
	defer close(out)
	if d.callCount >= len(d.responses) {
		return errors.New("no scripted response")
	}
	out <- llm.Token{Text: d.responses[d.callCount]}
	d.callCount++
	return nil
}

// scriptedDriver is a plain Driver (no NativeToolCaller) used to test that
// a non-native driver causes the runner to return an error.
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

type errorDriver struct {
	err error
}

func (d *errorDriver) Name() string { return "error-driver" }

func (d *errorDriver) Stream(_ context.Context, _ []llm.Message, out chan<- llm.Token) error {
	close(out)
	return d.err
}

func TestRunnerRunInvokesDriverAndProgress(t *testing.T) {
	driver := &nativeScriptedDriver{responses: []string{"repo overview"}}
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
	driver := &nativeScriptedDriver{responses: []string{"ignored"}}
	r := NewRunner(Config{Driver: driver})
	if err := r.Run(context.Background(), "   "); err != nil {
		t.Fatal(err)
	}
	if driver.callCount != 0 {
		t.Fatalf("calls = %d, want 0", driver.callCount)
	}
}

func TestRunnerRunRecordsCompletedTurnDetails(t *testing.T) {
	driver := &nativeScriptedDriver{responses: []string{"repo overview"}}
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

func TestRunnerRejectsFinalAnswerWhenCompletionCheckFails(t *testing.T) {
	driver := &nativeScriptedDriver{responses: []string{"done"}}
	session := NewSession()
	session.SetTaskState(TaskState{
		Objective:            "merge feature/go-rewrite into main",
		RequiredVerification: "verify main contains the resulting commit",
	})
	r := NewRunner(Config{
		Driver:  driver,
		Session: session,
		CompletionCheck: func(snapshot SessionSnapshot, finalText string) error {
			if snapshot.TaskState == nil {
				t.Fatal("expected task state during completion check")
			}
			if finalText != "done" {
				t.Fatalf("finalText = %q", finalText)
			}
			return errors.New("postcondition not satisfied")
		},
	})

	err := r.Run(context.Background(), "finish merge")
	if err == nil {
		t.Fatal("expected completion check failure")
	}
	if !strings.Contains(err.Error(), "postcondition not satisfied") {
		t.Fatalf("err = %v", err)
	}
	snap := session.Snapshot()
	if len(snap.Turns) != 1 || snap.Turns[0].Error == "" {
		t.Fatalf("expected recorded completion error, snapshot=%#v", snap)
	}
}

func TestRunnerRejectsBranchTargetMismatch(t *testing.T) {
	driver := &nativeScriptedDriver{responses: []string{"merge complete"}}
	session := NewSession()
	session.SetTaskState(TaskState{
		Objective:            "merge feature/go-rewrite into main",
		Operation:            "merge",
		SourceRef:            "feature/go-rewrite",
		TargetBranch:         "main",
		RequiredVerification: "verify branch main contains the resulting HEAD commit",
	})
	r := NewRunner(Config{
		Driver:  driver,
		Session: session,
		CompletionCheck: func(snapshot SessionSnapshot, finalText string) error {
			if snapshot.TaskState == nil || snapshot.TaskState.TargetBranch != "main" {
				t.Fatalf("unexpected task state: %#v", snapshot.TaskState)
			}
			return errors.New("task incomplete: target branch main does not contain HEAD")
		},
	})

	err := r.Run(context.Background(), "finish merge")
	if err == nil {
		t.Fatal("expected branch target mismatch failure")
	}
	if !strings.Contains(err.Error(), "target branch main does not contain HEAD") {
		t.Fatalf("err = %v", err)
	}
}

func TestRunnerReturnsErrorForNonNativeDriver(t *testing.T) {
	// A plain scriptedDriver does NOT implement NativeToolCaller.
	// The runner must return an error — no XML fallback.
	driver := &scriptedDriver{responses: []string{"some response"}}
	r := NewRunner(Config{Driver: driver})
	err := r.Run(context.Background(), "check")
	if err == nil {
		t.Fatal("expected error for non-NativeToolCaller driver")
	}
	if !strings.Contains(err.Error(), "does not support native tool calling") {
		t.Fatalf("error = %v, want mention of native tool calling", err)
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

func TestRunnerNativePathUsesSystemPrompt(t *testing.T) {
	driver := &nativeToolCallDriver{}
	reg := agenttools.NewRegistry()
	reg.Register(agenttools.Tool{
		Name: "git_status", AutoApprove: true,
		Execute: func(_ context.Context, _ map[string]any) (string, error) { return "ok", nil },
	})
	promptCalled := false
	r := NewRunner(Config{
		Driver: driver,
		Tools:  reg,
		SystemPrompt: func() string {
			promptCalled = true
			return "native-prompt"
		},
	})
	_ = r.Run(context.Background(), "check")
	if !promptCalled {
		t.Fatal("system prompt should be called")
	}
	// Verify the prompt was sent to the driver
	foundPrompt := false
	for _, msg := range driver.lastMsgs {
		if msg.Role == llm.RoleSystem && msg.Content == "native-prompt" {
			foundPrompt = true
		}
	}
	if !foundPrompt {
		t.Fatalf("expected system prompt in messages, got %#v", driver.lastMsgs)
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

func TestRunnerStreamsPlainTextTokensIncrementally(t *testing.T) {
	driver := &nativeChunkedDriver{chunks: []string{"repo ", "overview"}}
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

// nativeChunkedDriver sends multiple text tokens in one StreamWithTools call.
type nativeChunkedDriver struct {
	chunks    []string
	callCount int
}

func (d *nativeChunkedDriver) Name() string { return "native-chunked" }

func (d *nativeChunkedDriver) Stream(_ context.Context, _ []llm.Message, out chan<- llm.Token) error {
	close(out)
	return errors.New("Stream should not be called")
}

func (d *nativeChunkedDriver) StreamWithTools(_ context.Context, _ []llm.Message, _ []llm.ToolDef, out chan<- llm.Token) error {
	defer close(out)
	d.callCount++
	for _, chunk := range d.chunks {
		out <- llm.Token{Text: chunk}
	}
	return nil
}

func TestRunnerSetDriverSwitchesSubsequentTurns(t *testing.T) {
	first := &nativeScriptedDriver{responses: []string{"first answer"}}
	second := &nativeScriptedDriver{responses: []string{"second answer"}}
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

func TestRunnerSystemPromptIsPassedToDriver(t *testing.T) {
	driver := &nativeToolCallDriver{}
	reg := agenttools.NewRegistry()
	reg.Register(agenttools.Tool{
		Name: "git_status", AutoApprove: true,
		Execute: func(_ context.Context, _ map[string]any) (string, error) { return "ok", nil },
	})
	r := NewRunner(Config{
		Driver:       driver,
		Tools:        reg,
		SystemPrompt: func() string { return "  my system prompt  " },
	})
	_ = r.Run(context.Background(), "check")
	foundPrompt := false
	for _, msg := range driver.lastMsgs {
		if msg.Role == llm.RoleSystem && msg.Content == "my system prompt" {
			foundPrompt = true
		}
	}
	if !foundPrompt {
		t.Fatalf("system prompt not found in messages: %#v", driver.lastMsgs)
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

type nativeSequenceDriver struct {
	steps     [][]llm.Token
	callCount int
	allMsgs   [][]llm.Message
}

func (d *nativeSequenceDriver) Name() string { return "native-sequence" }

func (d *nativeSequenceDriver) Stream(_ context.Context, _ []llm.Message, out chan<- llm.Token) error {
	close(out)
	return errors.New("Stream should not be called on a NativeToolCaller driver")
}

func (d *nativeSequenceDriver) StreamWithTools(_ context.Context, msgs []llm.Message, _ []llm.ToolDef, out chan<- llm.Token) error {
	defer close(out)
	d.allMsgs = append(d.allMsgs, append([]llm.Message(nil), msgs...))
	if d.callCount >= len(d.steps) {
		return errors.New("no scripted step")
	}
	for _, tok := range d.steps[d.callCount] {
		out <- tok
	}
	d.callCount++
	return nil
}

func TestRunnerBlocksCommitWhileMergeConflictsRemain(t *testing.T) {
	driver := &nativeSequenceDriver{
		steps: [][]llm.Token{
			{{ToolCall: &llm.NativeToolCall{ID: "c1", Name: "run_command", ArgsJSON: `{"command":"git merge feature/go-rewrite"}`}}},
			{{ToolCall: &llm.NativeToolCall{ID: "c2", Name: "run_command", ArgsJSON: `{"command":"git commit -m \"merge\" "}`}}},
			{{Text: "stopped after conflict"}},
		},
	}
	reg := agenttools.NewRegistry()
	executedCommands := []string{}
	reg.Register(agenttools.Tool{
		Name:        "run_command",
		Description: "run shell command",
		Parameters:  []agenttools.ParameterDef{{Name: "command", Type: "string", Required: true}},
		AutoApprove: true,
		Execute: func(_ context.Context, args map[string]any) (string, error) {
			command, _ := args["command"].(string)
			executedCommands = append(executedCommands, command)
			if strings.Contains(command, "git merge feature/go-rewrite") {
				return "Auto-merging .gitignore\nCONFLICT (content): Merge conflict in .gitignore\nAutomatic merge failed; fix conflicts and then commit the result.\n\nexit 1", nil
			}
			return "unexpected execution", nil
		},
	})
	session := NewSession()
	r := NewRunner(Config{Driver: driver, Tools: reg, Session: session})

	if err := r.Run(context.Background(), "merge the branch"); err != nil {
		t.Fatal(err)
	}
	if len(executedCommands) != 1 {
		t.Fatalf("executed commands = %#v, want only merge command", executedCommands)
	}
	if len(driver.allMsgs) < 2 {
		t.Fatalf("driver messages = %#v", driver.allMsgs)
	}
	foundRuntimeNote := false
	for _, msg := range driver.allMsgs[1] {
		if msg.Role == llm.RoleSystem && strings.Contains(msg.Content, "Call git_merge_status to inspect unresolved files and next steps") {
			foundRuntimeNote = true
		}
	}
	if !foundRuntimeNote {
		t.Fatalf("expected runtime note in second request, got %#v", driver.allMsgs[1])
	}
	snap := session.Snapshot()
	foundBlockedResult := false
	for _, msg := range snap.History {
		if msg.Role == llm.RoleTool && strings.Contains(msg.Content, "blocked: unmerged git conflicts remain") {
			foundBlockedResult = true
		}
	}
	if !foundBlockedResult {
		t.Fatalf("expected blocked commit tool result, history=%#v", snap.History)
	}
}

func TestRunnerRequiresMutationBeforeRetryingFailedCommit(t *testing.T) {
	driver := &nativeSequenceDriver{
		steps: [][]llm.Token{
			{{ToolCall: &llm.NativeToolCall{ID: "c1", Name: "git_commit", ArgsJSON: `{"message":"merge branch"}`}}},
			{{ToolCall: &llm.NativeToolCall{ID: "c2", Name: "git_commit", ArgsJSON: `{"message":"merge branch"}`}}},
			{{Text: "waiting for a real fix"}},
		},
	}
	reg := agenttools.NewRegistry()
	gitCommitCalls := 0
	reg.Register(agenttools.Tool{
		Name:        "git_commit",
		Description: "commit changes",
		Parameters:  []agenttools.ParameterDef{{Name: "message", Type: "string", Required: true}},
		AutoApprove: true,
		Execute: func(_ context.Context, _ map[string]any) (string, error) {
			gitCommitCalls++
			return "[INFO] Checking merge-conflict files only.\nprettier.................................................................Passed\nyamllint.................................................................Failed\n.pre-commit-config.yaml:139\nLine too long (304 > 160 characters)\n\nexit 1", nil
		},
	})
	session := NewSession()
	r := NewRunner(Config{Driver: driver, Tools: reg, Session: session})

	if err := r.Run(context.Background(), "finish the merge"); err != nil {
		t.Fatal(err)
	}
	if gitCommitCalls != 1 {
		t.Fatalf("git commit calls = %d, want 1", gitCommitCalls)
	}
	snap := session.Snapshot()
	foundBlockedResult := false
	for _, msg := range snap.History {
		if msg.Role == llm.RoleTool && strings.Contains(msg.Content, "blocked: the previous commit attempt already failed") {
			foundBlockedResult = true
		}
	}
	if !foundBlockedResult {
		t.Fatalf("expected blocked repeated commit result, history=%#v", snap.History)
	}
}

func TestRunnerClearHistoryResetsSessionState(t *testing.T) {
	r := NewRunner(Config{
		Driver:       &nativeScriptedDriver{responses: []string{"done"}},
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
