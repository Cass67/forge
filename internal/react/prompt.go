package react

import (
	"fmt"
	"strings"

	"forge/internal/llm"
)

// BuildPrompt normalizes per-turn user input for the ReAct loop.
func BuildPrompt(input string) string {
	return strings.TrimSpace(input)
}

// BuildMessages assembles the runtime-owned prompt context for a turn.
func BuildMessages(systemPrompt string, snapshot SessionSnapshot) []llm.Message {
	var messages []llm.Message

	systemPrompt = strings.TrimSpace(systemPrompt)
	if systemPrompt != "" {
		messages = append(messages, llm.Message{Role: llm.RoleSystem, Content: systemPrompt})
	}

	if summary := compactionContext(snapshot); summary != "" {
		messages = append(messages, llm.Message{Role: llm.RoleSystem, Content: summary})
	}

	for _, msg := range snapshot.History {
		content := strings.TrimSpace(msg.Content)
		if content == "" {
			continue
		}
		messages = append(messages, llm.Message{Role: msg.Role, Content: content})
	}

	return messages
}

func compactionContext(snapshot SessionSnapshot) string {
	if snapshot.CompactedTurns == 0 {
		return ""
	}
	summary := strings.TrimSpace(snapshot.CompactionSummary)
	if summary == "" {
		return ""
	}
	return fmt.Sprintf("Earlier conversation summary (%d compacted turns): %s", snapshot.CompactedTurns, summary)
}
