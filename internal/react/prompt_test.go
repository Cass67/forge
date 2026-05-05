package react

import (
	"fmt"
	"strings"
	"testing"

	"forge/internal/hooks"
	"forge/internal/llm"
)

func TestDropOrphanedToolCallsRemovesUnpairedAssistant(t *testing.T) {
	msgs := []llm.Message{
		{Role: llm.RoleUser, Content: "hello"},
		{Role: llm.RoleAssistant, ToolCalls: []llm.NativeToolCall{{ID: "orphan", Name: "read_file", ArgsJSON: "{}"}}},
		{Role: llm.RoleUser, Content: "next turn"},
	}
	got := dropOrphanedToolCalls(msgs)
	if len(got) != 2 {
		t.Fatalf("want 2 messages after dropping orphan, got %d", len(got))
	}
	if got[0].Role != llm.RoleUser || got[0].Content != "hello" {
		t.Fatal("first message should be user hello")
	}
	if got[1].Role != llm.RoleUser || got[1].Content != "next turn" {
		t.Fatal("second message should be user next turn")
	}
}

func TestDropOrphanedToolCallsPreservesPaired(t *testing.T) {
	msgs := []llm.Message{
		{Role: llm.RoleUser, Content: "hello"},
		{Role: llm.RoleAssistant, ToolCalls: []llm.NativeToolCall{{ID: "c1", Name: "read_file", ArgsJSON: "{}"}}},
		{Role: llm.RoleTool, ToolCallID: "c1", Content: "result"},
		{Role: llm.RoleAssistant, Content: "done"},
	}
	got := dropOrphanedToolCalls(msgs)
	if len(got) != 4 {
		t.Fatalf("want 4 messages, got %d", len(got))
	}
}

func TestDropOrphanedToolCallsPreservesMultipleCalls(t *testing.T) {
	msgs := []llm.Message{
		{Role: llm.RoleUser, Content: "hello"},
		{Role: llm.RoleAssistant, ToolCalls: []llm.NativeToolCall{
			{ID: "c1", Name: "read_file", ArgsJSON: "{}"},
			{ID: "c2", Name: "write_file", ArgsJSON: "{}"},
		}},
		{Role: llm.RoleTool, ToolCallID: "c1", Content: "result1"},
		{Role: llm.RoleTool, ToolCallID: "c2", Content: "result2"},
		{Role: llm.RoleAssistant, Content: "done"},
	}
	got := dropOrphanedToolCalls(msgs)
	if len(got) != 5 {
		t.Fatalf("want 5 messages, got %d", len(got))
	}
}

func TestDropOrphanedToolCallsDropsPartiallyPaired(t *testing.T) {
	msgs := []llm.Message{
		{Role: llm.RoleUser, Content: "hello"},
		{Role: llm.RoleAssistant, ToolCalls: []llm.NativeToolCall{
			{ID: "c1", Name: "read_file", ArgsJSON: "{}"},
			{ID: "c2", Name: "write_file", ArgsJSON: "{}"},
		}},
		{Role: llm.RoleTool, ToolCallID: "c1", Content: "result1"},
		// c2 missing — orphaned
	}
	got := dropOrphanedToolCalls(msgs)
	if len(got) != 2 {
		t.Fatalf("want 2 messages after dropping partially-paired assistant, got %d", len(got))
	}
	if got[0].Role != llm.RoleUser || got[0].Content != "hello" {
		t.Fatal("first should be user hello")
	}
	if got[1].Role != llm.RoleTool || got[1].ToolCallID != "c1" {
		t.Fatal("second should be tool result for c1")
	}
}

func TestDropOrphanedToolCallsEmptyInput(t *testing.T) {
	var msgs []llm.Message
	got := dropOrphanedToolCalls(msgs)
	if len(got) != 0 {
		t.Fatal("empty input should return empty")
	}
	got2 := dropOrphanedToolCalls(nil)
	if len(got2) != 0 {
		t.Fatal("nil input should return nil")
	}
}

