package sessionstore

import (
	"fmt"
	"sort"
	"strings"

	"forge/internal/protocol"
)

type Replay struct {
	Turns []ReplayTurn
}

type ReplayTurn struct {
	TurnID    string
	Input     string
	Status    protocol.TurnStatus
	ToolCalls []protocol.ToolCallItem
	Results   []protocol.ToolResultItem
}

func ReplayItems(items []protocol.Item) (Replay, error) {
	sorted := append([]protocol.Item(nil), items...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Seq < sorted[j].Seq })
	turns := map[string]*ReplayTurn{}
	order := []string{}
	terminal := map[string]bool{}
	activity := map[string]bool{}
	for _, item := range sorted {
		turnID := replayTurnID(item.TurnID)
		switch item.Kind {
		case protocol.ItemUserMessage:
			turn := ensureReplayTurn(turnID, turns, &order)
			if item.Message != nil && strings.TrimSpace(turn.Input) == "" {
				turn.Input = item.Message.Text
			}
			activity[turnID] = true
		case protocol.ItemToolCall:
			turn := ensureReplayTurn(turnID, turns, &order)
			if item.ToolCall != nil {
				turn.ToolCalls = append(turn.ToolCalls, *item.ToolCall)
			}
			activity[turnID] = true
		case protocol.ItemToolResult:
			turn := ensureReplayTurn(turnID, turns, &order)
			if item.ToolResult != nil {
				turn.Results = append(turn.Results, *item.ToolResult)
			}
			activity[turnID] = true
		case protocol.ItemTurnComplete:
			turn := ensureReplayTurn(turnID, turns, &order)
			if terminal[turnID] {
				return Replay{}, fmt.Errorf("turn %s has multiple terminal items", turnID)
			}
			terminal[turnID] = true
			if item.TurnComplete != nil {
				turn.Status = item.TurnComplete.Status
			}
		case protocol.ItemFailure:
			turn := ensureReplayTurn(turnID, turns, &order)
			if !item.IsTerminal() {
				activity[turnID] = true
				continue
			}
			if terminal[turnID] {
				return Replay{}, fmt.Errorf("turn %s has multiple terminal items", turnID)
			}
			terminal[turnID] = true
			turn.Status = protocol.TurnStatusFailed
		}
	}
	var out Replay
	for _, id := range order {
		if !terminal[id] && activity[id] {
			turns[id].Status = protocol.TurnStatusResumable
		}
		out.Turns = append(out.Turns, *turns[id])
	}
	return out, nil
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
