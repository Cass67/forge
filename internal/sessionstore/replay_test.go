package sessionstore

import (
	"context"
	"strings"
	"testing"

	"forge/internal/llm"
	"forge/internal/protocol"
)

func TestReplayBuildsTurnFromDurableItems(t *testing.T) {
	items := []protocol.Item{
		{TurnID: "turn-1", Seq: 1, Kind: protocol.ItemUserMessage, Message: &protocol.MessageItem{Role: "user", Text: "read README"}},
		{TurnID: "turn-1", Seq: 2, Kind: protocol.ItemToolCall, ToolCall: &protocol.ToolCallItem{ToolName: "read_file", ToolCallID: "c1"}},
		{TurnID: "turn-1", Seq: 3, Kind: protocol.ItemToolResult, ToolResult: &protocol.ToolResultItem{ToolName: "read_file", ToolCallID: "c1", Text: "ok"}},
		{TurnID: "turn-1", Seq: 4, Kind: protocol.ItemTurnComplete, TurnComplete: &protocol.TurnCompleteItem{Status: protocol.TurnStatusCompleted}},
	}
	replay, err := ReplayItems(items)
	if err != nil {
		t.Fatal(err)
	}
	if len(replay.Turns) != 1 || len(replay.Turns[0].ToolCalls) != 1 || replay.Turns[0].Status != protocol.TurnStatusCompleted {
		t.Fatalf("replay = %#v", replay)
	}
}

