package react

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	agenttools "forge/internal/agent/tools"
	"forge/internal/hooks"
	"forge/internal/llm"
	"forge/internal/protocol"
	"forge/internal/sessionstore"
)

// nativeScriptedDriver is a minimal NativeToolCaller driver for tests.
// It responds with a plain text answer to every StreamWithTools call.
type nativeScriptedDriver struct {
	responses []string
	callCount int
	reset     bool
}

func (d *nativeScriptedDriver) Name() string { return "native-scripted" }

func (d *nativeScriptedDriver) Stream(_ context.Context, _ []llm.Message, out chan<- llm.Token) error {
	defer close(out)
	if d.callCount >= len(d.responses) {
		return errors.New("no scripted response")
	}
	out <- llm.Token{Text: d.responses[d.callCount]}
	d.callCount++
	return nil
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

func (d *nativeScriptedDriver) ResetConversation() { d.reset = true }

type captureMessagesDriver struct {
	lastMessages []llm.Message
	response     string
}

func (d *captureMessagesDriver) Name() string { return "capture-messages" }

func (d *captureMessagesDriver) Stream(_ context.Context, msgs []llm.Message, out chan<- llm.Token) error {
	defer close(out)
	d.lastMessages = append([]llm.Message(nil), msgs...)
	out <- llm.Token{Text: d.response}
	return nil
}

func (d *captureMessagesDriver) StreamWithTools(_ context.Context, msgs []llm.Message, _ []llm.ToolDef, out chan<- llm.Token) error {
	defer close(out)
	d.lastMessages = append([]llm.Message(nil), msgs...)
	out <- llm.Token{Text: d.response}
	return nil
}

// scriptedDriver is a plain Driver (no NativeToolCaller) used to test the
// plain-stream path and the error path when tool calling is required.
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
	retryTexts []string
	toolTexts  []string
	events     []string
	statsCalls int
	statsUsage []llm.Usage
	statsCtx   []int
}

func (r *recordingRenderer) AgentToken(text string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, "token")
	r.tokenTexts = append(r.tokenTexts, text)
}

func (r *recordingRenderer) AgentText(text string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, "text")
	r.fullTexts = append(r.fullTexts, text)
}

func (r *recordingRenderer) Retry(text string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.retryTexts = append(r.retryTexts, text)
}

func (r *recordingRenderer) ToolCall(string, string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, "tool_call")
}
func (r *recordingRenderer) ToolResult(_ string, text string, _ string, _ bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, "tool_result")
	r.toolTexts = append(r.toolTexts, text)
}
func (r *recordingRenderer) Stats(_ time.Duration, usage llm.Usage) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, "stats")
	r.statsCalls++
	r.statsUsage = append(r.statsUsage, usage)
}
func (r *recordingRenderer) StatsWithContext(_ time.Duration, usage llm.Usage, contextUsed int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, "stats")
	r.statsCalls++
	r.statsUsage = append(r.statsUsage, usage)
	r.statsCtx = append(r.statsCtx, contextUsed)
}
func (r *recordingRenderer) Error(string) {}
func (r *recordingRenderer) Info(string)  {}

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
	r := NewRunner(Config{
		Driver:  driver,
		Session: session,
	})

	if err := r.Run(context.Background(), "  inspect this file  "); err != nil {
		t.Fatal(err)
	}
	if driver.callCount != 1 {
		t.Fatalf("calls = %d, want 1", driver.callCount)
	}
	snap := session.Snapshot()
	if snap.Turn != 1 {
		t.Fatalf("turn = %d, want 1", snap.Turn)
	}
}

func TestRunnerArtifactGateFailsWhenWriteToolResultIsTextError(t *testing.T) {
	workspace := t.TempDir()
	artifact := "docs/plans/2026-05-23-term-wrangler-design.md"
	session := NewSession()
	session.SetActiveWorkspaceRoot(workspace)
	driver := &nativeSequenceDriver{steps: append([][]llm.Token{
		{{ToolCall: &llm.NativeToolCall{ID: "write-1", Name: "write_file", ArgsJSON: fmt.Sprintf(`{"path":%q,"content":"# Term Wrangler Design\n\n## Approach\n\nThe tool reports an error and must not satisfy the gate."}`, artifact)}}},
	}, repeatedTextSteps("Done, I wrote the plan.", 4)...)}
	reg := agenttools.NewRegistry()
	reg.Register(agenttools.Tool{Name: "write_file", Parameters: []agenttools.ParameterDef{{Name: "path", Type: "string", Required: true}, {Name: "content", Type: "string", Required: true}}, Execute: func(context.Context, map[string]any) (string, error) {
		return "error: disk full", nil
	}})
	r := NewRunner(Config{Session: session, Driver: driver, Tools: reg, MaxSteps: 6})

	if err := r.Run(context.Background(), "write "+artifact); err != nil {
		t.Fatalf("err = %v, want final answer accepted after bounded gate nudges", err)
	}
	if _, statErr := os.Stat(filepath.Join(workspace, artifact)); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("artifact stat err = %v, want not exist", statErr)
	}
}

func TestRunnerRunReturnsErrorWhenDriverMissing(t *testing.T) {
	r := NewRunner(Config{})
	if err := r.Run(context.Background(), "inspect"); err == nil {
		t.Fatal("expected error when driver is nil")
	}
}

func TestRunnerRunReturnsDurableInputAppendError(t *testing.T) {
	sinkErr := errors.New("durable append failed")
	session := NewSession()
	session.SetDurableSink(failingDurableSink{err: sinkErr})
	r := NewRunner(Config{Session: session})

	err := r.Run(context.Background(), "hello")
	if !errors.Is(err, sinkErr) {
		t.Fatalf("Run error = %v, want durable append error %v", err, sinkErr)
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

	if err := r.Run(context.Background(), "hello"); err != nil {
		t.Fatal(err)
	}

	snap := session.Snapshot()
	if len(snap.Turns) != 1 {
		t.Fatalf("turns = %d, want 1", len(snap.Turns))
	}
	if snap.Turns[0].Input != "hello" {
		t.Fatalf("turn input = %q", snap.Turns[0].Input)
	}
	if snap.Turns[0].FinalResponse != "repo overview" {
		t.Fatalf("turn final response = %q", snap.Turns[0].FinalResponse)
	}
	if len(snap.Turns[0].ToolCalls) != 0 {
		t.Fatalf("tool calls = %d, want 0", len(snap.Turns[0].ToolCalls))
	}
}

func TestRunnerRunUsesActiveTurnContextAndEndsTurn(t *testing.T) {
	driver := &nativeToolCallDriver{}
	session := NewSession()
	toolCtx := make(chan context.Context, 1)
	reg := agenttools.NewRegistry()
	reg.Register(agenttools.Tool{
		Name:        "git_status",
		Description: "git status",
		AutoApprove: true,
		Execute: func(ctx context.Context, _ map[string]any) (string, error) {
			toolCtx <- ctx
			return "nothing to commit", nil
		},
	})
	r := NewRunner(Config{Driver: driver, Tools: reg, Session: session})

	if err := r.Run(context.Background(), "check the repo"); err != nil {
		t.Fatal(err)
	}
	if ctx := <-toolCtx; ctx == context.Background() {
		t.Fatal("tool received original parent context, want active turn context")
	}
	if _, ok := session.ActiveTurnSnapshot(); ok {
		t.Fatal("active turn still present after Run completed")
	}
}

func TestRunnerRunRejectsOverlapWithoutRecordingSecondTurn(t *testing.T) {
	driver := &nativeToolCallDriver{}
	session := NewSession()
	toolStarted := make(chan struct{})
	releaseTool := make(chan struct{})
	reg := agenttools.NewRegistry()
	reg.Register(agenttools.Tool{
		Name:        "git_status",
		Description: "git status",
		AutoApprove: true,
		Execute: func(ctx context.Context, _ map[string]any) (string, error) {
			close(toolStarted)
			select {
			case <-releaseTool:
				return "nothing to commit", nil
			case <-ctx.Done():
				return "", ctx.Err()
			}
		},
	})
	r := NewRunner(Config{Driver: driver, Tools: reg, Session: session})
	firstErr := make(chan error, 1)
	go func() {
		firstErr <- r.Run(context.Background(), "check the repo")
	}()
	<-toolStarted

	err := r.Run(context.Background(), "second run")
	if err == nil || !strings.Contains(err.Error(), "active turn") {
		t.Fatalf("Run error = %v, want active turn overlap error", err)
	}
	snap := session.Snapshot()
	if snap.Turn != 1 {
		t.Fatalf("turn = %d, want overlap rejection to leave turn at 1", snap.Turn)
	}
	var userMessages int
	for _, msg := range snap.History {
		if msg.Role == llm.RoleUser {
			userMessages++
		}
	}
	if userMessages != 1 {
		t.Fatalf("user messages = %d, want only first run recorded", userMessages)
	}

	close(releaseTool)
	if err := <-firstErr; err != nil {
		t.Fatal(err)
	}
	for _, item := range session.Snapshot().Items {
		if item.TurnID != "turn-1" {
			t.Fatalf("item %s has TurnID %q, want turn-1; item=%#v", item.Kind, item.TurnID, item)
		}
	}
}

func TestRunnerRunRejectsOverlapBeforeRuntimeNoteMutation(t *testing.T) {
	session := NewSession()
	_, cancel, err := session.BeginTurn(context.Background(), "turn-existing")
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	r := NewRunner(Config{
		Driver:  &nativeScriptedDriver{responses: []string{"unused"}},
		Session: session,
		ConfigureHooks: func(registry *hooks.Registry) {
			registry.Register(hooks.PointPromptContext, "test:prompt", func(context.Context, hooks.Event) []hooks.Result {
				return []hooks.Result{hooks.OverlayResult{
					Key:        "test_overlap_overlay",
					Content:    "must not be applied before overlap rejection",
					Priority:   hooks.PriorityHigh,
					Provenance: "test",
				}}
			})
		},
	})
	before := session.Snapshot()

	err = r.Run(context.Background(), "inspect repo")
	if err == nil || !strings.Contains(err.Error(), "active turn") {
		t.Fatalf("Run error = %v, want active turn overlap error", err)
	}
	after := session.Snapshot()
	if after.RuntimeNote != before.RuntimeNote {
		t.Fatalf("runtime note changed from %q to %q", before.RuntimeNote, after.RuntimeNote)
	}
	if after.HookOutputSet != before.HookOutputSet || len(after.HookOutput.Overlays) != len(before.HookOutput.Overlays) {
		t.Fatalf("hook output changed from %#v to %#v", before.HookOutput, after.HookOutput)
	}
	if after.CompactedTurns != before.CompactedTurns || after.CompactionSummary != before.CompactionSummary {
		t.Fatalf("compaction state changed from (%d, %q) to (%d, %q)", before.CompactedTurns, before.CompactionSummary, after.CompactedTurns, after.CompactionSummary)
	}
	if len(after.History) != len(before.History) || len(after.Turns) != len(before.Turns) || len(after.Items) != len(before.Items) {
		t.Fatalf("session transcript changed: history %d->%d turns %d->%d items %d->%d", len(before.History), len(after.History), len(before.Turns), len(after.Turns), len(before.Items), len(after.Items))
	}
}

func TestRunnerRunActiveTurnCancellationCancelsBlockedToolContext(t *testing.T) {
	driver := &nativeToolCallDriver{}
	session := NewSession()
	toolStarted := make(chan struct{})
	toolCancelled := make(chan struct{})
	reg := agenttools.NewRegistry()
	reg.Register(agenttools.Tool{
		Name:        "git_status",
		Description: "git status",
		AutoApprove: true,
		Execute: func(ctx context.Context, _ map[string]any) (string, error) {
			close(toolStarted)
			<-ctx.Done()
			close(toolCancelled)
			return "", ctx.Err()
		},
	})
	r := NewRunner(Config{Driver: driver, Tools: reg, Session: session})
	runErr := make(chan error, 1)
	go func() {
		runErr <- r.Run(context.Background(), "check the repo")
	}()
	<-toolStarted

	if err := session.CancelActiveTurn("user cancelled"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-toolCancelled:
	case <-time.After(time.Second):
		t.Fatal("blocked tool context was not cancelled")
	}
	if err := <-runErr; err == nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("Run error = %v, want context canceled", err)
	}
	for _, item := range session.Snapshot().Items {
		if item.Kind == protocol.ItemTurnComplete && item.TurnComplete != nil && item.TurnComplete.Status == protocol.TurnStatusCompleted {
			t.Fatalf("cancelled turn recorded successful terminal item: %#v", item)
		}
	}
}

func TestRunnerRunReturnsOverlapErrorWhenTurnAlreadyActive(t *testing.T) {
	session := NewSession()
	_, cancel, err := session.BeginTurn(context.Background(), "turn-existing")
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	r := NewRunner(Config{Driver: &nativeScriptedDriver{responses: []string{"unused"}}, Session: session})

	err = r.Run(context.Background(), "inspect repo")
	if err == nil || !strings.Contains(err.Error(), "active turn") {
		t.Fatalf("Run error = %v, want active turn overlap error", err)
	}
	if session.Snapshot().Turn != 0 {
		t.Fatalf("recorded turns = %d, want overlap rejection before recording input", session.Snapshot().Turn)
	}
}

func TestRunnerInvokesTurnCompleteHookAfterSuccessfulTurn(t *testing.T) {
	driver := &nativeScriptedDriver{responses: []string{"repo overview"}}
	session := NewSession()
	var got SessionSnapshot
	called := false
	r := NewRunner(Config{
		Driver:  driver,
		Session: session,
		TurnComplete: func(snapshot SessionSnapshot) {
			called = true
			got = snapshot
		},
	})

	if err := r.Run(context.Background(), "hello"); err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("expected turn-complete hook")
	}
	if got.Turn != 1 {
		t.Fatalf("snapshot turn = %d", got.Turn)
	}
	if len(got.Turns) != 1 || got.Turns[0].FinalResponse != "repo overview" {
		t.Fatalf("snapshot turns = %#v", got.Turns)
	}
}

