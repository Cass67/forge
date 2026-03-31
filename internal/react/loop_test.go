package react

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	agenttools "forge/internal/agent/tools"
	"forge/internal/hooks"
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

type captureMessagesDriver struct {
	lastMessages []llm.Message
	response     string
}

func (d *captureMessagesDriver) Name() string { return "capture-messages" }

func (d *captureMessagesDriver) Stream(_ context.Context, _ []llm.Message, out chan<- llm.Token) error {
	close(out)
	return errors.New("Stream should not be called on a NativeToolCaller driver")
}

func (d *captureMessagesDriver) StreamWithTools(_ context.Context, msgs []llm.Message, _ []llm.ToolDef, out chan<- llm.Token) error {
	defer close(out)
	d.lastMessages = append([]llm.Message(nil), msgs...)
	out <- llm.Token{Text: d.response}
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

	if err := r.Run(context.Background(), "inspect repo"); err != nil {
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
	if err := r.Run(context.Background(), "inspect repo"); err != nil {
		t.Fatal(err)
	}

	r.SetDriver(driver)
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

func TestRunnerRetriesRetryableCompletionFailureOnce(t *testing.T) {
	driver := &nativeScriptedDriver{responses: []string{"I'll inspect the repo first.", "Inspected the repo and found the theme entrypoints."}}
	session := NewSession()
	renderer := &recordingRenderer{}
	r := NewRunner(Config{
		Driver:   driver,
		Session:  session,
		Renderer: renderer,
		CompletionCheck: func(_ SessionSnapshot, finalText string) error {
			if strings.Contains(finalText, "I'll inspect") {
				return NewRetryableCompletionError(
					"non-compliant completion: narrated intent without evidence",
					"You have not inspected the repository yet. Use tools first and then answer with concrete evidence.",
				)
			}
			return nil
		},
	})

	if err := r.Run(context.Background(), "theme this app"); err != nil {
		t.Fatal(err)
	}
	if driver.callCount != 2 {
		t.Fatalf("driver calls = %d, want 2", driver.callCount)
	}
	snap := session.Snapshot()
	if len(snap.History) != 3 {
		t.Fatalf("history = %#v", snap.History)
	}
	if snap.History[1].Role != llm.RoleUser || !strings.Contains(snap.History[1].Content, "You have not inspected") {
		t.Fatalf("history retry prompt = %#v", snap.History)
	}
	if got := snap.Turns[0].FinalResponse; got != "Inspected the repo and found the theme entrypoints." {
		t.Fatalf("final response = %q", got)
	}
	renderer.mu.Lock()
	defer renderer.mu.Unlock()
	if len(renderer.tokenTexts) != 0 {
		t.Fatalf("expected buffered final answer rendering, got tokens %#v", renderer.tokenTexts)
	}
	if len(renderer.fullTexts) != 1 || renderer.fullTexts[0] != "Inspected the repo and found the theme entrypoints." {
		t.Fatalf("full texts = %#v", renderer.fullTexts)
	}
}

func TestRunnerFailsAfterSecondRetryableCompletionFailure(t *testing.T) {
	driver := &nativeScriptedDriver{responses: []string{"I'll inspect the repo first.", "I'll inspect it next."}}
	session := NewSession()
	r := NewRunner(Config{
		Driver:  driver,
		Session: session,
		CompletionCheck: func(_ SessionSnapshot, _ string) error {
			return NewRetryableCompletionError(
				"non-compliant completion",
				"Use tools before answering.",
			)
		},
	})

	err := r.Run(context.Background(), "inspect repo")
	if err == nil {
		t.Fatal("expected failure after second retryable completion")
	}
	if !strings.Contains(err.Error(), "non-compliant completion") {
		t.Fatalf("err = %v", err)
	}
	if driver.callCount != 2 {
		t.Fatalf("driver calls = %d, want 2", driver.callCount)
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
	session.AppendNativeToolResult("call-1", `{"status":"running","session_id":9,"command":"npm run dev","pty":true,"cols":120,"rows":40}`)
	session.CompleteTurn(turn, "", []TurnToolCall{{Name: "exec_session_start"}}, nil)
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
	driver := &captureMessagesDriver{response: "- Finding: runtime guidance is present."}
	session := NewSession()
	session.SetTaskState(TaskState{
		Objective:            "review this repo and tell me what i need to change",
		Operation:            "review",
		RequiredVerification: "produce source-grounded findings first, ordered by severity, and keep the summary secondary to the actual review issues",
	})

	r := NewRunner(Config{
		Driver:  driver,
		Session: session,
	})

	if err := r.Run(context.Background(), "review this repo"); err != nil {
		t.Fatal(err)
	}

	var reviewMsg string
	for _, msg := range driver.lastMessages {
		if msg.Role == llm.RoleSystem && strings.Contains(msg.Content, "[hook:runtime]") && strings.Contains(msg.Content, "Review workflow active") {
			reviewMsg = msg.Content
			break
		}
	}
	if reviewMsg == "" {
		t.Fatalf("expected review runtime guidance in messages: %#v", driver.lastMessages)
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
		t.Fatal(err)
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
		t.Fatal(err)
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

func TestRunnerPromptHookOutputIncludesRuntimeGuidance(t *testing.T) {
	t.Run("review guidance", func(t *testing.T) {
		session := NewSession()
		session.SetTaskState(TaskState{
			Objective:            "review this repo",
			Operation:            "review",
			RequiredVerification: "lead with findings",
		})
		r := NewRunner(Config{Session: session})

		output := r.promptHookOutput(context.Background())

		if got := hookOverlayContent(output, "review_guidance"); !strings.Contains(got, "Lead with findings") {
			t.Fatalf("review_guidance = %q", got)
		}
	})

	t.Run("blocked plan guidance", func(t *testing.T) {
		session := NewSession()
		session.SetTaskState(TaskState{
			Objective:            "write a plan",
			Operation:            "plan",
			RequiredVerification: "produce a concise plan",
		})
		session.SetPlanState(PlanState{
			Steps: []PlanStep{
				{Step: "Get a user decision", Status: "blocked", Blocker: "need user decision on default behavior"},
			},
		})
		r := NewRunner(Config{Session: session})

		output := r.promptHookOutput(context.Background())

		if got := hookOverlayContent(output, "plan_blocker"); !strings.Contains(got, "ask_user_question") {
			t.Fatalf("plan_blocker = %q", got)
		}
	})

	t.Run("synthesis validation search git and repeat guidance", func(t *testing.T) {
		session := NewSession()
		r := NewRunner(Config{Session: session})
		r.planWorkflow = planWorkflowState{
			mode:              "analysis",
			active:            true,
			synthesisRequired: true,
		}
		r.validationWorkflow = validationWorkflowState{
			ran:    true,
			passed: false,
			cmd:    "go test ./internal/react",
		}
		r.searchWorkflow = sameFileSearchWorkflowState{
			toolName: "code_search",
			path:     "internal/react/loop.go",
			streak:   sameFileSearchThrashThreshold,
			nudged:   true,
		}
		r.gitWorkflow = gitWorkflowState{
			mergeActive:    true,
			commitBlocker:  commitBlockerEdit,
			blockerSummary: "commit blocked by pre-commit hook failures",
		}
		r.repeatWorkflow = repeatToolCallState{
			lastToolName: "read_file",
			lastTarget:   "internal/react/loop.go",
			streak:       repeatToolCallThreshold,
		}

		output := r.promptHookOutput(context.Background())

		if got := hookOverlayContent(output, "synthesis_guidance"); !strings.Contains(got, "Analysis guidance") {
			t.Fatalf("synthesis_guidance = %q", got)
		}
		if got := hookOverlayContent(output, "validation_failure"); !strings.Contains(got, "Last validation failed") {
			t.Fatalf("validation_failure = %q", got)
		}
		if got := hookOverlayContent(output, "search_thrash"); !strings.Contains(got, "Search thrash guidance") {
			t.Fatalf("search_thrash = %q", got)
		}
		if got := hookOverlayContent(output, "git_workflow"); !strings.Contains(got, "Git merge workflow active") {
			t.Fatalf("git_workflow = %q", got)
		}
		if got := hookOverlayContent(output, "repeat_loop"); !strings.Contains(got, "Loop detection") {
			t.Fatalf("repeat_loop = %q", got)
		}
	})
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

func hookOverlayContent(output hooks.ExecutionOutput, key string) string {
	for _, overlay := range output.Overlays {
		if overlay.Key == key {
			return overlay.Content
		}
	}
	return ""
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
	for i := 0; i < explorations; i++ {
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
	r := NewRunner(Config{Driver: driver, Tools: reg, Session: session, MaxSessionTurns: explorations + 5})

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
	for i := 0; i < explorations; i++ {
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
	r := NewRunner(Config{Driver: driver, Tools: reg, Session: session, MaxSessionTurns: explorations + 5})

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
	for i := 0; i < repeatedSearches; i++ {
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
	r := NewRunner(Config{Driver: driver, Tools: reg, Session: session, MaxSessionTurns: repeatedSearches + 5})

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
	if err := r.Run(context.Background(), "list files"); err != nil {
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

func TestBlockedToolResultDoesNotFireOnStagedChange(t *testing.T) {
	r := NewRunner(Config{})
	// Simulate: agent runs `git status --porcelain` and gets a staged modification.
	r.updateGitWorkflowForCommand("git status --porcelain", "M  README.md")

	if r.gitWorkflow.unmergedFiles {
		t.Fatal("staged change incorrectly set unmergedFiles=true")
	}

	// Commit should not be blocked.
	args := map[string]any{"command": "git commit -m \"update readme\""}
	if blocked := r.blockedToolResult("run_command", args); blocked != "" {
		t.Fatalf("commit blocked unexpectedly: %q", blocked)
	}
}

func TestBlockedToolResultFiresOnActualConflict(t *testing.T) {
	r := NewRunner(Config{})
	// Simulate: git status --porcelain reports an unmerged file.
	r.updateGitWorkflowForCommand("git status --porcelain", "UU README.md")

	if !r.gitWorkflow.unmergedFiles {
		t.Fatal("UU status code did not set unmergedFiles=true")
	}

	args := map[string]any{"command": "git commit -m \"update readme\""}
	if blocked := r.blockedToolResult("run_command", args); blocked == "" {
		t.Fatal("expected commit to be blocked when UU conflict present")
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
