package react

import (
	"testing"

	"forge/internal/llm"
)

func TestAppendAssistantWithToolCalls(t *testing.T) {
	s := NewSession()
	s.RecordInput("check the repo")
	calls := []llm.NativeToolCall{
		{ID: "c1", Name: "git_status", ArgsJSON: `{}`},
		{ID: "c2", Name: "run_command", ArgsJSON: `{"command":"ls"}`},
	}
	s.AppendAssistantWithToolCalls(calls)

	snap := s.Snapshot()
	if len(snap.History) != 2 {
		t.Fatalf("want 2 history entries, got %d", len(snap.History))
	}
	last := snap.History[1]
	if last.Role != llm.RoleAssistant {
		t.Fatalf("role = %q, want assistant", last.Role)
	}
	if len(last.ToolCalls) != 2 {
		t.Fatalf("tool calls = %d, want 2", len(last.ToolCalls))
	}
	if last.ToolCalls[0].ID != "c1" || last.ToolCalls[0].Name != "git_status" {
		t.Fatal("first tool call mismatch")
	}
	if len(snap.Turns) != 1 || len(snap.Turns[0].ToolCalls) != 2 {
		t.Fatalf("turn tool calls = %#v", snap.Turns)
	}
	if snap.Turns[0].ToolCalls[1].Name != "run_command" {
		t.Fatalf("turn tool calls = %#v", snap.Turns[0].ToolCalls)
	}
}

func TestAppendNativeToolResult(t *testing.T) {
	s := NewSession()
	s.RecordInput("run ls")
	s.AppendAssistantWithToolCalls([]llm.NativeToolCall{
		{ID: "c1", Name: "run_command", ArgsJSON: `{"command":"ls"}`},
	})
	s.AppendNativeToolResult("c1", "file1.go\nfile2.go")

	snap := s.Snapshot()
	if len(snap.History) != 3 {
		t.Fatalf("want 3 history entries, got %d", len(snap.History))
	}
	result := snap.History[2]
	if result.Role != llm.RoleTool {
		t.Fatalf("role = %q, want tool", result.Role)
	}
	if result.ToolCallID != "c1" {
		t.Fatalf("tool_call_id = %q, want c1", result.ToolCallID)
	}
	if result.Content != "file1.go\nfile2.go" {
		t.Fatalf("content = %q", result.Content)
	}
}

func TestAppendNativeToolResultGuardsEmptyID(t *testing.T) {
	s := NewSession()
	s.RecordInput("check")
	s.AppendAssistantWithToolCalls([]llm.NativeToolCall{{ID: "c1", Name: "git_status", ArgsJSON: "{}"}})
	s.AppendNativeToolResult("", "result")
	snap := s.Snapshot()
	// Empty toolCallID should be ignored — only user+assistant in history
	if len(snap.History) != 2 {
		t.Fatalf("empty toolCallID should be ignored, got %d history entries", len(snap.History))
	}
}

func TestMessagesIncludesToolRoleMessages(t *testing.T) {
	s := NewSession()
	s.RecordInput("check status")
	s.AppendAssistantWithToolCalls([]llm.NativeToolCall{
		{ID: "c1", Name: "git_status", ArgsJSON: `{}`},
	})
	s.AppendNativeToolResult("c1", "nothing to commit")

	msgs := s.Messages("system prompt")
	// system + user + assistant(tool_calls) + tool(result)
	if len(msgs) != 4 {
		t.Fatalf("want 4 messages, got %d\nhistory: %+v", len(msgs), msgs)
	}
	if msgs[2].Role != llm.RoleAssistant || len(msgs[2].ToolCalls) != 1 {
		t.Fatal("message 3 should be assistant with tool calls")
	}
	if msgs[3].Role != llm.RoleTool || msgs[3].ToolCallID != "c1" {
		t.Fatal("message 4 should be tool result with correct ID")
	}
}

