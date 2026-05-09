package react

import (
	"context"
	"errors"
	"fmt"
	"slices"
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

func (r *recordingRenderer) Retry(text string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.retryTexts = append(r.retryTexts, text)
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
	t.Run("inspect first action guidance", func(t *testing.T) {
		session := NewSession()
		session.SetTaskState(TaskState{
			Objective:            "inspect the image input flow",
			Operation:            "inspect",
			RequiredVerification: "inspect the repository with read/search tools before answering. For a casual repo overview, usually inspect the repo root and one high-signal file such as README.md, then give a brief overview grounded only in that evidence",
		})
		r := NewRunner(Config{Session: session})

		output := r.promptHookOutput(context.Background())

		got := hookOverlayContent(output, "inspect_first_action")
		if !strings.Contains(got, "Start with a short natural sentence") {
			t.Fatalf("inspect_first_action = %q", got)
		}
		if strings.Contains(got, "instead of prose") {
			t.Fatalf("inspect_first_action = %q", got)
		}
	})

	t.Run("overview first action guidance", func(t *testing.T) {
		session := NewSession()
		session.SetTaskState(TaskState{
			Objective:            "whats this repo all about",
			Operation:            "overview",
			RequiredVerification: "inspect the repository with read/search tools before answering. For a casual repo overview, usually inspect the repo root and one high-signal file such as README.md, then give a brief overview grounded only in that evidence",
		})
		r := NewRunner(Config{Session: session})

		output := r.promptHookOutput(context.Background())

		if got := hookOverlayContent(output, "inspect_first_action"); !strings.Contains(got, "Start with a short natural sentence") {
			t.Fatalf("inspect_first_action = %q", got)
		}
	})

	t.Run("inspect first action guidance clears after read evidence", func(t *testing.T) {
		session := NewSession()
		session.SetTaskState(TaskState{
			Objective:            "inspect the theme setup",
			Operation:            "inspect",
			RequiredVerification: "inspect the repository with read/search tools before answering",
		})
		session.RecordInput("inspect the theme setup")
		session.AppendAssistantWithToolCalls([]llm.NativeToolCall{{ID: "c1", Name: "read_file", ArgsJSON: `{"path":"README.md"}`}})
		session.AppendNativeToolResult("c1", "forge readme")
		r := NewRunner(Config{Session: session})

		output := r.promptHookOutput(context.Background())

		if got := hookOverlayContent(output, "inspect_first_action"); got != "" {
			t.Fatalf("inspect_first_action should be cleared after read evidence, got %q", got)
		}
	})

	t.Run("repo overview guidance flips to synthesis after read evidence", func(t *testing.T) {
		session := NewSession()
		session.SetTaskState(TaskState{
			Objective:            "whats this repo all about",
			Operation:            "overview",
			RequiredVerification: "inspect the repository with read/search tools before answering. For a casual repo overview, usually inspect the repo root and one high-signal file such as README.md, then give a brief overview grounded only in that evidence",
		})
		session.RecordInput("whats this repo all about")
		session.AppendAssistantWithToolCalls([]llm.NativeToolCall{{ID: "c1", Name: "read_file", ArgsJSON: `{"path":"README.md"}`}})
		session.AppendNativeToolResult("c1", "forge readme")
		r := NewRunner(Config{Session: session})

		output := r.promptHookOutput(context.Background())

		if got := hookOverlayContent(output, "inspect_first_action"); !strings.Contains(got, "stop exploring and answer briefly") {
			t.Fatalf("inspect_first_action = %q", got)
		}
	})

	t.Run("preview first action guidance", func(t *testing.T) {
		session := NewSession()
		session.SetTaskState(TaskState{
			Objective:            "show me 3 theme ideas and start a preview",
			Operation:            "preview",
			RequiredVerification: "use preview or artifact tools, verify the preview URL or visible result, and do not rely on an ad-hoc shell webserver",
		})
		r := NewRunner(Config{Session: session})

		output := r.promptHookOutput(context.Background())

		if got := hookOverlayContent(output, "preview_workflow"); !strings.Contains(got, "most likely directory or named file") {
			t.Fatalf("preview_workflow = %q", got)
		}
	})

	t.Run("preview guidance flips to build after exploration budget", func(t *testing.T) {
		session := NewSession()
		session.SetTaskState(TaskState{
			Objective:            "show me 3 theme ideas and start a preview",
			Operation:            "preview",
			RequiredVerification: "use preview or artifact tools, verify the preview URL or visible result, and do not rely on an ad-hoc shell webserver",
		})
		session.RecordInput("show me 3 theme ideas and start a preview")
		session.AppendAssistantWithToolCalls([]llm.NativeToolCall{{ID: "c1", Name: "read_file", ArgsJSON: `{"path":"internal/tui/chattheme.go"}`}})
		session.AppendNativeToolResult("c1", "theme source")
		r := NewRunner(Config{Session: session})
		r.planWorkflow = planWorkflowState{
			mode:              "preview",
			active:            true,
			synthesisRequired: true,
		}

		output := r.promptHookOutput(context.Background())

		if got := hookOverlayContent(output, "preview_workflow"); !strings.Contains(got, "Stop exploring. Write the preview artifact") {
			t.Fatalf("preview_workflow = %q", got)
		}
	})

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

// nativeToolCallDriver simulates a provider that returns a native tool call
// on the first invocation and a plain text response on subsequent invocations.
type nativeToolCallDriver struct {
	callCount int
	lastTools []llm.ToolDef
	lastMsgs  []llm.Message
	lastOpts  []llm.NativeToolOptions
}

func (d *nativeToolCallDriver) Name() string { return "native-tool-driver" }

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
		out <- llm.Token{ToolCall: &llm.NativeToolCall{ID: "c1", Name: "git_status", ArgsJSON: `{}`}}
	default:
		out <- llm.Token{Text: "No changes detected."}
	}
	return nil
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
	if len(rec.tokenTexts) != 0 {
		t.Fatalf("renderer tokenTexts = %#v, expected empty (buffered when tools present)", rec.tokenTexts)
	}
	if len(rec.fullTexts) == 0 || rec.fullTexts[0] != "I'll inspect the README first." {
		t.Fatalf("renderer fullTexts = %#v", rec.fullTexts)
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
	session.SetTaskState(TaskState{
		Objective:            "whats this repo all about",
		Operation:            "overview",
		RequiredVerification: "inspect the repository with read/search tools before answering",
	})
	r := NewRunner(Config{
		Driver:  driver,
		Tools:   reg,
		Session: session,
	})

	err := r.Run(context.Background(), "whats this repo all about")
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
	lastOpts  []llm.NativeToolOptions
}

func (d *nativeSequenceDriver) Name() string { return "native-sequence" }

func (d *nativeSequenceDriver) Stream(_ context.Context, msgs []llm.Message, out chan<- llm.Token) error {
	defer close(out)
	d.allMsgs = append(d.allMsgs, append([]llm.Message(nil), msgs...))
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

func TestAllowedToolsForExplicitPushRequestIncludesCommandTools(t *testing.T) {
	tools := allowedToolNamesForSnapshot(SessionSnapshot{LastInput: "push it"})

	if !containsString(tools, "run_command") {
		t.Fatalf("push request tools = %#v, want run_command", tools)
	}
	if !containsString(tools, "git_status") {
		t.Fatalf("push request tools = %#v, want git_status", tools)
	}
}

func TestAllowedToolsForPushVerificationIncludesGitTools(t *testing.T) {
	tools := allowedToolNamesForSnapshot(SessionSnapshot{LastInput: "did you push?"})

	if !containsString(tools, "git_status") {
		t.Fatalf("push verification tools = %#v, want git_status", tools)
	}
}

func TestAllowedToolsForPlainBugReportIncludesImplementationTools(t *testing.T) {
	tools := allowedToolNamesForSnapshot(SessionSnapshot{LastInput: "when typing in the input pane the cursor sticks to the last letter"})

	for _, want := range []string{"read_file", "search", "edit_file", "run_command"} {
		if !containsString(tools, want) {
			t.Fatalf("bug report tools = %#v, want %s", tools, want)
		}
	}
}

func TestAllowedToolsForActionFollowUpIncludesWriteAndCommandTools(t *testing.T) {
	for _, input := range []string{"do it", "continue", "use what you need"} {
		tools := allowedToolNamesForSnapshot(SessionSnapshot{LastInput: input})

		for _, want := range []string{"read_file", "edit_file", "write_file", "run_command", "git_status"} {
			if !containsString(tools, want) {
				t.Fatalf("%q tools = %#v, want %s", input, tools, want)
			}
		}
	}
}

func containsString(values []string, want string) bool {
	return slices.Contains(values, want)
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

func TestRunnerSelectsPluginToolForPluginOnlyInput(t *testing.T) {
	reg := agenttools.NewRegistry()
	reg.Register(agenttools.Tool{
		Name:        "plugin__demo__search_docs",
		Description: "search docs",
		Parameters:  []agenttools.ParameterDef{{Name: "query", Type: "string", Required: true}},
		Execute: func(_ context.Context, _ map[string]any) (string, error) {
			return "", nil
		},
	})
	r := NewRunner(Config{Tools: reg})

	ordinary := r.selectToolDefs(SessionSnapshot{LastInput: "hello there"})
	if len(ordinary) != 0 {
		t.Fatalf("ordinary chat should not expose plugin tools, got %#v", ordinary)
	}
	defs := r.selectToolDefs(SessionSnapshot{LastInput: "use the demo plugin to search docs"})
	if len(defs) != 1 || defs[0].Name != "plugin__demo__search_docs" {
		t.Fatalf("plugin-only input defs = %#v", defs)
	}
}

func TestRunnerDoesNotExposePluginToolsForOrdinaryRepoWork(t *testing.T) {
	reg := agenttools.NewRegistry()
	for _, name := range []string{"read_file", "list_dir", "plugin__oh-my-openagent__task", "plugin__oh-my-openagent__skill"} {
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
	r := NewRunner(Config{Tools: reg})

	defs := r.selectToolDefs(SessionSnapshot{LastInput: "inspect the repo architecture and summarize the weak spots"})
	names := toolDefNames(defs)

	for _, want := range []string{"read_file", "list_dir"} {
		if !containsString(names, want) {
			t.Fatalf("repo work tools = %#v, want %s", names, want)
		}
	}
	for _, blocked := range []string{"plugin__oh-my-openagent__task", "plugin__oh-my-openagent__skill"} {
		if containsString(names, blocked) {
			t.Fatalf("repo work tools = %#v, should not include plugin tool %s", names, blocked)
		}
	}
}

func TestRunnerGenericTaskTextDoesNotExposePluginTaskTool(t *testing.T) {
	reg := agenttools.NewRegistry()
	for _, name := range []string{"read_file", "plugin__oh-my-openagent__task"} {
		reg.Register(agenttools.Tool{Name: name, Description: name})
	}
	r := NewRunner(Config{Tools: reg})

	defs := r.selectToolDefs(SessionSnapshot{LastInput: "continue this task by reading the README"})
	names := toolDefNames(defs)

	if containsString(names, "plugin__oh-my-openagent__task") {
		t.Fatalf("generic task text exposed OMO task tool: %#v", names)
	}
	if !containsString(names, "read_file") {
		t.Fatalf("generic task tools = %#v, want read_file", names)
	}
}

func TestRunnerDelegationIntentRestrictsParentToDelegationTools(t *testing.T) {
	reg := agenttools.NewRegistry()
	for _, name := range []string{
		"read_file",
		"list_dir",
		"plugin__oh-my-openagent__skill",
		"plugin__oh-my-openagent__task",
		"spawn_agent",
		"wait_agent",
	} {
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
	r := NewRunner(Config{Tools: reg})

	defs := r.selectToolDefs(SessionSnapshot{LastInput: "ask three agents to review the codebase"})
	names := toolDefNames(defs)

	for _, want := range []string{"spawn_agent", "wait_agent"} {
		if !containsString(names, want) {
			t.Fatalf("delegation tools = %#v, want %s", names, want)
		}
	}
	for _, blocked := range []string{"read_file", "list_dir", "plugin__oh-my-openagent__skill", "plugin__oh-my-openagent__task"} {
		if containsString(names, blocked) {
			t.Fatalf("delegation tools = %#v, should not include competing parent tool %s", names, blocked)
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
	if decision.Reason != "delegation_intent" {
		t.Fatalf("reason = %q, want delegation_intent", decision.Reason)
	}
	if !decision.RequireToolCall {
		t.Fatalf("RequireToolCall = false, want true")
	}
	for _, want := range []string{"spawn_agent", "wait_agent"} {
		if !containsString(decision.ToolNames, want) {
			t.Fatalf("tool names = %#v, want %s", decision.ToolNames, want)
		}
	}
}

func TestRunnerBroadRepoAuditRestrictsParentToDelegationTools(t *testing.T) {
	reg := agenttools.NewRegistry()
	for _, name := range []string{"read_file", "list_dir", "plugin__oh-my-openagent__task", "spawn_agent", "wait_agent"} {
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
	r := NewRunner(Config{Tools: reg})

	defs := r.selectToolDefs(SessionSnapshot{LastInput: "audit this repo and tell me where we fall down compared to the tui big hitters out there, codex, claude, opencode, github copilot."})
	names := toolDefNames(defs)

	for _, want := range []string{"spawn_agent", "wait_agent"} {
		if !containsString(names, want) {
			t.Fatalf("audit tools = %#v, want %s", names, want)
		}
	}
	for _, blocked := range []string{"read_file", "list_dir", "plugin__oh-my-openagent__task"} {
		if containsString(names, blocked) {
			t.Fatalf("audit tools = %#v, should not include competing parent tool %s", names, blocked)
		}
	}
}

func TestRunnerRequiresToolCallBeforeDelegationCompletes(t *testing.T) {
	driver := &nativeSequenceDriver{steps: [][]llm.Token{
		{{ToolCall: &llm.NativeToolCall{ID: "wait-1", Name: "wait_agent", ArgsJSON: `{"id":"agent-1"}`}}},
		{{Text: "done"}},
	}}
	reg := agenttools.NewRegistry()
	for _, name := range []string{"spawn_agent", "wait_agent"} {
		toolName := name
		reg.Register(agenttools.Tool{
			Name:        toolName,
			Description: toolName,
			AutoApprove: true,
			Execute: func(_ context.Context, _ map[string]any) (string, error) {
				return `{"id":"agent-1","status":"completed"}`, nil
			},
		})
	}
	r := NewRunner(Config{
		Driver:  driver,
		Tools:   reg,
		Session: NewSession(),
	})

	if err := r.Run(context.Background(), "ask three agents to review the codebase"); err != nil {
		t.Fatal(err)
	}
	if len(driver.lastOpts) == 0 {
		t.Fatal("expected native tool options to be passed")
	}
	if !driver.lastOpts[0].RequireToolCall {
		t.Fatalf("RequireToolCall = false, want true for delegation intent")
	}
}

func TestRunnerRetriesTextWhenToolCallRequired(t *testing.T) {
	driver := &nativeSequenceDriver{steps: [][]llm.Token{
		{{Text: "Let me explore the repos first."}},
		{{ToolCall: &llm.NativeToolCall{ID: "spawn-1", Name: "spawn_agent", ArgsJSON: `{"role":"explorer","task_description":"inspect repo"}`}}},
		{{ToolCall: &llm.NativeToolCall{ID: "wait-1", Name: "wait_agent", ArgsJSON: `{"id":"agent-1"}`}}},
		{{Text: "delegation complete"}},
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
	reg.Register(agenttools.Tool{
		Name:        "wait_agent",
		Description: "wait child",
		AutoApprove: true,
		Execute: func(_ context.Context, _ map[string]any) (string, error) {
			return `{"id":"agent-1","role":"explorer","status":"completed","result":"done"}`, nil
		},
	})
	session := NewSession()
	r := NewRunner(Config{
		Driver:  driver,
		Tools:   reg,
		Session: session,
	})

	if err := r.Run(context.Background(), "ask an agent to inspect the repo"); err != nil {
		t.Fatal(err)
	}
	if driver.callCount < 4 {
		t.Fatalf("callCount = %d, want retry through tool calls and final answer", driver.callCount)
	}
	snap := session.Snapshot()
	if !historyIncludesCompletedToolCall(snap, "wait_agent") {
		t.Fatalf("history missing completed wait_agent: %#v", snap.History)
	}
	if got := snap.History[len(snap.History)-1].Content; got != "delegation complete" {
		t.Fatalf("final content = %q", got)
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

	if err := r.Run(context.Background(), "fix this bug after checking active agents"); err != nil {
		t.Fatal(err)
	}
	snap := session.Snapshot()
	if got := snap.History[len(snap.History)-1].Content; got != "agent-1 is running" {
		t.Fatalf("final content = %q", got)
	}
}

func TestRunnerRequiresToolCallForQueuedDelegationInput(t *testing.T) {
	driver := &nativeSequenceDriver{steps: [][]llm.Token{
		{{ToolCall: &llm.NativeToolCall{ID: "status-1", Name: "git_status", ArgsJSON: `{}`}}},
		{{Text: "I'll inspect first."}},
		{{ToolCall: &llm.NativeToolCall{ID: "spawn-1", Name: "spawn_agent", ArgsJSON: `{"role":"explorer","task_description":"inspect repo"}`}}},
		{{Text: "delegation complete"}},
	}}
	reg := agenttools.NewRegistry()
	session := NewSession()
	reg.Register(agenttools.Tool{
		Name:        "git_status",
		Description: "git status",
		AutoApprove: true,
		Execute: func(_ context.Context, _ map[string]any) (string, error) {
			session.QueuePendingInput("ask agents to review the codebase")
			return "working tree clean", nil
		},
	})
	reg.Register(agenttools.Tool{
		Name:        "spawn_agent",
		Description: "spawn agent",
		AutoApprove: true,
		Execute: func(_ context.Context, _ map[string]any) (string, error) {
			return `{"id":"agent-1","status":"running"}`, nil
		},
	})
	r := NewRunner(Config{
		Driver:  driver,
		Tools:   reg,
		Session: session,
	})

	if err := r.Run(context.Background(), "inspect repo status"); err != nil {
		t.Fatal(err)
	}
	if got := driver.callCount; got != 4 {
		t.Fatalf("driver calls = %d, want 4", got)
	}
	snap := session.Snapshot()
	if got := snap.LastInput; got != "ask agents to review the codebase" {
		t.Fatalf("last input = %q, want queued delegation input", got)
	}
	if got := snap.History[len(snap.History)-1].Content; got != "delegation complete" {
		t.Fatalf("final content = %q", got)
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

func TestRunnerStopsForcingToolsAfterDelegatedWait(t *testing.T) {
	reg := agenttools.NewRegistry()
	for _, name := range []string{"spawn_agent", "wait_agent"} {
		reg.Register(agenttools.Tool{Name: name, Description: name})
	}
	r := NewRunner(Config{Tools: reg})
	snap := SessionSnapshot{
		LastInput: "ask three agents to review the codebase",
		History: []llm.Message{
			{Role: llm.RoleAssistant, ToolCalls: []llm.NativeToolCall{{ID: "wait-1", Name: "wait_agent", ArgsJSON: `{}`}}},
			{Role: llm.RoleTool, ToolCallID: "wait-1", Content: `{"status":"completed"}`},
		},
	}

	if defs := r.selectToolDefs(snap); len(defs) != 0 {
		t.Fatalf("delegation should stop exposing tools after wait_agent result, got %#v", defs)
	}
}

func TestRunnerKeepsWaitToolAvailableForOutstandingSpawnedAgent(t *testing.T) {
	reg := agenttools.NewRegistry()
	for _, name := range []string{"spawn_agent", "wait_agent", "read_file"} {
		reg.Register(agenttools.Tool{Name: name, Description: name})
	}
	r := NewRunner(Config{Tools: reg})
	snap := SessionSnapshot{
		LastInput: "are agents still running?",
		History: []llm.Message{
			{Role: llm.RoleAssistant, ToolCalls: []llm.NativeToolCall{{ID: "spawn-1", Name: "spawn_agent", ArgsJSON: `{}`}}},
			{Role: llm.RoleTool, ToolCallID: "spawn-1", Content: `{"id":"agent-1","role":"repo-auditor","status":"running"}`},
		},
	}

	names := toolDefNames(r.selectToolDefs(snap))
	if !containsString(names, "wait_agent") {
		t.Fatalf("tools with outstanding agent = %#v, want wait_agent", names)
	}
	if shouldRequireToolCallForSnapshot(snap) {
		t.Fatal("status follow-up should not force a wait_agent tool call")
	}
}

func TestRunnerUsesAgentTaskStateForOutstandingAgent(t *testing.T) {
	reg := agenttools.NewRegistry()
	for _, name := range []string{"spawn_agent", "wait_agent", "read_file"} {
		reg.Register(agenttools.Tool{Name: name, Description: name})
	}
	r := NewRunner(Config{Tools: reg})
	snap := SessionSnapshot{
		LastInput: "are agents still running?",
		AgentTasks: []AgentTaskState{{
			ID:     "agent-1",
			Role:   "repo-auditor",
			Status: AgentStatusRunning,
		}},
		History: []llm.Message{
			{Role: llm.RoleTool, ToolCallID: "malformed", Content: `{not-json`},
		},
	}

	names := toolDefNames(r.selectToolDefs(snap))
	if !containsString(names, "wait_agent") {
		t.Fatalf("tools with state-backed outstanding agent = %#v, want wait_agent", names)
	}
	got := hookOverlayContent(promptHookOutputForSnapshot(t, snap), "agent_status")
	for _, want := range []string{"agent-1", "repo-auditor", "running"} {
		if !strings.Contains(got, want) {
			t.Fatalf("agent_status overlay = %q, want %q", got, want)
		}
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

func TestRunnerAgentTaskStateOverridesStaleTranscript(t *testing.T) {
	snap := SessionSnapshot{
		LastInput: "what happened?",
		AgentTasks: []AgentTaskState{{
			ID:     "agent-1",
			Role:   "repo-auditor",
			Status: AgentStatusCompleted,
			Result: "done",
		}},
		History: []llm.Message{
			{Role: llm.RoleAssistant, ToolCalls: []llm.NativeToolCall{{ID: "spawn-1", Name: "spawn_agent", ArgsJSON: `{}`}}},
			{Role: llm.RoleTool, ToolCallID: "spawn-1", Content: `{"id":"agent-1","role":"repo-auditor","status":"running"}`},
		},
	}

	if shouldRequireToolCallForSnapshot(snap) {
		t.Fatal("state-completed agent should not force wait_agent from stale transcript")
	}
	if got := hookOverlayContent(promptHookOutputForSnapshot(t, snap), "agent_status"); got != "" {
		t.Fatalf("agent_status overlay = %q, want empty", got)
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

	if names := toolDefNames(r.selectToolDefs(snap)); containsString(names, "wait_agent") {
		t.Fatalf("killed agent tools = %#v, should not expose wait_agent from stale transcript", names)
	}
	if shouldRequireToolCallForSnapshot(snap) {
		t.Fatal("killed agent should not force wait_agent from stale transcript")
	}
	if got := hookOverlayContent(promptHookOutputForSnapshot(t, snap), "agent_status"); got != "" {
		t.Fatalf("agent_status overlay = %q, want empty for killed agent", got)
	}
}

func TestRunnerFallsBackToTranscriptWhenAgentTaskStateIsPartial(t *testing.T) {
	reg := agenttools.NewRegistry()
	for _, name := range []string{"spawn_agent", "wait_agent", "agent_status", "kill_agent"} {
		reg.Register(agenttools.Tool{Name: name, Description: name})
	}
	r := NewRunner(Config{Tools: reg})
	snap := SessionSnapshot{
		LastInput: "what are agents doing?",
		AgentTasks: []AgentTaskState{{
			ID:     "missing-agent",
			Status: AgentStatusNotFound,
		}},
		History: []llm.Message{
			{Role: llm.RoleAssistant, ToolCalls: []llm.NativeToolCall{{ID: "spawn-1", Name: "spawn_agent", ArgsJSON: `{}`}}},
			{Role: llm.RoleTool, ToolCallID: "spawn-1", Content: `{"id":"agent-1","role":"repo-auditor","status":"running"}`},
		},
	}

	names := toolDefNames(r.selectToolDefs(snap))
	if !containsString(names, "wait_agent") {
		t.Fatalf("partial state fallback tools = %#v, want wait_agent", names)
	}
	got := hookOverlayContent(promptHookOutputForSnapshot(t, snap), "agent_status")
	if !strings.Contains(got, "agent-1") {
		t.Fatalf("agent_status overlay = %q, want transcript-backed agent-1", got)
	}
}

func TestRunnerAllowsRepeatedWaitForTimedOutAgentState(t *testing.T) {
	reg := agenttools.NewRegistry()
	for _, name := range []string{"spawn_agent", "wait_agent", "read_file"} {
		reg.Register(agenttools.Tool{Name: name, Description: name})
	}
	r := NewRunner(Config{Tools: reg})
	snap := SessionSnapshot{
		LastInput: "continue delegated work",
		AgentTasks: []AgentTaskState{{
			ID:     "agent-1",
			Role:   "repo-auditor",
			Status: AgentStatusTimeout,
		}},
	}

	names := toolDefNames(r.selectToolDefs(snap))
	if !containsString(names, "wait_agent") {
		t.Fatalf("timed-out unresolved agent tools = %#v, want wait_agent", names)
	}
}

func TestRunnerExposesStatusAndKillForOutstandingAgent(t *testing.T) {
	reg := agenttools.NewRegistry()
	for _, name := range []string{"spawn_agent", "wait_agent", "agent_status", "kill_agent", "read_file"} {
		reg.Register(agenttools.Tool{Name: name, Description: name})
	}
	r := NewRunner(Config{Tools: reg})
	snap := SessionSnapshot{
		LastInput: "what is the child agent doing?",
		AgentTasks: []AgentTaskState{{
			ID:     "agent-1",
			Role:   "repo-auditor",
			Status: AgentStatusRunning,
		}},
	}

	names := toolDefNames(r.selectToolDefs(snap))
	for _, want := range []string{"wait_agent", "agent_status", "kill_agent"} {
		if !containsString(names, want) {
			t.Fatalf("outstanding agent tools = %#v, want %s", names, want)
		}
	}
	if containsString(names, "spawn_agent") {
		t.Fatalf("outstanding agent tools = %#v, should not offer another spawn", names)
	}
}

func TestRunnerKeepsWaitingWhenOneOfMultipleAgentsCompletes(t *testing.T) {
	reg := agenttools.NewRegistry()
	for _, name := range []string{"spawn_agent", "wait_agent", "read_file"} {
		reg.Register(agenttools.Tool{Name: name, Description: name})
	}
	r := NewRunner(Config{Tools: reg})
	snap := SessionSnapshot{
		LastInput: "what are the agents doing?",
		History: []llm.Message{
			{Role: llm.RoleAssistant, ToolCalls: []llm.NativeToolCall{
				{ID: "spawn-1", Name: "spawn_agent", ArgsJSON: `{}`},
				{ID: "spawn-2", Name: "spawn_agent", ArgsJSON: `{}`},
			}},
			{Role: llm.RoleTool, ToolCallID: "spawn-1", Content: `{"id":"agent-1","role":"repo-auditor","status":"running"}`},
			{Role: llm.RoleTool, ToolCallID: "spawn-2", Content: `{"id":"agent-2","role":"code-reviewer","status":"running"}`},
			{Role: llm.RoleAssistant, ToolCalls: []llm.NativeToolCall{{ID: "wait-1", Name: "wait_agent", ArgsJSON: `{"id":"agent-1"}`}}},
			{Role: llm.RoleTool, ToolCallID: "wait-1", Content: `{"id":"agent-1","role":"repo-auditor","status":"completed","result":"done"}`},
		},
	}

	names := toolDefNames(r.selectToolDefs(snap))
	if !containsString(names, "wait_agent") {
		t.Fatalf("tools with one outstanding agent = %#v, want wait_agent", names)
	}
	if shouldRequireToolCallForSnapshot(snap) {
		t.Fatal("status follow-up should not force a wait_agent tool call")
	}
}

func TestRunnerRequiresWaitForOutstandingAgentDuringDelegationTurn(t *testing.T) {
	snap := SessionSnapshot{
		LastInput: "ask three agents to review the codebase",
		History: []llm.Message{
			{Role: llm.RoleAssistant, ToolCalls: []llm.NativeToolCall{{ID: "spawn-1", Name: "spawn_agent", ArgsJSON: `{}`}}},
			{Role: llm.RoleTool, ToolCallID: "spawn-1", Content: `{"id":"agent-1","role":"code-reviewer","status":"running"}`},
		},
	}

	if !shouldRequireToolCallForSnapshot(snap) {
		t.Fatal("delegation turn with outstanding agent should require wait_agent")
	}
}

func TestRunnerPromptIncludesOutstandingAgentStatus(t *testing.T) {
	session := NewSession()
	session.RecordInput("ask three agents to review the codebase")
	session.AppendAssistantWithToolCalls([]llm.NativeToolCall{{ID: "spawn-1", Name: "spawn_agent", ArgsJSON: `{}`}})
	session.AppendNativeToolResult("spawn-1", `{"id":"agent-1","role":"code-reviewer","status":"running"}`)
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

func TestRunnerRestoresActionToolsAfterDelegatedWaitWhenUserAskedForFileWrite(t *testing.T) {
	reg := agenttools.NewRegistry()
	for _, name := range []string{"spawn_agent", "wait_agent", "write_file", "run_command", "git_status", "git_commit"} {
		reg.Register(agenttools.Tool{Name: name, Description: name})
	}
	r := NewRunner(Config{Tools: reg})
	snap := SessionSnapshot{
		LastInput: "ask agents to compare the implementation, then write the findings to docs/findings/status.md and run tests",
		History: []llm.Message{
			{Role: llm.RoleAssistant, ToolCalls: []llm.NativeToolCall{{ID: "wait-1", Name: "wait_agent", ArgsJSON: `{}`}}},
			{Role: llm.RoleTool, ToolCallID: "wait-1", Content: `{"status":"completed"}`},
		},
	}

	defs := r.selectToolDefs(snap)
	names := toolDefNames(defs)
	for _, want := range []string{"write_file", "run_command"} {
		if !containsString(names, want) {
			t.Fatalf("post-delegation tools = %#v, want %s", names, want)
		}
	}
	for _, blocked := range []string{"spawn_agent", "wait_agent"} {
		if containsString(names, blocked) {
			t.Fatalf("post-delegation tools = %#v, should stop forcing %s", names, blocked)
		}
	}
}

func TestRunnerRestoresActionToolsAfterDelegatedWaitWhenChildReportsFileTarget(t *testing.T) {
	reg := agenttools.NewRegistry()
	for _, name := range []string{"spawn_agent", "wait_agent", "write_file", "run_command", "git_status", "git_commit"} {
		reg.Register(agenttools.Tool{Name: name, Description: name})
	}
	r := NewRunner(Config{Tools: reg})
	snap := SessionSnapshot{
		LastInput: "use repo-auditor to audit forge chat --model <compat-model>",
		History: []llm.Message{
			{Role: llm.RoleAssistant, ToolCalls: []llm.NativeToolCall{{ID: "wait-1", Name: "wait_agent", ArgsJSON: `{}`}}},
			{Role: llm.RoleTool, ToolCallID: "wait-1", Content: `{"status":"completed","result":"Intended report path would be: docs/reports/2026-05-07-best-of-claude-forge-plan-conformance.md and commit it."}`},
		},
	}

	defs := r.selectToolDefs(snap)
	names := toolDefNames(defs)
	for _, want := range []string{"write_file", "git_commit"} {
		if !containsString(names, want) {
			t.Fatalf("post-delegation tools = %#v, want %s", names, want)
		}
	}
	for _, blocked := range []string{"spawn_agent", "wait_agent"} {
		if containsString(names, blocked) {
			t.Fatalf("post-delegation tools = %#v, should stop forcing %s", names, blocked)
		}
	}
}

func TestRunnerDoesNotExposeCommandToolsFromIncidentalChildAuditText(t *testing.T) {
	reg := agenttools.NewRegistry()
	for _, name := range []string{"spawn_agent", "wait_agent", "write_file", "run_command", "git_status"} {
		reg.Register(agenttools.Tool{Name: name, Description: name})
	}
	r := NewRunner(Config{Tools: reg})
	snap := SessionSnapshot{
		LastInput: "use repo-auditor to audit this repo, then write docs/reports/live-agent-write.md with the findings. Do not commit.",
		History: []llm.Message{
			{Role: llm.RoleAssistant, ToolCalls: []llm.NativeToolCall{{ID: "wait-1", Name: "wait_agent", ArgsJSON: `{}`}}},
			{Role: llm.RoleTool, ToolCallID: "wait-1", Content: `{"status":"completed","result":"Audit findings mention run commands only as a reviewed capability. Intended report path: docs/reports/live-agent-write.md. Do not commit."}`},
		},
	}

	defs := r.selectToolDefs(snap)
	names := toolDefNames(defs)
	if !containsString(names, "write_file") {
		t.Fatalf("post-delegation tools = %#v, want write_file", names)
	}
	if containsString(names, "run_command") {
		t.Fatalf("post-delegation tools = %#v, should not include incidental run_command", names)
	}
}

func TestRunnerRestoresWriteToolsForPostDelegationWriteDocFollowUp(t *testing.T) {
	reg := agenttools.NewRegistry()
	for _, name := range []string{"spawn_agent", "wait_agent", "write_file", "edit_file", "apply_patch", "git_status"} {
		reg.Register(agenttools.Tool{Name: name, Description: name})
	}
	r := NewRunner(Config{Tools: reg})
	snap := SessionSnapshot{
		LastInput: "tool access is available .. write teh doc",
		History: []llm.Message{
			{Role: llm.RoleAssistant, ToolCalls: []llm.NativeToolCall{{ID: "wait-1", Name: "wait_agent", ArgsJSON: `{}`}}},
			{Role: llm.RoleTool, ToolCallID: "wait-1", Content: `{"status":"completed","result":"Use the parent agent to save the final assessment document."}`},
		},
	}

	defs := r.selectToolDefs(snap)
	names := toolDefNames(defs)
	for _, want := range []string{"write_file", "edit_file", "apply_patch"} {
		if !containsString(names, want) {
			t.Fatalf("post-delegation write-doc tools = %#v, want %s", names, want)
		}
	}
	for _, blocked := range []string{"spawn_agent", "wait_agent"} {
		if containsString(names, blocked) {
			t.Fatalf("post-delegation write-doc tools = %#v, should stop forcing %s", names, blocked)
		}
	}
}

func TestRunnerRestoresWriteToolsFromPendingDelegationAction(t *testing.T) {
	reg := agenttools.NewRegistry()
	for _, name := range []string{"spawn_agent", "wait_agent", "write_file", "edit_file", "apply_patch", "git_status"} {
		reg.Register(agenttools.Tool{Name: name, Description: name})
	}
	r := NewRunner(Config{Tools: reg})
	snap := SessionSnapshot{
		LastInput: "continue",
		PendingDelegationAction: &DelegationActionState{
			Kind:       DelegationActionWriteDoc,
			TargetPath: "docs/reports/audit.md",
		},
		History: []llm.Message{
			{Role: llm.RoleAssistant, ToolCalls: []llm.NativeToolCall{{ID: "wait-1", Name: "wait_agent", ArgsJSON: `{}`}}},
			{Role: llm.RoleTool, ToolCallID: "wait-1", Content: `{"status":"completed","result":"findings returned"}`},
		},
	}

	names := toolDefNames(r.selectToolDefs(snap))
	for _, want := range []string{"write_file", "edit_file", "apply_patch"} {
		if !containsString(names, want) {
			t.Fatalf("pending action tools = %#v, want %s", names, want)
		}
	}
	if containsString(names, "spawn_agent") || containsString(names, "wait_agent") {
		t.Fatalf("pending action tools = %#v, should not force delegation", names)
	}
}

func TestRunnerRecordsPendingDelegationWriteActionAfterWait(t *testing.T) {
	session := NewSession()
	session.RecordInput("use repo-auditor then write the report to docs/reports/audit.md")
	r := NewRunner(Config{Session: session})

	r.updatePostDelegationWorkflow("wait_agent", `{"id":"agent-1","status":"completed","result":"findings returned"}`, false)

	action := session.Snapshot().PendingDelegationAction
	if action == nil {
		t.Fatal("expected pending delegation action")
	}
	if action.Kind != DelegationActionWriteDoc || action.TargetPath != "docs/reports/audit.md" || action.SourceAgent != "agent-1" {
		t.Fatalf("pending delegation action = %#v", action)
	}
}

func TestRunnerRecordsPendingDelegationWriteActionAtSpawnForPathlessArtifactRequests(t *testing.T) {
	for _, input := range []string{
		"ask repo-auditor to inspect the repo, then write me a memo",
		"ask repo-auditor to inspect the repo and save a note",
		"ask repo-auditor to inspect the repo and create a report",
		"ask repo-auditor to inspect the repo and write findings",
	} {
		t.Run(input, func(t *testing.T) {
			session := NewSession()
			session.RecordInput(input)
			r := NewRunner(Config{Session: session})

			r.updatePostDelegationWorkflow("spawn_agent", `{"id":"agent-1","role":"repo-auditor","status":"running"}`, false)

			action := session.Snapshot().PendingDelegationAction
			if action == nil {
				t.Fatal("expected pending delegation action")
			}
			if action.Kind != DelegationActionWriteDoc || action.SourceAgent != "agent-1" {
				t.Fatalf("pending delegation action = %#v", action)
			}
			if action.TargetPath != "" {
				t.Fatalf("target path = %q, want empty for pathless request", action.TargetPath)
			}
		})
	}
}

func TestRunnerKeepsWriteToolsFromSpawnActionWhenWaitResultHasNoWriteText(t *testing.T) {
	reg := agenttools.NewRegistry()
	for _, name := range []string{"spawn_agent", "wait_agent", "write_file", "edit_file", "apply_patch", "git_status"} {
		reg.Register(agenttools.Tool{Name: name, Description: name})
	}
	session := NewSession()
	session.RecordInput("ask repo-auditor to inspect the repo, then write me a memo")
	session.SetPendingDelegationAction(DelegationActionState{Kind: DelegationActionWriteDoc, SourceAgent: "agent-1"})
	r := NewRunner(Config{Tools: reg, Session: session})
	snap := session.Snapshot()
	snap.LastInput = "continue"
	snap.History = []llm.Message{
		{Role: llm.RoleAssistant, ToolCalls: []llm.NativeToolCall{{ID: "wait-1", Name: "wait_agent", ArgsJSON: `{}`}}},
		{Role: llm.RoleTool, ToolCallID: "wait-1", Content: `{"id":"agent-1","status":"completed","result":"analysis complete"}`},
	}

	names := toolDefNames(r.selectToolDefs(snap))
	for _, want := range []string{"write_file", "edit_file", "apply_patch"} {
		if !containsString(names, want) {
			t.Fatalf("pending action tools = %#v, want %s", names, want)
		}
	}
	for _, blocked := range []string{"spawn_agent", "wait_agent"} {
		if containsString(names, blocked) {
			t.Fatalf("pending action tools = %#v, should not force %s", names, blocked)
		}
	}
}

func TestRunnerClearsPendingDelegationActionAfterParentWrite(t *testing.T) {
	session := NewSession()
	session.SetPendingDelegationAction(DelegationActionState{Kind: DelegationActionWriteDoc, TargetPath: "docs/reports/audit.md"})
	r := NewRunner(Config{Session: session})

	r.updatePostDelegationWorkflow("write_file", "wrote docs/reports/audit.md", false)

	if action := session.Snapshot().PendingDelegationAction; action != nil {
		t.Fatalf("pending delegation action after write = %#v", action)
	}
}

func TestRunnerKeepsWriteToolsForUnresolvedPostDelegationDocument(t *testing.T) {
	reg := agenttools.NewRegistry()
	for _, name := range []string{"spawn_agent", "wait_agent", "write_file", "edit_file", "apply_patch", "git_status"} {
		reg.Register(agenttools.Tool{Name: name, Description: name})
	}
	r := NewRunner(Config{Tools: reg})
	snap := SessionSnapshot{
		InitialInput: "forge has had many changes, did they all follow the plan, are there any gaps, whats next, figure this out and write me a nice doc",
		LastInput:    "what happened?",
		History: []llm.Message{
			{Role: llm.RoleUser, Content: "forge has had many changes, did they all follow the plan, are there any gaps, whats next, figure this out and write me a nice doc"},
			{Role: llm.RoleAssistant, ToolCalls: []llm.NativeToolCall{{ID: "wait-1", Name: "wait_agent", ArgsJSON: `{}`}}},
			{Role: llm.RoleTool, ToolCallID: "wait-1", Content: `{"status":"completed","result":"Bottom line: Forge hardening is mostly on-plan. The parent should save this as the final document."}`},
			{Role: llm.RoleAssistant, Content: "## Bottom Line\nForge hardening work appears mostly on-plan and materially implemented."},
			{Role: llm.RoleUser, Content: "what happened?"},
		},
	}

	defs := r.selectToolDefs(snap)
	names := toolDefNames(defs)
	for _, want := range []string{"write_file", "edit_file", "apply_patch"} {
		if !containsString(names, want) {
			t.Fatalf("post-delegation pending-document tools = %#v, want %s", names, want)
		}
	}
	for _, blocked := range []string{"spawn_agent", "wait_agent"} {
		if containsString(names, blocked) {
			t.Fatalf("post-delegation pending-document tools = %#v, should stop forcing %s", names, blocked)
		}
	}
}

func TestRunnerClearsPostDelegationDocumentWorkAfterWrite(t *testing.T) {
	reg := agenttools.NewRegistry()
	for _, name := range []string{"spawn_agent", "wait_agent", "write_file", "edit_file", "apply_patch", "git_status"} {
		reg.Register(agenttools.Tool{Name: name, Description: name})
	}
	r := NewRunner(Config{Tools: reg})
	snap := SessionSnapshot{
		LastInput: "what happened?",
		History: []llm.Message{
			{Role: llm.RoleUser, Content: "figure this out and write me a nice doc"},
			{Role: llm.RoleAssistant, ToolCalls: []llm.NativeToolCall{{ID: "wait-1", Name: "wait_agent", ArgsJSON: `{}`}}},
			{Role: llm.RoleTool, ToolCallID: "wait-1", Content: `{"status":"completed","result":"The parent should save this as the final document."}`},
			{Role: llm.RoleAssistant, ToolCalls: []llm.NativeToolCall{{ID: "write-1", Name: "write_file", ArgsJSON: `{}`}}},
			{Role: llm.RoleTool, ToolCallID: "write-1", Content: `wrote docs/reports/status.md`},
			{Role: llm.RoleUser, Content: "what happened?"},
		},
	}

	if defs := r.selectToolDefs(snap); len(defs) != 0 {
		t.Fatalf("post-delegation tools after successful write = %#v, want none", toolDefNames(defs))
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
	if err := r.executeNativeToolCalls(context.Background(), turn, []llm.NativeToolCall{{
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
