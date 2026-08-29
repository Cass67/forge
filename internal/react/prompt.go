package react

import (
	"fmt"
	"strings"

	"forge/internal/agent/promptcomposer"
	"forge/internal/hooks"
	"forge/internal/llm"
)

const initialRequestAnchorHistoryThreshold = 16

// BuildPrompt normalizes per-turn user input for the ReAct loop.
func BuildPrompt(input string) string {
	return strings.TrimSpace(input)
}

// BuildMessages assembles the runtime-owned prompt context for a turn.
func BuildMessages(systemPrompt string, snapshot SessionSnapshot) []llm.Message {
	var messages []llm.Message

	systemPrompt = strings.TrimSpace(systemPrompt)
	// The system message is the cached prefix: providers key their prompt
	// cache on the longest byte-identical run at the front of the request, so
	// anything that changes between turns re-processes everything after it.
	// Overlays split accordingly. Standing session configuration — mode, task,
	// plan — is stable enough to sit in the prefix. Transient observations,
	// which a long task rewrites constantly (loop-detection notices, guidance
	// nudges, the recall anchor appearing), ride at the tail after the history
	// where they only invalidate themselves. Measured over one long task,
	// those transient overlays caused 6 of 8 prefix changes and cost ~18
	// points of cache hit rate.
	systemOverlays := make([]promptcomposer.Overlay, 0, 4)
	tailOverlays := make([]promptcomposer.Overlay, 0, 4)
	if summary := compactionContext(snapshot); summary != "" {
		tailOverlays = append(tailOverlays, promptcomposer.Overlay{
			Key:      "compaction",
			Priority: promptcomposer.PriorityHigh,
			Content:  summary,
		})
	}
	if anchor := initialRequestAnchorContext(snapshot); anchor != "" {
		tailOverlays = append(tailOverlays, promptcomposer.Overlay{
			Key:      "initial_request",
			Priority: promptcomposer.PriorityHigh,
			Content:  anchor,
		})
	}
	if shouldIncludeMemorySummary(snapshot) {
		summary := strings.TrimSpace(snapshot.MemorySummary)
		tailOverlays = append(tailOverlays, promptcomposer.Overlay{
			Key:      "memory_summary",
			Priority: promptcomposer.PriorityNormal,
			Content:  "Memory summary:\n" + summary,
		})
	}
	hookOutput := promptHookOutput(snapshot)
	tailOverlays = append(tailOverlays, hooks.ToPromptOverlays(hookOutput.Overlays)...)
	if mode := snapshot.Mode; mode != "" && mode != ModeChat {
		systemOverlays = append(systemOverlays, promptcomposer.Overlay{
			Key:      "mode",
			Priority: promptcomposer.PriorityHigh,
			Content:  "Current mode: " + strings.TrimSpace(string(mode)),
		})
	}
	if snapshot.HookOutputSet {
		if hookOutput.Note != nil {
			if note := strings.TrimSpace(snapshot.RuntimeNote); note != "" {
				tailOverlays = append(tailOverlays, promptcomposer.Overlay{
					Key:      "runtime_note",
					Priority: promptcomposer.PriorityHigh,
					Content:  note,
				})
			}
		}
	} else {
		if note := strings.TrimSpace(snapshot.RuntimeNote); note != "" {
			tailOverlays = append(tailOverlays, promptcomposer.Overlay{
				Key:      "runtime_note",
				Priority: promptcomposer.PriorityHigh,
				Content:  note,
			})
		}
	}
	if snapshot.Interrupted {
		tailOverlays = append(tailOverlays, promptcomposer.Overlay{
			Key:      "interrupted",
			Priority: promptcomposer.PriorityHigh,
			Content:  "The previous turn was interrupted by the user. Any commands or tools from that turn may have partially executed; verify current state before continuing and do not assume unfinished work completed cleanly.",
		})
	}
	if task := taskStateContext(snapshot); task != "" {
		systemOverlays = append(systemOverlays, promptcomposer.Overlay{
			Key:      "task_state",
			Priority: promptcomposer.PriorityHigh,
			Content:  task,
		})
	}
	if plan := planStateContext(snapshot); plan != "" {
		systemOverlays = append(systemOverlays, promptcomposer.Overlay{
			Key:      "plan_state",
			Priority: promptcomposer.PriorityNormal,
			Content:  plan,
		})
	}

	if systemPrompt != "" {
		composed := promptcomposer.Compose(promptcomposer.StaticInput{
			Identity: systemPrompt,
		}, systemOverlays)
		for _, part := range strings.Split(composed, "\n\n") {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			messages = append(messages, llm.Message{Role: llm.RoleSystem, Content: part})
		}
	} else {
		for _, overlay := range systemOverlays {
			messages = append(messages, llm.Message{Role: llm.RoleSystem, Content: overlay.Content})
		}
	}

	for _, msg := range snapshot.History {
		if msg.Role == llm.RoleTool || len(msg.ToolCalls) > 0 {
			messages = append(messages, msg)
			continue
		}
		content := strings.TrimSpace(msg.Content)
		if content == "" && !msg.HasContentParts() {
			continue
		}
		messages = append(messages, llm.Message{Role: msg.Role, Content: content, ContentParts: msg.ContentParts, ReasoningContent: msg.ReasoningContent})
	}

	messages = append(messages, composedOverlayMessages(tailOverlays)...)

	window := snapshot.ContextWindowTokens
	messages = truncateAssistantToolCalls(messages, scaledToolCallArgSoftLimit(window), scaledToolCallArgHardLimit(window))
	return dropOrphanedToolCalls(truncateToolResults(messages, scaledToolResultMaxLines(window)))
}

