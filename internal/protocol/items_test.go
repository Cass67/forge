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
	recoverable := Item{Version: 1, Kind: ItemFailure, Failure: &FailureItem{Decision: FailureDecision{Class: FailureToolArgsInvalid, Recoverable: true}}}
	if !complete.IsTerminal() || !failure.IsTerminal() || recoverable.IsTerminal() {
		t.Fatalf("terminal checks failed: complete=%v failure=%v recoverable=%v", complete.IsTerminal(), failure.IsTerminal(), recoverable.IsTerminal())
	}
}

func TestToolResultOutputHandleMetadataJSON(t *testing.T) {
	plain, err := json.Marshal(ToolResultItem{ToolCallID: "call-1", Text: "ok"})
	if err != nil {
		t.Fatal(err)
	}
	if string(plain) != `{"tool_name":"","tool_call_id":"call-1","text":"ok"}` {
		t.Fatalf("plain tool result JSON = %s", plain)
	}

	withHandle, err := json.Marshal(ToolResultItem{ToolCallID: "call-1", Text: "summary", Handle: "thread-1/abc123", OriginalBytes: 10, SHA256: "abc123"})
	if err != nil {
		t.Fatal(err)
	}
	var decoded ToolResultItem
	if err := json.Unmarshal(withHandle, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Handle != "thread-1/abc123" || decoded.OriginalBytes != 10 || decoded.SHA256 != "abc123" {
		t.Fatalf("decoded metadata = %#v", decoded)
	}
}

func TestCheckpointItemJSON(t *testing.T) {
	item := Item{
		Version: 1,
		Kind:    ItemCheckpoint,
		Checkpoint: &CheckpointItem{
			ID:           "checkpoint-1",
			Phase:        "created",
			ChangedFiles: []string{"README.md"},
		},
	}
	encoded, err := json.Marshal(item)
	if err != nil {
		t.Fatal(err)
	}
	var decoded Item
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Kind != ItemCheckpoint || decoded.Checkpoint == nil {
		t.Fatalf("decoded checkpoint item = %#v", decoded)
	}
	if decoded.Checkpoint.ID != "checkpoint-1" || decoded.Checkpoint.Phase != "created" || len(decoded.Checkpoint.ChangedFiles) != 1 {
		t.Fatalf("decoded checkpoint = %#v", decoded.Checkpoint)
	}
}

func TestAgentHandoffItemJSON(t *testing.T) {
	item := Item{
		Version: 1,
		Kind:    ItemAgentHandoff,
		AgentHandoff: &AgentHandoffItem{
			AgentID:  "agent-1",
			Blocking: true,
			RemainingActions: []AgentFollowupActionItem{{
				Kind:        "write_file",
				TargetPath:  "docs/audit.md",
				Description: "Save report",
				Blocking:    true,
			}},
			Incidents: []AgentWorkspaceIncidentItem{{
				Kind:        "accidental_write",
				Paths:       []string{"README.md"},
				Description: "Child wrote report into README",
				Blocking:    true,
			}},
		},
	}
	encoded, err := json.Marshal(item)
	if err != nil {
		t.Fatal(err)
	}
	var decoded Item
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Kind != ItemAgentHandoff || decoded.AgentHandoff == nil {
		t.Fatalf("decoded handoff item = %#v", decoded)
	}
	if decoded.AgentHandoff.AgentID != "agent-1" || !decoded.AgentHandoff.Blocking {
		t.Fatalf("decoded handoff = %#v", decoded.AgentHandoff)
	}
	if decoded.AgentHandoff.RemainingActions[0].TargetPath != "docs/audit.md" || decoded.AgentHandoff.Incidents[0].Paths[0] != "README.md" {
		t.Fatalf("decoded handoff details = %#v", decoded.AgentHandoff)
	}
}
