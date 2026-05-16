package react

import (
	"strings"
	"testing"
	"time"

	"forge/internal/hooks"
	"forge/internal/llm"
	"forge/internal/protocol"
)

func TestSessionRecordsUserAndAssistantItemsOnce(t *testing.T) {
	s := NewSession()
	turn := s.RecordInput("hello")
	s.AppendAssistantMessage("hi")
	s.CompleteTurn(turn, "hi", nil, nil)
	snap := s.Snapshot()
	var user, assistant, terminal int
	for _, item := range snap.Items {
		switch item.Kind {
		case protocol.ItemUserMessage:
			user++
		case protocol.ItemAssistantMessage:
			assistant++
		case protocol.ItemTurnComplete:
			terminal++
		}
	}
	if user != 1 || assistant != 1 || terminal != 1 {
		t.Fatalf("item counts user=%d assistant=%d terminal=%d items=%#v", user, assistant, terminal, snap.Items)
	}
}

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

func TestAppendAssistantToolTurnPreservesPreamble(t *testing.T) {
	s := NewSession()
	s.RecordInput("check the repo")
	s.AppendAssistantToolTurn("I'll inspect the README first.", []llm.NativeToolCall{
		{ID: "c1", Name: "read_file", ArgsJSON: `{"path":"README.md"}`},
	})

	snap := s.Snapshot()
	if len(snap.History) != 2 {
		t.Fatalf("want 2 history entries, got %d", len(snap.History))
	}
	last := snap.History[1]
	if got, want := last.Content, "I'll inspect the README first."; got != want {
		t.Fatalf("content = %q, want %q", got, want)
	}
	if len(last.ToolCalls) != 1 || last.ToolCalls[0].Name != "read_file" {
		t.Fatalf("tool calls = %#v", last.ToolCalls)
	}
}

func TestAppendAssistantToolTurnRedactsSecretArgsInHistory(t *testing.T) {
	s := NewSession()
	s.RecordInput("write the file")
	secret := "TOKEN=" + strings.Repeat("x", 24)
	s.AppendAssistantToolTurn("", []llm.NativeToolCall{{
		ID:       "c1",
		Name:     "write_file",
		ArgsJSON: `{"path":"note.txt","content":"` + secret + `"}`,
	}})

	snap := s.Snapshot()
	if got := snap.History[1].ToolCalls[0].ArgsJSON; strings.Contains(got, secret) {
		t.Fatalf("stored tool args leaked secret: %s", got)
	}
	if got := snap.History[1].ToolCalls[0].ArgsJSON; !strings.Contains(got, "<REDACTED:generic-token>") {
		t.Fatalf("stored tool args missing redaction marker: %s", got)
	}
}

func TestAppendAssistantToolTurnRedactsSecretPreambleInHistory(t *testing.T) {
	s := NewSession()
	s.RecordInput("write the file")
	secret := "TOKEN=" + strings.Repeat("x", 24)
	s.AppendAssistantToolTurn("using "+secret, []llm.NativeToolCall{{
		ID:       "c1",
		Name:     "write_file",
		ArgsJSON: `{"path":"note.txt","content":"ok"}`,
	}})

	snap := s.Snapshot()
	content := snap.History[1].Content
	if strings.Contains(content, secret) {
		t.Fatal("stored assistant preamble leaked secret")
	}
	if !strings.Contains(content, "<REDACTED:generic-token>") {
		t.Fatal("stored assistant preamble missing redaction marker")
	}
}

