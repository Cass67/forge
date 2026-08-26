package sessionstore

import (
	"cmp"
	"encoding/json"
	"fmt"
	"slices"
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
	slices.SortStableFunc(sorted, func(a, b protocol.Item) int { return cmp.Compare(a.Seq, b.Seq) })
	turns := map[string]*ReplayTurn{}
	order := []string{}
	terminal := map[string]bool{}
	activity := map[string]bool{}
	replay := Replay{}
	shadowed, replacements := compactionOverlay(sorted)
	// A tool call belongs to the assistant message emitted with it. Folding it
	// into any earlier assistant message would merge two separate steps into
	// one, so only an immediately preceding assistant message may absorb it.
	assistantOpen := false

	for _, item := range sorted {
		// Compaction shadows earlier items instead of deleting them, so the
		// replayed prompt matches the compacted one the live session sent.
		if shadowed[item.Ref] {
			continue
		}
		if text, ok := replacements[item.Ref]; ok && item.ToolResult != nil {
			clone := *item.ToolResult
			clone.Text = text
			item.ToolResult = &clone
		}
		switch item.Kind {
		case protocol.ItemCompaction:
			if item.Compaction != nil {
				replay.CompactionSummary = strings.TrimSpace(item.Compaction.Summary)
			}
			continue
		case protocol.ItemSkillContext:
			if item.SkillContext != nil {
				replay.History = append(replay.History, llm.Message{Role: llm.RoleSystem, Content: skillContextContent(item.SkillContext.Name, item.SkillContext.Body)})
			}
			continue
		case protocol.ItemSessionMeta, protocol.ItemStats, protocol.ItemRetry, protocol.ItemCheckpoint, protocol.ItemAgentHandoff:
			continue
		}
		turnID := replayTurnID(item.TurnID)
		turn := ensureReplayTurn(turnID, turns, &order)
		wasAssistantOpen := assistantOpen
		assistantOpen = item.Kind == protocol.ItemAssistantMessage || item.Kind == protocol.ItemToolCall
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
				replay.History = append(replay.History, llm.Message{
					Role:             outRole,
					Content:          item.Message.Text,
					ReasoningContent: item.Message.ReasoningContent,
					ContentParts:     item.Message.ContentParts,
				})
				if firstInput && strings.TrimSpace(item.Message.Text) != "" {
					replay.RecentInputs = append(replay.RecentInputs, item.Message.Text)
				}
			}
			activity[turnID] = true
		case protocol.ItemAssistantMessage:
			if item.Message != nil {
				text := strings.TrimSpace(item.Message.Text)
				replay.History = append(replay.History, llm.Message{
					Role:             llm.RoleAssistant,
					Content:          item.Message.Text,
					ReasoningContent: item.Message.ReasoningContent,
					ContentParts:     item.Message.ContentParts,
				})
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
				if wasAssistantOpen && last >= 0 && replay.History[last].Role == llm.RoleAssistant {
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

func skillContextContent(name, body string) string {
	return fmt.Sprintf("[Skill: %s]\n\n%s", strings.TrimSpace(name), strings.TrimSpace(body))
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
	// v2 records the exact bytes; only fall back to re-encoding the v1 map,
	// which reorders keys and cannot represent non-object arguments.
	if strings.TrimSpace(call.ArgsJSON) != "" {
		out.ArgsJSON = call.ArgsJSON
		return out
	}
	if len(call.Args) == 0 {
		return out
	}
	encoded, err := json.Marshal(call.Args)
	if err == nil {
		out.ArgsJSON = string(encoded)
	}
	return out
}

// ResolveItems applies every compaction record and returns the items a prompt
// should be built from. Shadowed items are dropped and replaced text is folded
// in, so the result carries no outstanding compaction bookkeeping and can be
// adopted into another session directly.
func ResolveItems(items []protocol.Item) []protocol.Item {
	sorted := append([]protocol.Item(nil), items...)
	slices.SortStableFunc(sorted, func(a, b protocol.Item) int { return cmp.Compare(a.Seq, b.Seq) })
	shadowed, replacements := compactionOverlay(sorted)
	out := make([]protocol.Item, 0, len(sorted))
	for _, item := range sorted {
		if shadowed[item.Ref] {
			continue
		}
		if item.Kind == protocol.ItemCompaction && item.Compaction != nil {
			// The overlay is spent once applied; keep only the summary text.
			clone := *item.Compaction
			clone.ShadowedRefs = nil
			clone.Replacements = nil
			item.Compaction = &clone
		}
		if text, ok := replacements[item.Ref]; ok && item.ToolResult != nil {
			clone := *item.ToolResult
			clone.Text = text
			item.ToolResult = &clone
		}
		out = append(out, item)
	}
	return out
}

// CompactionOverlay exposes the current shadow state so a session can decide
// what is still live without rebuilding the whole projection.
func CompactionOverlay(items []protocol.Item) (map[string]bool, map[string]string) {
	sorted := append([]protocol.Item(nil), items...)
	slices.SortStableFunc(sorted, func(a, b protocol.Item) int { return cmp.Compare(a.Seq, b.Seq) })
	return compactionOverlay(sorted)
}

// compactionOverlay folds every compaction item into the set of sequences the
// prompt must skip and the shrunken text replayed in place of the originals.
func compactionOverlay(items []protocol.Item) (map[string]bool, map[string]string) {
	shadowed := map[string]bool{}
	replacements := map[string]string{}
	for _, item := range items {
		if item.Kind != protocol.ItemCompaction || item.Compaction == nil {
			continue
		}
		for _, ref := range item.Compaction.ShadowedRefs {
			if ref == "" {
				continue
			}
			shadowed[ref] = true
			delete(replacements, ref)
		}
		for _, replacement := range item.Compaction.Replacements {
			if replacement.Ref == "" || shadowed[replacement.Ref] {
				continue
			}
			replacements[replacement.Ref] = replacement.Text
		}
	}
	return shadowed, replacements
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
