package sessionstore

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"forge/internal/llm"
	"forge/internal/protocol"
)

type Replay struct {
	Turns             []ReplayTurn
	History           []llm.Message
	RecentInputs      []string
	PendingInput      []string
	CompactionSummary string
	Interrupted       bool
}

type ReplayTurn struct {
	TurnID        string
	Input         string
	FinalResponse string
	Status        protocol.TurnStatus
	Error         string
	ToolCalls     []protocol.ToolCallItem
	Results       []protocol.ToolResultItem
}

func ReplayItems(items []protocol.Item) (Replay, error) {
	sorted := append([]protocol.Item(nil), items...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Seq < sorted[j].Seq })
	turns := map[string]*ReplayTurn{}
	order := []string{}
	terminal := map[string]bool{}
	activity := map[string]bool{}
	replay := Replay{}

	for _, item := range sorted {
		switch item.Kind {
		case protocol.ItemCompaction:
			if item.Compaction != nil {
				replay.CompactionSummary = strings.TrimSpace(item.Compaction.Summary)
			}
			continue
		case protocol.ItemSessionMeta, protocol.ItemStats, protocol.ItemRetry, protocol.ItemCheckpoint, protocol.ItemAgentHandoff:
			continue
		}
		turnID := replayTurnID(item.TurnID)
		turn := ensureReplayTurn(turnID, turns, &order)
		switch item.Kind {
		case protocol.ItemUserMessage:
			if item.Message != nil {
				outRole := llm.Role(item.Message.Role)
				if outRole == "" {
					outRole = llm.RoleUser
				}
				firstInput := outRole == llm.RoleUser && strings.TrimSpace(turn.Input) == ""
				if firstInput {
					turn.Input = item.Message.Text
				}
				replay.History = append(replay.History, llm.Message{Role: outRole, Content: item.Message.Text})
				if firstInput && strings.TrimSpace(item.Message.Text) != "" {
					replay.RecentInputs = append(replay.RecentInputs, item.Message.Text)
				}
			}
			activity[turnID] = true
		case protocol.ItemAssistantMessage:
			if item.Message != nil {
				text := strings.TrimSpace(item.Message.Text)
				replay.History = append(replay.History, llm.Message{Role: llm.RoleAssistant, Content: item.Message.Text})
				if text != "" {
					turn.FinalResponse = text
				}
			}
		case protocol.ItemTurnContext:
			if item.TurnContext != nil && strings.TrimSpace(item.TurnContext.Input) != "" {
				switch item.TurnContext.Mode {
				case "queued_input":
					replay.PendingInput = append(replay.PendingInput, item.TurnContext.Input)
				case "queued_input_consumed":
					replay.PendingInput = removeFirstPendingInput(replay.PendingInput, item.TurnContext.Input)
				}
			}
		case protocol.ItemToolCall:
			if item.ToolCall != nil {
				turn.ToolCalls = append(turn.ToolCalls, *item.ToolCall)
				call := replayNativeToolCall(*item.ToolCall)
				last := len(replay.History) - 1
				if last >= 0 && replay.History[last].Role == llm.RoleAssistant {
					replay.History[last].ToolCalls = append(replay.History[last].ToolCalls, call)
				} else {
					replay.History = append(replay.History, llm.Message{Role: llm.RoleAssistant, ToolCalls: []llm.NativeToolCall{call}})
				}
			}
			activity[turnID] = true
		case protocol.ItemToolResult:
			if item.ToolResult != nil {
				turn.Results = append(turn.Results, *item.ToolResult)
				replay.History = append(replay.History, llm.Message{Role: llm.RoleTool, ToolCallID: item.ToolResult.ToolCallID, Content: item.ToolResult.Text})
			}
			activity[turnID] = true
		case protocol.ItemTurnComplete:
			if terminal[turnID] {
				return Replay{}, fmt.Errorf("turn %s has multiple terminal items", turnID)
			}
			terminal[turnID] = true
			turn.Status = protocol.TurnStatusCompleted
			if item.TurnComplete != nil {
				turn.Status = item.TurnComplete.Status
				if item.TurnComplete.Status == protocol.TurnStatusInterrupted {
					turn.Error = "interrupted"
					replay.Interrupted = true
				}
			}
		case protocol.ItemFailure:
			if !item.IsTerminal() {
				activity[turnID] = true
				continue
			}
			if terminal[turnID] {
				return Replay{}, fmt.Errorf("turn %s has multiple terminal items", turnID)
			}
			terminal[turnID] = true
			turn.Status = protocol.TurnStatusFailed
			if item.Failure != nil {
				turn.Error = item.Failure.Decision.Feedback
			}
		}
	}
	for _, id := range order {
		if !terminal[id] && activity[id] {
			turns[id].Status = protocol.TurnStatusResumable
		}
		replay.Turns = append(replay.Turns, *turns[id])
	}
	return replay, nil
}

func removeFirstPendingInput(inputs []string, consumed string) []string {
	for i, input := range inputs {
		if input == consumed {
			out := append([]string(nil), inputs[:i]...)
			out = append(out, inputs[i+1:]...)
			return out
		}
	}
	return inputs
}

func replayNativeToolCall(call protocol.ToolCallItem) llm.NativeToolCall {
	out := llm.NativeToolCall{ID: call.ToolCallID, Name: call.ToolName}
	if len(call.Args) == 0 {
		return out
	}
	encoded, err := json.Marshal(call.Args)
	if err == nil {
		out.ArgsJSON = string(encoded)
	}
	return out
}

func replayTurnID(turnID string) string {
	turnID = strings.TrimSpace(turnID)
	if turnID == "" {
		return "session"
	}
	return turnID
}

func ensureReplayTurn(turnID string, turns map[string]*ReplayTurn, order *[]string) *ReplayTurn {
	turn := turns[turnID]
	if turn == nil {
		turn = &ReplayTurn{TurnID: turnID}
		turns[turnID] = turn
		*order = append(*order, turnID)
	}
	return turn
}