func TestSetLastAssistantReasoningRedactsSecrets(t *testing.T) {
	s := NewSession()
	s.RecordInput("write the file")
	s.AppendAssistantToolTurn("", []llm.NativeToolCall{{ID: "c1", Name: "git_status", ArgsJSON: `{}`}})
	secret := "TOKEN=" + strings.Repeat("x", 24)
	s.SetLastAssistantReasoning("saw " + secret)

	snap := s.Snapshot()
	reasoning := snap.History[1].ReasoningContent
	if strings.Contains(reasoning, secret) {
		t.Fatal("stored assistant reasoning leaked secret")
	}
	if !strings.Contains(reasoning, "<REDACTED:generic-token>") {
		t.Fatal("stored assistant reasoning missing redaction marker")
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

func TestSessionPendingDelegationActionAppearsInSnapshotAndClears(t *testing.T) {
	s := NewSession()
	s.SetPendingDelegationAction(DelegationActionState{
		Kind:        DelegationActionWriteDoc,
		TargetPath:  "docs/reports/audit.md",
		SourceAgent: "agent-1",
		Description: "write delegated audit report",
	})

	snap := s.Snapshot()
	if snap.PendingDelegationAction == nil {
		t.Fatal("expected pending delegation action")
	}
	if snap.PendingDelegationAction.Kind != DelegationActionWriteDoc || snap.PendingDelegationAction.TargetPath != "docs/reports/audit.md" {
		t.Fatalf("pending delegation action = %#v", snap.PendingDelegationAction)
	}
	snap.PendingDelegationAction.TargetPath = "mutated.md"
	if got := s.Snapshot().PendingDelegationAction.TargetPath; got != "docs/reports/audit.md" {
		t.Fatalf("session action mutated through snapshot: %q", got)
	}

	s.ClearPendingDelegationAction()
	if got := s.Snapshot().PendingDelegationAction; got != nil {
		t.Fatalf("pending delegation action after clear = %#v", got)
	}
}

func TestSessionPendingDelegationActionKinds(t *testing.T) {
	for _, kind := range []DelegationActionKind{
		DelegationActionWriteDoc,
		DelegationActionRunVerification,
		DelegationActionCommit,
		DelegationActionAskUser,
	} {
		t.Run(string(kind), func(t *testing.T) {
			s := NewSession()
			s.SetPendingDelegationAction(DelegationActionState{Kind: kind})
			if got := s.Snapshot().PendingDelegationAction; got == nil || got.Kind != kind {
				t.Fatalf("pending action for %q = %#v", kind, got)
			}
		})
	}
}

func TestSessionAgentTaskStateTracksLifecycle(t *testing.T) {
	s := NewSession()
	turn := s.RecordInput("audit the repo")
	created := time.Date(2026, 5, 8, 20, 30, 0, 0, time.UTC)
	started := created.Add(time.Second)
	progressAt := started.Add(2 * time.Second)
	completed := progressAt.Add(3 * time.Second)

	s.UpsertAgentTask(AgentTaskState{
		ID:          " agent-1 ",
		Role:        " repo-auditor ",
		Description: " Inspect repository ",
		Prompt:      " Read files and report findings ",
		Status:      AgentStatusPending,
		CreatedAt:   created,
		ParentTurn:  turn,
	})
	s.UpsertAgentTask(AgentTaskState{
		ID:             "agent-1",
		Status:         AgentStatusRunning,
		StartedAt:      started,
		LastActivityAt: started,
	})
	s.RecordAgentTaskProgress("agent-1", "read_file", "README.md", progressAt)
	s.UpsertAgentTask(AgentTaskState{
		ID:             "agent-1",
		Status:         AgentStatusCompleted,
		CompletedAt:    completed,
		LastActivityAt: completed,
		Result:         "done",
	})

	snap := s.Snapshot()
	if len(snap.AgentTasks) != 1 {
		t.Fatalf("agent tasks = %#v", snap.AgentTasks)
	}
	task := snap.AgentTasks[0]
	if task.ID != "agent-1" || task.Role != "repo-auditor" || task.Description != "Inspect repository" || task.Prompt != "Read files and report findings" {
		t.Fatalf("normalized task identity = %#v", task)
	}
	if task.Status != AgentStatusCompleted || task.ParentTurn != turn {
		t.Fatalf("task status/turn = %#v", task)
	}
	if !task.CreatedAt.Equal(created) || !task.StartedAt.Equal(started) || !task.CompletedAt.Equal(completed) || !task.LastActivityAt.Equal(completed) {
		t.Fatalf("task timestamps = %#v", task)
	}
	if task.Result != "done" || task.Error != "" {
		t.Fatalf("task terminal data = %#v", task)
	}
	if task.LastToolName != "read_file" || len(task.RecentActivity) != 1 || task.RecentActivity[0].Summary != "README.md" {
		t.Fatalf("task progress = %#v", task)
	}
}

func TestSessionAgentTaskStateCoversTerminalStatuses(t *testing.T) {
	s := NewSession()
	statuses := []AgentStatus{
		AgentStatusFailed,
		AgentStatusKilled,
		AgentStatusTimeout,
		AgentStatusNotFound,
	}
	for _, status := range statuses {
		s.UpsertAgentTask(AgentTaskState{ID: string(status), Status: status, Error: "terminal"})
	}

	snap := s.Snapshot()
	if len(snap.AgentTasks) != len(statuses) {
		t.Fatalf("agent tasks = %#v", snap.AgentTasks)
	}
	for i, status := range statuses {
		if snap.AgentTasks[i].Status != status {
			t.Fatalf("agent task %d status = %q, want %q", i, snap.AgentTasks[i].Status, status)
		}
	}
}

func TestSessionAgentTaskSnapshotIsCloned(t *testing.T) {
	s := NewSession()
	s.UpsertAgentTask(AgentTaskState{ID: "agent-1", Status: AgentStatusRunning})
	s.RecordAgentTaskProgress("agent-1", "list_dir", ".", time.Now())

	snap := s.Snapshot()
	snap.AgentTasks[0].Status = AgentStatusCompleted
	snap.AgentTasks[0].RecentActivity[0].ToolName = "mutated"

	next := s.Snapshot()
	if next.AgentTasks[0].Status != AgentStatusRunning {
		t.Fatalf("session task status mutated through snapshot: %#v", next.AgentTasks[0])
	}
	if next.AgentTasks[0].RecentActivity[0].ToolName != "list_dir" {
		t.Fatalf("session task activity mutated through snapshot: %#v", next.AgentTasks[0])
	}
}

func TestSessionClearRemovesAgentTaskState(t *testing.T) {
	s := NewSession()
	s.UpsertAgentTask(AgentTaskState{ID: "agent-1", Status: AgentStatusRunning})
	s.Clear()

	if got := s.Snapshot().AgentTasks; len(got) != 0 {
		t.Fatalf("agent tasks after clear = %#v", got)
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

func TestSessionSetHookOverlayUpsertsByKey(t *testing.T) {
	s := NewSession()
	s.SetHookOverlay(HookOverlay{
		Key:        "suggested_skill",
		Content:    "first",
		Priority:   HookPriorityNormal,
		Provenance: "runtime",
	})
	s.SetHookOverlay(HookOverlay{
		Key:        "suggested_skill",
		Content:    "second",
		Priority:   HookPriorityHigh,
		Provenance: "runtime",
	})

	got := s.Snapshot().HookOverlays
	if len(got) != 1 {
		t.Fatalf("hook overlays = %#v", got)
	}
	if got[0].Content != "second" || got[0].Priority != HookPriorityHigh {
		t.Fatalf("hook overlays = %#v", got)
	}
	if len(s.Snapshot().HookOutput.Overlays) != 1 {
		t.Fatalf("hook output overlays = %#v", s.Snapshot().HookOutput.Overlays)
	}
	if s.Snapshot().HookOutput.Overlays[0].Content != "second" {
		t.Fatalf("hook output overlays = %#v", s.Snapshot().HookOutput.Overlays)
	}
}

func TestSessionClearHookOverlayRemovesMatchingKey(t *testing.T) {
	s := NewSession()
	s.SetHookOverlays([]HookOverlay{
		{Key: "suggested_skill", Content: "first"},
		{Key: "plan_blocker", Content: "second"},
	})

	s.ClearHookOverlay("suggested_skill")

	got := s.Snapshot().HookOverlays
	if len(got) != 1 || got[0].Key != "plan_blocker" {
		t.Fatalf("hook overlays = %#v", got)
	}
	if got := s.Snapshot().HookOutput.Overlays; len(got) != 1 || got[0].Key != "plan_blocker" {
		t.Fatalf("hook output overlays = %#v", got)
	}
}

func TestSessionSetHookOutputStoresNormalizedHookState(t *testing.T) {
	s := NewSession()
	s.SetHookOutput(hooks.ExecutionOutput{
		Overlays: []hooks.OverlayResult{{
			Key:        "suggested_skill",
			Content:    "Use the TDD workflow before editing runtime behavior.",
			Priority:   hooks.PriorityHigh,
			Provenance: "runtime",
		}},
		Note: &hooks.NoteResult{
			Message:    "Runtime note from normalized hook output.",
			Priority:   hooks.PriorityNormal,
			Provenance: "runtime",
		},
	})

	snap := s.Snapshot()
	if len(snap.HookOutput.Overlays) != 1 {
		t.Fatalf("hook output overlays = %#v", snap.HookOutput.Overlays)
	}
	if snap.HookOutput.Note == nil || snap.HookOutput.Note.Message != "Runtime note from normalized hook output." {
		t.Fatalf("hook output note = %#v", snap.HookOutput.Note)
	}
	if len(snap.HookOverlays) != 1 || snap.HookOverlays[0].Key != "suggested_skill" {
		t.Fatalf("hook overlays = %#v", snap.HookOverlays)
	}
	if snap.RuntimeNote != "Runtime note from normalized hook output." {
		t.Fatalf("runtime note = %q", snap.RuntimeNote)
	}
}

func TestSessionSetRuntimeNoteReplacesTypedNoteMetadata(t *testing.T) {
	s := NewSession()
	s.SetHookOutput(hooks.ExecutionOutput{
		Note: &hooks.NoteResult{
			Message:    "old typed note",
			Priority:   hooks.PriorityLow,
			Provenance: "typed-handler",
		},
	})

	s.SetRuntimeNote("legacy runtime note")

	snap := s.Snapshot()
	if snap.HookOutput.Note == nil {
		t.Fatal("expected hook output note")
	}
	if snap.HookOutput.Note.Message != "legacy runtime note" {
		t.Fatalf("hook output note = %#v", snap.HookOutput.Note)
	}
	if snap.HookOutput.Note.Priority != hooks.PriorityHigh {
		t.Fatalf("hook output note priority = %v", snap.HookOutput.Note.Priority)
	}
	if snap.HookOutput.Note.Provenance != "runtime" {
		t.Fatalf("hook output note provenance = %q", snap.HookOutput.Note.Provenance)
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

func TestSessionMapsOverviewOperationToInspectMode(t *testing.T) {
	s := NewSession()
	s.SetTaskState(TaskState{
		Objective:            "tell me about this repo",
		Operation:            "overview",
		RequiredVerification: "give a brief overview",
	})
	if got := s.Snapshot().Mode; got != ModeInspect {
		t.Fatalf("mode after overview task state = %q, want %q", got, ModeInspect)
	}
}
