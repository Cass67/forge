package sessionstore

import (
	"context"
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

func TestReplayRejectsMultipleTerminalItemsForTurn(t *testing.T) {
	items := []protocol.Item{
		{TurnID: "turn-1", Seq: 1, Kind: protocol.ItemTurnComplete, TurnComplete: &protocol.TurnCompleteItem{Status: protocol.TurnStatusCompleted}},
		{TurnID: "turn-1", Seq: 2, Kind: protocol.ItemFailure, Failure: &protocol.FailureItem{Decision: protocol.FailureDecision{Class: protocol.FailureToolRuntimeFailed}}},
	}
	if _, err := ReplayItems(items); err == nil {
		t.Fatal("expected multiple terminal item error")
	}
}

func TestReplayAllowsRecoverableFailureBeforeCompletion(t *testing.T) {
	items := []protocol.Item{
		{TurnID: "turn-1", Seq: 1, Kind: protocol.ItemUserMessage, Message: &protocol.MessageItem{Role: "user", Text: "hello"}},
		{TurnID: "turn-1", Seq: 2, Kind: protocol.ItemFailure, Failure: &protocol.FailureItem{Decision: protocol.ClassifyToolArgFailure("bad args")}},
		{TurnID: "turn-1", Seq: 3, Kind: protocol.ItemTurnComplete, TurnComplete: &protocol.TurnCompleteItem{Status: protocol.TurnStatusCompleted}},
	}
	replay, err := ReplayItems(items)
	if err != nil {
		t.Fatal(err)
	}
	if len(replay.Turns) != 1 || replay.Turns[0].Status != protocol.TurnStatusCompleted || replay.Turns[0].Error != "" {
		t.Fatalf("replay = %#v", replay)
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