func TestBuildPromptTrimsInput(t *testing.T) {
	if got := BuildPrompt("  inspect repo  "); got != "inspect repo" {
		t.Fatalf("BuildPrompt() = %q", got)
	}
}

func TestSessionMessagesIncludeCompactionSummaryContext(t *testing.T) {
	session := NewSession()
	first := session.RecordInput("prompt 1")
	session.AppendAssistantMessage("answer 1")
	session.CompleteTurn(first, "answer 1", nil, nil)

	second := session.RecordInput("prompt 2")
	session.AppendAssistantMessage("answer 2")
	session.CompleteTurn(second, "answer 2", nil, nil)

	if !CompactSessionHistory(session, 1) {
		t.Fatal("expected compaction")
	}

	messages := session.Messages("system prompt")
	if len(messages) < 4 {
		t.Fatalf("messages = %#v", messages)
	}
	if messages[0].Role != llm.RoleSystem || messages[0].Content != "system prompt" {
		t.Fatalf("system message = %#v", messages[0])
	}
	if messages[1].Role != llm.RoleSystem {
		t.Fatalf("summary message role = %q, want system", messages[1].Role)
	}
	if !strings.Contains(messages[1].Content, "Earlier conversation summary") {
		t.Fatalf("summary message = %q", messages[1].Content)
	}
	if !strings.Contains(messages[1].Content, "user: prompt 1") {
		t.Fatalf("summary message missing compacted turn detail: %q", messages[1].Content)
	}
	if !strings.Contains(messages[1].Content, "outcome: answer 1") {
		t.Fatalf("summary message missing semantic outcome detail: %q", messages[1].Content)
	}
	foundAnchor := false
	for _, msg := range messages {
		if msg.Role == llm.RoleSystem && strings.Contains(msg.Content, "Initial user request: prompt 1") {
			foundAnchor = true
			if !strings.Contains(msg.Content, "Conversation recall only") {
				t.Fatalf("initial request anchor missing recall-only scope: %q", msg.Content)
			}
		}
	}
	if !foundAnchor {
		t.Fatalf("messages missing compacted initial request anchor: %#v", messages)
	}
	var dialogue []llm.Message
	for _, msg := range messages {
		if msg.Role != llm.RoleSystem {
			dialogue = append(dialogue, msg)
		}
	}
	if len(dialogue) != 2 {
		t.Fatalf("remaining dialogue = %#v", dialogue)
	}
	if dialogue[0].Role != llm.RoleUser || dialogue[0].Content != "prompt 2" {
		t.Fatalf("remaining user message = %#v", dialogue[0])
	}
	if dialogue[1].Role != llm.RoleAssistant || dialogue[1].Content != "answer 2" {
		t.Fatalf("remaining assistant message = %#v", dialogue[1])
	}
}

func TestSessionMessagesIncludeInitialRequestAnchorForLongHistory(t *testing.T) {
	session := NewSession()
	initial := "how do we implement dragging of images into this pane then actioning them"
	turn := session.RecordInput(initial)
	session.AppendAssistantMessage("I'll inspect the image input flow.")
	session.CompleteTurn(turn, "I'll inspect the image input flow.", nil, nil)
	for i := 0; i < 18; i++ {
		session.AppendUserMessage(fmt.Sprintf("tool result chunk %d", i))
	}

	messages := session.Messages("system prompt")
	found := false
	for _, msg := range messages {
		if msg.Role == llm.RoleSystem && strings.Contains(msg.Content, "Initial user request:") {
			found = true
			if !strings.Contains(msg.Content, "Conversation recall only") {
				t.Fatalf("initial request anchor missing recall-only scope: %q", msg.Content)
			}
			if !strings.Contains(msg.Content, initial) {
				t.Fatalf("initial request anchor = %q", msg.Content)
			}
		}
	}
	if !found {
		t.Fatalf("messages missing initial request anchor: %#v", messages)
	}
}

