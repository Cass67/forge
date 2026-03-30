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
	if plan := planStateContext(snapshot); plan != "" {
		messages = append(messages, llm.Message{Role: llm.RoleSystem, Content: plan})
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
	if strings.EqualFold(strings.TrimSpace(snapshot.TaskState.Operation), "plan") {
		parts = append(parts, "Planning guidance: gather only enough repo evidence to support the plan, avoid exhaustive repo-wide searches, and once the next actionable plan is clear, stop exploring and synthesize it. Use update_plan for multi-step plans and leave unresolved details as open questions instead of continuing broad research.")
	}
	if strings.EqualFold(strings.TrimSpace(snapshot.TaskState.Operation), "analysis") {
		parts = append(parts, "Analysis guidance: gather enough source-grounded evidence to support the answer, avoid repetitive repo-wide searching once the pattern is clear, and summarize findings or recommendations instead of continuing low-yield exploration.")
	}
	if strings.EqualFold(strings.TrimSpace(snapshot.TaskState.Operation), "merge") && strings.TrimSpace(snapshot.TaskState.TargetBranch) != "" {
		parts = append(parts, "Merge guidance: use git_merge_status for unresolved conflicts and conflict previews, and use git_branch_state with the target branch before claiming the merge is complete. Prefer these tools over repeated raw git log or graph commands.")
	}
	return strings.Join(parts, "\n")
}

func planStateContext(snapshot SessionSnapshot) string {
	if snapshot.PlanState == nil || len(snapshot.PlanState.Steps) == 0 {
		return ""
	}
	return "Current plan:\n" + FormatPlanState(*snapshot.PlanState)
}
