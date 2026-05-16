package sessionstore

import (
	"context"
	"testing"

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

func TestReplayRejectsMultipleTerminalItemsForTurn(t *testing.T) {
	items := []protocol.Item{
		{TurnID: "turn-1", Seq: 1, Kind: protocol.ItemTurnComplete, TurnComplete: &protocol.TurnCompleteItem{Status: protocol.TurnStatusCompleted}},
		{TurnID: "turn-1", Seq: 2, Kind: protocol.ItemFailure, Failure: &protocol.FailureItem{Decision: protocol.FailureDecision{Class: protocol.FailureToolRuntimeFailed}}},
	}
	if _, err := ReplayItems(items); err == nil {
		t.Fatal("expected multiple terminal item error")
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