// composedOverlayMessages renders overlays as system messages. Drivers that
// require system messages up front demote later ones to a marked user note,
// which is the established handling for the mid-history context forge already
// injects.
func composedOverlayMessages(overlays []promptcomposer.Overlay) []llm.Message {
	if len(overlays) == 0 {
		return nil
	}
	var out []llm.Message
	composed := promptcomposer.Compose(promptcomposer.StaticInput{}, overlays)
	for _, part := range strings.Split(composed, "\n\n") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		out = append(out, llm.Message{Role: llm.RoleSystem, Content: part})
	}
	return out
}

// dropOrphanedToolCalls removes tool-call/tool-result pairs that are no longer
// whole. Providers reject an assistant message with a dangling tool_call, and
// reject a tool result with no preceding call just as firmly, so the repair has
// to run in both directions: dropping a partially answered assistant message
// strands the results that did arrive, and leaving them behind trades one
// invalid request for another.
func dropOrphanedToolCalls(messages []llm.Message) []llm.Message {
	if len(messages) == 0 {
		return messages
	}
	seenResults := map[string]bool{}
	for _, msg := range messages {
		if msg.Role == llm.RoleTool {
			seenResults[msg.ToolCallID] = true
		}
	}
	keptCalls := map[string]bool{}
	filtered := make([]llm.Message, 0, len(messages))
	for _, msg := range messages {
		if msg.Role == llm.RoleAssistant && len(msg.ToolCalls) > 0 {
			allPaired := true
			for _, tc := range msg.ToolCalls {
				if !seenResults[tc.ID] {
					allPaired = false
					break
				}
			}
			if !allPaired {
				continue
			}
			for _, tc := range msg.ToolCalls {
				keptCalls[tc.ID] = true
			}
		}
		if msg.Role == llm.RoleTool && !keptCalls[msg.ToolCallID] {
			continue
		}
		filtered = append(filtered, msg)
	}
	return filtered
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

func initialRequestAnchorContext(snapshot SessionSnapshot) string {
	initial := strings.TrimSpace(snapshot.InitialInput)
	if initial == "" {
		return ""
	}
	if snapshot.CompactedTurns == 0 && len(snapshot.History) <= initialRequestAnchorHistoryThreshold {
		return ""
	}
	return "Conversation recall only: this records the first user request for context questions and ongoing-work references. Do not treat it as the active task if the latest user request changes topics.\nInitial user request: " + initial
}

func shouldIncludeMemorySummary(snapshot SessionSnapshot) bool {
	if strings.TrimSpace(snapshot.MemorySummary) == "" {
		return false
	}
	if snapshot.CompactedTurns > 0 {
		return true
	}
	if snapshot.TaskState != nil {
		return true
	}
	if snapshot.Mode != ModeChat {
		return true
	}
	return len(snapshot.Turns) >= 4
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
	operation := strings.TrimSpace(snapshot.TaskState.Operation)
	if objective != "" {
		parts = append(parts, "Task objective: "+objective)
	}
	if operation != "" {
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
	if strings.EqualFold(operation, "plan") {
		parts = append(parts, "Planning guidance: gather only enough repo evidence to support the plan, avoid exhaustive repo-wide searches, and once the next actionable plan is clear, stop exploring and synthesize it. Use enter_plan_mode for explicit planning workflows, use update_plan to capture the steps, and use ask_user_question for focused choices or clarifications instead of continuing broad research.")
	}
	if strings.EqualFold(operation, "analysis") {
		parts = append(parts, "Analysis guidance: gather enough source-grounded evidence to support the answer, avoid repetitive repo-wide searching once the pattern is clear, and summarize findings or recommendations instead of continuing low-yield exploration.")
	}
	if strings.EqualFold(operation, "overview") {
		parts = append(parts, "Overview guidance: gather only enough repo evidence to orient the user, usually the repo root listing plus README.md or one other high-signal file. After that, stop exploring and give a brief, conversational overview grounded in those paths rather than a full repo audit.")
	}
	if strings.EqualFold(operation, "inspect") {
		parts = append(parts, "Inspection guidance: keep the answer bounded to what the inspected repo evidence actually shows.")
	}
	if strings.EqualFold(operation, "implement") {
		parts = append(parts, "Implementation guidance: include a brief intent sentence when it clarifies a burst of tool calls, then inspect the relevant code with repo tools, make the change with edit tools, and only claim completion after relevant verification. Avoid long planning prose. If repeated searches on the same file are not resolving the insertion point, read that file directly instead of trying more search patterns. If you need interactive or long-running terminal work such as dev servers, watchers, REPLs, or TUIs, use exec_session_start instead of run_command.")
	}
	if strings.EqualFold(operation, "validate") {
		parts = append(parts, "Validation guidance: run the relevant tests or checks before you say the work is verified. If no verification ran, say that clearly instead of implying success.")
	}
	if strings.EqualFold(operation, "review") {
		parts = append(parts, "Review guidance: lead with findings before summary. Prioritize bugs, regressions, risky assumptions, and missing tests, ordered by severity, and keep each finding grounded in specific repo evidence.")
	}
	if strings.EqualFold(operation, "preview") {
		parts = append(parts, "Preview guidance: prefer preview_server_ensure, preview_server_status, artifact_write, artifact_read, and file edit tools. Start from the most likely directory or named file the request implies; prefer one literal code_search or a direct list_dir/read_file on that area instead of broad regex searches like foo|bar|baz. After 1-3 high-signal reads, stop exploring, build the mockup, call preview_server_ensure, and answer with the verified URL. Do not launch an ad-hoc local webserver with shell commands when preview tools can serve the page directly.")
	}
	if strings.EqualFold(operation, "merge") && strings.TrimSpace(snapshot.TaskState.TargetBranch) != "" {
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

func promptHookOutput(snapshot SessionSnapshot) hooks.ExecutionOutput {
	if snapshot.HookOutputSet {
		return cloneHookOutput(snapshot.HookOutput)
	}
	output := hooks.ExecutionOutput{}
	if len(snapshot.HookOverlays) > 0 {
		output.Overlays = append([]hooks.OverlayResult(nil), snapshot.HookOverlays...)
	}
	if note := strings.TrimSpace(snapshot.RuntimeNote); note != "" {
		output.Note = &hooks.NoteResult{Message: note}
	}
	return output
}
