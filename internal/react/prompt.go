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
	if note := strings.TrimSpace(snapshot.RuntimeNote); note != "" {
		messages = append(messages, llm.Message{Role: llm.RoleSystem, Content: note})
	}
	if task := taskStateContext(snapshot); task != "" {
		messages = append(messages, llm.Message{Role: llm.RoleSystem, Content: task})
	}

	for _, msg := range snapshot.History {
		// Pass through tool-role messages and assistant messages with native tool calls
		// even if their text content is empty — the ToolCallID / ToolCalls fields carry
		// the payload that the provider needs.
		if msg.Role == llm.RoleTool || len(msg.ToolCalls) > 0 {
			messages = append(messages, msg)
			continue
		}
		content := strings.TrimSpace(msg.Content)
		if content == "" {
			continue
		}
		messages = append(messages, llm.Message{Role: msg.Role, Content: content})
	}

	return truncateToolResults(messages, toolResultMaxLines)
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

func taskStateContext(snapshot SessionSnapshot) string {
	if snapshot.TaskState == nil {
		return ""
	}
	objective := strings.TrimSpace(snapshot.TaskState.Objective)
	requiredVerification := strings.TrimSpace(snapshot.TaskState.RequiredVerification)
	if objective == "" && requiredVerification == "" {
		return ""
	}
	parts := make([]string, 0, 2)
	if objective != "" {
		parts = append(parts, "Task objective: "+objective)
	}
	if operation := strings.TrimSpace(snapshot.TaskState.Operation); operation != "" {
		parts = append(parts, "Task operation: "+operation)
	}
	if sourceRef := strings.TrimSpace(snapshot.TaskState.SourceRef); sourceRef != "" {
		parts = append(parts, "Task source ref: "+sourceRef)
	}
	if targetBranch := strings.TrimSpace(snapshot.TaskState.TargetBranch); targetBranch != "" {
		parts = append(parts, "Task target branch: "+targetBranch)
	}
	if requiredVerification != "" {
		parts = append(parts, "Required verification: "+requiredVerification)
	}
	return strings.Join(parts, "\n")
}