func TestReplaySkillContextDoesNotCreateTurn(t *testing.T) {
	replay, err := ReplayItems([]protocol.Item{{
		Version:      protocol.CurrentItemVersion,
		ThreadID:     "thread-1",
		Seq:          1,
		Kind:         protocol.ItemSkillContext,
		SkillContext: &protocol.SkillContextItem{Name: "brainstorming", Body: "Write docs/plans/design.md"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(replay.Turns) != 0 || len(replay.RecentInputs) != 0 {
		t.Fatalf("replay turns/recent = %#v/%#v, want none", replay.Turns, replay.RecentInputs)
	}
	if len(replay.History) != 1 || replay.History[0].Role != llm.RoleSystem || !strings.Contains(replay.History[0].Content, "[Skill: brainstorming]") {
		t.Fatalf("history = %#v", replay.History)
	}
}

func TestReplayRebuildsHistoryRecentInputsAndCompaction(t *testing.T) {
	items := []protocol.Item{
		{TurnID: "turn-1", Seq: 1, Kind: protocol.ItemUserMessage, Message: &protocol.MessageItem{Role: string(llm.RoleUser), Text: "inspect"}},
		{TurnID: "turn-1", Seq: 2, Kind: protocol.ItemAssistantMessage, Message: &protocol.MessageItem{Role: string(llm.RoleAssistant), Text: "checking"}},
		{TurnID: "turn-1", Seq: 3, Kind: protocol.ItemToolCall, ToolCall: &protocol.ToolCallItem{ToolName: "read_file", ToolCallID: "call-1"}},
		{TurnID: "turn-1", Seq: 4, Kind: protocol.ItemToolResult, ToolResult: &protocol.ToolResultItem{ToolName: "read_file", ToolCallID: "call-1", Text: "contents"}},
		{Seq: 5, Kind: protocol.ItemCompaction, Compaction: &protocol.CompactionItem{Summary: "older turns"}},
		{TurnID: "turn-1", Seq: 6, Kind: protocol.ItemTurnComplete, TurnComplete: &protocol.TurnCompleteItem{Status: protocol.TurnStatusCompleted}},
	}
	replay, err := ReplayItems(items)
	if err != nil {
		t.Fatal(err)
	}
	if len(replay.History) != 3 || replay.History[0].Role != llm.RoleUser || replay.History[1].Role != llm.RoleAssistant || replay.History[2].Role != llm.RoleTool {
		t.Fatalf("history = %#v", replay.History)
	}
	if len(replay.History[1].ToolCalls) != 1 || replay.History[1].ToolCalls[0].Name != "read_file" {
		t.Fatalf("assistant tool calls = %#v", replay.History[1].ToolCalls)
	}
	if len(replay.RecentInputs) != 1 || replay.RecentInputs[0] != "inspect" {
		t.Fatalf("recent inputs = %#v", replay.RecentInputs)
	}
	if replay.CompactionSummary != "older turns" {
		t.Fatalf("compaction summary = %q", replay.CompactionSummary)
	}
}

func TestReplayMarksUnterminatedTurnWithActivityResumable(t *testing.T) {
	items := []protocol.Item{
		{TurnID: "turn-1", Seq: 1, Kind: protocol.ItemUserMessage, Message: &protocol.MessageItem{Role: "user", Text: "read README"}},
		{TurnID: "turn-1", Seq: 2, Kind: protocol.ItemToolCall, ToolCall: &protocol.ToolCallItem{ToolName: "read_file", ToolCallID: "c1"}},
		{TurnID: "turn-1", Seq: 3, Kind: protocol.ItemToolResult, ToolResult: &protocol.ToolResultItem{ToolName: "read_file", ToolCallID: "c1", Text: "ok"}},
	}

	replay, err := ReplayItems(items)
	if err != nil {
		t.Fatal(err)
	}

	if len(replay.Turns) != 1 {
		t.Fatalf("turns = %#v, want one turn", replay.Turns)
	}
	if replay.Turns[0].Status != protocol.TurnStatusResumable {
		t.Fatalf("status = %q, want %q", replay.Turns[0].Status, protocol.TurnStatusResumable)
	}
}

func TestReplayKeepsTerminalTurnStatusCompleted(t *testing.T) {
	items := []protocol.Item{
		{TurnID: "turn-1", Seq: 1, Kind: protocol.ItemUserMessage, Message: &protocol.MessageItem{Role: "user", Text: "hello"}},
		{TurnID: "turn-1", Seq: 2, Kind: protocol.ItemTurnComplete, TurnComplete: &protocol.TurnCompleteItem{Status: protocol.TurnStatusCompleted}},
	}

	replay, err := ReplayItems(items)
	if err != nil {
		t.Fatal(err)
	}

	if len(replay.Turns) != 1 || replay.Turns[0].Status != protocol.TurnStatusCompleted {
		t.Fatalf("replay = %#v, want completed turn", replay)
	}
}

func TestReplayIgnoresSessionLevelItemsWithoutTurnActivity(t *testing.T) {
	items := []protocol.Item{
		{Seq: 1, Kind: protocol.ItemSessionMeta, SessionMeta: &protocol.SessionMetaItem{Source: "test"}},
		{Seq: 2, Kind: protocol.ItemStats, Stats: &protocol.StatsItem{}},
	}

	replay, err := ReplayItems(items)
	if err != nil {
		t.Fatal(err)
	}

	if len(replay.Turns) != 0 {
		t.Fatalf("turns = %#v, want no bogus empty turns", replay.Turns)
	}
}

func TestReplayRejectsMultipleTerminalItemsForTurn(t *testing.T) {
	items := []protocol.Item{
		{TurnID: "turn-1", Seq: 1, Kind: protocol.ItemTurnComplete, TurnComplete: &protocol.TurnCompleteItem{Status: protocol.TurnStatusCompleted}},
		{TurnID: "turn-1", Seq: 2, Kind: protocol.ItemFailure, Failure: &protocol.FailureItem{Decision: protocol.FailureDecision{Class: protocol.FailureToolRuntimeFailed}}},
	}
	if _, err := ReplayItems(items); err == nil {
		t.Fatal("expected multiple terminal item error")
	}
}

func TestReplayTreatsRecoverableFailureAsTurnActivity(t *testing.T) {
	items := []protocol.Item{
		{TurnID: "turn-1", Seq: 1, Kind: protocol.ItemUserMessage, Message: &protocol.MessageItem{Role: "user", Text: "read README"}},
		{TurnID: "turn-1", Seq: 2, Kind: protocol.ItemFailure, Failure: &protocol.FailureItem{Decision: protocol.ClassifyToolArgFailure("bad args")}},
		{TurnID: "turn-1", Seq: 3, Kind: protocol.ItemToolResult, ToolResult: &protocol.ToolResultItem{ToolCallID: "c1", Text: "bad args"}},
		{TurnID: "turn-1", Seq: 4, Kind: protocol.ItemTurnComplete, TurnComplete: &protocol.TurnCompleteItem{Status: protocol.TurnStatusCompleted}},
	}

	replay, err := ReplayItems(items)
	if err != nil {
		t.Fatal(err)
	}
	if len(replay.Turns) != 1 || replay.Turns[0].Status != protocol.TurnStatusCompleted || len(replay.Turns[0].Results) != 1 || replay.Turns[0].Error != "" {
		t.Fatalf("replay = %#v, want completed turn with recoverable failure activity", replay)
	}
}

func TestJSONLDurableItemsReplayAfterStoreReopen(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	store := NewJSONLThreadStore(dir)
	threadID := "thread-1"
	items := []protocol.Item{
		{Version: 1, ID: "item-1", ThreadID: threadID, TurnID: "turn-1", Seq: 1, Kind: protocol.ItemUserMessage, Message: &protocol.MessageItem{Role: "user", Text: "hello"}},
		{Version: 1, ID: "item-2", ThreadID: threadID, TurnID: "turn-1", Seq: 2, Kind: protocol.ItemTurnComplete, TurnComplete: &protocol.TurnCompleteItem{Status: protocol.TurnStatusCompleted}},
	}
	if _, err := store.AppendItems(ctx, threadID, items); err != nil {
		t.Fatal(err)
	}
	reopened := NewJSONLThreadStore(dir)
	loaded, err := reopened.ReadItems(ctx, threadID)
	if err != nil {
		t.Fatal(err)
	}
	replay, err := ReplayItems(loaded)
	if err != nil {
		t.Fatal(err)
	}
	if len(replay.Turns) != 1 || replay.Turns[0].Status != protocol.TurnStatusCompleted {
		t.Fatalf("replay after reopen = %#v", replay)
	}
}

func TestJSONLDurableUnterminatedToolTurnReplaysResumableAfterStoreReopen(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	store := NewJSONLThreadStore(dir)
	threadID := "thread-1"
	items := []protocol.Item{
		{Version: protocol.CurrentItemVersion, ID: "item-1", ThreadID: threadID, TurnID: "turn-1", Seq: 1, Kind: protocol.ItemUserMessage, Message: &protocol.MessageItem{Role: "user", Text: "read README"}},
		{Version: protocol.CurrentItemVersion, ID: "item-2", ThreadID: threadID, TurnID: "turn-1", Seq: 2, Kind: protocol.ItemToolCall, ToolCall: &protocol.ToolCallItem{ToolName: "read_file", ToolCallID: "c1", Args: map[string]any{"path": "README.md"}}},
		{Version: protocol.CurrentItemVersion, ID: "item-3", ThreadID: threadID, TurnID: "turn-1", Seq: 3, Kind: protocol.ItemToolResult, ToolResult: &protocol.ToolResultItem{ToolName: "read_file", ToolCallID: "c1", Text: "README contents"}},
	}
	if _, err := store.AppendItems(ctx, threadID, items); err != nil {
		t.Fatal(err)
	}

	reopened := NewJSONLThreadStore(dir)
	loaded, err := reopened.ReadItems(ctx, threadID)
	if err != nil {
		t.Fatal(err)
	}
	replay, err := ReplayItems(loaded)
	if err != nil {
		t.Fatal(err)
	}

	if len(replay.Turns) != 1 {
		t.Fatalf("turns = %#v, want one turn", replay.Turns)
	}
	turn := replay.Turns[0]
	if turn.Status != protocol.TurnStatusResumable {
		t.Fatalf("status = %q, want %q", turn.Status, protocol.TurnStatusResumable)
	}
	if len(turn.ToolCalls) != 1 || turn.ToolCalls[0].ToolCallID != "c1" {
		t.Fatalf("tool calls = %#v, want replayed tool call", turn.ToolCalls)
	}
}

func TestPolicyDropsExactArgsWhenSensitiveKeyPresent(t *testing.T) {
	item := DefaultPersistencePolicy().Apply(protocol.Item{
		Kind: protocol.ItemToolCall,
		ToolCall: &protocol.ToolCallItem{
			ToolName:   "http",
			ToolCallID: "c1",
			Args:       map[string]any{"api_key": "sk-live-abcdef0123456789"},
			ArgsJSON:   `{"api_key":"sk-live-abcdef0123456789"}`,
		},
	})
	if strings.Contains(item.ToolCall.ArgsJSON, "sk-live-abcdef0123456789") {
		t.Fatalf("secret survived in ArgsJSON: %q", item.ToolCall.ArgsJSON)
	}
	if item.ToolCall.Args["api_key"] != "<REDACTED>" {
		t.Fatalf("map not redacted: %v", item.ToolCall.Args)
	}
}

func TestReplayPreservesExactToolCallArgs(t *testing.T) {
	// Key order must survive: a reordered argument object changes the prompt
	// bytes and invalidates the provider's cached prefix.
	exact := `{"path":"a.go","limit":10,"offset":0}`
	replay, err := ReplayItems([]protocol.Item{
		{Seq: 1, Ref: "r1", Kind: protocol.ItemAssistantMessage, Message: &protocol.MessageItem{Role: "assistant"}},
		{Seq: 2, Ref: "r2", Kind: protocol.ItemToolCall, ToolCall: &protocol.ToolCallItem{
			ToolName: "read", ToolCallID: "c1", ArgsJSON: exact,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := replay.History[0].ToolCalls[0].ArgsJSON; got != exact {
		t.Fatalf("args round-tripped to %q, want %q", got, exact)
	}
}
