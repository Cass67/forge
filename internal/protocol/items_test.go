package protocol

import (
	"encoding/json"
	"testing"
	"time"
)

func TestItemEnvelopeRoundTrips(t *testing.T) {
	item := Item{
		Version:  1,
		ID:       "item-1",
		ThreadID: "thread-1",
		TurnID:   "turn-1",
		Seq:      1,
		Kind:     ItemToolCall,
		At:       time.Unix(10, 0).UTC(),
		ToolCall: &ToolCallItem{ToolName: "read_file", ToolCallID: "call-1", Args: map[string]any{"path": "README.md"}},
	}
	encoded, err := json.Marshal(item)
	if err != nil {
		t.Fatal(err)
	}
	var decoded Item
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Version != 1 || decoded.Kind != ItemToolCall || decoded.ToolCall.ToolName != "read_file" {
		t.Fatalf("decoded = %#v", decoded)
	}
}

func TestTerminalItemsAreExplicit(t *testing.T) {
	complete := Item{Version: 1, Kind: ItemTurnComplete, TurnComplete: &TurnCompleteItem{Status: TurnStatusCompleted}}
	failure := Item{Version: 1, Kind: ItemFailure, Failure: &FailureItem{Decision: FailureDecision{Class: FailureToolRuntimeFailed}}}
	if !complete.IsTerminal() || !failure.IsTerminal() {
		t.Fatalf("terminal checks failed: complete=%v failure=%v", complete.IsTerminal(), failure.IsTerminal())
	}
}
