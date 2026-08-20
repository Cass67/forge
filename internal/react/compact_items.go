package react

import (
	"forge/internal/protocol"
	"forge/internal/sessionstore"
)

// compactProtectedTailItems keeps the freshest steps out of span compaction.
// Shadowing what the model is actively working on forces it to re-read, which
// looks like a tool-call loop and costs more context than it frees.
const compactProtectedTailItems = 16

// liveItemsLocked returns the log entries that currently reach the prompt,
// carrying the text the prompt actually shows. Reading the original text
// instead made every compaction pass re-replace what it had already replaced,
// so each pass reported success for work that changed nothing.
func (s *Session) liveItemsLocked() []protocol.Item {
	shadowed, replacements := sessionstore.CompactionOverlay(s.items)
	live := make([]protocol.Item, 0, len(s.items))
	for _, item := range s.items {
		if shadowed[item.Ref] || item.Kind == protocol.ItemCompaction {
			continue
		}
		if text, ok := replacements[item.Ref]; ok && item.ToolResult != nil {
			clone := *item.ToolResult
			clone.Text = text
			item.ToolResult = &clone
		}
		live = append(live, item)
	}
	return live
}

// contributesToPrompt reports whether an item becomes a message in the prompt.
// Only these are worth shadowing; the rest are bookkeeping.
func contributesToPrompt(kind protocol.ItemKind) bool {
	switch kind {
	case protocol.ItemUserMessage, protocol.ItemAssistantMessage,
		protocol.ItemToolCall, protocol.ItemToolResult, protocol.ItemSkillContext:
		return true
	default:
		return false
	}
}

// balancedCutAfter reports whether shadowing live[:idx+1] leaves no tool call
// without its result. Cutting mid tool-loop produces an assistant message whose
// tool_calls have no matching results, which providers reject outright.
func balancedCutAfter(live []protocol.Item, idx int) bool {
	open := 0
	for i := 0; i <= idx && i < len(live); i++ {
		switch live[i].Kind {
		case protocol.ItemToolCall:
			open++
		case protocol.ItemToolResult:
			open--
		}
	}
	return open == 0
}

// shadowSpanLocked shadows the oldest prompt-bearing items after the anchor,
// stopping at the newest balanced cut that still leaves the protected tail.
// It returns the sequences shadowed, or nil when no safe cut exists.
func (s *Session) shadowSpanLocked(live []protocol.Item) []string {
	limit := len(live) - compactProtectedTailItems
	if limit <= 0 {
		return nil
	}
	// Never shadow the opening user message: it is the standing request the
	// rest of the run is serving.
	anchor := -1
	for i, item := range live {
		if item.Kind == protocol.ItemUserMessage {
			anchor = i
			break
		}
	}
	best := -1
	for idx := anchor + 1; idx < limit; idx++ {
		if balancedCutAfter(live, idx) {
			best = idx
		}
	}
	if best <= anchor {
		return nil
	}
	refs := make([]string, 0, best-anchor)
	for i := anchor + 1; i <= best; i++ {
		if contributesToPrompt(live[i].Kind) {
			refs = append(refs, live[i].Ref)
		}
	}
	return refs
}

// appendCompactionLocked records one compaction in the log. Compaction is a
// log entry like any other: it never edits or removes what came before.
func (s *Session) appendCompactionLocked(shadowed []string, replacements []protocol.CompactionReplacement) protocol.Item {
	return s.appendItemLocked(protocol.Item{
		Kind: protocol.ItemCompaction,
		Compaction: &protocol.CompactionItem{
			Summary:      s.compactionSummary,
			ShadowedRefs: shadowed,
			Replacements: replacements,
		},
	})
}
