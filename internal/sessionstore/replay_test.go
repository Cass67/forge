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
	if len(replay.Turns) != 1 || replay.Turns[0].Status != protocol.TurnStatusCompleted || len(replay.Turns[0].Results) != 1 {
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