func TestSessionTaskStateAppearsInSnapshot(t *testing.T) {
	s := NewSession()
	s.SetTaskState(TaskState{
		Objective:            "merge feature/go-rewrite into main",
		RequiredVerification: "verify main contains the resulting commit",
	})

	snap := s.Snapshot()
	if snap.TaskState == nil {
		t.Fatal("expected task state in snapshot")
	}
	if snap.TaskState.Objective != "merge feature/go-rewrite into main" {
		t.Fatalf("objective = %q", snap.TaskState.Objective)
	}
	if snap.TaskState.RequiredVerification != "verify main contains the resulting commit" {
		t.Fatalf("required verification = %q", snap.TaskState.RequiredVerification)
	}
}

func TestSessionPlanStateAppearsInSnapshot(t *testing.T) {
	s := NewSession()
	s.SetPlanState(PlanState{
		Explanation: "Doing the runtime work in slices",
		Steps: []PlanStep{
			{Step: "Inspect code", Status: "completed"},
			{Step: "Patch runtime", Status: "in_progress"},
		},
	})

	snap := s.Snapshot()
	if snap.PlanState == nil || len(snap.PlanState.Steps) != 2 {
		t.Fatalf("plan state = %#v", snap.PlanState)
	}
}

func TestPlanStateHelpersExposeActiveAndBlockedSteps(t *testing.T) {
	state := PlanState{
		Steps: []PlanStep{
			{Step: "Wait for approval", Status: "blocked", Blocker: "need user confirmation"},
			{Step: "Patch runtime", Status: "pending"},
		},
	}

	if !state.HasActiveStep() {
		t.Fatal("expected active step")
	}
	active, ok := state.ActiveStep()
	if !ok {
		t.Fatal("expected active step details")
	}
	if active.Step != "Wait for approval" {
		t.Fatalf("active step = %#v", active)
	}
	blocked, ok := state.BlockedStep()
	if !ok {
		t.Fatal("expected blocked step details")
	}
	if blocked.Blocker != "need user confirmation" {
		t.Fatalf("blocked step = %#v", blocked)
	}
}

func TestPlanStateHelpersReportNoActiveStepWhenAllCompleted(t *testing.T) {
	state := PlanState{
		Steps: []PlanStep{
			{Step: "Inspect code", Status: "completed"},
			{Step: "Patch runtime", Status: "completed"},
		},
	}

	if state.HasActiveStep() {
		t.Fatal("did not expect active step")
	}
	if _, ok := state.ActiveStep(); ok {
		t.Fatal("did not expect active step details")
	}
	if _, ok := state.BlockedStep(); ok {
		t.Fatal("did not expect blocked step details")
	}
}

func TestSessionQueuesAndDrainsPendingInput(t *testing.T) {
	s := NewSession()
	s.QueuePendingInput("steer toward tests")
	s.QueuePendingInput("focus on service/main.py")

	if !s.HasPendingInput() {
		t.Fatal("expected pending input")
	}
	got := s.TakePendingInput()
	if len(got) != 2 {
		t.Fatalf("pending input = %#v", got)
	}
	if got[0] != "steer toward tests" || got[1] != "focus on service/main.py" {
		t.Fatalf("pending input order = %#v", got)
	}
	if s.HasPendingInput() {
		t.Fatal("expected pending input to be drained")
	}
}

func TestSessionDefaultsToChatModeAndTracksTaskMode(t *testing.T) {
	s := NewSession()
	if got := s.Snapshot().Mode; got != ModeChat {
		t.Fatalf("default mode = %q, want %q", got, ModeChat)
	}

	s.SetTaskState(TaskState{
		Objective:            "plan the runtime work",
		Operation:            "plan",
		RequiredVerification: "produce a plan",
	})
	if got := s.Snapshot().Mode; got != ModePlan {
		t.Fatalf("mode after task state = %q, want %q", got, ModePlan)
	}

	s.SetMode(ModeImplement)
	if got := s.Snapshot().Mode; got != ModeImplement {
		t.Fatalf("mode after explicit set = %q, want %q", got, ModeImplement)
	}
}