func TestRunnerTurnCompleteHookCanFeedMemorySummaryIntoNextTurn(t *testing.T) {
	driver := &captureMessagesDriver{response: "second answer"}
	session := NewSession()
	r := NewRunner(Config{
		Driver:  driver,
		Session: session,
		TurnComplete: func(snapshot SessionSnapshot) {
			if len(snapshot.Turns) == 1 {
				session.SetMemorySummary("Remembered: runtime flow was already inspected.")
			}
		},
	})

	firstDriver := &nativeScriptedDriver{responses: []string{"first answer"}}
	r.SetDriver(firstDriver)
	if err := r.Run(context.Background(), "hello"); err != nil {
		t.Fatal(err)
	}

	r.SetDriver(driver)
	session.SetMode(ModeInspect)
	if err := r.Run(context.Background(), "keep going"); err != nil {
		t.Fatal(err)
	}

	foundMemory := false
	for _, msg := range driver.lastMessages {
		if msg.Role == llm.RoleSystem && strings.Contains(msg.Content, "Memory summary:") {
			foundMemory = true
			break
		}
	}
	if !foundMemory {
		t.Fatalf("expected memory summary in second turn messages: %#v", driver.lastMessages)
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

func TestRunnerIncludesInterruptedGuidanceAfterExecSessionTurn(t *testing.T) {
	driver := &captureMessagesDriver{response: "checked current state before continuing"}
	session := NewSession()

	turn := session.RecordInput("start the dev server")
	session.AppendAssistantWithToolCalls([]llm.NativeToolCall{{
		ID:       "call-1",
		Name:     "exec_session_start",
		ArgsJSON: `{"command":"npm run dev","cols":120,"rows":40}`,
	}})
	if err := session.AppendNativeToolResult("call-1", `{"status":"running","session_id":9,"command":"npm run dev","pty":true,"cols":120,"rows":40}`); err != nil {
		t.Fatal(err)
	}
	if err := session.CompleteTurn(turn, "", []TurnToolCall{{Name: "exec_session_start"}}, nil); err != nil {
		t.Fatal(err)
	}
	session.MarkInterrupted()

	r := NewRunner(Config{
		Driver:  driver,
		Session: session,
	})

	if err := r.Run(context.Background(), "continue from there"); err != nil {
		t.Fatal(err)
	}

	var interruptedMsg string
	for _, msg := range driver.lastMessages {
		if msg.Role == llm.RoleSystem && strings.Contains(strings.ToLower(msg.Content), "previous turn was interrupted") {
			interruptedMsg = msg.Content
			break
		}
	}
	if interruptedMsg == "" {
		t.Fatalf("expected interrupted guidance in messages: %#v", driver.lastMessages)
	}
	if !strings.Contains(interruptedMsg, "verify current state before continuing") {
		t.Fatalf("interrupted guidance = %q", interruptedMsg)
	}
}

func TestRunnerIncludesReviewRuntimeGuidance(t *testing.T) {
	driver := &nativeSequenceDriver{
		steps: [][]llm.Token{
			{{ToolCall: &llm.NativeToolCall{ID: "c1", Name: "list_dir", ArgsJSON: `{"path":"."}`}}},
			{{Text: "- Finding: runtime guidance is present."}},
		},
	}
	session := NewSession()
	session.SetTaskState(TaskState{
		Objective:            "review this repo and tell me what i need to change",
		Operation:            "review",
		RequiredVerification: "produce source-grounded findings first, ordered by severity, and keep the summary secondary to the actual review issues",
	})
	reg := agenttools.NewRegistry()
	reg.Register(agenttools.Tool{
		Name:        "list_dir",
		Description: "list a directory",
		Parameters:  []agenttools.ParameterDef{{Name: "path", Type: "string", Required: true}},
		AutoApprove: true,
		Execute: func(_ context.Context, args map[string]any) (string, error) {
			if got := fmt.Sprint(args["path"]); got != "." {
				t.Fatalf("path = %q, want .", got)
			}
			return "README.md\ninternal/", nil
		},
	})

	r := NewRunner(Config{
		Driver:  driver,
		Tools:   reg,
		Session: session,
	})

	if err := r.Run(context.Background(), "review this repo"); err != nil {
		t.Fatal(err)
	}

	if len(driver.allMsgs) == 0 {
		t.Fatalf("expected driver messages, got none")
	}
	var reviewMsg string
	for _, msg := range driver.allMsgs[len(driver.allMsgs)-1] {
		if msg.Role == llm.RoleSystem && strings.Contains(msg.Content, "[hook:runtime]") && strings.Contains(msg.Content, "Review workflow active") {
			reviewMsg = msg.Content
			break
		}
	}
	if reviewMsg == "" {
		t.Fatalf("expected review runtime guidance in messages: %#v", driver.allMsgs)
	}
	if !strings.Contains(reviewMsg, "Lead with findings") {
		t.Fatalf("review guidance = %q", reviewMsg)
	}
}

func TestRunnerIncludesBlockedPlanRuntimeGuidance(t *testing.T) {
	driver := &captureMessagesDriver{response: "Plan is blocked on user input."}
	session := NewSession()
	session.SetTaskState(TaskState{
		Objective:            "write a plan for prompt/runtime work",
		Operation:            "plan",
		RequiredVerification: "produce a concise plan grounded in enough repo evidence",
	})
	session.SetPlanState(PlanState{
		Steps: []PlanStep{
			{Step: "Confirm whether plan mode should be mandatory by default", Status: "blocked", Blocker: "need user decision on default behavior"},
			{Step: "Implement the approved runtime behavior", Status: "pending"},
		},
	})

	r := NewRunner(Config{
		Driver:  driver,
		Session: session,
	})

	if err := r.Run(context.Background(), "keep going"); err != nil {
		t.Fatalf("err = %v, want honest blocked report accepted as final answer", err)
	}

	var blockedMsg string
	for _, msg := range driver.lastMessages {
		if msg.Role == llm.RoleSystem && strings.Contains(msg.Content, "[hook:runtime]") && strings.Contains(msg.Content, "Current plan is blocked") {
			blockedMsg = msg.Content
			break
		}
	}
	if blockedMsg == "" {
		t.Fatalf("expected blocked plan runtime guidance in messages: %#v", driver.lastMessages)
	}
	if !strings.Contains(blockedMsg, "ask_user_question") {
		t.Fatalf("blocked guidance = %q", blockedMsg)
	}
}

func TestRunnerBlockedPlanOverlayCoexistsWithSuggestedSkillOverlay(t *testing.T) {
	driver := &captureMessagesDriver{response: "Plan is blocked on user input."}
	session := NewSession()
	session.SetHookOverlay(HookOverlay{
		Key:        "suggested_skill",
		Content:    "suggested skill: /brainstorming (planning work benefits from explicit design before implementation)",
		Priority:   HookPriorityNormal,
		Provenance: "runtime",
	})
	session.SetTaskState(TaskState{
		Objective:            "write a plan for prompt/runtime work",
		Operation:            "plan",
		RequiredVerification: "produce a concise plan grounded in enough repo evidence",
	})
	session.SetPlanState(PlanState{
		Steps: []PlanStep{
			{Step: "Confirm whether plan mode should be mandatory by default", Status: "blocked", Blocker: "need user decision on default behavior"},
			{Step: "Implement the approved runtime behavior", Status: "pending"},
		},
	})

	r := NewRunner(Config{
		Driver:  driver,
		Session: session,
	})

	if err := r.Run(context.Background(), "keep going"); err != nil {
		t.Fatalf("err = %v, want honest blocked report accepted as final answer", err)
	}

	foundSuggested := false
	foundBlocked := false
	for _, msg := range driver.lastMessages {
		if msg.Role != llm.RoleSystem {
			continue
		}
		if strings.Contains(msg.Content, "suggested skill: /brainstorming") {
			foundSuggested = true
		}
		if strings.Contains(msg.Content, "Current plan is blocked") {
			foundBlocked = true
		}
	}
	if !foundSuggested || !foundBlocked {
		t.Fatalf("expected both overlays, got %#v", driver.lastMessages)
	}
}

func TestRunnerBeforeToolHookAllowsBroadAlternationSearch(t *testing.T) {
	r := NewRunner(Config{})

	output := r.beforeToolHookOutput(context.Background(), "search", map[string]any{
		"pattern": "tui|theming|theme|terminal ui|tailwind|colors|palette|config|styles|css|scss",
		"path":    ".",
	})

	if output.Block != nil {
		t.Fatalf("search should not be hard-blocked: %#v", output.Block)
	}
}

func TestRunnerBeforeToolHookBlocksCommitWorkflow(t *testing.T) {
	t.Run("unmerged conflicts", func(t *testing.T) {
		r := NewRunner(Config{})
		r.gitWorkflow.unmergedFiles = true

		output := r.beforeToolHookOutput(context.Background(), "run_command", map[string]any{
			"command": `git commit -m "merge"`,
		})

		if output.Block == nil || !strings.Contains(output.Block.Message, "unmerged git conflicts remain") {
			t.Fatalf("block = %#v", output.Block)
		}
	})

	t.Run("restage blocker allows git add", func(t *testing.T) {
		r := NewRunner(Config{})
		r.gitWorkflow.commitBlocker = commitBlockerRestage

		output := r.beforeToolHookOutput(context.Background(), "run_command", map[string]any{
			"command": "git add README.md",
		})

		if output.Block != nil {
			t.Fatalf("unexpected block = %#v", output.Block)
		}
	})

	t.Run("edit blocker", func(t *testing.T) {
		r := NewRunner(Config{})
		r.gitWorkflow.commitBlocker = commitBlockerEdit

		output := r.beforeToolHookOutput(context.Background(), "git_commit", map[string]any{
			"message": "merge branch",
		})

		if output.Block == nil || !strings.Contains(output.Block.Message, "previous commit attempt already failed") {
			t.Fatalf("block = %#v", output.Block)
		}
	})
}

func TestRunnerPlanStateBlocksFinalSuccessWhenStepInProgress(t *testing.T) {
	driver := &nativeSequenceDriver{steps: [][]llm.Token{
		{{Text: "Done, implemented the requested change."}},
		{{Text: "I still need to finish the implementation step."}},
		{{Text: "I still need to finish the implementation step."}},
	}}
	session := NewSession()
	session.SetPlanState(PlanState{Steps: []PlanStep{{Step: "Implement the requested change", Status: "in_progress"}}})
	r := NewRunner(Config{Driver: driver, Session: session})

	if err := r.Run(context.Background(), "continue"); err != nil {
		t.Fatalf("err = %v, want final answer accepted after bounded gate nudges", err)
	}
	if strings.Contains(r.LastResponse(), "implemented the requested change") {
		t.Fatalf("final response = %q, should not claim completion while plan is in progress", r.LastResponse())
	}
	if !sessionHistoryContains(session, "plan state inconsistent", "Implement the requested change") {
		t.Fatalf("history missing concrete plan feedback: %#v", session.Snapshot().History)
	}
	if driver.callCount != 3 {
		t.Fatalf("driver calls = %d, want two nudges then acceptance", driver.callCount)
	}
}

func TestRunnerPlanStateBlocksFinalSuccessWhenStepBlocked(t *testing.T) {
	driver := &nativeSequenceDriver{steps: repeatedTextSteps("Complete, all work is done.", 4)}
	session := NewSession()
	session.SetPlanState(PlanState{Steps: []PlanStep{{Step: "Choose deployment target", Status: "blocked", Blocker: "need user decision"}}})
	r := NewRunner(Config{Driver: driver, Session: session, MaxSteps: 5})

	if err := r.Run(context.Background(), "continue"); err != nil {
		t.Fatalf("err = %v, want final answer accepted after bounded gate nudges", err)
	}
	if turns := session.Snapshot().Turns; len(turns) == 0 || strings.TrimSpace(turns[len(turns)-1].FinalResponse) == "" {
		t.Fatalf("turns = %#v, want persisted final response after gate cap", turns)
	}
	if !sessionHistoryContains(session, "plan state inconsistent", "Choose deployment target", "need user decision") {
		t.Fatalf("history missing concrete blocked-plan feedback: %#v", session.Snapshot().History)
	}
}

func TestRunnerPlanStateFailureReportWithBlockedStepIsDeliveredAfterNudges(t *testing.T) {
	driver := &nativeSequenceDriver{steps: repeatedTextSteps("I cannot complete this because the plan is blocked on user approval.", 3)}
	session := NewSession()
	session.SetPlanState(PlanState{Steps: []PlanStep{{Step: "Get approval", Status: "blocked", Blocker: "waiting for user approval"}}})
	r := NewRunner(Config{Driver: driver, Session: session, MaxSteps: 5})

	if err := r.Run(context.Background(), "continue"); err != nil {
		t.Fatalf("err = %v, want final answer accepted after bounded gate nudges", err)
	}
	if driver.callCount != 3 {
		t.Fatalf("driver calls = %d, want two nudges then acceptance", driver.callCount)
	}
	turns := session.Snapshot().Turns
	if len(turns) == 0 {
		t.Fatal("missing turn record")
	}
	last := turns[len(turns)-1]
	if !strings.Contains(last.FinalResponse, "blocked on user approval") {
		t.Fatalf("FinalResponse = %q, want honest blocker report delivered to user", last.FinalResponse)
	}
}

func TestRunnerAllowsFinalSuccessWhenAllPlanStepsComplete(t *testing.T) {
	driver := &nativeSequenceDriver{steps: [][]llm.Token{{{Text: "Done, implemented the requested change."}}}}
	session := NewSession()
	session.SetPlanState(PlanState{Steps: []PlanStep{{Step: "Implement the requested change", Status: "completed"}}})
	r := NewRunner(Config{Driver: driver, Session: session})

	if err := r.Run(context.Background(), "continue"); err != nil {
		t.Fatal(err)
	}
	if got := r.LastResponse(); got != "Done, implemented the requested change." {
		t.Fatalf("LastResponse = %q", got)
	}
}

func TestRunnerPlanStateBlocksPendingAndUnknownStatuses(t *testing.T) {
	for _, status := range []string{"pending", "waiting_for_review"} {
		t.Run(status, func(t *testing.T) {
			driver := &nativeSequenceDriver{steps: repeatedTextSteps("Done, implemented the requested change.", 4)}
			session := NewSession()
			session.SetPlanState(PlanState{Steps: []PlanStep{{Step: "Implement the requested change", Status: status}}})
			r := NewRunner(Config{Driver: driver, Session: session, MaxSteps: 5})

			if err := r.Run(context.Background(), "continue"); err != nil {
				t.Fatalf("err = %v, want final answer accepted after bounded gate nudges", err)
			}
			if !sessionHistoryContains(session, "plan state inconsistent", "Implement the requested change") {
				t.Fatalf("history missing plan feedback: %#v", session.Snapshot().History)
			}
		})
	}
}

func TestRunnerPlanStateGateNoPlanStateLeavesFinalBehaviorUnchanged(t *testing.T) {
	driver := &nativeSequenceDriver{steps: [][]llm.Token{{{Text: "Done, implemented the requested change."}}}}
	session := NewSession()
	r := NewRunner(Config{Driver: driver, Session: session})

	if err := r.Run(context.Background(), "continue"); err != nil {
		t.Fatal(err)
	}
	if got := r.LastResponse(); got != "Done, implemented the requested change." {
		t.Fatalf("LastResponse = %q", got)
	}
}

func TestRunnerStaleFinalOutputNotAppendedAfterValidation(t *testing.T) {
	session := NewSession()
	turn := session.RecordInput("answer")
	r := NewRunner(Config{Session: session})
	_, cancel, err := session.BeginTurn(context.Background(), "turn-1")
	if err != nil {
		t.Fatal(err)
	}

	ok, err := r.validateFinalCompletion(context.Background(), turn, "Done.", false)
	if !ok || err != nil {
		t.Fatalf("ok, err = %v, %v; want initial validation success", ok, err)
	}
	cancel()
	before := session.Snapshot()
	err = r.appendFinalAssistantMessageAndCompleteTurn(context.Background(), turn, "Done.", nil)

	if !errors.Is(err, ErrStaleTurn) {
		t.Fatalf("err = %v; want stale turn", err)
	}
	after := session.Snapshot()
	if len(after.History) != len(before.History) {
		t.Fatalf("stale final append mutated history: before=%#v after=%#v", before.History, after.History)
	}
}

func TestRunnerFinalValidationRequiresActiveMatchingTurn(t *testing.T) {
	session := NewSession()
	turn := session.RecordInput("answer")
	r := NewRunner(Config{Session: session})

	ok, err := r.validateFinalCompletion(context.Background(), turn, "Done.", false)

	if ok || !errors.Is(err, ErrStaleTurn) {
		t.Fatalf("ok, err = %v, %v; want stale turn", ok, err)
	}
}

func hookOverlayContent(output hooks.ExecutionOutput, key string) string {
	for _, overlay := range output.Overlays {
		if overlay.Key == key {
			return overlay.Content
		}
	}
	return ""
}

func promptHookOutputForSnapshot(t *testing.T, snap SessionSnapshot) hooks.ExecutionOutput {
	t.Helper()
	return newLoopHookRegistry().Dispatch(context.Background(), hooks.Event{
		Point:    hooks.PointPromptContext,
		Snapshot: snap,
	})
}

func TestRunnerAllowsNonNativeDriverWithTaskState(t *testing.T) {
	// A plain scriptedDriver does NOT implement NativeToolCaller.
	// With no forced tool requirements, it falls back to plain text mode.
	driver := &scriptedDriver{responses: []string{"some response"}}
	session := NewSession()
	session.SetTaskState(TaskState{
		Objective:            "whats this repo all about",
		Operation:            "overview",
		RequiredVerification: "inspect the repository with read/search tools before answering",
	})
	r := NewRunner(Config{Driver: driver, Session: session})
	if err := r.Run(context.Background(), "whats this repo all about"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := r.LastResponse(); got != "some response" {
		t.Fatalf("last response = %q, want some response", got)
	}
}

func TestRunnerAllowsPlainChatWithoutNativeToolCaller(t *testing.T) {
	driver := &scriptedDriver{responses: []string{"plain answer"}}
	r := NewRunner(Config{Driver: driver})

	if err := r.Run(context.Background(), "hello there"); err != nil {
		t.Fatal(err)
	}
	if got := r.LastResponse(); got != "plain answer" {
		t.Fatalf("last response = %q, want plain answer", got)
	}
}

func TestRunnerDoesNotBypassModelForAmbiguousReportCreation(t *testing.T) {
	session := NewSession()
	turn := session.RecordInput("what's broken?")
	mustCompleteTurn(t, session, turn, "prior answer", nil, nil)
	driver := &nativeScriptedDriver{responses: []string{"model handled it"}}
	reg := agenttools.NewRegistry()
	reg.Register(agenttools.Tool{
		Name:        "write_file",
		Description: "write file",
		AutoApprove: true,
		Execute: func(context.Context, map[string]any) (string, error) {
			t.Fatal("write_file should not be called directly")
			return "", nil
		},
	})
	r := NewRunner(Config{Driver: driver, Tools: reg, Session: session})

	if err := r.Run(context.Background(), "create a report for that bug"); err != nil {
		t.Fatal(err)
	}
	if driver.callCount != 1 {
		t.Fatalf("driver calls = %d, want 1", driver.callCount)
	}
	if got := r.LastResponse(); got != "model handled it" {
		t.Fatalf("last response = %q, want model handled it", got)
	}
}

func TestRunnerDoesNotBypassModelWhenPriorAnswerReferenceHasNoSaveTarget(t *testing.T) {
	session := NewSession()
	turn := session.RecordInput("summarize report risks")
	mustCompleteTurn(t, session, turn, "prior answer", nil, nil)
	driver := &nativeScriptedDriver{responses: []string{"model handled it"}}
	reg := agenttools.NewRegistry()
	reg.Register(agenttools.Tool{
		Name:        "write_file",
		Description: "write file",
		AutoApprove: true,
		Execute: func(context.Context, map[string]any) (string, error) {
			t.Fatal("write_file should not be called directly")
			return "", nil
		},
	})
	r := NewRunner(Config{Driver: driver, Tools: reg, Session: session})

	if err := r.Run(context.Background(), "write the answer about the report"); err != nil {
		t.Fatal(err)
	}
	if driver.callCount != 1 {
		t.Fatalf("driver calls = %d, want 1", driver.callCount)
	}
}

func TestRunnerDoesNotBypassModelWhenPriorAnswerReferenceIsNotWriteObject(t *testing.T) {
	for _, input := range []string{
		"write about it in prose",
		"write an answer in chat",
	} {
		t.Run(input, func(t *testing.T) {
			session := NewSession()
			turn := session.RecordInput("summarize report risks")
			mustCompleteTurn(t, session, turn, "prior answer", nil, nil)
			driver := &nativeScriptedDriver{responses: []string{"model handled it"}}
			reg := agenttools.NewRegistry()
			reg.Register(agenttools.Tool{
				Name:        "write_file",
				Description: "write file",
				AutoApprove: true,
				Execute: func(context.Context, map[string]any) (string, error) {
					t.Fatal("write_file should not be called directly")
					return "", nil
				},
			})
			r := NewRunner(Config{Driver: driver, Tools: reg, Session: session})

			if err := r.Run(context.Background(), input); err != nil {
				t.Fatal(err)
			}
			if driver.callCount != 1 {
				t.Fatalf("driver calls = %d, want 1", driver.callCount)
			}
		})
	}
}

func TestRunnerDoesNotBypassModelForMarkdownContentRequests(t *testing.T) {
	for _, input := range []string{
		"write markdown",
		"write the markdown",
		"write it to me in markdown",
	} {
		t.Run(input, func(t *testing.T) {
			session := NewSession()
			turn := session.RecordInput("summarize report risks")
			mustCompleteTurn(t, session, turn, "prior answer", nil, nil)
			driver := &nativeScriptedDriver{responses: []string{"model handled it"}}
			reg := agenttools.NewRegistry()
			reg.Register(agenttools.Tool{
				Name:        "write_file",
				Description: "write file",
				AutoApprove: true,
				Execute: func(context.Context, map[string]any) (string, error) {
					t.Fatal("write_file should not be called directly")
					return "", nil
				},
			})
			r := NewRunner(Config{Driver: driver, Tools: reg, Session: session})

			if err := r.Run(context.Background(), input); err != nil {
				t.Fatal(err)
			}
			if driver.callCount != 1 {
				t.Fatalf("driver calls = %d, want 1", driver.callCount)
			}
		})
	}
}

func TestReadOutputMissingHandleErrorIsModelCorrectable(t *testing.T) {
	session := NewSession()
	turn := session.RecordInput("read delegated output")
	active, cancel, err := session.BeginTurn(context.Background(), "turn-1")
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	reg := agenttools.NewRegistry()
	reg.Register(agenttools.Tool{
		Name:        "read_output",
		Description: "read output",
		Parameters:  []agenttools.ParameterDef{{Name: "handle", Type: "string", Required: true}},
		Execute: func(context.Context, map[string]any) (string, error) {
			return "", errors.New(`read output handle "session/missing": lstat /tmp/session/missing: no such file or directory`)
		},
	})
	r := NewRunner(Config{Tools: reg, Session: session})

	if err := r.executeNativeToolCalls(active.Context, turn, []llm.NativeToolCall{{ID: "read-1", Name: "read_output", ArgsJSON: `{"handle":"session/missing"}`}}); err != nil {
		t.Fatal(err)
	}
	if !sessionHistoryContains(session, "read output handle", "no such file or directory") {
		t.Fatalf("history missing correctable read_output error: %#v", session.Snapshot().History)
	}
}

func TestMalformedToolArgSchemaAliasFeedsRepeatLoopDetector(t *testing.T) {
	session := NewSession()
	turn := session.RecordInput("read files")
	active, cancel, err := session.BeginTurn(context.Background(), "turn-1")
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	reg := agenttools.NewRegistry()
	reg.Register(agenttools.Tool{
		Name:        "read_file",
		Description: "read file",
		Parameters:  []agenttools.ParameterDef{{Name: "path", Type: "string", Required: true}},
		Execute: func(context.Context, map[string]any) (string, error) {
			t.Fatal("malformed read_file args should not execute")
			return "", nil
		},
	})
	r := NewRunner(Config{Tools: reg, Session: session})
	call := llm.NativeToolCall{ID: "read-1", Name: "read_file", ArgsJSON: `{"filePath":"README.md","summary":true}`}

	for i := 0; i < repeatToolCallThreshold; i++ {
		call.ID = fmt.Sprintf("read-%d", i)
		if err := r.executeNativeToolCalls(active.Context, turn, []llm.NativeToolCall{call}); err != nil {
			t.Fatal(err)
		}
	}

	if r.repeatWorkflow.streak < repeatToolCallThreshold || r.repeatWorkflow.lastTarget != "README.md" {
		t.Fatalf("repeat workflow = %#v, want malformed schema alias tracked", r.repeatWorkflow)
	}
	if got := r.repeatWorkflow.overlayContent(repeatToolCallThreshold); !strings.Contains(got, "Loop detection") {
		t.Fatalf("repeat overlay = %q, want loop detection", got)
	}
}

func TestRunnerPreservesFailedChildCauseWhenParentResponseEmpty(t *testing.T) {
	session := NewSession()
	session.UpsertAgentTask(AgentTaskState{
		ID:         "agent-1",
		Role:       "explorer",
		Status:     AgentStatusFailed,
		Result:     "opencode-go stream failed: unexpected end of JSON input",
		ParentTurn: 1,
	})
	driver := &nativeSequenceDriver{steps: repeatedTokenSteps(nil, maxCompletionRetriesPerTurn+1)}
	r := NewRunner(Config{Driver: driver, Session: session})

	err := r.Run(context.Background(), "continue")
	if err == nil || !strings.Contains(err.Error(), "opencode-go stream failed") {
		t.Fatalf("err = %v, want failed child cause", err)
	}
}

func TestRunnerReadOnlyDelegationSummaryStillAllowed(t *testing.T) {
	session := NewSession()
	turn := session.RecordInput("ask agent to inspect the repo")
	session.UpsertAgentTask(AgentTaskState{
		ID:         "agent-1",
		Role:       "repo-auditor",
		Status:     AgentStatusCompleted,
		Result:     "Repo inspection found no required writes.",
		ParentTurn: turn,
	})
	r := NewRunner(Config{Session: session})
	_, cancel, err := session.BeginTurn(context.Background(), "turn-1")
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()

	handled, err := r.tryCompletedAgentResultFallbackAfterError(context.Background(), turn)
	if !handled || err != nil {
		t.Fatalf("handled, err = %v, %v", handled, err)
	}
	if got := r.LastResponse(); !strings.Contains(got, "Repo inspection found no required writes") {
		t.Fatalf("LastResponse = %q, want read-only child summary", got)
	}
}

// nativeToolCallDriver simulates a provider that returns a native tool call
// on the first invocation and a plain text response on subsequent invocations.
type nativeToolCallDriver struct {
	callCount int
	lastTools []llm.ToolDef
	lastMsgs  []llm.Message
	lastOpts  []llm.NativeToolOptions
	usage     llm.Usage
}

func (d *nativeToolCallDriver) Name() string { return "native-tool-driver" }

func (d *nativeToolCallDriver) LastUsage() llm.Usage { return d.usage }

func (d *nativeToolCallDriver) Stream(_ context.Context, _ []llm.Message, out chan<- llm.Token) error {
	close(out)
	return errors.New("Stream should not be called on a NativeToolCaller driver")
}

func (d *nativeToolCallDriver) StreamWithTools(_ context.Context, msgs []llm.Message, tools []llm.ToolDef, out chan<- llm.Token) error {
	return d.StreamWithToolsOptions(context.Background(), msgs, tools, llm.NativeToolOptions{}, out)
}

func (d *nativeToolCallDriver) StreamWithToolsOptions(_ context.Context, msgs []llm.Message, tools []llm.ToolDef, opts llm.NativeToolOptions, out chan<- llm.Token) error {
	defer close(out)
	d.callCount++
	d.lastTools = tools
	d.lastMsgs = msgs
	d.lastOpts = append(d.lastOpts, opts)
	switch d.callCount {
	case 1:
		d.usage = llm.Usage{InputTokens: 100, OutputTokens: 20}
		out <- llm.Token{ToolCall: &llm.NativeToolCall{ID: "c1", Name: "git_status", ArgsJSON: `{}`}}
	default:
		d.usage = llm.Usage{InputTokens: 120, OutputTokens: 30}
		out <- llm.Token{Text: "No changes detected."}
	}
	return nil
}

type promptPressureDriver struct {
	maxToolContentBytes int
	sawCompactedTool    bool
	callCount           int
	toolCallsBeforeDone int
}

func (d *promptPressureDriver) Name() string { return "prompt-pressure-driver" }

func (d *promptPressureDriver) Stream(_ context.Context, _ []llm.Message, out chan<- llm.Token) error {
	close(out)
	return errors.New("Stream should not be called on a NativeToolCaller driver")
}

func (d *promptPressureDriver) StreamWithTools(_ context.Context, msgs []llm.Message, tools []llm.ToolDef, out chan<- llm.Token) error {
	return d.StreamWithToolsOptions(context.Background(), msgs, tools, llm.NativeToolOptions{}, out)
}

func (d *promptPressureDriver) StreamWithToolsOptions(_ context.Context, msgs []llm.Message, _ []llm.ToolDef, _ llm.NativeToolOptions, out chan<- llm.Token) error {
	defer close(out)
	d.callCount++
	toolBytes := 0
	for _, msg := range msgs {
		if msg.Role != llm.RoleTool {
			continue
		}
		toolBytes += len(msg.Content)
		if strings.Contains(msg.Content, "tool result compacted") {
			d.sawCompactedTool = true
		}
	}
	if toolBytes > d.maxToolContentBytes {
		d.maxToolContentBytes = toolBytes
	}
	if d.callCount <= d.toolCallsBeforeDone {
		out <- llm.Token{ToolCall: &llm.NativeToolCall{ID: fmt.Sprintf("status-%d", d.callCount), Name: "git_status", ArgsJSON: `{}`}}
		return nil
	}
	out <- llm.Token{Text: "Finished after bounded tool context."}
	return nil
}

func TestRunnerRendersToolCallAndStatsBeforeToolExecution(t *testing.T) {
	driver := &nativeToolCallDriver{}
	renderer := &recordingRenderer{}
	reg := agenttools.NewRegistry()
	reg.Register(agenttools.Tool{
		Name:        "git_status",
		Description: "git status",
		AutoApprove: true,
		Execute: func(_ context.Context, _ map[string]any) (string, error) {
			renderer.mu.Lock()
			defer renderer.mu.Unlock()
			if !slices.Contains(renderer.events, "tool_call") {
				t.Fatalf("tool execution started before tool_call was rendered; events=%v", renderer.events)
			}
			if !slices.Contains(renderer.events, "stats") {
				t.Fatalf("tool execution started before stats were emitted; events=%v", renderer.events)
			}
			return "nothing to commit", nil
		},
	})
	r := NewRunner(Config{
		Driver:   driver,
		Tools:    reg,
		Session:  NewSession(),
		Renderer: renderer,
	})

	if err := r.Run(context.Background(), "check the repo"); err != nil {
		t.Fatal(err)
	}
}

func TestRunnerPersistsStatsItem(t *testing.T) {
	driver := &nativeToolCallDriver{}
	reg := agenttools.NewRegistry()
	reg.Register(agenttools.Tool{Name: "git_status", Description: "git status", AutoApprove: true, Execute: func(context.Context, map[string]any) (string, error) {
		return "nothing to commit", nil
	}})
	session := NewSession()
	r := NewRunner(Config{Driver: driver, Tools: reg, Session: session, Renderer: &recordingRenderer{}})
	if err := r.Run(context.Background(), "check the repo"); err != nil {
		t.Fatal(err)
	}
	toolCalls := 0
	for _, item := range session.Snapshot().Items {
		if item.Kind == protocol.ItemToolCall {
			toolCalls++
		}
		if item.Kind == protocol.ItemStats && item.Stats != nil && item.Stats.Usage.InputTokens > 0 {
			if toolCalls != 1 {
				t.Fatalf("tool call items = %d, want 1; items=%#v", toolCalls, session.Snapshot().Items)
			}
			return
		}
	}
	t.Fatalf("stats item not found: %#v", session.Snapshot().Items)
}

type nativeToolCallWithPreambleDriver struct {
	callCount int
}

func (d *nativeToolCallWithPreambleDriver) Name() string { return "native-tool-driver-with-preamble" }

func (d *nativeToolCallWithPreambleDriver) Stream(_ context.Context, _ []llm.Message, out chan<- llm.Token) error {
	close(out)
	return errors.New("Stream should not be called on a NativeToolCaller driver")
}

func (d *nativeToolCallWithPreambleDriver) StreamWithTools(_ context.Context, _ []llm.Message, _ []llm.ToolDef, out chan<- llm.Token) error {
	defer close(out)
	d.callCount++
	switch d.callCount {
	case 1:
		out <- llm.Token{Text: "I'll inspect the README first."}
		out <- llm.Token{ToolCall: &llm.NativeToolCall{ID: "c1", Name: "read_file", ArgsJSON: `{"path":"README.md"}`}}
	default:
		out <- llm.Token{Text: "The repo is a terminal-first coding agent."}
	}
	return nil
}

type nativeToolCallSecretPreambleDriver struct {
	callCount int
	secret    string
}

func (d *nativeToolCallSecretPreambleDriver) Name() string {
	return "native-tool-driver-secret-preamble"
}

func (d *nativeToolCallSecretPreambleDriver) Stream(_ context.Context, _ []llm.Message, out chan<- llm.Token) error {
	close(out)
	return errors.New("Stream should not be called on a NativeToolCaller driver")
}

func (d *nativeToolCallSecretPreambleDriver) StreamWithTools(_ context.Context, _ []llm.Message, _ []llm.ToolDef, out chan<- llm.Token) error {
	defer close(out)
	d.callCount++
	switch d.callCount {
	case 1:
		out <- llm.Token{Text: "using " + d.secret}
		out <- llm.Token{ToolCall: &llm.NativeToolCall{ID: "c1", Name: "read_file", ArgsJSON: `{"path":"README.md"}`}}
	default:
		out <- llm.Token{Text: "done"}
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

func TestRunnerStatsIncludePromptContextEstimate(t *testing.T) {
	driver := &nativeToolCallDriver{}
	renderer := &recordingRenderer{}
	reg := agenttools.NewRegistry()
	reg.Register(agenttools.Tool{
		Name:        "git_status",
		Description: "git status",
		AutoApprove: true,
		Execute: func(context.Context, map[string]any) (string, error) {
			return "clean", nil
		},
	})
	r := NewRunner(Config{
		Driver:   driver,
		Tools:    reg,
		Renderer: renderer,
		SystemPrompt: func() string {
			return "system prompt with enough content to estimate"
		},
	})

	if err := r.Run(context.Background(), "inspect repository state"); err != nil {
		t.Fatal(err)
	}
	if len(renderer.statsCtx) == 0 {
		t.Fatal("expected stats events to include prompt context estimates")
	}
	for i, got := range renderer.statsCtx {
		if got <= 0 {
			t.Fatalf("stats context estimate %d = %d, want positive", i, got)
		}
	}
}

func TestRunnerProactivelyCompactsSameTurnToolResultsBeforeProviderCall(t *testing.T) {
	driver := &promptPressureDriver{toolCallsBeforeDone: 12}
	reg := agenttools.NewRegistry()
	largeButBelowSingleResultThreshold := strings.Repeat("x", 40*1024)
	reg.Register(agenttools.Tool{
		Name:        "git_status",
		Description: "git status",
		AutoApprove: true,
		Execute: func(context.Context, map[string]any) (string, error) {
			return largeButBelowSingleResultThreshold, nil
		},
	})
	r := NewRunner(Config{Driver: driver, Tools: reg, Session: NewSession()})

	if err := r.Run(context.Background(), "check the repo repeatedly"); err != nil {
		t.Fatal(err)
	}
	if !driver.sawCompactedTool {
		t.Fatal("provider never saw compacted tool-result context during long same-turn loop")
	}
	if driver.maxToolContentBytes > 256*1024 {
		t.Fatalf("max tool content sent to provider = %d bytes, want bounded below 256KiB", driver.maxToolContentBytes)
	}
}

func TestRunnerCreatesOneCheckpointBeforeMutatingTools(t *testing.T) {
	root := initReactTestGitRepo(t)
	t.Chdir(root)
	writeReactTestFile(t, filepath.Join(root, "README.md"), "original\n")
	runReactGit(t, root, "add", "README.md")
	runReactGit(t, root, "commit", "-m", "initial")

	reg := agenttools.NewRegistry()
	var runCount atomic.Int32
	reg.Register(agenttools.Tool{
		Name:        "write_file",
		Description: "write file",
		AutoApprove: true,
		Execute: func(_ context.Context, _ map[string]any) (string, error) {
			runCount.Add(1)
			return "ok", nil
		},
	})
	session := NewSession()
	active, cancel, err := session.BeginTurn(context.Background(), "turn-1")
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	r := NewRunner(Config{Tools: reg, Session: session})

	err = r.executeNativeToolCalls(active.Context, active.Number, []llm.NativeToolCall{
		{ID: "write-1", Name: "write_file", ArgsJSON: `{"path":"README.md","content":"one"}`},
		{ID: "write-2", Name: "write_file", ArgsJSON: `{"path":"README.md","content":"two"}`},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := runCount.Load(); got != 2 {
		t.Fatalf("write_file executions = %d, want 2", got)
	}

	items := session.Snapshot().Items
	checkpointCount := 0
	firstToolCall := -1
	firstCheckpoint := -1
	for i, item := range items {
		if item.Kind == protocol.ItemToolCall && firstToolCall < 0 {
			firstToolCall = i
		}
		if item.Kind == protocol.ItemCheckpoint {
			checkpointCount++
			if firstCheckpoint < 0 {
				firstCheckpoint = i
			}
			if item.Checkpoint == nil || item.Checkpoint.ID == "" || item.Checkpoint.Phase != "created" {
				t.Fatalf("checkpoint item = %#v", item.Checkpoint)
			}
		}
	}
	if checkpointCount != 1 {
		t.Fatalf("checkpoint items = %d, want 1; items=%#v", checkpointCount, items)
	}
	if firstCheckpoint < 0 || firstToolCall < 0 || firstCheckpoint > firstToolCall {
		t.Fatalf("checkpoint index = %d, first tool call index = %d; want checkpoint before tool call", firstCheckpoint, firstToolCall)
	}
}

func TestRunnerReportsCheckpointDurableAppendError(t *testing.T) {
	root := initReactTestGitRepo(t)
	t.Chdir(root)
	writeReactTestFile(t, filepath.Join(root, "README.md"), "original\n")
	runReactGit(t, root, "add", "README.md")
	runReactGit(t, root, "commit", "-m", "initial")

	session := NewSession()
	session.SetDurableSink(failingDurableSink{err: errors.New("durable append failed")})
	active, cancel, err := session.BeginTurn(context.Background(), "turn-1")
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	var progress []string
	r := NewRunner(Config{
		Session: session,
		Progress: func(message string) {
			progress = append(progress, message)
		},
	})

	r.ensurePreMutationCheckpoint(context.Background(), active.Number)

	if len(progress) != 1 || !strings.Contains(progress[0], "checkpoint item was not persisted") {
		t.Fatalf("progress = %#v, want checkpoint persistence warning", progress)
	}
}

func TestRunnerRecordsCheckpointScopeForWriteFile(t *testing.T) {
	root := initReactTestGitRepo(t)
	t.Chdir(root)
	writeReactTestFile(t, filepath.Join(root, "a.txt"), "a original\n")
	writeReactTestFile(t, filepath.Join(root, "b.txt"), "b original\n")
	runReactGit(t, root, "add", "a.txt", "b.txt")
	runReactGit(t, root, "commit", "-m", "initial")

	reg := agenttools.NewRegistry()
	reg.Register(agenttools.Tool{
		Name:        "write_file",
		Description: "write file",
		AutoApprove: true,
		Execute: func(_ context.Context, args map[string]any) (string, error) {
			path, _ := args["path"].(string)
			content, _ := args["content"].(string)
			writeReactTestFile(t, filepath.Join(root, path), content)
			return "ok", nil
		},
	})
	session := NewSession()
	active, cancel, err := session.BeginTurn(context.Background(), "turn-1")
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	r := NewRunner(Config{Tools: reg, Session: session})

	if err := r.executeNativeToolCalls(active.Context, active.Number, []llm.NativeToolCall{{ID: "write-1", Name: "write_file", ArgsJSON: `{"path":"a.txt","content":"turn mutation\n"}`}}); err != nil {
		t.Fatal(err)
	}
	writeReactTestFile(t, filepath.Join(root, "b.txt"), "unrelated user mutation\n")

	var checkpointID string
	for _, item := range session.Snapshot().Items {
		if item.Checkpoint != nil {
			checkpointID = item.Checkpoint.ID
		}
	}
	if checkpointID == "" {
		t.Fatal("missing checkpoint item")
	}
	if err := r.checkpointManager.Restore(context.Background(), checkpointID); err != nil {
		t.Fatalf("Restore() error = %v", err)
	}
	if got := readReactTestFile(t, filepath.Join(root, "a.txt")); got != "a original\n" {
		t.Fatalf("a.txt = %q, want checkpoint content", got)
	}
	if got := readReactTestFile(t, filepath.Join(root, "b.txt")); got != "unrelated user mutation\n" {
		t.Fatalf("b.txt = %q, want unrelated post-checkpoint content", got)
	}
}

func TestRunnerAppendsPostEditDiagnosticsAfterSuccessfulMutatingTool(t *testing.T) {
	reg := agenttools.NewRegistry()
	reg.Register(agenttools.Tool{
		Name:             "write_file",
		Description:      "write file",
		AutoApprove:      true,
		MutatesWorkspace: true,
		LastDiff: func() string {
			return "diff --git a/a.txt b/a.txt"
		},
		Execute: func(_ context.Context, _ map[string]any) (string, error) {
			return "ok", nil
		},
	})
	session := NewSession()
	active, cancel, err := session.BeginTurn(context.Background(), "turn-1")
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	r := NewRunner(Config{
		Tools:   reg,
		Session: session,
		PostEditValidator: &PostEditValidator{
			Command:        []string{"/bin/sh", "-c", "printf 'go test failed' >&2; exit 1"},
			Timeout:        time.Second,
			MaxOutputBytes: 1024,
		},
	})

	if err := r.executeNativeToolCalls(active.Context, active.Number, []llm.NativeToolCall{{ID: "write-1", Name: "write_file", ArgsJSON: `{"path":"a.txt","content":"x"}`}}); err != nil {
		t.Fatal(err)
	}

	var found bool
	for _, msg := range session.Snapshot().History {
		if msg.Role == llm.RoleUser && strings.Contains(msg.Content, "Runtime diagnostic feedback") && strings.Contains(msg.Content, "Post-edit validation failed") && strings.Contains(msg.Content, "go test failed") {
			found = true
		}
	}
	if !found {
		t.Fatalf("session history missing post-edit validation feedback: %#v", session.Snapshot().History)
	}
}

func TestRunnerSkipsPostEditValidationWithoutMutationSignal(t *testing.T) {
	root := t.TempDir()
	marker := filepath.Join(root, "validator-ran")
	reg := agenttools.NewRegistry()
	reg.Register(agenttools.Tool{
		Name:             "edit_file",
		Description:      "edit file",
		AutoApprove:      true,
		MutatesWorkspace: true,
		Execute: func(_ context.Context, _ map[string]any) (string, error) {
			return "edit_file failed: old_text not found in a.txt", nil
		},
	})
	session := NewSession()
	active, cancel, err := session.BeginTurn(context.Background(), "turn-1")
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	r := NewRunner(Config{
		Tools:   reg,
		Session: session,
		PostEditValidator: &PostEditValidator{
			Command:        []string{"/bin/sh", "-c", fmt.Sprintf("printf ran > %q; exit 1", marker)},
			Timeout:        time.Second,
			MaxOutputBytes: 1024,
		},
	})

	if err := r.executeNativeToolCalls(active.Context, active.Number, []llm.NativeToolCall{{ID: "edit-1", Name: "edit_file", ArgsJSON: `{"path":"a.txt","old_text":"old","new_text":"new"}`}}); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("validator marker stat error = %v, want not exist", err)
	}
}

func TestRunnerRunsPostEditValidationWithMutationDiff(t *testing.T) {
	reg := agenttools.NewRegistry()
	reg.Register(agenttools.Tool{
		Name:             "edit_file",
		Description:      "edit file",
		AutoApprove:      true,
		MutatesWorkspace: true,
		LastDiff: func() string {
			return "diff --git a/a.txt b/a.txt"
		},
		Execute: func(_ context.Context, _ map[string]any) (string, error) {
			return "edited a.txt", nil
		},
	})
	session := NewSession()
	active, cancel, err := session.BeginTurn(context.Background(), "turn-1")
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	r := NewRunner(Config{
		Tools:   reg,
		Session: session,
		PostEditValidator: &PostEditValidator{
			Command:        []string{"/bin/sh", "-c", "printf validator-failed >&2; exit 1"},
			Timeout:        time.Second,
			MaxOutputBytes: 1024,
		},
	})

	if err := r.executeNativeToolCalls(active.Context, active.Number, []llm.NativeToolCall{{ID: "edit-1", Name: "edit_file", ArgsJSON: `{"path":"a.txt","old_text":"old","new_text":"new"}`}}); err != nil {
		t.Fatal(err)
	}

	var found bool
	for _, msg := range session.Snapshot().History {
		if msg.Role == llm.RoleUser && strings.Contains(msg.Content, "Runtime diagnostic feedback") && strings.Contains(msg.Content, "validator-failed") {
			found = true
		}
	}
	if !found {
		t.Fatalf("session history missing post-edit diagnostics: %#v", session.Snapshot().History)
	}
}

func TestRunnerRunsPostEditValidationAfterGitCommitSuccess(t *testing.T) {
	root := initReactTestGitRepo(t)
	writeReactTestFile(t, filepath.Join(root, "a.txt"), "a\n")
	runReactGit(t, root, "add", "a.txt")
	reg := agenttools.NewRegistry()
	reg.Register(agenttools.NewGitCommit(root, func(agenttools.Action) (bool, error) { return true, nil }))
	session := NewSession()
	active, cancel, err := session.BeginTurn(context.Background(), "turn-1")
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	r := NewRunner(Config{
		Tools:   reg,
		Session: session,
		PostEditValidator: &PostEditValidator{
			Command:        []string{"/bin/sh", "-c", "printf git-validator >&2; exit 1"},
			Timeout:        time.Second,
			MaxOutputBytes: 1024,
		},
	})

	if err := r.executeNativeToolCalls(active.Context, active.Number, []llm.NativeToolCall{{ID: "commit-1", Name: "git_commit", ArgsJSON: `{"message":"initial"}`}}); err != nil {
		t.Fatal(err)
	}

	if !sessionHistoryContains(session, "Runtime diagnostic feedback", "git-validator") {
		t.Fatalf("session history missing git commit validation diagnostics: %#v", session.Snapshot().History)
	}
}

func TestRunnerSkipsPostEditValidationAfterGitCommitNoopAndDenied(t *testing.T) {
	for _, tc := range []struct {
		name    string
		prepare func(t *testing.T, root string)
		approve bool
	}{
		{
			name: "nothing to commit",
			prepare: func(t *testing.T, root string) {
				writeReactTestFile(t, filepath.Join(root, "a.txt"), "a\n")
				runReactGit(t, root, "add", "a.txt")
				runReactGit(t, root, "commit", "-m", "initial")
			},
			approve: true,
		},
		{
			name: "denied",
			prepare: func(t *testing.T, root string) {
				writeReactTestFile(t, filepath.Join(root, "a.txt"), "a\n")
				runReactGit(t, root, "add", "a.txt")
			},
			approve: false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := initReactTestGitRepo(t)
			marker := filepath.Join(root, "validator-ran")
			tc.prepare(t, root)
			reg := agenttools.NewRegistry()
			reg.Register(agenttools.NewGitCommit(root, func(agenttools.Action) (bool, error) { return tc.approve, nil }))
			session := NewSession()
			active, cancel, err := session.BeginTurn(context.Background(), "turn-1")
			if err != nil {
				t.Fatal(err)
			}
			defer cancel()
			r := NewRunner(Config{
				Tools:   reg,
				Session: session,
				PostEditValidator: &PostEditValidator{
					Command:        []string{"/bin/sh", "-c", fmt.Sprintf("printf ran > %q; exit 1", marker)},
					Timeout:        time.Second,
					MaxOutputBytes: 1024,
				},
			})

			if err := r.executeNativeToolCalls(active.Context, active.Number, []llm.NativeToolCall{{ID: "commit-1", Name: "git_commit", ArgsJSON: `{"message":"initial"}`}}); err != nil {
				t.Fatal(err)
			}

			if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("validator marker stat error = %v, want not exist", err)
			}
		})
	}
}

func initReactTestGitRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	runReactGit(t, root, "init")
	runReactGit(t, root, "config", "user.email", "test@example.com")
	runReactGit(t, root, "config", "user.name", "Test User")
	return root
}

func runReactGit(t *testing.T, root string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s failed: %v\n%s", strings.Join(args, " "), err, out)
	}
}

func writeReactTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func readReactTestFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}

func sessionHistoryContains(session *Session, parts ...string) bool {
	for _, msg := range session.Snapshot().History {
		content := msg.Content
		matched := true
		for _, part := range parts {
			if !strings.Contains(content, part) {
				matched = false
				break
			}
		}
		if matched {
			return true
		}
	}
	return false
}

func TestRunnerDoesNotRetryAlreadyExhaustedStreamErrorAfterSuccessfulTool(t *testing.T) {
	driver := &nativeSequenceDriver{
		steps: [][]llm.Token{
			{{ToolCall: &llm.NativeToolCall{ID: "c1", Name: "run_command", ArgsJSON: `{"command":"mkdir -p ~/git/panamanian_hats && git init"}`}}},
		},
		errs: []error{
			nil,
			&llm.RetryAttemptsExhaustedError{Attempts: 3, Err: errors.New("stream idle timeout after 30s")},
			&llm.RetryAttemptsExhaustedError{Attempts: 3, Err: errors.New("stream idle timeout after 30s")},
			&llm.RetryAttemptsExhaustedError{Attempts: 3, Err: errors.New("stream idle timeout after 30s")},
			&llm.RetryAttemptsExhaustedError{Attempts: 3, Err: errors.New("stream idle timeout after 30s")},
		},
	}
	reg := agenttools.NewRegistry()
	reg.Register(agenttools.Tool{
		Name:        "run_command",
		Description: "run shell command",
		Parameters:  []agenttools.ParameterDef{{Name: "command", Type: "string", Required: true}},
		AutoApprove: true,
		Execute: func(context.Context, map[string]any) (string, error) {
			return "Initialized empty Git repository\nexit 0", nil
		},
	})
	rec := &recordingRenderer{}
	r := NewRunner(Config{Driver: driver, Tools: reg, Session: NewSession(), Renderer: rec})

	err := r.Run(context.Background(), "run command to create a new git repo")
	if err == nil {
		t.Fatal("expected exhausted stream error")
	}
	if driver.callCount != 2 {
		t.Fatalf("driver calls = %d, want one tool call and one exhausted post-tool completion", driver.callCount)
	}
	if len(rec.retryTexts) != 0 {
		t.Fatalf("retry notices = %#v, want none after retry driver already exhausted attempts", rec.retryTexts)
	}
	snap := r.session.Snapshot()
	foundToolResult := false
	for _, msg := range snap.History {
		if msg.Role == llm.RoleTool && strings.Contains(msg.Content, "Initialized empty Git repository") {
			foundToolResult = true
		}
	}
	if !foundToolResult {
		t.Fatalf("history missing successful tool result: %#v", snap.History)
	}
}

func TestRunnerStillRetriesExhaustedServerErrors(t *testing.T) {
	driver := &nativeSequenceDriver{
		steps: [][]llm.Token{
			{},
			{{Text: "recovered after server retry"}},
		},
		errs: []error{
			&llm.RetryAttemptsExhaustedError{Attempts: 3, Err: errors.New("500 server overloaded")},
		},
	}
	rec := &recordingRenderer{}
	r := NewRunner(Config{Driver: driver, Session: NewSession(), Renderer: rec})

	if err := r.Run(context.Background(), "hello"); err != nil {
		t.Fatal(err)
	}
	if driver.callCount != 2 {
		t.Fatalf("driver calls = %d, want exhausted server error plus retry", driver.callCount)
	}
	if got := r.LastResponse(); got != "recovered after server retry" {
		t.Fatalf("last response = %q", got)
	}
	if len(rec.retryTexts) == 0 {
		t.Fatal("expected retry notice for exhausted server error")
	}
}

func TestRunnerPreservesAssistantPreambleOnToolTurn(t *testing.T) {
	driver := &nativeToolCallWithPreambleDriver{}
	reg := agenttools.NewRegistry()
	reg.Register(agenttools.Tool{
		Name:        "read_file",
		Description: "read file",
		AutoApprove: true,
		Execute: func(_ context.Context, _ map[string]any) (string, error) {
			return "Forge is a terminal-first coding agent.", nil
		},
	})
	session := NewSession()
	rec := &recordingRenderer{}
	r := NewRunner(Config{
		Driver:   driver,
		Tools:    reg,
		Session:  session,
		Renderer: rec,
	})

	if err := r.Run(context.Background(), "tell me about this repo"); err != nil {
		t.Fatal(err)
	}

	snap := session.Snapshot()
	if len(snap.History) < 2 {
		t.Fatalf("history = %#v", snap.History)
	}
	firstAssistant := snap.History[1]
	if got, want := firstAssistant.Content, "I'll inspect the README first."; got != want {
		t.Fatalf("assistant preamble = %q, want %q", got, want)
	}
	if len(firstAssistant.ToolCalls) != 1 || firstAssistant.ToolCalls[0].Name != "read_file" {
		t.Fatalf("assistant tool calls = %#v", firstAssistant.ToolCalls)
	}
	// The preamble streams as it is written rather than arriving as a block
	// once the step is over; withholding it made a working turn read as
	// silence between tool cards.
	streamed := strings.Join(rec.tokenTexts, "")
	if !strings.Contains(streamed, "I'll inspect the README first.") {
		t.Fatalf("preamble was not streamed: tokenTexts = %#v", rec.tokenTexts)
	}
}

func TestRunnerRedactsSecretPreambleOnToolTurn(t *testing.T) {
	secret := "TOKEN=" + strings.Repeat("x", 24)
	driver := &nativeToolCallSecretPreambleDriver{secret: secret}
	reg := agenttools.NewRegistry()
	reg.Register(agenttools.Tool{
		Name:        "read_file",
		Description: "read file",
		AutoApprove: true,
		Execute: func(_ context.Context, _ map[string]any) (string, error) {
			return "Forge is a terminal-first coding agent.", nil
		},
	})
	session := NewSession()
	rec := &recordingRenderer{}
	r := NewRunner(Config{
		Driver:   driver,
		Tools:    reg,
		Session:  session,
		Renderer: rec,
	})

	if err := r.Run(context.Background(), "tell me about this repo"); err != nil {
		t.Fatal(err)
	}

	snap := session.Snapshot()
	if strings.Contains(snap.History[1].Content, secret) {
		t.Fatal("stored assistant preamble leaked secret")
	}
	if !strings.Contains(snap.History[1].Content, "<REDACTED:generic-token>") {
		t.Fatal("stored assistant preamble missing redaction marker")
	}
	if len(rec.fullTexts) == 0 {
		t.Fatal("renderer did not receive assistant preamble")
	}
	rendered := strings.Join(rec.fullTexts, "\n")
	if strings.Contains(rendered, secret) {
		t.Fatal("rendered assistant preamble leaked secret")
	}
	if !strings.Contains(rendered, "<REDACTED:generic-token>") {
		t.Fatal("rendered assistant preamble missing redaction marker")
	}
}

type pendingInputDriver struct {
	callCount int
	lastMsgs  []llm.Message
}

func (d *pendingInputDriver) Name() string { return "pending-input-driver" }

func (d *pendingInputDriver) Stream(_ context.Context, _ []llm.Message, out chan<- llm.Token) error {
	close(out)
	return errors.New("Stream should not be called on a NativeToolCaller driver")
}

func (d *pendingInputDriver) StreamWithTools(_ context.Context, msgs []llm.Message, _ []llm.ToolDef, out chan<- llm.Token) error {
	defer close(out)
	d.callCount++
	d.lastMsgs = append([]llm.Message(nil), msgs...)
	switch d.callCount {
	case 1:
		out <- llm.Token{ToolCall: &llm.NativeToolCall{ID: "c1", Name: "git_status", ArgsJSON: `{}`}}
	default:
		lastUser := ""
		for i := len(msgs) - 1; i >= 0; i-- {
			if msgs[i].Role == llm.RoleUser {
				lastUser = msgs[i].Content
				break
			}
		}
		if lastUser != "steer toward tests" {
			out <- llm.Token{Text: "missing pending steer"}
			return nil
		}
		out <- llm.Token{Text: "handled queued steer after tool result"}
	}
	return nil
}

func TestRunnerConsumesQueuedPendingInputWithinActiveLoop(t *testing.T) {
	driver := &pendingInputDriver{}
	reg := agenttools.NewRegistry()
	session := NewSession()
	reg.Register(agenttools.Tool{
		Name:        "git_status",
		Description: "git status",
		AutoApprove: true,
		Execute: func(_ context.Context, _ map[string]any) (string, error) {
			session.QueuePendingInput("steer toward tests")
			return "working tree clean", nil
		},
	})
	reg.Register(agenttools.Tool{
		Name:        "spawn_agent",
		Description: "spawn agent",
		AutoApprove: true,
		Execute: func(_ context.Context, _ map[string]any) (string, error) {
			return "ok", nil
		},
	})
	r := NewRunner(Config{
		Driver:  driver,
		Tools:   reg,
		Session: session,
	})

	if err := r.Run(context.Background(), "inspect repo"); err != nil {
		t.Fatal(err)
	}
	if driver.callCount != 2 {
		t.Fatalf("driver calls = %d, want 2", driver.callCount)
	}
	if got := r.LastResponse(); got != "handled queued steer after tool result" {
		t.Fatalf("last response = %q", got)
	}
	snap := session.Snapshot()
	if len(snap.History) < 4 {
		t.Fatalf("history = %#v", snap.History)
	}
	foundQueuedUser := false
	for _, msg := range snap.History {
		if msg.Role == llm.RoleUser && msg.Content == "steer toward tests" {
			foundQueuedUser = true
			break
		}
	}
	if !foundQueuedUser {
		t.Fatalf("expected queued input in history, got %#v", snap.History)
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
	session := NewSession()
	session.SetTaskState(TaskState{
		Objective:            "whats this repo all about",
		Operation:            "overview",
		RequiredVerification: "inspect the repository with read/search tools before answering",
	})
	r := NewRunner(Config{
		Driver:  driver,
		Tools:   reg,
		Session: session,
		SystemPrompt: func() string {
			promptCalled = true
			return "native-prompt"
		},
	})
	_ = r.Run(context.Background(), "whats this repo all about")
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

	if err := r.Run(context.Background(), "hello"); err != nil {
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
	defer close(out)
	d.callCount++
	for _, chunk := range d.chunks {
		out <- llm.Token{Text: chunk}
	}
	return nil
}

func (d *nativeChunkedDriver) StreamWithTools(_ context.Context, _ []llm.Message, _ []llm.ToolDef, out chan<- llm.Token) error {
	defer close(out)
	d.callCount++
	for _, chunk := range d.chunks {
		out <- llm.Token{Text: chunk}
	}
	return nil
}

func TestRunnerRejectsLegacyXMLToolCallMarkupFromNativeProvider(t *testing.T) {
	responses := make([]string, maxCompletionRetriesPerTurn+1)
	for i := range responses {
		responses[i] = "<tool_call>\n{\"name\":\"list_dir\",\"args\":{\"path\":\".\"}}\n</tool_call>"
	}
	driver := &nativeScriptedDriver{responses: responses}
	reg := agenttools.NewRegistry()
	called := false
	reg.Register(agenttools.Tool{
		Name:        "list_dir",
		Description: "list a directory",
		Parameters:  []agenttools.ParameterDef{{Name: "path", Type: "string", Required: true}},
		AutoApprove: true,
		Execute: func(_ context.Context, _ map[string]any) (string, error) {
			called = true
			return "README.md\ninternal/", nil
		},
	})
	session := NewSession()
	session.SetTaskState(TaskState{
		Objective:            "whats this repo all about",
		Operation:            "overview",
		RequiredVerification: "inspect the repository with read/search tools before answering. For a casual repo overview, usually inspect the repo root and one high-signal file such as README.md, then give a brief overview grounded only in that evidence",
	})
	r := NewRunner(Config{Driver: driver, Tools: reg, Session: session})

	err := r.Run(context.Background(), "whats this repo all about")
	if err == nil {
		t.Fatal("expected runner error when provider emits legacy XML markup")
	}
	if !strings.Contains(err.Error(), "deprecated XML tool-call markup") {
		t.Fatalf("err = %v", err)
	}
	if called {
		t.Fatal("legacy XML markup should not execute a tool")
	}
}

func TestRunnerRecoversFromLegacyXMLToolCallMarkupFromNativeProvider(t *testing.T) {
	driver := &nativeScriptedDriver{responses: []string{
		"<tool_call>\n{\"name\":\"list_dir\",\"args\":{\"path\":\".\"}}\n</tool_call>",
		"recovered with native response",
	}}
	reg := agenttools.NewRegistry()
	called := false
	reg.Register(agenttools.Tool{
		Name:        "list_dir",
		Description: "list a directory",
		Parameters:  []agenttools.ParameterDef{{Name: "path", Type: "string", Required: true}},
		AutoApprove: true,
		Execute: func(_ context.Context, _ map[string]any) (string, error) {
			called = true
			return "README.md\ninternal/", nil
		},
	})
	r := NewRunner(Config{Driver: driver, Tools: reg, Session: NewSession()})

	if err := r.Run(context.Background(), "whats this repo all about"); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if called {
		t.Fatal("legacy XML markup should not execute a tool")
	}
	if got := r.LastResponse(); got != "recovered with native response" {
		t.Fatalf("LastResponse = %q", got)
	}
}

func TestRunnerRejectsSelfClosingXMLToolCallMarkupFromNativeProvider(t *testing.T) {
	responses := make([]string, maxCompletionRetriesPerTurn+1)
	for i := range responses {
		responses[i] = `<tool_call name="shell.exec" arguments='{"cmd":"ls"}' />`
	}
	driver := &nativeScriptedDriver{responses: responses}
	reg := agenttools.NewRegistry()
	called := false
	reg.Register(agenttools.Tool{
		Name:        "shell.exec",
		Description: "run a shell command",
		Parameters:  []agenttools.ParameterDef{{Name: "cmd", Type: "string", Required: true}},
		AutoApprove: true,
		Execute: func(_ context.Context, _ map[string]any) (string, error) {
			called = true
			return "README.md", nil
		},
	})
	r := NewRunner(Config{Driver: driver, Tools: reg, Session: NewSession()})

	err := r.Run(context.Background(), "list files")
	if err == nil {
		t.Fatal("expected runner error when provider emits self-closing XML tool markup")
	}
	if !strings.Contains(err.Error(), "deprecated XML tool-call markup") {
		t.Fatalf("err = %v", err)
	}
	if called {
		t.Fatal("self-closing XML tool markup should not execute a tool")
	}
}

func TestRunnerRejectsMalformedXMLToolCallMarkupFromNativeProvider(t *testing.T) {
	responses := make([]string, maxCompletionRetriesPerTurn+1)
	for i := range responses {
		responses[i] = `<tool_call>{"cmd":"ls"}</tool_call>`
	}
	driver := &nativeScriptedDriver{responses: responses}
	r := NewRunner(Config{Driver: driver, Tools: agenttools.NewRegistry(), Session: NewSession()})

	err := r.Run(context.Background(), "testing")
	if err == nil {
		t.Fatal("expected runner error when provider emits malformed XML tool markup")
	}
	if !strings.Contains(err.Error(), "deprecated XML tool-call markup") {
		t.Fatalf("err = %v", err)
	}
}

func TestRunnerRawToolMarkupDoesNotStreamSuspiciousPrefixesBeforeRejection(t *testing.T) {
	tests := []struct {
		name   string
		chunks []string
	}{
		{name: "DSML", chunks: []string{"<", "｜｜DSML｜｜tool_calls>", `[{"name":"bash","arguments":{"command":"ls"}}]`}},
		{name: "JSON", chunks: []string{"{", `"name":"bash",`, `"arguments":{"command":"ls"}}`}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			steps := make([][]llm.Token, maxCompletionRetriesPerTurn+1)
			for i := range steps {
				for _, chunk := range tt.chunks {
					steps[i] = append(steps[i], llm.Token{Text: chunk})
				}
			}
			driver := &nativeSequenceDriver{steps: steps}
			renderer := &recordingRenderer{}
			r := NewRunner(Config{Driver: driver, Renderer: renderer, Session: NewSession()})

			if err := r.Run(context.Background(), "list files"); err == nil {
				t.Fatal("expected raw markup to fail")
			}
			renderer.mu.Lock()
			defer renderer.mu.Unlock()
			if len(renderer.tokenTexts) != 0 {
				t.Fatalf("AgentToken chunks = %#v, want none for rejected raw markup", renderer.tokenTexts)
			}
			if len(renderer.fullTexts) != 0 {
				t.Fatalf("AgentText chunks = %#v, want none for rejected raw markup", renderer.fullTexts)
			}
		})
	}
}

func TestRunnerRawToolMarkupDoesNotStreamEmbeddedMarkupBeforeRejection(t *testing.T) {
	tests := []struct {
		name   string
		chunks []string
	}{
		{name: "XML", chunks: []string{"Please run ", "<tool_calls>", `<tool_call>{"name":"bash","arguments":{"command":"ls"}}</tool_call></tool_calls>`}},
		{name: "DSML", chunks: []string{"Please run ", "<｜｜DSML｜｜", `tool_calls>[{"name":"bash","arguments":{"command":"ls"}}]`}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			steps := make([][]llm.Token, maxCompletionRetriesPerTurn+1)
			for i := range steps {
				for _, chunk := range tt.chunks {
					steps[i] = append(steps[i], llm.Token{Text: chunk})
				}
			}
			driver := &nativeSequenceDriver{steps: steps}
			renderer := &recordingRenderer{}
			r := NewRunner(Config{Driver: driver, Renderer: renderer, Session: NewSession()})

			if err := r.Run(context.Background(), "list files"); err == nil {
				t.Fatal("expected raw markup to fail")
			}
			renderer.mu.Lock()
			defer renderer.mu.Unlock()
			streamed := strings.Join(renderer.tokenTexts, "")
			if strings.Contains(streamed, "<tool_call") || strings.Contains(streamed, "DSML") {
				t.Fatalf("AgentToken leaked raw markup: %#v", renderer.tokenTexts)
			}
			if len(renderer.fullTexts) != 0 {
				t.Fatalf("AgentText chunks = %#v, want none for rejected raw markup", renderer.fullTexts)
			}
		})
	}
}

func TestRunnerAllowsDSMLInvokeExplanationWithoutRawMarkupDelimiters(t *testing.T) {
	final := "The words DSML invoke can appear in documentation without being raw markup."
	driver := &nativeScriptedDriver{responses: []string{final}}
	r := NewRunner(Config{Driver: driver, Session: NewSession()})

	if err := r.Run(context.Background(), "explain DSML"); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if got := r.LastResponse(); got != final {
		t.Fatalf("LastResponse = %q", got)
	}
}

func TestRunnerAllowsRawToolMarkupLookingJSONCodeBlockFinalText(t *testing.T) {
	final := "Use this config:\n\n```json\n{\n  \"name\": \"bash\",\n  \"arguments\": {\"command\": \"ls\"}\n}\n```"
	driver := &nativeScriptedDriver{responses: []string{final}}
	r := NewRunner(Config{Driver: driver, Session: NewSession()})

	if err := r.Run(context.Background(), "show config"); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if got := r.LastResponse(); got != final {
		t.Fatalf("LastResponse = %q", got)
	}
}

func TestRunnerAllowsGenericFencedJSONFinalText(t *testing.T) {
	final := "Use this example:\n\n```json\n{\n  \"name\": \"example\",\n  \"enabled\": true\n}\n```"
	driver := &nativeScriptedDriver{responses: []string{final}}
	r := NewRunner(Config{Driver: driver, Session: NewSession()})

	if err := r.Run(context.Background(), "show json example"); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if got := r.LastResponse(); got != final {
		t.Fatalf("LastResponse = %q", got)
	}
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
	session := NewSession()
	session.SetTaskState(TaskState{
		Objective:            "whats this repo all about",
		Operation:            "overview",
		RequiredVerification: "inspect the repository with read/search tools before answering",
	})
	r := NewRunner(Config{
		Driver:       driver,
		Tools:        reg,
		Session:      session,
		SystemPrompt: func() string { return "  my system prompt  " },
	})
	_ = r.Run(context.Background(), "whats this repo all about")
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

func TestRunnerNativePathHandlesMalformedArgsJSONAsToolFeedback(t *testing.T) {
	driver := &nativeSequenceDriver{steps: [][]llm.Token{
		{{ToolCall: &llm.NativeToolCall{ID: "bad-json", Name: "read_file", ArgsJSON: `{"path":`}}},
		{{Text: "recovered after malformed args feedback"}},
	}}
	reg := agenttools.NewRegistry()
	executed := false
	reg.Register(agenttools.Tool{
		Name:        "read_file",
		Description: "read file",
		Parameters:  []agenttools.ParameterDef{{Name: "path", Type: "string", Required: true}},
		AutoApprove: true,
		Execute: func(_ context.Context, _ map[string]any) (string, error) {
			executed = true
			return "should not execute", nil
		},
	})
	session := NewSession()
	r := NewRunner(Config{Driver: driver, Tools: reg, Session: session})

	if err := r.Run(context.Background(), "read README"); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if executed {
		t.Fatal("tool executed despite malformed JSON args")
	}
	if got := r.LastResponse(); got != "recovered after malformed args feedback" {
		t.Fatalf("LastResponse = %q", got)
	}
	found := false
	for _, msg := range session.Snapshot().History {
		if msg.Role == llm.RoleTool && msg.ToolCallID == "bad-json" && strings.Contains(msg.Content, "malformed tool call arguments") {
			found = true
		}
	}
	if !found {
		t.Fatalf("history missing malformed-args feedback: %#v", session.Snapshot().History)
	}
}

func TestRunnerRepeatedUnknownToolFailureClassIsModelOutputInvalid(t *testing.T) {
	steps := make([][]llm.Token, maxCompletionRetriesPerTurn+1)
	for i := range steps {
		steps[i] = []llm.Token{{ToolCall: &llm.NativeToolCall{ID: fmt.Sprintf("bad-%d", i), Name: "bash", ArgsJSON: `{"command":"ls"}`}}}
	}
	driver := &nativeSequenceDriver{steps: steps}
	reg := agenttools.NewRegistry()
	reg.Register(agenttools.Tool{Name: "read_file", Description: "read file"})
	session := NewSession()
	r := NewRunner(Config{Driver: driver, Tools: reg, Session: session})

	err := r.Run(context.Background(), "inspect the repo")
	if err == nil {
		t.Fatal("expected repeated unknown tool to fail")
	}
	for _, item := range session.Snapshot().Items {
		if item.Kind == protocol.ItemFailure {
			if item.Failure == nil || item.Failure.Decision.Class != protocol.FailureModelOutputInvalid {
				t.Fatalf("failure item = %#v, want model_output_invalid", item.Failure)
			}
			return
		}
	}
	t.Fatalf("missing failure item: %#v", session.Snapshot().Items)
}

func TestRunnerMalformedArgsEmitsDurableFailureItem(t *testing.T) {
	driver := &nativeSequenceDriver{steps: [][]llm.Token{
		{{ToolCall: &llm.NativeToolCall{ID: "bad-json", Name: "read_file", ArgsJSON: `{"path":`}}},
		{{ToolCall: &llm.NativeToolCall{ID: "read-1", Name: "read_file", ArgsJSON: `{"path":"README.md"}`}}},
		{{ToolCall: &llm.NativeToolCall{ID: "read-2", Name: "read_file", ArgsJSON: `{"path":"README.md"}`}}},
		{{Text: "recovered"}},
	}}
	reg := agenttools.NewRegistry()
	reg.Register(agenttools.Tool{Name: "read_file", Description: "read file", Parameters: []agenttools.ParameterDef{{Name: "path", Type: "string", Required: true}}, AutoApprove: true, Execute: func(context.Context, map[string]any) (string, error) { return "readme", nil }})
	session := NewSession()
	r := NewRunner(Config{Driver: driver, Tools: reg, Session: session})
	if err := r.Run(context.Background(), "read README"); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, item := range session.Snapshot().Items {
		if item.Kind == protocol.ItemFailure && item.Failure.Decision.Class == protocol.FailureToolArgsInvalid && item.Failure.Decision.Recoverable {
			found = true
		}
	}
	if !found {
		t.Fatalf("missing recoverable failure item: %#v", session.Snapshot().Items)
	}
}

func TestMalformedModelOutputAssertionsUseDurableItems(t *testing.T) {
	driver := &nativeSequenceDriver{steps: [][]llm.Token{
		{{ToolCall: &llm.NativeToolCall{ID: "bad-json", Name: "read_file", ArgsJSON: `{"path":`}}},
		{{Text: "recovered"}},
	}}
	reg := agenttools.NewRegistry()
	reg.Register(agenttools.Tool{Name: "read_file", Description: "read file", Parameters: []agenttools.ParameterDef{{Name: "path", Type: "string", Required: true}}, AutoApprove: true, Execute: func(context.Context, map[string]any) (string, error) { return "", nil }})
	session := NewSession()
	r := NewRunner(Config{Driver: driver, Tools: reg, Session: session})
	if err := r.Run(context.Background(), "read README"); err != nil {
		t.Fatal(err)
	}
	for _, item := range session.Snapshot().Items {
		if item.Kind == protocol.ItemFailure && item.Failure.Decision.Class == protocol.FailureToolArgsInvalid {
			return
		}
	}
	t.Fatalf("missing structured malformed-output failure item: %#v", session.Snapshot().Items)
}

func TestRunnerToolValidationFailureContinuesLoop(t *testing.T) {
	driver := &nativeSequenceDriver{steps: [][]llm.Token{
		{{ToolCall: &llm.NativeToolCall{ID: "read-1", Name: "read_file", ArgsJSON: `{}`}}},
		{{Text: "recovered after path feedback"}},
	}}
	reg := agenttools.NewRegistry()
	executed := false
	reg.Register(agenttools.Tool{
		Name:        "read_file",
		Description: "read file",
		Parameters:  []agenttools.ParameterDef{{Name: "path", Type: "string", Required: true}},
		AutoApprove: true,
		Execute: func(_ context.Context, _ map[string]any) (string, error) {
			executed = true
			return "should not execute", nil
		},
	})
	session := NewSession()
	r := NewRunner(Config{Driver: driver, Tools: reg, Session: session})

	if err := r.Run(context.Background(), "read the file"); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if executed {
		t.Fatal("tool executed despite missing required path")
	}
	if got := r.LastResponse(); got != "recovered after path feedback" {
		t.Fatalf("LastResponse = %q", got)
	}
	if driver.callCount != 2 {
		t.Fatalf("driver calls = %d, want recovery turn", driver.callCount)
	}
	found := false
	for _, msg := range session.Snapshot().History {
		if msg.Role == llm.RoleTool && strings.Contains(msg.Content, "read_file.path is required") {
			found = true
		}
	}
	if !found {
		t.Fatalf("history missing validation feedback: %#v", session.Snapshot().History)
	}
}

func TestRunnerUpdatePlanMissingStepsContinuesLoop(t *testing.T) {
	additional := false
	driver := &nativeSequenceDriver{steps: [][]llm.Token{
		{{ToolCall: &llm.NativeToolCall{ID: "plan-1", Name: "update_plan", ArgsJSON: `{"explanation":"start"}`}}},
		{{Text: "recovered after plan feedback"}},
	}}
	reg := agenttools.NewRegistry()
	executed := false
	reg.Register(agenttools.Tool{
		Name:        "update_plan",
		Description: "update plan",
		Schema: &llm.ToolSchema{
			Type: "object",
			Properties: map[string]*llm.ToolSchema{
				"steps":       {Type: "array", Items: &llm.ToolSchema{Type: "object"}},
				"explanation": {Type: "string"},
			},
			Required:             []string{"steps"},
			AdditionalProperties: &additional,
		},
		AutoApprove: true,
		Execute: func(_ context.Context, _ map[string]any) (string, error) {
			executed = true
			return "should not execute", nil
		},
	})
	session := NewSession()
	session.SetTaskState(TaskState{Objective: "make a plan", Operation: "plan"})
	r := NewRunner(Config{Driver: driver, Tools: reg, Session: session})

	if err := r.Run(context.Background(), "make a plan"); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if executed {
		t.Fatal("tool executed despite missing required steps")
	}
	if got := r.LastResponse(); got != "recovered after plan feedback" {
		t.Fatalf("LastResponse = %q", got)
	}
	found := false
	for _, msg := range session.Snapshot().History {
		if msg.Role == llm.RoleTool && strings.Contains(msg.Content, "update_plan.steps is required") {
			found = true
		}
	}
	if !found {
		t.Fatalf("history missing validation feedback: %#v", session.Snapshot().History)
	}
}

func TestRunnerStructuredSchemaValidationFailureContinuesLoop(t *testing.T) {
	additional := false
	driver := &nativeSequenceDriver{steps: [][]llm.Token{
		{{ToolCall: &llm.NativeToolCall{ID: "plan-1", Name: "update_plan", ArgsJSON: `{"steps":[{"step":"Inspect","status":"doing"}]}`}}},
		{{Text: "recovered after enum feedback"}},
	}}
	reg := agenttools.NewRegistry()
	executed := false
	reg.Register(agenttools.Tool{
		Name: "update_plan",
		Schema: &llm.ToolSchema{
			Type: "object",
			Properties: map[string]*llm.ToolSchema{
				"steps": {
					Type: "array",
					Items: &llm.ToolSchema{
						Type: "object",
						Properties: map[string]*llm.ToolSchema{
							"step":   {Type: "string"},
							"status": {Type: "string", Enum: []string{"pending", "in_progress", "blocked", "completed"}},
						},
						Required:             []string{"step", "status"},
						AdditionalProperties: &additional,
					},
				},
			},
			Required:             []string{"steps"},
			AdditionalProperties: &additional,
		},
		AutoApprove: true,
		Execute: func(_ context.Context, _ map[string]any) (string, error) {
			executed = true
			return "should not execute", nil
		},
	})
	session := NewSession()
	session.SetTaskState(TaskState{Objective: "make a plan", Operation: "plan"})
	r := NewRunner(Config{Driver: driver, Tools: reg, Session: session})

	if err := r.Run(context.Background(), "make a plan"); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if executed {
		t.Fatal("tool executed despite invalid nested enum")
	}
	found := false
	for _, msg := range session.Snapshot().History {
		if msg.Role == llm.RoleTool && strings.Contains(msg.Content, "update_plan.steps[0].status must be one of") {
			found = true
		}
	}
	if !found {
		t.Fatalf("history missing schema validation feedback: %#v", session.Snapshot().History)
	}
}

func TestRunnerAskUserQuestionExecutionErrorContinuesLoop(t *testing.T) {
	additional := false
	driver := &nativeSequenceDriver{steps: [][]llm.Token{
		{{ToolCall: &llm.NativeToolCall{ID: "ask-1", Name: "ask_user_question", ArgsJSON: `{"question":"Pick one","options":[{"label":"Only one"}]}`}}},
		{{Text: "recovered after ask feedback"}},
	}}
	reg := agenttools.NewRegistry()
	reg.Register(agenttools.Tool{
		Name:        "ask_user_question",
		Description: "ask user",
		Schema: &llm.ToolSchema{
			Type: "object",
			Properties: map[string]*llm.ToolSchema{
				"question": {Type: "string"},
				"options": {
					Type: "array",
					Items: &llm.ToolSchema{
						Type: "object",
						Properties: map[string]*llm.ToolSchema{
							"label": {Type: "string"},
						},
						Required:             []string{"label"},
						AdditionalProperties: &additional,
					},
				},
			},
			Required:             []string{"question", "options"},
			AdditionalProperties: &additional,
		},
		AutoApprove: true,
		Execute: func(_ context.Context, _ map[string]any) (string, error) {
			return "", fmt.Errorf("at least two options are required")
		},
	})
	session := NewSession()
	session.SetTaskState(TaskState{Objective: "ask", Operation: "plan"})
	r := NewRunner(Config{Driver: driver, Tools: reg, Session: session})

	if err := r.Run(context.Background(), "ask"); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if got := r.LastResponse(); got != "recovered after ask feedback" {
		t.Fatalf("LastResponse = %q", got)
	}
	found := false
	for _, msg := range session.Snapshot().History {
		if msg.Role == llm.RoleTool && strings.Contains(msg.Content, "at least two options are required") {
			found = true
		}
	}
	if !found {
		t.Fatalf("history missing ask_user_question feedback: %#v", session.Snapshot().History)
	}
}

func TestRunnerFatalToolExecutionErrorStopsLoop(t *testing.T) {
	driver := &nativeSequenceDriver{steps: [][]llm.Token{
		{{ToolCall: &llm.NativeToolCall{ID: "write-1", Name: "write_file", ArgsJSON: `{"path":"README.md","content":"x"}`}}},
	}}
	reg := agenttools.NewRegistry()
	reg.Register(agenttools.Tool{
		Name:        "write_file",
		Description: "write file",
		Parameters: []agenttools.ParameterDef{
			{Name: "path", Type: "string", Required: true},
			{Name: "content", Type: "string", Required: true},
		},
		AutoApprove: true,
		Execute: func(_ context.Context, _ map[string]any) (string, error) {
			return "", fmt.Errorf("disk unavailable")
		},
	})
	r := NewRunner(Config{Driver: driver, Tools: reg, Session: NewSession()})

	if err := r.Run(context.Background(), "write file"); err == nil || !strings.Contains(err.Error(), "disk unavailable") {
		t.Fatalf("err = %v, want fatal disk error", err)
	}
}

type nativeSequenceDriver struct {
	steps     [][]llm.Token
	errs      []error
	callCount int
	allMsgs   [][]llm.Message
	lastOpts  []llm.NativeToolOptions
}

func (d *nativeSequenceDriver) Name() string { return "native-sequence" }

func (d *nativeSequenceDriver) Stream(_ context.Context, msgs []llm.Message, out chan<- llm.Token) error {
	defer close(out)
	d.allMsgs = append(d.allMsgs, append([]llm.Message(nil), msgs...))
	if d.callCount < len(d.errs) && d.errs[d.callCount] != nil {
		err := d.errs[d.callCount]
		d.callCount++
		return err
	}
	if d.callCount >= len(d.steps) {
		return errors.New("no scripted step")
	}
	for _, tok := range d.steps[d.callCount] {
		if tok.ToolCall != nil {
			return errors.New("Stream cannot emit tool calls")
		}
		out <- tok
	}
	d.callCount++
	return nil
}

func (d *nativeSequenceDriver) StreamWithTools(_ context.Context, msgs []llm.Message, tools []llm.ToolDef, out chan<- llm.Token) error {
	return d.StreamWithToolsOptions(context.Background(), msgs, tools, llm.NativeToolOptions{}, out)
}

func (d *nativeSequenceDriver) StreamWithToolsOptions(_ context.Context, msgs []llm.Message, _ []llm.ToolDef, opts llm.NativeToolOptions, out chan<- llm.Token) error {
	defer close(out)
	d.allMsgs = append(d.allMsgs, append([]llm.Message(nil), msgs...))
	d.lastOpts = append(d.lastOpts, opts)
	if d.callCount < len(d.errs) && d.errs[d.callCount] != nil {
		err := d.errs[d.callCount]
		d.callCount++
		return err
	}
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
	session.SetTaskState(TaskState{
		Objective:            "merge feature/go-rewrite into main",
		Operation:            "merge",
		RequiredVerification: "verify branch main contains the resulting HEAD commit",
		SourceRef:            "feature/go-rewrite",
		TargetBranch:         "main",
	})
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
		if msg.Role == llm.RoleSystem && strings.Contains(msg.Content, "Call git_merge_status to inspect unresolved files, conflict previews, and next steps") {
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
	session.SetTaskState(TaskState{
		Objective:            "merge feature/go-rewrite into main",
		Operation:            "merge",
		RequiredVerification: "verify branch main contains the resulting HEAD commit",
		SourceRef:            "feature/go-rewrite",
		TargetBranch:         "main",
	})
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

func TestRunnerNudgesOnExcessivePlanExploration(t *testing.T) {
	// Build planExplorationBudget+1 exploration steps to cross the synthesis threshold,
	// then a final text response. The model should never be blocked — only nudged via
	// a runtime hook overlay injected into the system prompt.
	const explorations = planExplorationBudget + 1
	steps := make([][]llm.Token, explorations+1)
	for i := range explorations {
		steps[i] = []llm.Token{{ToolCall: &llm.NativeToolCall{
			ID:       fmt.Sprintf("c%d", i+1),
			Name:     "search",
			ArgsJSON: fmt.Sprintf(`{"pattern":"pattern%d"}`, i),
		}}}
	}
	steps[explorations] = []llm.Token{{Text: "Plan: all done."}}

	driver := &nativeSequenceDriver{steps: steps}
	reg := agenttools.NewRegistry()
	searchCalls := 0
	reg.Register(agenttools.Tool{
		Name:        "search",
		Description: "search text",
		Parameters:  []agenttools.ParameterDef{{Name: "pattern", Type: "string", Required: true}},
		AutoApprove: true,
		Execute: func(_ context.Context, _ map[string]any) (string, error) {
			searchCalls++
			return "match", nil
		},
	})

	session := NewSession()
	session.SetTaskState(TaskState{
		Objective:            "write a plan for removing dead xml code",
		Operation:            "plan",
		RequiredVerification: "produce a concise plan grounded in enough repo evidence",
	})

	r := NewRunner(Config{Driver: driver, Tools: reg, Session: session})
	if err := r.Run(context.Background(), "write a cleanup plan"); err != nil {
		t.Fatal(err)
	}
	// All exploration calls must execute — no blocking
	if searchCalls != explorations {
		t.Fatalf("search calls = %d, want %d (no blocking)", searchCalls, explorations)
	}
	// Synthesis note must appear in a request sent after the budget is exceeded
	foundSynthesisNote := false
	for _, msgs := range driver.allMsgs[planExplorationBudget:] {
		for _, msg := range msgs {
			if msg.Role == llm.RoleSystem && strings.Contains(msg.Content, "[hook:runtime]") && strings.Contains(msg.Content, "Planning task guidance") {
				foundSynthesisNote = true
			}
		}
	}
	if !foundSynthesisNote {
		t.Fatalf("expected planning synthesis note after budget exceeded, allMsgs=%d", len(driver.allMsgs))
	}
	// No blocking should have occurred
	snap := session.Snapshot()
	for _, msg := range snap.History {
		if msg.Role == llm.RoleTool && strings.Contains(msg.Content, "blocked:") {
			t.Fatalf("expected no blocking, got blocked tool result: %s", msg.Content)
		}
	}
}

func TestRunnerNudgesOnExcessiveAnalysisExploration(t *testing.T) {
	// Build analysisExplorationBudget+1 exploration steps to cross the threshold,
	// then a final text response. Tools must never be blocked — only nudged.
	const explorations = analysisExplorationBudget + 1
	steps := make([][]llm.Token, explorations+1)
	for i := range explorations {
		steps[i] = []llm.Token{{ToolCall: &llm.NativeToolCall{
			ID:       fmt.Sprintf("c%d", i+1),
			Name:     "search",
			ArgsJSON: fmt.Sprintf(`{"pattern":"pattern%d"}`, i),
		}}}
	}
	steps[explorations] = []llm.Token{{Text: "Findings: all done."}}

	driver := &nativeSequenceDriver{steps: steps}
	reg := agenttools.NewRegistry()
	searchCalls := 0
	reg.Register(agenttools.Tool{
		Name:        "search",
		Description: "search text",
		Parameters:  []agenttools.ParameterDef{{Name: "pattern", Type: "string", Required: true}},
		AutoApprove: true,
		Execute: func(_ context.Context, _ map[string]any) (string, error) {
			searchCalls++
			return "match", nil
		},
	})

	session := NewSession()
	session.SetTaskState(TaskState{
		Objective:            "audit the repo and explain cleanup targets",
		Operation:            "analysis",
		RequiredVerification: "produce source-grounded findings and stop when the answer can be written",
	})

	r := NewRunner(Config{Driver: driver, Tools: reg, Session: session})
	if err := r.Run(context.Background(), "audit the repo"); err != nil {
		t.Fatal(err)
	}
	// All exploration calls must execute — no blocking
	if searchCalls != explorations {
		t.Fatalf("search calls = %d, want %d (no blocking)", searchCalls, explorations)
	}
	// Analysis synthesis note must appear after budget is exceeded
	foundSynthesisNote := false
	for _, msgs := range driver.allMsgs[analysisExplorationBudget:] {
		for _, msg := range msgs {
			if msg.Role == llm.RoleSystem && strings.Contains(msg.Content, "[hook:runtime]") && strings.Contains(msg.Content, "Analysis guidance") {
				foundSynthesisNote = true
			}
		}
	}
	if !foundSynthesisNote {
		t.Fatalf("expected analysis synthesis note after budget exceeded, allMsgs=%d", len(driver.allMsgs))
	}
	// No blocking should have occurred
	snap := session.Snapshot()
	for _, msg := range snap.History {
		if msg.Role == llm.RoleTool && strings.Contains(msg.Content, "blocked:") {
			t.Fatalf("expected no blocking, got blocked tool result: %s", msg.Content)
		}
	}
}

func TestRunnerNudgesOnRepeatedSameFileCodeSearch(t *testing.T) {
	const repeatedSearches = sameFileSearchThrashThreshold + 1
	steps := make([][]llm.Token, repeatedSearches+1)
	for i := range repeatedSearches {
		steps[i] = []llm.Token{{ToolCall: &llm.NativeToolCall{
			ID:       fmt.Sprintf("c%d", i+1),
			Name:     "code_search",
			ArgsJSON: fmt.Sprintf(`{"path":"internal/tui/chatmodel.go","query":"pattern%d"}`, i),
		}}}
	}
	steps[repeatedSearches] = []llm.Token{{Text: "Done."}}

	driver := &nativeSequenceDriver{steps: steps}
	reg := agenttools.NewRegistry()
	codeSearchCalls := 0
	reg.Register(agenttools.Tool{
		Name:        "code_search",
		Description: "search code",
		Parameters: []agenttools.ParameterDef{
			{Name: "path", Type: "string", Required: true},
			{Name: "query", Type: "string", Required: true},
		},
		AutoApprove: true,
		Execute: func(_ context.Context, _ map[string]any) (string, error) {
			codeSearchCalls++
			return "no matches", nil
		},
	})

	session := NewSession()
	session.SetTaskState(TaskState{
		Objective:            "add a file tree panel to the tui",
		Operation:            "implement",
		RequiredVerification: "inspect the relevant code, make the change with edit tools, and run the relevant verification before claiming completion",
	})

	r := NewRunner(Config{Driver: driver, Tools: reg, Session: session})
	if err := r.Run(context.Background(), "add the panel"); err != nil {
		t.Fatal(err)
	}
	if codeSearchCalls != repeatedSearches {
		t.Fatalf("code_search calls = %d, want %d (no blocking)", codeSearchCalls, repeatedSearches)
	}
	foundOverlay := false
	for _, msgs := range driver.allMsgs[sameFileSearchThrashThreshold:] {
		for _, msg := range msgs {
			if msg.Role == llm.RoleSystem && strings.Contains(msg.Content, "[hook:runtime]") && strings.Contains(msg.Content, "Search thrash guidance") {
				foundOverlay = true
			}
		}
	}
	if !foundOverlay {
		t.Fatalf("expected search thrash overlay after threshold, allMsgs=%d", len(driver.allMsgs))
	}
	snap := session.Snapshot()
	for _, msg := range snap.History {
		if msg.Role == llm.RoleTool && strings.Contains(msg.Content, "blocked:") {
			t.Fatalf("expected no blocking, got blocked tool result: %s", msg.Content)
		}
	}
}

func TestRunnerBlocksRepeatedSameFileReadsAfterThreshold(t *testing.T) {
	// Repetition is nudged first and only refused once the nudge has gone
	// unheeded, so the block lands at the block threshold, not the nudge one.
	steps := make([][]llm.Token, repeatToolCallBlockThreshold+2)
	for i := 0; i < repeatToolCallBlockThreshold+1; i++ {
		steps[i] = []llm.Token{{ToolCall: &llm.NativeToolCall{
			ID:       fmt.Sprintf("c%d", i+1),
			Name:     "read_file",
			ArgsJSON: `{"path":"src/main.rs"}`,
		}}}
	}
	steps[len(steps)-1] = []llm.Token{{Text: "Done."}}
	driver := &nativeSequenceDriver{steps: steps}
	reg := agenttools.NewRegistry()
	readCalls := 0
	reg.Register(agenttools.Tool{
		Name:        "read_file",
		Description: "read file",
		Parameters:  []agenttools.ParameterDef{{Name: "path", Type: "string", Required: true}},
		AutoApprove: true,
		Execute: func(_ context.Context, _ map[string]any) (string, error) {
			readCalls++
			return "contents", nil
		},
	})
	session := NewSession()
	r := NewRunner(Config{Driver: driver, Tools: reg, Session: session})

	if err := r.Run(context.Background(), "inspect src/main.rs"); err != nil {
		t.Fatal(err)
	}
	if readCalls != repeatToolCallBlockThreshold {
		t.Fatalf("read calls = %d, want repeated read blocked after threshold %d", readCalls, repeatToolCallBlockThreshold)
	}
	if !sessionHistoryContains(session, "blocked: identical read_file", "src/main.rs") {
		t.Fatalf("history missing repeated read block: %#v", session.Snapshot().History)
	}
}

func TestRunnerUsesConfiguredToolThrashThreshold(t *testing.T) {
	steps := [][]llm.Token{
		{{ToolCall: &llm.NativeToolCall{ID: "c1", Name: "code_search", ArgsJSON: `{"path":"internal/tui/chatmodel.go","query":"one"}`}}},
		{{ToolCall: &llm.NativeToolCall{ID: "c2", Name: "code_search", ArgsJSON: `{"path":"internal/tui/chatmodel.go","query":"two"}`}}},
		{{Text: "Done."}},
	}
	driver := &nativeSequenceDriver{steps: steps}
	reg := agenttools.NewRegistry()
	reg.Register(agenttools.Tool{
		Name:        "code_search",
		Description: "search code",
		Parameters:  []agenttools.ParameterDef{{Name: "path", Type: "string", Required: true}, {Name: "query", Type: "string", Required: true}},
		AutoApprove: true,
		Execute: func(_ context.Context, _ map[string]any) (string, error) {
			return "no matches", nil
		},
	})

	session := NewSession()
	session.SetTaskState(TaskState{
		Objective:            "add a file tree panel to the tui",
		Operation:            "implement",
		RequiredVerification: "inspect the relevant code, make the change with edit tools, and run the relevant verification before claiming completion",
	})
	r := NewRunner(Config{Driver: driver, Tools: reg, Session: session, ToolThrashCircuitBreaker: 2})
	if err := r.Run(context.Background(), "add the panel"); err != nil {
		t.Fatal(err)
	}
	if len(driver.allMsgs) < 3 {
		t.Fatalf("driver message batches = %d, want at least 3", len(driver.allMsgs))
	}
	foundOverlay := false
	for _, msg := range driver.allMsgs[2] {
		if msg.Role == llm.RoleSystem && strings.Contains(msg.Content, "Search thrash guidance") {
			foundOverlay = true
		}
	}
	if !foundOverlay {
		t.Fatalf("expected configured threshold to emit search thrash overlay on third step")
	}
}

func TestRepeatToolCallTargetUsesSearchPattern(t *testing.T) {
	if got := repeatToolCallTarget("search", map[string]any{"pattern": "theme"}); got != "theme" {
		t.Fatalf("search target = %q", got)
	}
}

func TestRepeatToolCallTargetCoversNoOpEditAndFailedWait(t *testing.T) {
	editArgs := map[string]any{"path": "main.go", "old_text": "old", "new_text": "old"}
	if got := repeatToolCallTarget("edit_file", editArgs); got != "main.go:old->old" {
		t.Fatalf("edit target = %q", got)
	}
	if got := repeatToolCallTarget("wait_agent", map[string]any{"id": "agent-1"}); got != "agent-1" {
		t.Fatalf("wait target = %q", got)
	}
}

func TestReactToolSummaryRedactsSecretArguments(t *testing.T) {
	secret := "TOKEN=" + strings.Repeat("x", 24)
	summary := reactToolSummary(map[string]any{"command": "printf '%s' '" + secret + "'"})
	if strings.Contains(summary, secret) {
		t.Fatalf("tool summary leaked secret: %q", summary)
	}
	if !strings.Contains(summary, "<REDACTED:generic-token>") {
		t.Fatalf("tool summary missing redaction marker: %q", summary)
	}
}

func TestValidateToolArgsRejectsOmittedPlaceholderStrings(t *testing.T) {
	tool := agenttools.Tool{
		Name: "run_command",
		Parameters: []agenttools.ParameterDef{
			{Name: "command", Type: "string", Required: true},
		},
	}
	err := validateToolArgs(tool, map[string]any{"command": "<omitted 658 chars>"})
	if !strings.Contains(err, "placeholder") {
		t.Fatalf("validateToolArgs error = %q, want placeholder rejection", err)
	}
}

func TestWorkspaceEscapeToolErrorsAreModelCorrectable(t *testing.T) {
	err := errors.New(`path "/tmp/test_ghostty.scpt" escapes working directory`)
	for _, name := range []string{"write_file", "edit_file", "apply_patch"} {
		t.Run(name, func(t *testing.T) {
			if !isModelCorrectableToolExecutionError(name, err) {
				t.Fatalf("%s workspace escape should be model-correctable", name)
			}
		})
	}
}

func containsString(values []string, want string) bool {
	return slices.Contains(values, want)
}

func repeatedTextSteps(text string, count int) [][]llm.Token {
	steps := make([][]llm.Token, 0, count)
	for range count {
		steps = append(steps, []llm.Token{{Text: text}})
	}
	return steps
}

func repeatedTokenSteps(tokens []llm.Token, count int) [][]llm.Token {
	steps := make([][]llm.Token, 0, count)
	for range count {
		steps = append(steps, append([]llm.Token(nil), tokens...))
	}
	return steps
}

func toolDefNames(defs []llm.ToolDef) []string {
	names := make([]string, 0, len(defs))
	for _, def := range defs {
		names = append(names, def.Name)
	}
	return names
}

func TestRunnerClearHistoryResetsSessionState(t *testing.T) {
	driver := &nativeScriptedDriver{responses: []string{"done"}}
	r := NewRunner(Config{
		Driver:       driver,
		Renderer:     silentRenderer{},
		SystemPrompt: func() string { return "system prompt" },
		Session:      NewSession(),
	})

	if err := r.Run(context.Background(), "hello"); err != nil {
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
	if !driver.reset {
		t.Fatal("expected driver conversation state to reset")
	}
}

type toolResultCapture struct {
	silentRenderer
	calls []struct {
		name    string
		text    string
		isError bool
	}
}

func (r *toolResultCapture) ToolResult(name, text, diff string, isError bool) {
	r.calls = append(r.calls, struct {
		name    string
		text    string
		isError bool
	}{name, text, isError})
}

func TestRunnerTracksValidationPassOnGoTest(t *testing.T) {
	driver := &nativeSequenceDriver{
		steps: [][]llm.Token{
			{{ToolCall: &llm.NativeToolCall{ID: "c1", Name: "run_command", ArgsJSON: `{"command":"go test ./..."}`}}},
			{{Text: "all tests pass"}},
		},
	}
	reg := agenttools.NewRegistry()
	reg.Register(agenttools.Tool{
		Name:        "run_command",
		Description: "run shell command",
		Parameters:  []agenttools.ParameterDef{{Name: "command", Type: "string", Required: true}},
		AutoApprove: true,
		Execute: func(_ context.Context, _ map[string]any) (string, error) {
			return "ok  forge/internal/react\nexit 0", nil
		},
	})
	rec := &toolResultCapture{}
	r := NewRunner(Config{Driver: driver, Tools: reg, Renderer: rec})
	if err := r.Run(context.Background(), "verify tests pass"); err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, c := range rec.calls {
		if c.name == "__validation" && strings.Contains(c.text, "validation passed") && !c.isError {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected __validation passed call, got %#v", rec.calls)
	}
	if note := r.session.Snapshot().RuntimeNote; note != "" {
		t.Fatalf("runtime note should be empty on pass, got %q", note)
	}
}

func TestRunnerTracksValidationFailOnGoTest(t *testing.T) {
	driver := &nativeSequenceDriver{
		steps: [][]llm.Token{
			{{ToolCall: &llm.NativeToolCall{ID: "c1", Name: "run_command", ArgsJSON: `{"command":"go test ./..."}`}}},
			{{Text: "tests failed"}},
		},
	}
	reg := agenttools.NewRegistry()
	reg.Register(agenttools.Tool{
		Name:        "run_command",
		Description: "run shell command",
		Parameters:  []agenttools.ParameterDef{{Name: "command", Type: "string", Required: true}},
		AutoApprove: true,
		Execute: func(_ context.Context, _ map[string]any) (string, error) {
			return "--- FAIL: TestFoo\nFAIL\tforge/internal/react\nexit 1", nil
		},
	})
	rec := &toolResultCapture{}
	r := NewRunner(Config{Driver: driver, Tools: reg, Renderer: rec})
	if err := r.Run(context.Background(), "run the tests"); err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, c := range rec.calls {
		if c.name == "__validation" && strings.Contains(c.text, "validation failed") && c.isError {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected __validation failed call, got %#v", rec.calls)
	}
	foundOverlay := false
	for _, msgs := range driver.allMsgs[1:] {
		for _, msg := range msgs {
			if msg.Role == llm.RoleSystem && strings.Contains(msg.Content, "[hook:runtime]") && strings.Contains(msg.Content, "Last validation failed") {
				foundOverlay = true
			}
		}
	}
	if !foundOverlay {
		t.Fatalf("expected failure validation overlay, allMsgs=%#v", driver.allMsgs)
	}
	if note := r.session.Snapshot().RuntimeNote; note != "" {
		t.Fatalf("runtime note should be empty after overlay migration, got %q", note)
	}
}

func TestRunnerEmitsTaskContextOnSetTaskState(t *testing.T) {
	rec := &toolResultCapture{}
	r := NewRunner(Config{
		Driver:   &nativeScriptedDriver{responses: []string{"done"}},
		Renderer: rec,
	})
	r.SetTaskState(TaskState{
		Objective:            "merge the feature branch",
		RequiredVerification: "verify branch main contains HEAD",
	})
	var found bool
	for _, c := range rec.calls {
		if c.name == "__task_context" && strings.Contains(c.text, "Objective:") && strings.Contains(c.text, "Verify:") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected __task_context call, got %#v", rec.calls)
	}
}

func TestRunnerNonValidationCommandSkipsValidationTracking(t *testing.T) {
	driver := &nativeSequenceDriver{
		steps: [][]llm.Token{
			{{ToolCall: &llm.NativeToolCall{ID: "c1", Name: "run_command", ArgsJSON: `{"command":"ls -la"}`}}},
			{{Text: "done"}},
		},
	}
	reg := agenttools.NewRegistry()
	reg.Register(agenttools.Tool{
		Name:        "run_command",
		Description: "run shell command",
		Parameters:  []agenttools.ParameterDef{{Name: "command", Type: "string", Required: true}},
		AutoApprove: true,
		Execute: func(_ context.Context, _ map[string]any) (string, error) {
			return "file.go\nexit 0", nil
		},
	})
	rec := &toolResultCapture{}
	r := NewRunner(Config{Driver: driver, Tools: reg, Renderer: rec})
	if err := r.Run(context.Background(), "run ls -la"); err != nil {
		t.Fatal(err)
	}
	for _, c := range rec.calls {
		if c.name == "__validation" {
			t.Fatalf("expected no __validation call for ls, got %#v", rec.calls)
		}
	}
}

func TestHasPorcelainConflictsOnlyMatchesConflictCodes(t *testing.T) {
	// Normal staged changes must NOT be treated as conflicts.
	normal := "M  README.md\nA  newfile.go\nD  old.go\n?? untracked.txt"
	if hasPorcelainConflicts(normal) {
		t.Fatalf("staged/unstaged changes falsely detected as conflicts: %q", normal)
	}

	// Empty output = no conflicts.
	if hasPorcelainConflicts("") {
		t.Fatal("empty output falsely detected as conflicts")
	}

	// Lines with conflict XY codes must be detected.
	for _, code := range []string{"UU", "AA", "DD", "AU", "UA", "DU", "UD"} {
		line := code + " somefile.go"
		if !hasPorcelainConflicts(line) {
			t.Errorf("conflict code %q not detected", code)
		}
	}
}

func TestBlockedToolResultClearsAfterConflictResolution(t *testing.T) {
	r := NewRunner(Config{})
	// Conflict detected via git status --porcelain.
	r.updateGitWorkflowForCommand("git status --porcelain", "UU README.md")
	if !r.gitWorkflow.unmergedFiles {
		t.Fatal("setup: expected unmergedFiles=true")
	}
	// Agent resolves and re-checks: no more conflict lines.
	r.updateGitWorkflowForCommand("git status --porcelain", "M  README.md")
	if r.gitWorkflow.unmergedFiles {
		t.Fatal("unmergedFiles should be cleared after clean porcelain output")
	}
}

func TestLsFilesUnmergedRecognizedAsConflictCheck(t *testing.T) {
	if !isGitUnmergedListCommand("git ls-files -u") {
		t.Fatal("git ls-files -u not recognized as unmerged-list command")
	}
	if !isGitUnmergedListCommand("git ls-files --unmerged") {
		t.Fatal("git ls-files --unmerged not recognized as unmerged-list command")
	}
	r := NewRunner(Config{})
	r.updateGitWorkflowForCommand("git ls-files -u", "")
	if r.gitWorkflow.unmergedFiles {
		t.Fatal("empty ls-files -u output should not set unmergedFiles")
	}
	r.updateGitWorkflowForCommand("git ls-files -u", "100644 abc123 1\tREADME.md")
	if !r.gitWorkflow.unmergedFiles {
		t.Fatal("non-empty ls-files -u output should set unmergedFiles")
	}
}

func TestRunnerGitWorkflowOverlayAppearsOnUnmergedFiles(t *testing.T) {
	driver := &captureMessagesDriver{response: "I see unmerged files."}
	r := NewRunner(Config{Driver: driver})

	r.updateGitWorkflowForCommand("git status --porcelain", "UU README.md")
	r.syncRuntimeNote()

	if err := r.Run(context.Background(), "help me resolve conflicts"); err != nil {
		t.Fatal(err)
	}

	var gitMsg string
	for _, msg := range driver.lastMessages {
		if msg.Role == llm.RoleSystem && strings.Contains(msg.Content, "[hook:runtime]") && strings.Contains(msg.Content, "Git merge workflow active") {
			gitMsg = msg.Content
			break
		}
	}
	if gitMsg == "" {
		t.Fatalf("expected git_workflow hook overlay in messages: %#v", driver.lastMessages)
	}
	if !strings.Contains(gitMsg, "git_merge_status") {
		t.Fatalf("git workflow overlay = %q", gitMsg)
	}
}

func TestRunnerGitWorkflowOverlayShiftsAfterConflictResolution(t *testing.T) {
	driver := &captureMessagesDriver{response: "Conflicts resolved, ready to commit."}
	r := NewRunner(Config{Driver: driver})

	// Conflict appears.
	r.updateGitWorkflowForCommand("git status --porcelain", "UU README.md")
	r.syncRuntimeNote()

	// Conflicts are staged/resolved — porcelain no longer shows UU lines, but merge is still active.
	r.updateGitWorkflowForCommand("git status --porcelain", "M  README.md")
	r.syncRuntimeNote()

	if err := r.Run(context.Background(), "commit the resolution"); err != nil {
		t.Fatal(err)
	}

	// Merge is still active so the overlay should still be present, but with the
	// shorter "inspect current merge state" guidance rather than the conflict-resolution message.
	var gitMsg string
	for _, msg := range driver.lastMessages {
		if msg.Role == llm.RoleSystem && strings.Contains(msg.Content, "Git merge workflow active") {
			gitMsg = msg.Content
			break
		}
	}
	if gitMsg == "" {
		t.Fatal("expected git_workflow overlay to remain while merge is still active after conflict resolution")
	}
	if strings.Contains(gitMsg, "Resolve each conflicted file") {
		t.Fatalf("expected shorter merge-active message, got: %q", gitMsg)
	}
}

func TestRunnerCommitFailureDoesNotActivateMergeWorkflow(t *testing.T) {
	r := NewRunner(Config{})

	r.updateGitWorkflowForCommitResult("error committing: exit status 1\n- hook id: ruff")

	if r.gitWorkflow.mergeActive {
		t.Fatal("pre-commit failure should not activate merge workflow")
	}
	if r.gitWorkflow.commitBlocker != commitBlockerEdit {
		t.Fatalf("commit blocker = %v, want edit blocker", r.gitWorkflow.commitBlocker)
	}
	if overlay := r.gitWorkflow.overlayContent(); strings.Contains(overlay, "git_merge_status") || !strings.Contains(overlay, "Git commit blocked") {
		t.Fatalf("commit failure overlay = %q", overlay)
	}
}

func TestRunnerStagingFixesClearsCommitBlocker(t *testing.T) {
	r := NewRunner(Config{})
	r.gitWorkflow.commitBlocker = commitBlockerEdit
	r.gitWorkflow.blockerSummary = "commit blocked by pre-commit hook failures"

	r.updateGitWorkflowForCommand("git add -- fixed.go", "fatal: pathspec failed\nexit 1")
	if r.gitWorkflow.commitBlocker != commitBlockerEdit {
		t.Fatalf("failed git add cleared blocker: %#v", r.gitWorkflow)
	}

	r.updateGitWorkflowForCommand("git add -- fixed.go", "\nexit 0")
	if r.gitWorkflow.commitBlocker != commitBlockerNone || r.gitWorkflow.blockerSummary != "" {
		t.Fatalf("git workflow = %#v, want cleared commit blocker", r.gitWorkflow)
	}
}

func TestRunnerApplyPatchClearsCommitBlocker(t *testing.T) {
	r := NewRunner(Config{})
	r.gitWorkflow.commitBlocker = commitBlockerEdit
	r.gitWorkflow.blockerSummary = "commit blocked by pre-commit hook failures"

	r.updateGitWorkflow("apply_patch", nil, "Done")

	if r.gitWorkflow.commitBlocker != commitBlockerNone || r.gitWorkflow.blockerSummary != "" {
		t.Fatalf("git workflow = %#v, want cleared commit blocker", r.gitWorkflow)
	}
}

func TestRunnerGitWorkflowOverlayClearsOnSuccessfulCommit(t *testing.T) {
	driver := &captureMessagesDriver{response: "Committed."}
	r := NewRunner(Config{Driver: driver})

	// Start a merge workflow.
	r.updateGitWorkflowForCommand("git status --porcelain", "UU README.md")
	r.syncRuntimeNote()

	// Successful commit clears all git workflow state.
	r.updateGitWorkflowForCommitResult("[main abc1234] merge: resolved README conflict\n 2 files changed, 3 insertions(+), 1 deletion(-)")
	r.syncRuntimeNote()

	if err := r.Run(context.Background(), "good, what next"); err != nil {
		t.Fatal(err)
	}

	for _, msg := range driver.lastMessages {
		if msg.Role == llm.RoleSystem && strings.Contains(msg.Content, "Git merge workflow active") {
			t.Fatalf("git_workflow overlay should be gone after successful commit, but found: %q", msg.Content)
		}
	}
}

func TestRunnerReportsToolExposureDecision(t *testing.T) {
	reg := agenttools.NewRegistry()
	for _, name := range []string{"read_file", "spawn_agent", "wait_agent"} {
		toolName := name
		reg.Register(agenttools.Tool{
			Name:        toolName,
			Description: toolName,
			AutoApprove: true,
			Execute: func(_ context.Context, _ map[string]any) (string, error) {
				return "ok", nil
			},
		})
	}
	var decisions []ToolExposureDecision
	driver := &nativeSequenceDriver{steps: [][]llm.Token{
		{{ToolCall: &llm.NativeToolCall{ID: "spawn-1", Name: "spawn_agent", ArgsJSON: `{}`}}},
		{{Text: "delegating"}},
	}}
	r := NewRunner(Config{
		Driver: driver,
		Tools:  reg,
		ToolExposureObserver: func(decision ToolExposureDecision) {
			decisions = append(decisions, decision)
		},
	})

	if err := r.Run(context.Background(), "ask three agents to review the codebase TOKEN="+strings.Repeat("x", 24)); err != nil {
		t.Fatal(err)
	}
	if len(decisions) == 0 {
		t.Fatal("expected tool exposure decision")
	}
	decision := decisions[0]
	if decision.Reason != "all" {
		t.Fatalf("reason = %q, want all", decision.Reason)
	}
	if decision.RequireToolCall {
		t.Fatalf("RequireToolCall = true, want false (tool calls are never forced)")
	}
	for _, want := range []string{"spawn_agent", "wait_agent"} {
		if !containsString(decision.ToolNames, want) {
			t.Fatalf("tool names = %#v, want %s", decision.ToolNames, want)
		}
	}
}

func TestRunnerAllowsTextAfterRequiredToolCallInSameTurn(t *testing.T) {
	driver := &nativeSequenceDriver{steps: [][]llm.Token{
		{{ToolCall: &llm.NativeToolCall{ID: "status-1", Name: "agent_status", ArgsJSON: `{}`}}},
		{{Text: "agent-1 is running"}},
	}}
	reg := agenttools.NewRegistry()
	reg.Register(agenttools.Tool{
		Name:        "agent_status",
		Description: "status",
		AutoApprove: true,
		Execute: func(_ context.Context, _ map[string]any) (string, error) {
			return `{"agents":[{"id":"agent-1","role":"explorer","status":"running"}]}`, nil
		},
	})
	session := NewSession()
	session.UpsertAgentTask(AgentTaskState{ID: "agent-1", Role: "explorer", Status: AgentStatusRunning})
	r := NewRunner(Config{
		Driver:  driver,
		Tools:   reg,
		Session: session,
	})

	if err := r.Run(context.Background(), "check active agents"); err != nil {
		t.Fatal(err)
	}
	snap := session.Snapshot()
	if got := snap.History[len(snap.History)-1].Content; got != "agent-1 is running" {
		t.Fatalf("final content = %q", got)
	}
}

func TestRunnerRetriesTransientStreamErrorWhileAgentRuns(t *testing.T) {
	driver := &nativeSequenceDriver{
		errs: []error{errors.New("request timeout")},
		steps: [][]llm.Token{
			{},
			{{ToolCall: &llm.NativeToolCall{ID: "status-1", Name: "agent_status", ArgsJSON: `{}`}}},
			{{Text: "agent-1 is still running"}},
		},
	}
	reg := agenttools.NewRegistry()
	reg.Register(agenttools.Tool{
		Name:        "agent_status",
		Description: "status",
		AutoApprove: true,
		Execute: func(_ context.Context, _ map[string]any) (string, error) {
			return `{"agents":[{"id":"agent-1","role":"cci","status":"running"}]}`, nil
		},
	})
	session := NewSession()
	session.UpsertAgentTask(AgentTaskState{ID: "agent-1", Role: "cci", Status: AgentStatusRunning})
	rec := &recordingRenderer{}
	r := NewRunner(Config{
		Driver:   driver,
		Tools:    reg,
		Session:  session,
		Renderer: rec,
	})

	if err := r.Run(context.Background(), "ask agents to compare the repo and gather the cci report"); err != nil {
		t.Fatal(err)
	}
	if got := driver.callCount; got != 3 {
		t.Fatalf("driver calls = %d, want timeout retry, tool call, final text", got)
	}
	if len(rec.retryTexts) == 0 {
		t.Fatal("expected retry notice after transient timeout")
	}
	snap := session.Snapshot()
	if got := snap.Turns[len(snap.Turns)-1].Error; got != "" {
		t.Fatalf("turn error = %q, want none", got)
	}
	if got := snap.History[len(snap.History)-1].Content; got != "agent-1 is still running" {
		t.Fatalf("final content = %q", got)
	}
}

func TestRunnerFallsBackToCompletedAgentResultWhenParentStreamTimesOut(t *testing.T) {
	driver := &nativeSequenceDriver{errs: []error{&llm.RetryAttemptsExhaustedError{Attempts: 3, Err: errors.New("stream idle timeout after 30s")}}}
	session := NewSession()
	rec := &recordingRenderer{}
	r := NewRunner(Config{Driver: driver, Session: session, Renderer: rec})
	turn := session.RecordInput("compare forge with codex and opencode")
	session.UpsertAgentTask(AgentTaskState{
		ID:          "agent-1",
		Role:        "explorer",
		Status:      AgentStatusCompleted,
		Result:      "complete comparison from child agent",
		ParentTurn:  turn,
		CompletedAt: time.Now(),
	})
	_, cancel, err := session.BeginTurn(context.Background(), fmt.Sprintf("turn-%d", turn))
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()

	if err := r.runLoop(context.Background(), turn); err != nil {
		t.Fatal(err)
	}
	if driver.callCount != 1 {
		t.Fatalf("driver calls = %d, want one exhausted synthesis attempt", driver.callCount)
	}
	if len(rec.retryTexts) != 0 {
		t.Fatalf("retry notices = %#v, want none", rec.retryTexts)
	}
	if got := r.LastResponse(); !strings.Contains(got, "complete comparison from child agent") {
		t.Fatalf("last response = %q, want completed child result", got)
	}
	snap := session.Snapshot()
	if got := snap.Turns[len(snap.Turns)-1].Error; got != "" {
		t.Fatalf("turn error = %q, want none", got)
	}
}

func TestRunnerDoesNotUseCompletedAgentFallbackWhileSameTurnAgentStillRuns(t *testing.T) {
	for _, status := range []AgentStatus{AgentStatusRunning, AgentStatusPending} {
		t.Run(string(status), func(t *testing.T) {
			driver := &nativeSequenceDriver{
				errs: []error{errors.New("request timeout")},
				steps: [][]llm.Token{
					{},
					{{Text: "agent-2 is still active"}},
				},
			}
			session := NewSession()
			rec := &recordingRenderer{}
			r := NewRunner(Config{Driver: driver, Session: session, Renderer: rec})
			turn := session.RecordInput("compare forge with codex and opencode")
			session.UpsertAgentTask(AgentTaskState{
				ID:          "agent-1",
				Role:        "explorer",
				Status:      AgentStatusCompleted,
				Result:      "complete comparison from child agent",
				ParentTurn:  turn,
				CompletedAt: time.Now(),
			})
			session.UpsertAgentTask(AgentTaskState{
				ID:         "agent-2",
				Role:       "cci",
				Status:     status,
				ParentTurn: turn,
			})

			_, cancel, err := session.BeginTurn(context.Background(), fmt.Sprintf("turn-%d", turn))
			if err != nil {
				t.Fatal(err)
			}
			defer cancel()

			if err := r.runLoop(context.Background(), turn); err != nil {
				t.Fatal(err)
			}
			if driver.callCount != 2 {
				t.Fatalf("driver calls = %d, want retry then final status", driver.callCount)
			}
			if len(rec.retryTexts) == 0 {
				t.Fatal("expected retry notice while same-turn agent remains active")
			}
			if got := r.LastResponse(); got != "agent-2 is still active" {
				t.Fatalf("last response = %q, want active-agent status", got)
			}
		})
	}
}

func TestRunnerDoesNotFailEmptyResponseWhileChildAgentRuns(t *testing.T) {
	driver := &nativeSequenceDriver{steps: [][]llm.Token{
		{{ToolCall: &llm.NativeToolCall{ID: "spawn-1", Name: "spawn_agent", ArgsJSON: `{"role":"explorer","task_description":"inspect repo"}`}}},
		{},
		{},
		{},
		{},
	}}
	reg := agenttools.NewRegistry()
	reg.Register(agenttools.Tool{
		Name:        "spawn_agent",
		Description: "spawn child",
		AutoApprove: true,
		Execute: func(_ context.Context, _ map[string]any) (string, error) {
			return `{"id":"agent-1","role":"explorer","status":"running"}`, nil
		},
	})
	for _, name := range []string{"wait_agent", "agent_status", "kill_agent"} {
		toolName := name
		reg.Register(agenttools.Tool{
			Name:        toolName,
			Description: toolName,
			AutoApprove: true,
			Execute: func(_ context.Context, _ map[string]any) (string, error) {
				return `{}`, nil
			},
		})
	}
	session := NewSession()
	r := NewRunner(Config{
		Driver:  driver,
		Tools:   reg,
		Session: session,
	})

	if err := r.Run(context.Background(), "ask an agent to inspect the repo"); err != nil {
		t.Fatal(err)
	}

	snap := session.Snapshot()
	if len(snap.History) == 0 {
		t.Fatal("expected history entries")
	}
	last := snap.History[len(snap.History)-1]
	for _, want := range []string{"agent-1", "running"} {
		if !strings.Contains(last.Content, want) {
			t.Fatalf("fallback content = %q, want %q", last.Content, want)
		}
	}
	if len(outstandingSpawnedAgents(snap)) == 0 {
		t.Fatal("expected child agent to remain outstanding")
	}
}

func TestAgentTaskPromptLineRedactsRecentActivitySummary(t *testing.T) {
	secret := "TOKEN=" + strings.Repeat("x", 24)
	line := formatAgentTaskPromptLine(AgentTaskState{
		ID:           "agent-1",
		Role:         "repo-auditor",
		Status:       AgentStatusRunning,
		LastToolName: "run_command",
		RecentActivity: []AgentTaskActivity{{
			ToolName: "run_command",
			Summary:  "printed " + secret,
		}},
	})
	if strings.Contains(line, secret) {
		t.Fatalf("agent task prompt leaked secret: %q", line)
	}
	if !strings.Contains(line, "<REDACTED:generic-token>") {
		t.Fatalf("agent task prompt missing redaction marker: %q", line)
	}
}

func TestRunnerKilledAgentStateOverridesStaleTranscript(t *testing.T) {
	reg := agenttools.NewRegistry()
	for _, name := range []string{"spawn_agent", "wait_agent", "agent_status", "kill_agent"} {
		reg.Register(agenttools.Tool{Name: name, Description: name})
	}
	r := NewRunner(Config{Tools: reg})
	snap := SessionSnapshot{
		LastInput: "is the child agent still running?",
		AgentTasks: []AgentTaskState{{
			ID:     "agent-1",
			Role:   "repo-auditor",
			Status: AgentStatusKilled,
			Error:  context.Canceled.Error(),
		}},
		History: []llm.Message{
			{Role: llm.RoleAssistant, ToolCalls: []llm.NativeToolCall{{ID: "spawn-1", Name: "spawn_agent", ArgsJSON: `{}`}}},
			{Role: llm.RoleTool, ToolCallID: "spawn-1", Content: `{"id":"agent-1","role":"repo-auditor","status":"running"}`},
		},
	}

	defs, decision := r.selectToolDefsWithDecision(snap)
	if decision.Reason != "all" {
		t.Fatalf("tool exposure reason = %q, want all", decision.Reason)
	}
	if len(toolDefNames(defs)) != 4 {
		t.Fatalf("tools = %#v, want the full registry exposed", toolDefNames(defs))
	}
	if got := hookOverlayContent(promptHookOutputForSnapshot(t, snap), "agent_status"); got != "" {
		t.Fatalf("agent_status overlay = %q, want empty for killed agent", got)
	}
}

func TestRunnerPromptIncludesOutstandingAgentStatus(t *testing.T) {
	session := NewSession()
	session.RecordInput("ask three agents to review the codebase")
	if err := session.AppendAssistantWithToolCalls([]llm.NativeToolCall{{ID: "spawn-1", Name: "spawn_agent", ArgsJSON: `{}`}}); err != nil {
		t.Fatal(err)
	}
	if err := session.AppendNativeToolResult("spawn-1", `{"id":"agent-1","role":"code-reviewer","status":"running"}`); err != nil {
		t.Fatal(err)
	}
	r := NewRunner(Config{Session: session})

	output := r.promptHookOutput(context.Background())
	got := hookOverlayContent(output, "agent_status")
	for _, want := range []string{"Outstanding child agents", "agent-1", "code-reviewer", "Do not say no agents are running"} {
		if !strings.Contains(got, want) {
			t.Fatalf("agent_status overlay = %q, want %q", got, want)
		}
	}
}

func TestRunnerPromptIncludesAgentTaskProgress(t *testing.T) {
	snap := SessionSnapshot{
		AgentTasks: []AgentTaskState{{
			ID:           "agent-1",
			Role:         "repo-auditor",
			Status:       AgentStatusRunning,
			LastToolName: "read_file",
			RecentActivity: []AgentTaskActivity{{
				ToolName: "read_file",
				Summary:  "README.md",
			}},
		}},
	}

	got := hookOverlayContent(promptHookOutputForSnapshot(t, snap), "agent_status")
	for _, want := range []string{"last: read_file", "README.md"} {
		if !strings.Contains(got, want) {
			t.Fatalf("agent_status overlay = %q, want %q", got, want)
		}
	}
}

func TestRunnerClearsBlockingHandoffAfterParentWrite(t *testing.T) {
	session := NewSession()
	session.UpsertAgentTask(AgentTaskState{
		ID:     "agent-1",
		Role:   "repo-auditor",
		Status: AgentStatusCompleted,
		Result: "audit report",
		Handoff: &AgentHandoff{RemainingActions: []AgentFollowupAction{{
			Kind:        AgentActionWriteFile,
			TargetPath:  "docs/audit.md",
			Description: "Save audit report",
			Blocking:    true,
		}}},
	})
	r := NewRunner(Config{Session: session})

	r.clearBlockingHandoffsAfterWrite("write_file", map[string]any{"path": "docs/audit.md"})

	tasks := session.Snapshot().AgentTasks
	if len(tasks) != 1 || tasks[0].Handoff != nil {
		t.Fatalf("agent tasks after parent write = %#v, want handoff cleared", tasks)
	}
}

func TestRunnerPluginPromptOverlayDoesNotDuplicate(t *testing.T) {
	session := NewSession()
	r := NewRunner(Config{
		Session: session,
		ConfigureHooks: func(registry *hooks.Registry) {
			registry.Register(hooks.PointPromptContext, "plugin:demo", func(context.Context, hooks.Event) []hooks.Result {
				return []hooks.Result{hooks.OverlayResult{
					Key:        "plugin_demo_context",
					Content:    "plugin prompt",
					Priority:   hooks.PriorityHigh,
					Provenance: "plugin:demo",
				}}
			})
		},
	})
	r.syncRuntimeNote()
	r.syncRuntimeNote()

	count := 0
	for _, overlay := range session.Snapshot().HookOutput.Overlays {
		if overlay.Key == "plugin_demo_context" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("plugin overlay count = %d, overlays=%#v", count, session.Snapshot().HookOutput.Overlays)
	}
}

func TestRunnerPreservesNonBlockingBeforeToolHookOutput(t *testing.T) {
	reg := agenttools.NewRegistry()
	reg.Register(agenttools.Tool{
		Name:        "plugin__demo__echo",
		Description: "echo",
		Parameters:  []agenttools.ParameterDef{{Name: "message", Type: "string", Required: true}},
		Execute: func(_ context.Context, args map[string]any) (string, error) {
			message, _ := args["message"].(string)
			return message, nil
		},
	})
	session := NewSession()
	r := NewRunner(Config{
		Tools:   reg,
		Session: session,
		ConfigureHooks: func(registry *hooks.Registry) {
			registry.Register(hooks.PointBeforeTool, "plugin:demo:before", func(context.Context, hooks.Event) []hooks.Result {
				return []hooks.Result{hooks.OverlayResult{
					Key:        "plugin_demo_before",
					Content:    "before hook note",
					Priority:   hooks.PriorityHigh,
					Provenance: "plugin:demo",
				}}
			})
		},
	})
	turn := session.RecordInput("use the demo plugin")
	active, cancel, err := session.BeginTurn(context.Background(), "turn-1")
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	if err := r.executeNativeToolCalls(active.Context, turn, []llm.NativeToolCall{{
		ID:       "c1",
		Name:     "plugin__demo__echo",
		ArgsJSON: `{"message":"hello"}`,
	}}); err != nil {
		t.Fatal(err)
	}

	found := false
	for _, overlay := range session.Snapshot().HookOutput.Overlays {
		if overlay.Key == "plugin_demo_before" && overlay.Content == "before hook note" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected before_tool overlay to persist, overlays=%#v", session.Snapshot().HookOutput.Overlays)
	}
}

func TestRunnerExecuteNativeToolCallsRejectsResultAfterTurnEnd(t *testing.T) {
	reg := agenttools.NewRegistry()
	session := NewSession()
	reg.Register(agenttools.Tool{
		Name:        "late_result",
		Description: "ends the active turn before returning",
		Execute: func(context.Context, map[string]any) (string, error) {
			if err := session.EndTurn("turn-1", TurnEndReasonCancelled); err != nil {
				return "", err
			}
			return "late success", nil
		},
	})
	r := NewRunner(Config{Tools: reg, Session: session})
	turn := session.RecordInput("run late tool")
	active, cancel, err := session.BeginTurn(context.Background(), "turn-1")
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()

	err = r.executeNativeToolCalls(active.Context, turn, []llm.NativeToolCall{{ID: "c1", Name: "late_result", ArgsJSON: `{}`}})
	if err != nil && !errors.Is(err, context.Canceled) {
		t.Fatalf("executeNativeToolCalls error = %v, want nil or context canceled", err)
	}

	for _, item := range session.Snapshot().Items {
		if item.Kind == protocol.ItemToolResult {
			t.Fatalf("stale tool result was appended: %#v", item)
		}
	}
}

func TestRunnerExecuteNativeToolCallsRejectsResultWithoutActiveTurn(t *testing.T) {
	reg := agenttools.NewRegistry()
	session := NewSession()
	reg.Register(agenttools.Tool{
		Name:        "late_result",
		Description: "returns after turn is no longer active",
		Execute: func(context.Context, map[string]any) (string, error) {
			return "late success", nil
		},
	})
	r := NewRunner(Config{Tools: reg, Session: session})
	turn := session.RecordInput("run late tool")

	err := r.executeNativeToolCalls(context.Background(), turn, []llm.NativeToolCall{{ID: "c1", Name: "late_result", ArgsJSON: `{}`}})
	if !errors.Is(err, ErrStaleTurn) {
		t.Fatalf("executeNativeToolCalls error = %v, want ErrStaleTurn", err)
	}

	for _, item := range session.Snapshot().Items {
		if item.Kind == protocol.ItemToolResult {
			t.Fatalf("stale tool result was appended: %#v", item)
		}
	}
}

func TestRunnerExecuteNativeToolCallsSkipsStaleResultSideEffectsAfterCancellation(t *testing.T) {
	reg := agenttools.NewRegistry()
	session := NewSession()
	renderer := &recordingRenderer{}
	reg.Register(agenttools.Tool{
		Name:        "late_result",
		Description: "cancels the active turn before returning",
		Execute: func(context.Context, map[string]any) (string, error) {
			if err := session.CancelActiveTurn("user cancelled"); err != nil {
				return "", err
			}
			return "late success", nil
		},
	})
	r := NewRunner(Config{
		Tools:    reg,
		Session:  session,
		Renderer: renderer,
		ConfigureHooks: func(registry *hooks.Registry) {
			registry.Register(hooks.PointAfterTool, "test:after", func(context.Context, hooks.Event) []hooks.Result {
				return []hooks.Result{hooks.OverlayResult{Key: "late_after", Content: "should not apply", Provenance: "test"}}
			})
		},
	})
	turn := session.RecordInput("run late tool")
	active, cancel, err := session.BeginTurn(context.Background(), "turn-1")
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()

	err = r.executeNativeToolCalls(active.Context, turn, []llm.NativeToolCall{{ID: "c1", Name: "late_result", ArgsJSON: `{}`}})
	if err != nil && !errors.Is(err, context.Canceled) {
		t.Fatalf("executeNativeToolCalls error = %v, want nil or context canceled", err)
	}

	for _, event := range renderer.events {
		if event == "tool_result" {
			t.Fatalf("stale tool result rendered: events=%#v", renderer.events)
		}
	}
	snap := session.Snapshot()
	if snap.HookOutputSet || len(snap.HookOutput.Overlays) != 0 {
		t.Fatalf("stale hook output applied: %#v", snap.HookOutput)
	}
	for _, item := range snap.Items {
		if item.Kind == protocol.ItemToolResult {
			t.Fatalf("stale tool result was appended: %#v", item)
		}
	}
}

func TestRunnerExecuteNativeToolCallsSkipsToolCallRegistrationAfterBeforeHookCancellation(t *testing.T) {
	reg := agenttools.NewRegistry()
	session := NewSession()
	renderer := &recordingRenderer{}
	reg.Register(agenttools.Tool{
		Name:        "custom_tool",
		Description: "custom",
		Execute: func(context.Context, map[string]any) (string, error) {
			return "should not execute", nil
		},
	})
	r := NewRunner(Config{
		Tools:    reg,
		Session:  session,
		Renderer: renderer,
		ConfigureHooks: func(hooksReg *hooks.Registry) {
			hooksReg.Register(hooks.PointBeforeTool, "test:cancel", func(context.Context, hooks.Event) []hooks.Result {
				if err := session.CancelActiveTurn("user cancelled"); err != nil {
					t.Fatalf("cancel active turn: %v", err)
				}
				return nil
			})
		},
	})
	turn := session.RecordInput("run tool")
	active, cancel, err := session.BeginTurn(context.Background(), "turn-1")
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()

	err = r.executeNativeToolCalls(active.Context, turn, []llm.NativeToolCall{{ID: "c1", Name: "custom_tool", ArgsJSON: `{}`}})
	if err != nil && !errors.Is(err, context.Canceled) {
		t.Fatalf("executeNativeToolCalls error = %v, want nil or context canceled", err)
	}

	for _, event := range renderer.events {
		if event == "tool_call" || event == "tool_result" {
			t.Fatalf("stale tool event rendered: events=%#v", renderer.events)
		}
	}
	for _, item := range session.Snapshot().Items {
		if item.Kind == protocol.ItemToolCall || item.Kind == protocol.ItemToolResult {
			t.Fatalf("stale tool item was appended: %#v", item)
		}
	}
}

func TestRunnerExecuteNativeToolCallsSkipsBeforeHookDispatchWhenTurnAlreadyCancelled(t *testing.T) {
	reg := agenttools.NewRegistry()
	session := NewSession()
	hookCalls := 0
	reg.Register(agenttools.Tool{
		Name:        "custom_tool",
		Description: "custom",
		Execute: func(context.Context, map[string]any) (string, error) {
			return "should not execute", nil
		},
	})
	r := NewRunner(Config{
		Tools:   reg,
		Session: session,
		ConfigureHooks: func(hooksReg *hooks.Registry) {
			hooksReg.Register(hooks.PointBeforeTool, "test:count", func(context.Context, hooks.Event) []hooks.Result {
				hookCalls++
				return nil
			})
		},
	})
	turn := session.RecordInput("run tool")
	active, cancel, err := session.BeginTurn(context.Background(), "turn-1")
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	if err := session.CancelActiveTurn("user cancelled"); err != nil {
		t.Fatal(err)
	}

	err = r.executeNativeToolCalls(active.Context, turn, []llm.NativeToolCall{{ID: "c1", Name: "custom_tool", ArgsJSON: `{}`}})
	if err != nil && !errors.Is(err, context.Canceled) {
		t.Fatalf("executeNativeToolCalls error = %v, want nil or context canceled", err)
	}
	if hookCalls != 0 {
		t.Fatalf("before_tool hook calls = %d, want 0", hookCalls)
	}
	for _, item := range session.Snapshot().Items {
		if item.Kind == protocol.ItemToolCall || item.Kind == protocol.ItemToolResult {
			t.Fatalf("stale tool item was appended: %#v", item)
		}
	}
}

func TestRunnerExecutesSerialMetadataToolsSequentially(t *testing.T) {
	reg := agenttools.NewRegistry()
	var mu sync.Mutex
	events := []string{}
	record := func(event string) {
		mu.Lock()
		defer mu.Unlock()
		events = append(events, event)
	}

	reg.Register(agenttools.Tool{
		Name:        "custom_serial",
		Description: "serial test tool",
		Concurrency: agenttools.ToolConcurrencySerial,
		Execute: func(_ context.Context, _ map[string]any) (string, error) {
			record("serial:start")
			time.Sleep(50 * time.Millisecond)
			record("serial:end")
			return "serial", nil
		},
	})
	reg.Register(agenttools.Tool{
		Name:        "custom_parallel",
		Description: "parallel test tool",
		Execute: func(_ context.Context, _ map[string]any) (string, error) {
			record("parallel:start")
			record("parallel:end")
			return "parallel", nil
		},
	})
	session := NewSession()
	r := NewRunner(Config{Tools: reg, Session: session})
	turn := session.RecordInput("run serial metadata tools")
	active, cancel, err := session.BeginTurn(context.Background(), "turn-1")
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()

	if err := r.executeNativeToolCalls(active.Context, turn, []llm.NativeToolCall{
		{ID: "c1", Name: "custom_serial", ArgsJSON: `{}`},
		{ID: "c2", Name: "custom_parallel", ArgsJSON: `{}`},
	}); err != nil {
		t.Fatal(err)
	}

	want := []string{"serial:start", "serial:end", "parallel:start", "parallel:end"}
	if !slices.Equal(events, want) {
		t.Fatalf("events = %#v, want %#v", events, want)
	}
}

func TestRunnerOutputHandleE2EStoresFullPayloadOutOfBand(t *testing.T) {
	largePayload := "large-output-sentinel:" + strings.Repeat("abcdef", 64)
	driver := &nativeSequenceDriver{steps: [][]llm.Token{
		{{ToolCall: &llm.NativeToolCall{ID: "c1", Name: "read_file", ArgsJSON: `{"path":"large.txt"}`}}},
		{{Text: "handled summarized tool output"}},
	}}
	reg := agenttools.NewRegistry()
	reg.Register(agenttools.Tool{
		Name:        "read_file",
		Description: "large output test tool",
		Parameters:  []agenttools.ParameterDef{{Name: "path", Type: "string", Required: true}},
		AutoApprove: true,
		Execute: func(_ context.Context, _ map[string]any) (string, error) {
			return largePayload, nil
		},
	})
	session := NewSession()
	session.SetTaskState(TaskState{Objective: "inspect large output", Operation: "inspect", RequiredVerification: "inspect with read tools before answering"})
	store := sessionstore.NewFileOutputStore(t.TempDir())
	r := NewRunner(Config{Driver: driver, Tools: reg, Session: session, OutputStore: store, OutputStoreThresholdBytes: 16})

	if err := r.Run(context.Background(), "run large output tool"); err != nil {
		t.Fatal(err)
	}

	result := lastToolResult(t, session)
	if result.Handle == "" || result.OriginalBytes != len(largePayload) || result.SHA256 == "" {
		t.Fatalf("tool result metadata = %#v", result)
	}
	if strings.Contains(result.Text, largePayload) || !strings.Contains(result.Text, result.Handle) {
		t.Fatalf("tool result summary = %q", result.Text)
	}
	stored, err := store.Read(context.Background(), sessionstore.OutputHandle{ID: result.Handle, Bytes: result.OriginalBytes, SHA256: result.SHA256}, 0, int64(len(largePayload)))
	if err != nil {
		t.Fatal(err)
	}
	if string(stored) != largePayload {
		t.Fatalf("stored output = %q, want full payload", stored)
	}
	if len(driver.allMsgs) < 2 {
		t.Fatalf("driver messages = %#v", driver.allMsgs)
	}
	foundModelSafeToolResult := false
	for _, msg := range driver.allMsgs[1] {
		if msg.Role != llm.RoleTool {
			continue
		}
		foundModelSafeToolResult = true
		if strings.Contains(msg.Content, largePayload) {
			t.Fatalf("model-visible tool result contained full payload: %q", msg.Content)
		}
		if !strings.Contains(msg.Content, result.Handle) {
			t.Fatalf("model-visible tool result missing handle summary: %q", msg.Content)
		}
	}
	if !foundModelSafeToolResult {
		t.Fatalf("second model step missing tool result: %#v", driver.allMsgs[1])
	}
	for _, msg := range session.Snapshot().History {
		if strings.Contains(msg.Content, largePayload) {
			t.Fatalf("session history contained full payload: %#v", msg)
		}
	}
}

func TestRunnerPostEditDiagnosticsE2EFeedsNextModelStep(t *testing.T) {
	driver := &nativeSequenceDriver{steps: [][]llm.Token{
		{{ToolCall: &llm.NativeToolCall{ID: "edit-1", Name: "edit_file", ArgsJSON: `{"path":"a.txt","old_text":"old","new_text":"new"}`}}},
		{{Text: "fixed after diagnostics"}},
	}}
	reg := agenttools.NewRegistry()
	reg.Register(agenttools.Tool{
		Name:             "edit_file",
		Description:      "edit file",
		AutoApprove:      true,
		MutatesWorkspace: true,
		LastDiff: func() string {
			return "diff --git a/a.txt b/a.txt"
		},
		Execute: func(_ context.Context, _ map[string]any) (string, error) {
			return "edited a.txt", nil
		},
	})
	session := NewSession()
	session.SetTaskState(TaskState{Objective: "edit a.txt", Operation: "implement", RequiredVerification: "edit and validate"})
	r := NewRunner(Config{
		Driver:  driver,
		Tools:   reg,
		Session: session,
		PostEditValidator: &PostEditValidator{
			Command:        []string{"/bin/sh", "-c", "printf post-edit-validator-failed >&2; exit 1"},
			Timeout:        time.Second,
			MaxOutputBytes: 1024,
		},
	})

	if err := r.Run(context.Background(), "edit a.txt"); err != nil {
		t.Fatal(err)
	}

	if len(driver.allMsgs) < 2 {
		t.Fatalf("driver messages = %#v", driver.allMsgs)
	}
	foundNextStepFeedback := false
	for _, msg := range driver.allMsgs[1] {
		if msg.Role == llm.RoleUser && strings.Contains(msg.Content, "Runtime diagnostic feedback") && strings.Contains(msg.Content, "post-edit-validator-failed") {
			foundNextStepFeedback = true
		}
	}
	if !foundNextStepFeedback {
		t.Fatalf("next model step missing diagnostic feedback: %#v", driver.allMsgs[1])
	}
	if !sessionHistoryContains(session, "Runtime diagnostic feedback", "post-edit-validator-failed") {
		t.Fatalf("session history missing diagnostics: %#v", session.Snapshot().History)
	}
}

func TestRunnerCheckpointE2ERestoresOnlyPathMutatedByTool(t *testing.T) {
	root := initReactTestGitRepo(t)
	t.Chdir(root)
	writeReactTestFile(t, filepath.Join(root, "a.txt"), "a original\n")
	writeReactTestFile(t, filepath.Join(root, "b.txt"), "b original\n")
	runReactGit(t, root, "add", "a.txt", "b.txt")
	runReactGit(t, root, "commit", "-m", "initial")
	driver := &nativeSequenceDriver{steps: [][]llm.Token{
		{{ToolCall: &llm.NativeToolCall{ID: "write-1", Name: "write_file", ArgsJSON: `{"path":"a.txt","content":"tool mutation\n"}`}}},
		{{Text: "wrote file"}},
	}}
	reg := agenttools.NewRegistry()
	reg.Register(agenttools.Tool{
		Name:        "write_file",
		Description: "write file",
		AutoApprove: true,
		Execute: func(_ context.Context, args map[string]any) (string, error) {
			path, _ := args["path"].(string)
			content, _ := args["content"].(string)
			writeReactTestFile(t, filepath.Join(root, path), content)
			return "ok", nil
		},
	})
	session := NewSession()
	session.SetTaskState(TaskState{Objective: "write a.txt", Operation: "implement", RequiredVerification: "write the requested file"})
	r := NewRunner(Config{Driver: driver, Tools: reg, Session: session})

	if err := r.Run(context.Background(), "write a.txt"); err != nil {
		t.Fatal(err)
	}
	writeReactTestFile(t, filepath.Join(root, "b.txt"), "unrelated user mutation\n")

	items := session.Snapshot().Items
	checkpointCount := 0
	toolCallCount := 0
	firstCheckpoint := -1
	firstToolCall := -1
	checkpointID := ""
	for i, item := range items {
		if item.Kind == protocol.ItemCheckpoint {
			checkpointCount++
			if firstCheckpoint < 0 {
				firstCheckpoint = i
				checkpointID = item.Checkpoint.ID
			}
		}
		if item.Kind == protocol.ItemToolCall && firstToolCall < 0 {
			toolCallCount++
			firstToolCall = i
		} else if item.Kind == protocol.ItemToolCall {
			toolCallCount++
		}
	}
	if checkpointCount != 1 {
		t.Fatalf("checkpoint items = %d, want 1; items=%#v", checkpointCount, items)
	}
	if toolCallCount != 1 {
		t.Fatalf("tool call items = %d, want 1; items=%#v", toolCallCount, items)
	}
	if firstCheckpoint < 0 || firstToolCall < 0 || firstCheckpoint > firstToolCall {
		t.Fatalf("checkpoint index = %d, first tool call index = %d; want checkpoint before tool call", firstCheckpoint, firstToolCall)
	}
	if err := r.checkpointManager.Restore(context.Background(), checkpointID); err != nil {
		t.Fatalf("Restore() error = %v", err)
	}
	if got := readReactTestFile(t, filepath.Join(root, "a.txt")); got != "a original\n" {
		t.Fatalf("a.txt = %q, want restored checkpoint content", got)
	}
	if got := readReactTestFile(t, filepath.Join(root, "b.txt")); got != "unrelated user mutation\n" {
		t.Fatalf("b.txt = %q, want unrelated mutation preserved", got)
	}
}

func TestExecuteNativeToolCallsStoresLargeOutputOutOfBand(t *testing.T) {
	reg := agenttools.NewRegistry()
	reg.Register(agenttools.Tool{
		Name:        "large_output",
		Description: "large output test tool",
		Execute: func(_ context.Context, _ map[string]any) (string, error) {
			return "0123456789", nil
		},
	})
	session := NewSession()
	store := sessionstore.NewFileOutputStore(t.TempDir())
	r := NewRunner(Config{Tools: reg, Session: session, OutputStore: store, OutputStoreThresholdBytes: 5})
	turn := session.RecordInput("run large output tool")
	active, cancel, err := session.BeginTurn(context.Background(), "turn-1")
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()

	if err := r.executeNativeToolCalls(active.Context, turn, []llm.NativeToolCall{{ID: "c1", Name: "large_output", ArgsJSON: `{}`}}); err != nil {
		t.Fatal(err)
	}

	snap := session.Snapshot()
	var result *protocol.ToolResultItem
	for i := range snap.Items {
		if snap.Items[i].ToolResult != nil {
			result = snap.Items[i].ToolResult
		}
	}
	if result == nil {
		t.Fatal("missing tool result item")
	}
	if result.Handle == "" || result.OriginalBytes != 10 || result.SHA256 == "" {
		t.Fatalf("stored metadata = %#v", result)
	}
	if result.Text == "0123456789" || !strings.Contains(result.Text, result.Handle) || !strings.Contains(result.Text, "10 bytes") {
		t.Fatalf("summary text = %q", result.Text)
	}
	got, err := store.Read(context.Background(), sessionstore.OutputHandle{ID: result.Handle, Bytes: result.OriginalBytes, SHA256: result.SHA256}, 2, 4)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "2345" {
		t.Fatalf("stored output = %q, want 2345", got)
	}
}

func TestExecuteNativeToolCallsHidesOutputStoreHandleFromRenderer(t *testing.T) {
	reg := agenttools.NewRegistry()
	reg.Register(agenttools.Tool{
		Name:        "large_output",
		Description: "large output test tool",
		Execute: func(_ context.Context, _ map[string]any) (string, error) {
			return strings.Repeat("x", 32), nil
		},
	})
	session := NewSession()
	store := sessionstore.NewFileOutputStore(t.TempDir())
	renderer := &recordingRenderer{}
	r := NewRunner(Config{Tools: reg, Session: session, Renderer: renderer, OutputStore: store, OutputStoreThresholdBytes: 5})
	turn := session.RecordInput("run large output tool")
	active, cancel, err := session.BeginTurn(context.Background(), "turn-1")
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()

	if err := r.executeNativeToolCalls(active.Context, turn, []llm.NativeToolCall{{ID: "c1", Name: "large_output", ArgsJSON: `{}`}}); err != nil {
		t.Fatal(err)
	}

	result := lastToolResult(t, session)
	// The model must be told the handle, the checksum, and how to read it:
	// wording that implied retrieval was not yet possible made models give up
	// on large output instead of calling read_output.
	if result.Handle == "" || !strings.Contains(result.Text, result.Handle) {
		t.Fatalf("session result = %#v, want handle-bearing model-visible result", result)
	}
	if !strings.Contains(result.Text, result.SHA256) {
		t.Fatalf("session result text omits the checksum: %q", result.Text)
	}
	if !strings.Contains(result.Text, "read_output") {
		t.Fatalf("session result text does not tell the model how to read it: %q", result.Text)
	}
	if len(renderer.toolTexts) != 1 {
		t.Fatalf("renderer tool texts = %#v, want one", renderer.toolTexts)
	}
	display := renderer.toolTexts[0]
	if strings.Contains(display, result.Handle) || strings.Contains(display, "SHA256") || strings.Contains(display, "Tool output stored out-of-band") {
		t.Fatalf("renderer display leaked output-store internals: %q", display)
	}
	if !strings.Contains(display, "output stored") || !strings.Contains(display, "32 bytes") {
		t.Fatalf("renderer display = %q, want concise stored-output summary", display)
	}
}

func TestExecuteNativeToolCallsKeepsReadOutputInlineWhenLarge(t *testing.T) {
	store := sessionstore.NewFileOutputStore(t.TempDir())
	largePayload := "read-output-sentinel:" + strings.Repeat("abcdef", 64)
	handle, err := store.Put(context.Background(), "session", []byte(largePayload))
	if err != nil {
		t.Fatalf("store output: %v", err)
	}
	reg := agenttools.NewRegistry()
	reg.Register(agenttools.NewReadOutput(store))
	session := NewSession()
	r := NewRunner(Config{Tools: reg, Session: session, OutputStore: store, OutputStoreThresholdBytes: 5})
	turn := session.RecordInput("read stored output")
	active, cancel, err := session.BeginTurn(context.Background(), "turn-1")
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()

	args := fmt.Sprintf(`{"handle":%q,"limit":%d,"offset":0}`, handle.ID, len(largePayload))
	if err := r.executeNativeToolCalls(active.Context, turn, []llm.NativeToolCall{{ID: "c1", Name: "read_output", ArgsJSON: args}}); err != nil {
		t.Fatal(err)
	}

	result := lastToolResult(t, session)
	if result.Handle != "" || result.OriginalBytes != 0 || result.SHA256 != "" {
		t.Fatalf("read_output result was re-stored out of band: %#v", result)
	}
	if !strings.Contains(result.Text, "read-output-sentinel:") || !strings.Contains(result.Text, `"content":`) {
		t.Fatalf("read_output result = %q, want inline JSON content", result.Text)
	}
}

func TestExecuteNativeToolCallsKeepsLargeOutputInlineWhenOutputStoreNil(t *testing.T) {
	reg := agenttools.NewRegistry()
	reg.Register(agenttools.Tool{
		Name:        "large_output",
		Description: "large output test tool",
		Execute: func(_ context.Context, _ map[string]any) (string, error) {
			return "0123456789", nil
		},
	})
	session := NewSession()
	r := NewRunner(Config{Tools: reg, Session: session, OutputStoreThresholdBytes: 5})
	turn := session.RecordInput("run large output tool")
	active, cancel, err := session.BeginTurn(context.Background(), "turn-1")
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()

	if err := r.executeNativeToolCalls(active.Context, turn, []llm.NativeToolCall{{ID: "c1", Name: "large_output", ArgsJSON: `{}`}}); err != nil {
		t.Fatal(err)
	}

	result := lastToolResult(t, session)
	if result.Text != "0123456789" || result.Handle != "" || result.OriginalBytes != 0 || result.SHA256 != "" {
		t.Fatalf("tool result = %#v, want inline without handle metadata", result)
	}
}

func TestExecuteNativeToolCallsKeepsSmallOutputInlineWhenOutputStoreConfigured(t *testing.T) {
	reg := agenttools.NewRegistry()
	reg.Register(agenttools.Tool{
		Name:        "small_output",
		Description: "small output test tool",
		Execute: func(_ context.Context, _ map[string]any) (string, error) {
			return "0123", nil
		},
	})
	session := NewSession()
	r := NewRunner(Config{Tools: reg, Session: session, OutputStore: sessionstore.NewFileOutputStore(t.TempDir()), OutputStoreThresholdBytes: 5})
	turn := session.RecordInput("run small output tool")
	active, cancel, err := session.BeginTurn(context.Background(), "turn-1")
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()

	if err := r.executeNativeToolCalls(active.Context, turn, []llm.NativeToolCall{{ID: "c1", Name: "small_output", ArgsJSON: `{}`}}); err != nil {
		t.Fatal(err)
	}

	result := lastToolResult(t, session)
	if result.Text != "0123" || result.Handle != "" || result.OriginalBytes != 0 || result.SHA256 != "" {
		t.Fatalf("tool result = %#v, want inline without handle metadata", result)
	}
}

func lastToolResult(t *testing.T, session *Session) *protocol.ToolResultItem {
	t.Helper()
	var result *protocol.ToolResultItem
	snap := session.Snapshot()
	for i := range snap.Items {
		if snap.Items[i].ToolResult != nil {
			result = snap.Items[i].ToolResult
		}
	}
	if result == nil {
		t.Fatal("missing tool result item")
	}
	return result
}

func messagesText(messages []llm.Message) string {
	var b strings.Builder
	for _, msg := range messages {
		b.WriteString(msg.Content)
		b.WriteByte('\n')
	}
	return b.String()
}

func assertContainsAll(t *testing.T, text string, parts ...string) {
	t.Helper()
	for _, part := range parts {
		if !strings.Contains(text, part) {
			t.Fatalf("text %q missing %q", text, part)
		}
	}
}

func TestRepeatOverlayCatchesVariedRangeRereads(t *testing.T) {
	s := repeatToolCallState{
		lastToolName: "read_file",
		lastTarget:   "game.js#500-600",
		recent: []string{
			"read_file:game.js#1-100",
			"read_file:game.js#100-200",
			"read_file:game.js",
			"read_file:other.js#1-50",
			"read_file:game.js#200-300",
			"read_file:game.js#300-400",
			"read_file:game.js#400-500",
			"read_file:game.js#500-600",
		},
		streak: 1,
	}
	got := s.overlayContent(repeatToolCallThreshold)
	if !strings.Contains(got, "Loop detection") || !strings.Contains(got, `"game.js"`) {
		t.Fatalf("overlay = %q, want same-file reread nudge", got)
	}

	// Linear paging under the threshold stays silent.
	s.recent = s.recent[:4]
	if got := s.overlayContent(repeatToolCallThreshold); got != "" {
		t.Fatalf("overlay = %q, want empty for light paging", got)
	}
}
