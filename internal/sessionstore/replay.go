package sessionstore

import (
	"fmt"
	"sort"

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
	for _, item := range sorted {
		turnID := item.TurnID
		if turnID == "" {
			turnID = "session"
		}
		turn := turns[turnID]
		if turn == nil {
			turn = &ReplayTurn{TurnID: turnID}
			turns[turnID] = turn
			order = append(order, turnID)
		}
		switch item.Kind {
		case protocol.ItemUserMessage:
			if item.Message != nil {
				turn.Input = item.Message.Text
			}
		case protocol.ItemToolCall:
			if item.ToolCall != nil {
				turn.ToolCalls = append(turn.ToolCalls, *item.ToolCall)
			}
		case protocol.ItemToolResult:
			if item.ToolResult != nil {
				turn.Results = append(turn.Results, *item.ToolResult)
			}
		case protocol.ItemTurnComplete:
			if terminal[turnID] {
				return Replay{}, fmt.Errorf("turn %s has multiple terminal items", turnID)
			}
			terminal[turnID] = true
			if item.TurnComplete != nil {
				turn.Status = item.TurnComplete.Status
			}
		case protocol.ItemFailure:
			if terminal[turnID] {
				return Replay{}, fmt.Errorf("turn %s has multiple terminal items", turnID)
			}
			terminal[turnID] = true
			turn.Status = protocol.TurnStatusFailed
		}
	}
	var out Replay
	for _, id := range order {
		out.Turns = append(out.Turns, *turns[id])
	}
	return out, nil
}