func TestBuildMessages_LargeToolResultTruncated(t *testing.T) {
	lines := make([]string, 300)
	for i := range lines {
		lines[i] = fmt.Sprintf("output line %d", i+1)
	}
	bigResult := strings.Join(lines, "\n")

	snap := SessionSnapshot{
		History: []llm.Message{
			{Role: llm.RoleUser, Content: "run something"},
			{Role: llm.RoleAssistant, ToolCalls: []llm.NativeToolCall{{ID: "c1", Name: "run_command", ArgsJSON: `{}`}}},
			{Role: llm.RoleTool, ToolCallID: "c1", Content: bigResult},
		},
	}
	msgs := BuildMessages("sys", snap)

	var toolMsg *llm.Message
	for i := range msgs {
		if msgs[i].Role == llm.RoleTool {
			toolMsg = &msgs[i]
			break
		}
	}
	if toolMsg == nil {
		t.Fatal("no tool message in output")
	}
	// Middle lines must be absent from LLM context
	if strings.Contains(toolMsg.Content, "output line 150") {
		t.Error("middle lines should be truncated from LLM context")
	}
	// Head and tail must be preserved
	if !strings.Contains(toolMsg.Content, "output line 1") {
		t.Error("head lines should be present in LLM context")
	}
	if !strings.Contains(toolMsg.Content, "output line 300") {
		t.Error("tail lines should be present in LLM context")
	}
	// Truncation marker must appear
	if !strings.Contains(toolMsg.Content, "lines truncated)") {
		t.Error("truncation marker should be present")
	}
	// Original snapshot must not be mutated — truncateToolResults must copy
	if !strings.Contains(snap.History[2].Content, "output line 150") {
		t.Error("original snapshot history must not be mutated by BuildMessages")
	}
}

func TestBuildMessages_LargeToolCallArgsTruncated(t *testing.T) {
	largeContent := strings.Repeat("<div>preview</div>\n", 500)
	snap := SessionSnapshot{
		History: []llm.Message{
			{Role: llm.RoleUser, Content: "show me a preview"},
			{
				Role: llm.RoleAssistant,
				ToolCalls: []llm.NativeToolCall{{
					ID:       "c1",
					Name:     "artifact_write",
					ArgsJSON: fmt.Sprintf(`{"path":"artifacts/theme-preview/index.html","content":%q}`, largeContent),
				}},
			},
			{Role: llm.RoleTool, ToolCallID: "c1", Content: `{"handle":"artifact-1","path":"artifacts/theme-preview/index.html"}`},
		},
	}

	msgs := BuildMessages("sys", snap)
	if len(msgs) < 3 {
		t.Fatalf("messages = %#v", msgs)
	}
	if got := msgs[2].ToolCalls; len(got) != 1 {
		t.Fatalf("tool calls = %#v", got)
	}
	if strings.Contains(msgs[2].ToolCalls[0].ArgsJSON, largeContent[:32]) {
		t.Fatalf("expected large tool-call content to be compacted, got %q", msgs[2].ToolCalls[0].ArgsJSON)
	}
	if !strings.Contains(msgs[2].ToolCalls[0].ArgsJSON, "artifacts/theme-preview/index.html") {
		t.Fatalf("expected path to remain visible, got %q", msgs[2].ToolCalls[0].ArgsJSON)
	}
	if !strings.Contains(msgs[2].ToolCalls[0].ArgsJSON, "omitted 9500 chars") {
		t.Fatalf("expected compacted placeholder, got %q", msgs[2].ToolCalls[0].ArgsJSON)
	}
	if !strings.Contains(snap.History[1].ToolCalls[0].ArgsJSON, "<div>preview</div>") {
		t.Fatal("original snapshot tool-call args must not be mutated by BuildMessages")
	}
}

func TestBuildMessages_IncludesRuntimeNoteAsSystemMessage(t *testing.T) {
	snap := SessionSnapshot{
		RuntimeNote: "Git merge workflow active. Resolve unmerged files before retrying commit.",
		History: []llm.Message{
			{Role: llm.RoleUser, Content: "merge the branch"},
		},
	}

	msgs := BuildMessages("sys", snap)
	if len(msgs) < 3 {
		t.Fatalf("messages = %#v", msgs)
	}
	if msgs[1].Role != llm.RoleSystem {
		t.Fatalf("runtime note role = %q, want system", msgs[1].Role)
	}
	if !strings.Contains(msgs[1].Content, "Git merge workflow active") {
		t.Fatalf("runtime note = %q", msgs[1].Content)
	}
}

func TestBuildMessages_IncludesTaskStateAsSystemMessage(t *testing.T) {
	snap := SessionSnapshot{
		TaskState: &TaskState{
			Objective:            "merge feature/go-rewrite into main",
			RequiredVerification: "verify main contains the resulting commit",
			Operation:            "merge",
			TargetBranch:         "main",
		},
		History: []llm.Message{
			{Role: llm.RoleUser, Content: "merge it"},
		},
	}

	msgs := BuildMessages("sys", snap)
	if len(msgs) < 3 {
		t.Fatalf("messages = %#v", msgs)
	}
	if msgs[1].Role != llm.RoleSystem {
		t.Fatalf("task state role = %q, want system", msgs[1].Role)
	}
	if !strings.Contains(msgs[1].Content, "Task objective") {
		t.Fatalf("task state message = %q", msgs[1].Content)
	}
	if !strings.Contains(msgs[1].Content, "verify main contains the resulting commit") {
		t.Fatalf("task state missing verification = %q", msgs[1].Content)
	}
	if !strings.Contains(msgs[1].Content, "git_branch_state") || !strings.Contains(msgs[1].Content, "git_merge_status") {
		t.Fatalf("task state missing merge guidance = %q", msgs[1].Content)
	}
}

func TestBuildMessages_IncludesPlanStateAsSystemMessage(t *testing.T) {
	snap := SessionSnapshot{
		PlanState: &PlanState{
			Explanation: "Working through the approved runtime alignment",
			Steps: []PlanStep{
				{Step: "Add apply_patch", Status: "completed"},
				{Step: "Add update_plan", Status: "blocked", Blocker: "waiting on user confirmation for the next edit slice"},
			},
		},
		History: []llm.Message{{Role: llm.RoleUser, Content: "keep going"}},
	}

	msgs := BuildMessages("sys", snap)
	if len(msgs) < 3 {
		t.Fatalf("messages = %#v", msgs)
	}
	if msgs[1].Role != llm.RoleSystem || !strings.Contains(msgs[1].Content, "Current plan:") {
		t.Fatalf("plan message = %#v", msgs[1])
	}
	if !strings.Contains(msgs[1].Content, "[blocked] Add update_plan") {
		t.Fatalf("plan content = %q", msgs[1].Content)
	}
	if !strings.Contains(msgs[1].Content, "blocker: waiting on user confirmation for the next edit slice") {
		t.Fatalf("plan content = %q", msgs[1].Content)
	}
}

func TestBuildMessages_IncludesMemorySummaryAsSystemMessage(t *testing.T) {
	snap := SessionSnapshot{
		MemorySummary: "Previously learned: the runtime branch-switch guard should stay local-first.",
		TaskState: &TaskState{
			Objective:            "keep the branch-switch guard local-first",
			Operation:            "implement",
			RequiredVerification: "verify the runtime guard stays local-first",
		},
		History: []llm.Message{{Role: llm.RoleUser, Content: "keep going"}},
	}

	msgs := BuildMessages("sys", snap)
	found := false
	for _, msg := range msgs {
		if msg.Role == llm.RoleSystem && strings.Contains(msg.Content, "Memory summary:") {
			if !strings.Contains(msg.Content, "runtime branch-switch guard") {
				t.Fatalf("memory content = %q", msg.Content)
			}
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("memory summary not found in messages: %#v", msgs)
	}
}

func TestBuildMessages_OmitsMemorySummaryForShortPlainChat(t *testing.T) {
	snap := SessionSnapshot{
		Mode:          ModeChat,
		MemorySummary: "Previously learned: the runtime branch-switch guard should stay local-first.",
		History:       []llm.Message{{Role: llm.RoleUser, Content: "keep going"}},
		Turns: []TurnRecord{
			{Number: 1, Input: "first"},
			{Number: 2, Input: "second"},
		},
	}

	msgs := BuildMessages("sys", snap)
	for _, msg := range msgs {
		if msg.Role == llm.RoleSystem && strings.Contains(msg.Content, "Memory summary:") {
			t.Fatalf("unexpected memory summary in short plain chat: %#v", msgs)
		}
	}
}

func TestBuildMessages_IncludesHookOverlayAsSystemMessage(t *testing.T) {
	snap := SessionSnapshot{
		HookOverlays: []HookOverlay{{
			Key:        "reminder",
			Content:    "Prefer the preview lifecycle tools over an ad-hoc webserver.",
			Priority:   HookPriorityHigh,
			Provenance: "runtime",
		}},
		History: []llm.Message{{Role: llm.RoleUser, Content: "keep going"}},
	}

	msgs := BuildMessages("sys", snap)
	found := false
	for _, msg := range msgs {
		if msg.Role == llm.RoleSystem && strings.Contains(msg.Content, "[hook:runtime]") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("messages missing hook overlay: %#v", msgs)
	}
}

func TestBuildMessages_UsesNormalizedHookOutputWithoutDuplicatingVisibleNote(t *testing.T) {
	snap := SessionSnapshot{
		HookOutputSet: true,
		HookOutput: hooks.ExecutionOutput{
			Overlays: []hooks.OverlayResult{{
				Key:        "reminder",
				Content:    "Prefer the preview lifecycle tools over an ad-hoc webserver.",
				Priority:   hooks.PriorityHigh,
				Provenance: "runtime",
			}},
			Note: &hooks.NoteResult{
				Message:    "Normalized runtime note.",
				Priority:   hooks.PriorityHigh,
				Provenance: "runtime",
			},
		},
		HookOverlays: []HookOverlay{{
			Key:        "reminder",
			Content:    "legacy copy should not render twice",
			Priority:   HookPriorityHigh,
			Provenance: "runtime",
		}},
		RuntimeNote: "Normalized runtime note.",
		History:     []llm.Message{{Role: llm.RoleUser, Content: "keep going"}},
	}

	msgs := BuildMessages("sys", snap)
	overlayCount := 0
	noteCount := 0
	for _, msg := range msgs {
		if msg.Role != llm.RoleSystem {
			continue
		}
		if strings.Contains(msg.Content, "[hook:runtime]") && strings.Contains(msg.Content, "Prefer the preview lifecycle tools over an ad-hoc webserver.") {
			overlayCount++
		}
		if strings.Contains(msg.Content, "Normalized runtime note.") {
			noteCount++
		}
	}
	if overlayCount != 1 {
		t.Fatalf("overlay count = %d, messages = %#v", overlayCount, msgs)
	}
	if noteCount != 1 {
		t.Fatalf("runtime note count = %d, messages = %#v", noteCount, msgs)
	}
}

func TestBuildMessages_TypedHookOutputCanAuthoritativelyClearLegacyPromptState(t *testing.T) {
	snap := SessionSnapshot{
		HookOutputSet: true,
		HookOutput:    hooks.ExecutionOutput{},
		HookOverlays: []HookOverlay{{
			Key:        "stale_overlay",
			Content:    "stale legacy overlay",
			Priority:   HookPriorityHigh,
			Provenance: "runtime",
		}},
		RuntimeNote: "stale legacy note",
		History:     []llm.Message{{Role: llm.RoleUser, Content: "keep going"}},
	}

	msgs := BuildMessages("sys", snap)
	for _, msg := range msgs {
		if msg.Role != llm.RoleSystem {
			continue
		}
		if strings.Contains(msg.Content, "stale legacy overlay") || strings.Contains(msg.Content, "stale legacy note") {
			t.Fatalf("stale legacy prompt state leaked into messages: %#v", msgs)
		}
	}
}

func TestBuildMessages_PlacesDynamicSystemOverlaysAfterBaseSystemPrompt(t *testing.T) {
	snap := SessionSnapshot{
		RuntimeNote: "runtime note",
		Mode:        ModePlan,
		TaskState: &TaskState{
			Objective:            "inspect prompt composition",
			RequiredVerification: "make sure overlays follow the base system prompt",
			Operation:            "analysis",
		},
		PlanState: &PlanState{
			Steps: []PlanStep{
				{Step: "Add prompt composer", Status: "in_progress"},
			},
		},
		History: []llm.Message{{Role: llm.RoleUser, Content: "inspect the prompt flow"}},
	}

	msgs := BuildMessages("base system", snap)
	if len(msgs) < 5 {
		t.Fatalf("messages = %#v", msgs)
	}
	for i, want := range []string{"base system", "Current mode: plan", "runtime note", "Task objective", "Current plan:"} {
		if msgs[i].Role != llm.RoleSystem {
			t.Fatalf("message %d role = %q, want system", i, msgs[i].Role)
		}
		if !strings.Contains(msgs[i].Content, want) {
			t.Fatalf("message %d = %q, want substring %q", i, msgs[i].Content, want)
		}
	}
}

func TestBuildMessages_IncludesPlanTaskGuidance(t *testing.T) {
	snap := SessionSnapshot{
		TaskState: &TaskState{
			Objective:            "write a plan for removing dead xml code",
			Operation:            "plan",
			RequiredVerification: "produce a concise plan grounded in enough repo evidence",
		},
		History: []llm.Message{{Role: llm.RoleUser, Content: "write a cleanup plan"}},
	}

	msgs := BuildMessages("sys", snap)
	if len(msgs) < 3 {
		t.Fatalf("messages = %#v", msgs)
	}
	if !strings.Contains(msgs[1].Content, "Planning guidance") {
		t.Fatalf("task state missing planning guidance: %q", msgs[1].Content)
	}
	if !strings.Contains(msgs[1].Content, "avoid exhaustive repo-wide searches") {
		t.Fatalf("task state missing synthesis guidance: %q", msgs[1].Content)
	}
	if !strings.Contains(msgs[1].Content, "enter_plan_mode") || !strings.Contains(msgs[1].Content, "ask_user_question") {
		t.Fatalf("task state missing plan/question tool guidance: %q", msgs[1].Content)
	}
}

func TestBuildMessages_IncludesOverviewTaskGuidance(t *testing.T) {
	snap := SessionSnapshot{
		TaskState: &TaskState{
			Objective:            "tell me about this repo",
			Operation:            "overview",
			RequiredVerification: "inspect the repository with read/search tools before answering. For a casual repo overview, usually inspect the repo root and one high-signal file such as README.md, then give a brief overview grounded only in that evidence",
		},
		History: []llm.Message{{Role: llm.RoleUser, Content: "tell me about this repo"}},
	}

	msgs := BuildMessages("sys", snap)
	if len(msgs) < 3 {
		t.Fatalf("messages = %#v", msgs)
	}
	if !strings.Contains(msgs[1].Content, "Overview guidance") {
		t.Fatalf("task state missing overview guidance: %q", msgs[1].Content)
	}
	if !strings.Contains(msgs[1].Content, "stop exploring") {
		t.Fatalf("task state missing stop-exploring guidance: %q", msgs[1].Content)
	}
}

func TestBuildMessages_IncludesAnalysisTaskGuidance(t *testing.T) {
	snap := SessionSnapshot{
		TaskState: &TaskState{
			Objective:            "audit the repo and explain cleanup targets",
			Operation:            "analysis",
			RequiredVerification: "produce source-grounded findings and stop when the answer can be written",
		},
		History: []llm.Message{{Role: llm.RoleUser, Content: "audit the repo"}},
	}

	msgs := BuildMessages("sys", snap)
	if len(msgs) < 3 {
		t.Fatalf("messages = %#v", msgs)
	}
	if !strings.Contains(msgs[1].Content, "Analysis guidance") {
		t.Fatalf("task state missing analysis guidance: %q", msgs[1].Content)
	}
	if !strings.Contains(msgs[1].Content, "summarize findings") {
		t.Fatalf("task state missing analysis synthesis guidance: %q", msgs[1].Content)
	}
}

func TestBuildMessages_IncludesImplementationTaskGuidance(t *testing.T) {
	snap := SessionSnapshot{
		TaskState: &TaskState{
			Objective:            "i need a new theme for this app",
			Operation:            "implement",
			RequiredVerification: "inspect the relevant code, make the change with edit tools, and run the relevant verification before claiming completion",
		},
		History: []llm.Message{{Role: llm.RoleUser, Content: "i need a new theme for this app"}},
	}

	msgs := BuildMessages("sys", snap)
	if len(msgs) < 3 {
		t.Fatalf("messages = %#v", msgs)
	}
	if !strings.Contains(msgs[1].Content, "Implementation guidance") {
		t.Fatalf("task state missing implementation guidance: %q", msgs[1].Content)
	}
	if !strings.Contains(msgs[1].Content, "brief intent sentence") {
		t.Fatalf("task state missing first action guidance: %q", msgs[1].Content)
	}
	if !strings.Contains(msgs[1].Content, "If repeated searches on the same file are not resolving the insertion point, read that file directly") {
		t.Fatalf("task state missing same-file search guidance: %q", msgs[1].Content)
	}
	if !strings.Contains(msgs[1].Content, "exec_session_start") {
		t.Fatalf("task state missing PTY exec guidance: %q", msgs[1].Content)
	}
}

func TestBuildMessages_IncludesPreviewTaskGuidance(t *testing.T) {
	snap := SessionSnapshot{
		TaskState: &TaskState{
			Objective:            "start a preview for themes_preview.html and show me the verified url",
			Operation:            "preview",
			RequiredVerification: "use preview or artifact tools, verify the preview URL or visible result, and do not rely on an ad-hoc shell webserver",
		},
		History: []llm.Message{{Role: llm.RoleUser, Content: "start a preview for themes_preview.html"}},
	}

	msgs := BuildMessages("sys", snap)
	if len(msgs) < 3 {
		t.Fatalf("messages = %#v", msgs)
	}
	if !strings.Contains(msgs[1].Content, "Preview guidance") {
		t.Fatalf("task state missing preview guidance: %q", msgs[1].Content)
	}
	if !strings.Contains(msgs[1].Content, "After 1-3 high-signal reads") {
		t.Fatalf("task state missing preview pacing guidance: %q", msgs[1].Content)
	}
	if !strings.Contains(msgs[1].Content, "Do not launch an ad-hoc local webserver") {
		t.Fatalf("task state missing preview routing guidance: %q", msgs[1].Content)
	}
}

func TestBuildMessages_IncludesReviewTaskGuidance(t *testing.T) {
	snap := SessionSnapshot{
		TaskState: &TaskState{
			Objective:            "review this repo and tell me what i need to change",
			Operation:            "review",
			RequiredVerification: "produce source-grounded findings first, ordered by severity, and keep the summary secondary to the actual review issues",
		},
		History: []llm.Message{{Role: llm.RoleUser, Content: "review this repo and tell me what i need to change"}},
	}

	msgs := BuildMessages("sys", snap)
	if len(msgs) < 3 {
		t.Fatalf("messages = %#v", msgs)
	}
	if !strings.Contains(msgs[1].Content, "Review guidance") {
		t.Fatalf("task state missing review guidance: %q", msgs[1].Content)
	}
	if !strings.Contains(msgs[1].Content, "findings before summary") {
		t.Fatalf("task state missing findings-first guidance: %q", msgs[1].Content)
	}
}

func TestBuildMessages_IncludesInterruptedTurnGuidance(t *testing.T) {
	snap := SessionSnapshot{
		Interrupted: true,
		History:     []llm.Message{{Role: llm.RoleUser, Content: "continue"}},
	}
	msgs := BuildMessages("sys", snap)
	found := false
	for _, msg := range msgs {
		if msg.Role == llm.RoleSystem && strings.Contains(msg.Content, "previous turn was interrupted") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("messages missing interrupted guidance: %#v", msgs)
	}
}
