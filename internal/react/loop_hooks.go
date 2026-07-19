package react

import (
	"context"
	"fmt"
	"maps"
	"strings"

	"forge/internal/hooks"
	"forge/internal/llm"
	"forge/internal/secscan"
)

func newLoopHookRegistry() *hooks.Registry {
	registry := hooks.NewRegistry()
	registry.Register(hooks.PointPromptContext, "inspect_first_action", inspectFirstActionPromptHook)
	registry.Register(hooks.PointPromptContext, "review_guidance", reviewPromptHook)
	registry.Register(hooks.PointPromptContext, "preview_workflow", previewWorkflowPromptHook)
	registry.Register(hooks.PointPromptContext, "plan_blocker", blockedPlanPromptHook)
	registry.Register(hooks.PointPromptContext, "agent_handoff", agentHandoffPromptHook)
	registry.Register(hooks.PointPromptContext, "synthesis_guidance", synthesisPromptHook)
	registry.Register(hooks.PointPromptContext, "validation_failure", validationPromptHook)
	registry.Register(hooks.PointPromptContext, "search_thrash", searchThrashPromptHook)
	registry.Register(hooks.PointPromptContext, "git_workflow", gitWorkflowPromptHook)
	registry.Register(hooks.PointPromptContext, "repeat_loop", repeatLoopPromptHook)
	registry.Register(hooks.PointPromptContext, "agent_status", agentStatusPromptHook)
	registry.Register(hooks.PointBeforeTool, "git_commit_blocker", beforeToolGitCommitBlockHook)
	return registry
}

func inspectFirstActionPromptHook(_ context.Context, event hooks.Event) []hooks.Result {
	payload, ok := event.Transient.(promptHookPayload)
	if !ok || payload.Mode != ModeInspect {
		return nil
	}
	snap, ok := event.Snapshot.(SessionSnapshot)
	if ok && snapshotHasRepoReadEvidence(snap) {
		if !isRepoOverviewTask(snap) {
			return nil
		}
		return []hooks.Result{hooks.OverlayResult{
			Key:        "inspect_first_action",
			Content:    "Repo overview workflow active. If you already have the repo root listing and one high-signal file such as README.md, stop exploring and answer briefly in 2-4 bullets or a short paragraph. Keep it conversational and do not turn it into a full repo audit.",
			Priority:   hooks.PriorityHigh,
			Provenance: "runtime",
		}}
	}
	content := "Repo inspection workflow active. Start with a short natural sentence explaining what you are checking, then call the repo read/search tool. For a general overview, list_dir(.) or read_file(README.md) is usually enough to begin."
	if ok && isRepoOverviewTask(snap) {
		content = "Repo overview workflow active. Start with a short natural sentence explaining what you are checking, then call the repo read/search tool. Usually list_dir(.) plus README.md or one other high-signal file is enough; once you have that, stop exploring and answer briefly."
	}
	return []hooks.Result{hooks.OverlayResult{
		Key:        "inspect_first_action",
		Content:    content,
		Priority:   hooks.PriorityHigh,
		Provenance: "runtime",
	}}
}

func reviewPromptHook(_ context.Context, event hooks.Event) []hooks.Result {
	payload, ok := event.Transient.(promptHookPayload)
	if !ok || payload.Mode != ModeReview {
		return nil
	}
	return []hooks.Result{hooks.OverlayResult{
		Key:        "review_guidance",
		Content:    "Review workflow active. Lead with findings before summary, keep findings grounded in repo evidence, and call out regressions, risks, or missing tests explicitly.",
		Priority:   hooks.PriorityHigh,
		Provenance: "runtime",
	}}
}

func previewWorkflowPromptHook(_ context.Context, event hooks.Event) []hooks.Result {
	payload, ok := event.Transient.(promptHookPayload)
	if !ok || payload.Mode != ModePreview {
		return nil
	}
	snap, ok := event.Snapshot.(SessionSnapshot)
	if !ok || snapshotHasPreviewVerificationEvidence(snap) {
		return nil
	}

	var content string
	switch {
	case snapshotHasWriteEvidence(snap):
		content = "Preview workflow active. The preview content is already written. Call preview_server_ensure now, then answer with the verified URL."
	case payload.PlanWorkflow.active && strings.EqualFold(payload.PlanWorkflow.mode, "preview") && payload.PlanWorkflow.synthesisRequired:
		content = "Preview workflow active. You have enough repo evidence for the mockup. Stop exploring. Write the preview artifact or target file now, then call preview_server_ensure and answer with the verified URL. If the user asked for multiple concepts, present them together on one preview page instead of researching more."
	case !snapshotHasAnyToolEvidence(snap):
		content = "Preview workflow active. Start with the most likely directory or named file from the request rather than a repo-wide survey. Prefer list_dir on the likely folder, read_file on the candidate file, or code_search with one literal identifier. Avoid shotgun alternation searches like foo|bar|baz."
	case snapshotHasRepoReadEvidence(snap):
		content = "Preview workflow active. Keep research tight: after 1-3 high-signal reads, stop exploring, write the mockup artifact or target file, and call preview_server_ensure."
	default:
		return nil
	}

	return []hooks.Result{hooks.OverlayResult{
		Key:        "preview_workflow",
		Content:    content,
		Priority:   hooks.PriorityHigh,
		Provenance: "runtime",
	}}
}

func snapshotHasRepoReadEvidence(snapshot SessionSnapshot) bool {
	for _, msg := range snapshot.History {
		if msg.Role != llm.RoleAssistant {
			continue
		}
		for _, call := range msg.ToolCalls {
			switch strings.TrimSpace(call.Name) {
			case "read_file", "search", "code_search", "list_dir", "glob",
				"git_status", "git_diff", "git_log", "git_branch_state", "git_merge_status",
				"artifact_read", "preview_server_status",
				"lsp_definition", "lsp_references", "lsp_hover", "lsp_document_symbols":
				return true
			}
		}
	}
	return false
}

func isRepoOverviewTask(snapshot SessionSnapshot) bool {
	if snapshot.TaskState == nil {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(snapshot.TaskState.Operation), "overview")
}

func snapshotHasAnyToolEvidence(snapshot SessionSnapshot) bool {
	for _, msg := range snapshot.History {
		switch msg.Role {
		case llm.RoleAssistant:
			if len(msg.ToolCalls) > 0 {
				return true
			}
		case llm.RoleTool:
			return true
		}
	}
	return false
}

func snapshotHasWriteEvidence(snapshot SessionSnapshot) bool {
	for _, msg := range snapshot.History {
		if msg.Role != llm.RoleAssistant {
			continue
		}
		for _, call := range msg.ToolCalls {
			switch strings.TrimSpace(call.Name) {
			case "edit_file", "write_file", "apply_patch", "artifact_write":
				return true
			}
		}
	}
	return false
}

func snapshotHasPreviewVerificationEvidence(snapshot SessionSnapshot) bool {
	previewCalls := map[string]struct{}{}
	for _, msg := range snapshot.History {
		switch msg.Role {
		case llm.RoleAssistant:
			for _, call := range msg.ToolCalls {
				switch strings.TrimSpace(call.Name) {
				case "preview_server_ensure", "preview_server_status":
					previewCalls[call.ID] = struct{}{}
				}
			}
		case llm.RoleTool:
			if _, ok := previewCalls[msg.ToolCallID]; !ok {
				continue
			}
			if previewVerificationResultLooksLive(msg.Content) {
				return true
			}
		}
	}
	return false
}

func previewVerificationResultLooksLive(result string) bool {
	lower := strings.ToLower(strings.TrimSpace(result))
	if lower == "" {
		return false
	}
	if !strings.Contains(lower, "http://127.0.0.1:") && !strings.Contains(lower, `"url"`) {
		return false
	}
	return strings.Contains(lower, `"status":"live"`) || strings.Contains(lower, `"status":"running"`)
}

func looksLikeLegacyXMLToolCall(text string) bool {
	if _, ok := parseLegacyXMLToolCall(text); ok {
		return true
	}
	if _, ok := parseXMLToolCallsWrapper(text); ok {
		return true
	}
	return containsXMLToolCallMarkup(text)
}

func containsXMLToolCallMarkup(text string) bool {
	lower := strings.ToLower(strings.TrimSpace(text))
	return strings.Contains(lower, "<tool_call") ||
		strings.Contains(lower, "</tool_call") ||
		strings.Contains(lower, "<tool_calls") ||
		strings.Contains(lower, "</tool_calls") ||
		strings.Contains(lower, "<function_calls") ||
		strings.Contains(lower, "</function_calls")
}

// parseXMLToolCallsWrapper detects the <tool_calls>...</tool_calls> XML wrapper
// format used by some providers.
func parseXMLToolCallsWrapper(text string) (llm.NativeToolCall, bool) {
	const open = "<tool_calls>"
	const close = "</tool_calls>"
	start := strings.Index(text, open)
	if start < 0 {
		return llm.NativeToolCall{}, false
	}
	end := strings.LastIndex(text, close)
	if end < 0 || end <= start {
		return llm.NativeToolCall{}, false
	}
	inner := strings.TrimSpace(text[start+len(open) : end])
	if inner == "" {
		return llm.NativeToolCall{}, false
	}
	return llm.NativeToolCall{ID: "legacy_xml_wrapper_1", Name: "", ArgsJSON: "{}"}, true
}

// stripXMLToolCallMarkup removes <tool_calls>...</tool_calls> and
// <tool_call>...</tool_call> XML markup from text. This prevents models
// from leaking tool call syntax into the chat history and display when
// they emit XML alongside native tool calls.
func stripXMLToolCallMarkup(text string) string {
	text = stripXMLBlock(text, "<tool_calls>", "</tool_calls>")
	text = stripXMLBlock(text, "<tool_call>", "</tool_call>")
	text = stripSelfClosingXMLTag(text, "<tool_call")
	return strings.TrimSpace(text)
}

func stripSelfClosingXMLTag(text, open string) string {
	for {
		lower := strings.ToLower(text)
		start := strings.Index(lower, open)
		if start < 0 {
			return text
		}
		end := strings.Index(lower[start:], "/>")
		if end < 0 {
			return text
		}
		end += start
		text = strings.TrimSpace(text[:start] + text[end+len("/>"):])
	}
}

func stripXMLBlock(text, open, close string) string {
	for {
		start := strings.Index(text, open)
		if start < 0 {
			return text
		}
		end := strings.Index(text[start+len(open):], close)
		if end < 0 {
			return text
		}
		end += start + len(open)
		text = strings.TrimSpace(text[:start] + text[end+len(close):])
	}
}

func blockedPlanPromptHook(_ context.Context, event hooks.Event) []hooks.Result {
	payload, ok := event.Transient.(promptHookPayload)
	if !ok {
		return nil
	}
	blocker := currentPlanBlocker(payload.PlanState)
	if payload.Mode != ModePlan || blocker == "" {
		return nil
	}
	return []hooks.Result{hooks.OverlayResult{
		Key:        "plan_blocker",
		Content:    "Current plan is blocked: " + blocker + ". Resolve the blocker directly if you can, otherwise use ask_user_question to get the missing decision before continuing broad work.",
		Priority:   hooks.PriorityHigh,
		Provenance: "runtime",
	}}
}

func agentHandoffPromptHook(_ context.Context, event hooks.Event) []hooks.Result {
	snap, ok := event.Snapshot.(SessionSnapshot)
	if !ok {
		return nil
	}
	tasks := blockingAgentHandoffs(snap)
	if len(tasks) == 0 {
		return nil
	}
	return []hooks.Result{hooks.OverlayResult{
		Key:        "agent_handoff",
		Content:    agentHandoffOverlayContent(tasks),
		Priority:   hooks.PriorityHigh,
		Provenance: "runtime",
	}}
}

func blockingAgentHandoffs(snap SessionSnapshot) []AgentTaskState {
	var out []AgentTaskState
	for _, task := range snap.AgentTasks {
		if task.Status != AgentStatusCompleted || task.Handoff == nil || !task.Handoff.Blocking() {
			continue
		}
		out = append(out, task)
	}
	return out
}

func agentHandoffOverlayContent(tasks []AgentTaskState) string {
	var b strings.Builder
	b.WriteString("Resolve child-agent handoff before final response. The parent/orchestrator owns remaining writes, repairs, verification, commits, and user questions. Do not ask the user to run repair commands.\n")
	for _, task := range tasks {
		role := strings.TrimSpace(task.Role)
		if role == "" {
			role = "unknown-role"
		}
		fmt.Fprintf(&b, "- %s (%s):\n", strings.TrimSpace(task.ID), role)
		if task.Handoff == nil {
			continue
		}
		for _, action := range task.Handoff.RemainingActions {
			target := strings.TrimSpace(action.TargetPath)
			if target == "" {
				target = "no target path"
			}
			fmt.Fprintf(&b, "  action %s %s: %s\n", action.Kind, target, strings.TrimSpace(action.Description))
		}
		for _, incident := range task.Handoff.Incidents {
			paths := strings.Join(incident.Paths, ", ")
			if paths == "" {
				paths = "no paths"
			}
			fmt.Fprintf(&b, "  incident %s %s: %s\n", incident.Kind, paths, strings.TrimSpace(incident.Description))
			if incident.Kind == AgentIncidentAccidentalWrite {
				b.WriteString("  Inspect git_diff/git_status and relevant file contents before any restore. If the diff may include user work, ask one concise question before changing it.\n")
			}
		}
	}
	return strings.TrimSpace(b.String())
}

func synthesisPromptHook(_ context.Context, event hooks.Event) []hooks.Result {
	payload, ok := event.Transient.(promptHookPayload)
	if !ok {
		return nil
	}
	content := strings.TrimSpace(payload.PlanWorkflow.overlayContent())
	if content == "" {
		return nil
	}
	return []hooks.Result{hooks.OverlayResult{
		Key:        "synthesis_guidance",
		Content:    content,
		Priority:   hooks.PriorityHigh,
		Provenance: "runtime",
	}}
}

func validationPromptHook(_ context.Context, event hooks.Event) []hooks.Result {
	payload, ok := event.Transient.(promptHookPayload)
	if !ok {
		return nil
	}
	content := strings.TrimSpace(payload.ValidationWorkflow.overlayContent())
	if content == "" {
		return nil
	}
	return []hooks.Result{hooks.OverlayResult{
		Key:        "validation_failure",
		Content:    content,
		Priority:   hooks.PriorityHigh,
		Provenance: "runtime",
	}}
}

func searchThrashPromptHook(_ context.Context, event hooks.Event) []hooks.Result {
	payload, ok := event.Transient.(promptHookPayload)
	if !ok {
		return nil
	}
	content := strings.TrimSpace(payload.SearchWorkflow.overlayContent())
	if content == "" {
		return nil
	}
	return []hooks.Result{hooks.OverlayResult{
		Key:        "search_thrash",
		Content:    content,
		Priority:   hooks.PriorityHigh,
		Provenance: "runtime",
	}}
}

func gitWorkflowPromptHook(_ context.Context, event hooks.Event) []hooks.Result {
	payload, ok := event.Transient.(promptHookPayload)
	if !ok {
		return nil
	}
	content := strings.TrimSpace(payload.GitWorkflow.overlayContent())
	if content == "" {
		return nil
	}
	return []hooks.Result{hooks.OverlayResult{
		Key:        "git_workflow",
		Content:    content,
		Priority:   hooks.PriorityHigh,
		Provenance: "runtime",
	}}
}

func repeatLoopPromptHook(_ context.Context, event hooks.Event) []hooks.Result {
	payload, ok := event.Transient.(promptHookPayload)
	if !ok {
		return nil
	}
	content := strings.TrimSpace(payload.RepeatWorkflow.overlayContent(payload.ToolThrashCircuitBreaker))
	if content == "" {
		return nil
	}
	return []hooks.Result{hooks.OverlayResult{
		Key:        "repeat_loop",
		Content:    content,
		Priority:   hooks.PriorityHigh,
		Provenance: "runtime",
	}}
}

func agentStatusPromptHook(_ context.Context, event hooks.Event) []hooks.Result {
	snap, ok := event.Snapshot.(SessionSnapshot)
	if !ok {
		return nil
	}
	agents := outstandingSpawnedAgents(snap)
	if len(agents) == 0 {
		return nil
	}
	var b strings.Builder
	b.WriteString("Outstanding child agents are still unresolved:\n")
	tasksByID := make(map[string]AgentTaskState, len(snap.AgentTasks))
	for _, task := range snap.AgentTasks {
		if id := strings.TrimSpace(task.ID); id != "" {
			tasksByID[id] = task
		}
	}
	for _, agent := range agents {
		if task, ok := tasksByID[strings.TrimSpace(agent.ID)]; ok && agentStillOutstanding(task.Status) {
			fmt.Fprintf(&b, "- %s\n", formatAgentTaskPromptLine(task))
			continue
		}
		role := strings.TrimSpace(agent.Role)
		if role == "" {
			role = "unknown-role"
		}
		status := strings.TrimSpace(string(agent.Status))
		if status == "" {
			status = string(AgentStatusRunning)
		}
		fmt.Fprintf(&b, "- %s (%s): %s\n", strings.TrimSpace(agent.ID), role, status)
	}
	b.WriteString("Do not say no agents are running while this list is non-empty. If the user asks about agents, report this state; use wait_agent with the agent id before continuing delegated work.")
	return []hooks.Result{hooks.OverlayResult{
		Key:        "agent_status",
		Content:    strings.TrimSpace(b.String()),
		Priority:   hooks.PriorityHigh,
		Provenance: "runtime",
	}}
}

func formatAgentTaskPromptLine(task AgentTaskState) string {
	role := strings.TrimSpace(task.Role)
	if role == "" {
		role = "unknown-role"
	}
	status := strings.TrimSpace(string(task.Status))
	if status == "" {
		status = string(AgentStatusRunning)
	}
	line := fmt.Sprintf("%s (%s): %s", strings.TrimSpace(task.ID), role, status)
	if tool := strings.TrimSpace(task.LastToolName); tool != "" {
		line += "; last: " + tool
		if len(task.RecentActivity) > 0 {
			last := task.RecentActivity[len(task.RecentActivity)-1]
			if summary := strings.TrimSpace(last.Summary); summary != "" {
				line += " " + redactRuntimeText(summary)
			}
		}
	}
	return line
}

func redactRuntimeText(text string) string {
	if text == "" {
		return ""
	}
	scanner := secscan.NewDefaultScanner()
	return secscan.Redact(text, scanner.Scan(text))
}

func beforeToolGitCommitBlockHook(_ context.Context, event hooks.Event) []hooks.Result {
	payload, ok := event.Transient.(beforeToolHookPayload)
	if !ok {
		return nil
	}
	if !isCommitToolCall(payload.ToolName, payload.Args) {
		return nil
	}
	switch {
	case payload.GitWorkflow.unmergedFiles:
		return []hooks.Result{hooks.BlockResult{
			Message:    "blocked: unmerged git conflicts remain. Resolve conflicted files, stage them, and call git_merge_status before retrying commit.",
			Provenance: "runtime",
		}}
	case payload.GitWorkflow.commitBlocker == commitBlockerRestage:
		if payload.ToolName == "run_command" && strings.Contains(strings.ToLower(stringArg(payload.Args, "command")), "git add") {
			return nil
		}
		return []hooks.Result{hooks.BlockResult{
			Message:    "blocked: the previous commit attempt modified files via hooks. Re-stage those files and call git_merge_status before retrying commit.",
			Provenance: "runtime",
		}}
	case payload.GitWorkflow.commitBlocker == commitBlockerEdit:
		return []hooks.Result{hooks.BlockResult{
			Message:    "blocked: the previous commit attempt already failed and nothing has changed since then. Fix the reported hook issues and call git_merge_status before retrying commit.",
			Provenance: "runtime",
		}}
	default:
		return nil
	}
}

func mergePromptHookOutput(base, runtime hooks.ExecutionOutput) hooks.ExecutionOutput {
	merged := hooks.ExecutionOutput{
		Overlays: filterPromptHookOverlays(base.Overlays, runtime.Overlays),
		Failures: append([]hooks.Failure(nil), runtime.Failures...),
	}
	if base.Note != nil {
		note := *base.Note
		merged.Note = &note
	}
	if runtime.Note != nil && (merged.Note == nil || runtime.Note.Priority > merged.Note.Priority) {
		note := *runtime.Note
		merged.Note = &note
	}
	merged.Overlays = append(merged.Overlays, runtime.Overlays...)
	return merged
}

func filterPromptHookOverlays(overlays []hooks.OverlayResult, runtimeOverlays []hooks.OverlayResult) []hooks.OverlayResult {
	runtimePluginKeys := make(map[string]struct{}, len(runtimeOverlays))
	for _, overlay := range runtimeOverlays {
		if isPluginOverlay(overlay) {
			runtimePluginKeys[strings.ToLower(strings.TrimSpace(overlay.Key))] = struct{}{}
		}
	}
	filtered := make([]hooks.OverlayResult, 0, len(overlays))
	for _, overlay := range overlays {
		key := strings.TrimSpace(overlay.Key)
		if _, ok := loopHookOverlayKeys[key]; ok {
			continue
		}
		if _, ok := runtimePluginKeys[strings.ToLower(key)]; ok {
			continue
		}
		filtered = append(filtered, overlay)
	}
	return filtered
}

func isPluginOverlay(overlay hooks.OverlayResult) bool {
	return strings.HasPrefix(strings.TrimSpace(overlay.Provenance), "plugin:") ||
		strings.HasPrefix(strings.TrimSpace(overlay.Key), "plugin_")
}

func hasHookOutputContent(output hooks.ExecutionOutput) bool {
	return len(output.Overlays) > 0 || output.Note != nil || output.Block != nil || len(output.Failures) > 0
}

func cloneArgs(args map[string]any) map[string]any {
	if len(args) == 0 {
		return nil
	}
	cloned := make(map[string]any, len(args))
	maps.Copy(cloned, args)
	return cloned
}

func currentPlanBlocker(state *PlanState) string {
	if state == nil {
		return ""
	}
	if step, ok := state.BlockedStep(); ok {
		if blocker := strings.TrimSpace(step.Blocker); blocker != "" {
			return blocker
		}
		return strings.TrimSpace(step.Step)
	}
	return ""
}

func (r *Runner) updateSameFileSearchWorkflow(toolName string, args map[string]any, blocked bool) {
	if blocked {
		r.syncRuntimeNote()
		return
	}
	path := strings.TrimSpace(stringArg(args, "path"))
	switch toolName {
	case "code_search", "search":
		if path == "" {
			r.searchWorkflow = sameFileSearchWorkflowState{}
			r.syncRuntimeNote()
			return
		}
		if r.searchWorkflow.toolName == toolName && r.searchWorkflow.path == path {
			r.searchWorkflow.streak++
		} else {
			r.searchWorkflow = sameFileSearchWorkflowState{
				toolName: toolName,
				path:     path,
				streak:   1,
			}
		}
		r.searchWorkflow.nudged = r.searchWorkflow.streak >= toolThrashThreshold(r.toolThrashCircuitBreaker, sameFileSearchThrashThreshold)
	default:
		r.searchWorkflow = sameFileSearchWorkflowState{}
	}
	r.syncRuntimeNote()
}

func (r *Runner) updatePlanWorkflow(toolName string, args map[string]any, _ string, blocked bool) {
	if !r.planWorkflow.active {
		return
	}
	if blocked {
		r.syncRuntimeNote()
		return
	}
	if allowsPlanSynthesis(toolName) {
		// update_plan or think: reset exploration counter so the model can keep working
		r.planWorkflow.explorationBatches = 0
		r.planWorkflow.synthesisRequired = false
		r.planWorkflow.synthesisEscalated = false
		r.syncRuntimeNote()
		return
	}
	if !isExplorationToolCall(toolName, args) {
		// write mutation: the model made progress — reset exploration state
		r.planWorkflow.explorationBatches = 0
		r.planWorkflow.synthesisRequired = false
		r.planWorkflow.synthesisEscalated = false
		r.syncRuntimeNote()
		return
	}
	r.planWorkflow.explorationBatches++
	budget := synthesisGuardBudget(r.planWorkflow.mode)
	if r.planWorkflow.explorationBatches >= budget*2 {
		r.planWorkflow.synthesisEscalated = true
	}
	if r.planWorkflow.explorationBatches >= budget {
		r.planWorkflow.synthesisRequired = true
	}
	r.syncRuntimeNote()
}

func (s planWorkflowState) overlayContent() string {
	if !s.active || !s.synthesisRequired {
		return ""
	}
	if s.synthesisEscalated {
		return "URGENT: you have explored far too much without acting. Stop all exploration immediately. You must either make an edit, run a command, or provide a concrete text answer RIGHT NOW in this exact message. No more reading, searching, or listing."
	}
	switch s.mode {
	case "analysis":
		return "Analysis guidance: you have enough evidence to answer. Avoid exhaustive repo-wide searches, stop exploring and summarize findings or recommendations now. Put any uncertainty into open questions instead of doing more low-yield research."
	case "preview":
		return ""
	case "implement":
		return "Implementation guidance: you have gathered enough context. Stop exploring and either make an edit with the edit tools or provide a concrete text summary of what you found and what needs to change."
	case "inspect":
		return "Inspection guidance: you have gathered enough evidence. Stop searching and provide a concise summary grounded in the paths you already inspected."
	case "overview":
		return "Overview guidance: you have enough context. Stop exploring and give a brief, conversational overview grounded in the paths you already inspected."
	case "review":
		return "Review guidance: you have enough evidence. Stop searching and deliver your findings first, ordered by severity, with specific references to the code you inspected."
	case "validate":
		return "Validation guidance: you have enough context. Stop exploring and run the relevant verification command, or summarize what you found if no verification is needed."
	case "chat":
		return "Guidance: you have gathered enough context. Stop exploring and either act (edit, run, write) or provide a concrete answer grounded in what you already inspected."
	default:
		return "Planning task guidance: you have enough evidence to write the plan. Avoid exhaustive repo-wide searches, stop exploring and synthesize the next actionable plan now. Use update_plan to capture the steps, and put any uncertainty into open questions instead of doing more broad research."
	}
}

func (s gitWorkflowState) overlayContent() string {
	if s.unmergedFiles {
		return "Git merge workflow active. Call git_merge_status to inspect unresolved files, conflict previews, and next steps. Resolve each conflicted file, stage the resolutions, and only retry commit once unmerged files are gone."
	}
	if s.commitBlocker != commitBlockerNone {
		summary := strings.TrimSpace(s.blockerSummary)
		if summary == "" {
			summary = "commit blockers remain"
		}
		return "Git merge workflow active. " + summary + ". Call git_merge_status after each fix and do not retry the same commit until the blockers are cleared."
	}
	if s.mergeActive {
		return "Git merge workflow active. Call git_merge_status to inspect the current merge state, then keep resolving and validating the merge until commit succeeds."
	}
	return ""
}

func isCommitToolCall(toolName string, args map[string]any) bool {
	if toolName == "git_commit" {
		return true
	}
	if toolName != "run_command" {
		return false
	}
	return isGitCommitLike(strings.ToLower(strings.TrimSpace(stringArg(args, "command"))))
}

func stringArg(args map[string]any, key string) string {
	value, _ := args[key].(string)
	return value
}

func isGitCommitLike(command string) bool {
	return strings.Contains(command, "git commit")
}

func isGitMergeLike(command string) bool {
	return strings.HasPrefix(command, "git merge ") || strings.Contains(command, " git merge ")
}

// isGitUnmergedListCommand matches commands whose output consists exclusively of
// unmerged-file entries, so any non-empty line is a conflict indicator.
func isGitUnmergedListCommand(command string) bool {
	return strings.Contains(command, "git diff --name-only --diff-filter=u") ||
		strings.Contains(command, "git diff --name-only --diff-filter=U") ||
		strings.Contains(command, "git ls-files -u") ||
		strings.Contains(command, "git ls-files --unmerged")
}

// isGitStatusPorcelainCommand matches git status --porcelain, whose output mixes
// all change types; only specific XY codes indicate unmerged files.
func isGitStatusPorcelainCommand(command string) bool {
	return strings.Contains(command, "git status --porcelain")
}

func hasMergeConflict(result string) bool {
	lower := strings.ToLower(result)
	return strings.Contains(lower, "automatic merge failed") || strings.Contains(lower, "conflict (")
}

// hasNonEmptyOutput returns true when result contains at least one non-blank,
// non-exit-code line. Used for commands that only emit unmerged-file paths.
func hasNonEmptyOutput(result string) bool {
	trimmed := strings.TrimSpace(result)
	if trimmed == "" || trimmed == "exit 0" {
		return false
	}
	for line := range strings.SplitSeq(trimmed, "\n") {
		line = strings.TrimSpace(line)
		if line != "" && !strings.HasPrefix(line, "exit ") {
			return true
		}
	}
	return false
}

// hasPorcelainConflicts checks git status --porcelain output for lines whose XY
// status code indicates an unmerged (conflicted) file. Normal staged/unstaged
// changes (e.g. "M  README.md") are not treated as conflicts.
func hasPorcelainConflicts(result string) bool {
	conflictPrefixes := []string{"UU ", "AA ", "DD ", "AU ", "UA ", "DU ", "UD "}
	for line := range strings.SplitSeq(result, "\n") {
		line = strings.TrimSpace(line)
		for _, prefix := range conflictPrefixes {
			if strings.HasPrefix(line, prefix) {
				return true
			}
		}
	}
	return false
}

func isSuccessfulGitCommit(result string) bool {
	lower := strings.ToLower(result)
	return !strings.Contains(lower, "error committing:") &&
		!strings.Contains(lower, "exit 1") &&
		(strings.Contains(lower, "file changed") || strings.Contains(lower, "files changed") ||
			strings.Contains(lower, "nothing to commit") || strings.Contains(lower, "create mode"))
}

func summarizeCommitFailure(result string) string {
	lower := strings.ToLower(result)
	switch {
	case strings.Contains(lower, "yamllint"):
		return "commit blocked by yamllint/pre-commit failures"
	case strings.Contains(lower, "prettier"):
		return "commit blocked by prettier/pre-commit failures"
	case strings.Contains(lower, "hook id:"):
		return "commit blocked by pre-commit hook failures"
	default:
		return "commit blockers remain"
	}
}

func (r *Runner) updateValidationWorkflow(toolName string, args map[string]any, result string) {
	if toolName != "run_command" {
		return
	}
	command := strings.TrimSpace(stringArg(args, "command"))
	if !isValidationCommand(strings.ToLower(command)) {
		return
	}
	passed := isValidationPass(result)
	r.validationWorkflow.ran = true
	r.validationWorkflow.passed = passed
	r.validationWorkflow.cmd = command
	if r.renderer != nil {
		r.renderer.ToolResult("__validation", formatValidationResult(command, passed), "", !passed)
	}
	r.syncRuntimeNote()
}

func isValidationCommand(command string) bool {
	for _, prefix := range []string{
		"go test", "go build", "go vet",
		"npm test", "npm run build",
		"bun test", "bun run build",
		"yarn test", "yarn build",
		"pnpm test", "pnpm build",
		"pytest", "cargo test", "cargo build", "cargo check",
		"golangci-lint",
	} {
		if strings.HasPrefix(command, prefix) {
			return true
		}
	}
	return false
}

func isValidationPass(result string) bool {
	if idx := strings.LastIndex(result, "\nexit "); idx >= 0 {
		code := strings.TrimSpace(result[idx+len("\nexit "):])
		return code == "0"
	}
	lower := strings.ToLower(result)
	if strings.Contains(lower, "timeout") || strings.Contains(lower, "timed out") || strings.Contains(lower, "deadline exceeded") {
		return false
	}
	return !strings.Contains(lower, "\nfail\t")
}

func formatValidationResult(cmd string, passed bool) string {
	if passed {
		return "validation passed: " + cmd
	}
	return "validation failed: " + cmd
}

func (s validationWorkflowState) overlayContent() string {
	if !s.ran || s.passed {
		return ""
	}
	return "Last validation failed: " + s.cmd + " — fix the reported errors before finishing."
}

func (s sameFileSearchWorkflowState) overlayContent() string {
	if !s.nudged || s.path == "" {
		return ""
	}
	return "Search thrash guidance: you have repeatedly searched the same file without switching to a direct read. Stop trying more patterns on " + s.path + ". Read that file now, inspect the relevant function or block directly, then continue editing."
}

func (r *Runner) updateRepeatToolCallWorkflow(toolName string, args map[string]any, _ string) {
	target := repeatToolCallTarget(toolName, args)
	if target == "" {
		r.repeatWorkflow = repeatToolCallState{}
		r.syncRuntimeNote()
		return
	}
	key := toolName + ":" + target
	recent := append(r.repeatWorkflow.recent, key)
	if len(recent) > repeatToolCallWindow {
		recent = recent[len(recent)-repeatToolCallWindow:]
	}
	r.repeatWorkflow = repeatToolCallState{
		lastToolName: toolName,
		lastTarget:   target,
		recent:       recent,
		streak:       repeatToolCallOccurrences(recent, key),
	}
	r.syncRuntimeNote()
}

func repeatToolCallTarget(toolName string, args map[string]any) string {
	switch toolName {
	case "read_file":
		path := strings.TrimSpace(stringArg(args, "path"))
		if path == "" {
			path = strings.TrimSpace(stringArg(args, "filePath"))
		}
		if path == "" {
			return ""
		}
		// Include the requested range so paging through a large file with
		// different start_line/end_line values is not treated as a repeat.
		if rangeKey := readRangeKey(args); rangeKey != "" {
			return path + "#" + rangeKey
		}
		return path
	case "list_dir":
		return strings.TrimSpace(stringArg(args, "path"))
	case "code_search":
		return strings.TrimSpace(stringArg(args, "query"))
	case "search":
		return strings.TrimSpace(stringArg(args, "pattern"))
	case "run_command":
		return strings.TrimSpace(stringArg(args, "command"))
	case "glob":
		return strings.TrimSpace(stringArg(args, "pattern"))
	case "edit_file":
		path := strings.TrimSpace(stringArg(args, "path"))
		oldText := strings.TrimSpace(stringArg(args, "old_text"))
		newText := strings.TrimSpace(stringArg(args, "new_text"))
		if path == "" || oldText != newText {
			return ""
		}
		return path + ":" + oldText + "->" + newText
	case "wait_agent":
		return strings.TrimSpace(stringArg(args, "id"))
	default:
		return ""
	}
}

func readRangeKey(args map[string]any) string {
	start := stringArg(args, "start_line")
	end := stringArg(args, "end_line")
	if start == "" {
		if v, ok := args["start_line"].(float64); ok {
			start = fmt.Sprintf("%d", int(v))
		}
	}
	if end == "" {
		if v, ok := args["end_line"].(float64); ok {
			end = fmt.Sprintf("%d", int(v))
		}
	}
	if start == "" && end == "" {
		return ""
	}
	return start + "-" + end
}

func (s repeatToolCallState) overlayContent(threshold int) string {
	if s.streak >= toolThrashThreshold(threshold, repeatToolCallThreshold) {
		return fmt.Sprintf("Loop detection: you have called %s on the same target %q %d times recently without making progress. Stop repeating this action. Either the approach is wrong or you already have the information you need. Switch to a different tool or synthesize your findings now.", s.lastToolName, s.lastTarget, s.streak)
	}
	if path, count := s.rereadPathCount(); count >= rereadSameFileThreshold {
		return fmt.Sprintf("Loop detection: you have read %q %d times recently (different ranges) without editing anything. The file has not changed; re-reading it will not reveal anything new. Stop verifying and act on what you already know.", path, count)
	}
	return ""
}

// rereadPathCount counts recent read_file calls on the most recent read path,
// ignoring the line range, so verification spirals that page through the same
// file with varying ranges are still caught.
func (s repeatToolCallState) rereadPathCount() (string, int) {
	if s.lastToolName != "read_file" {
		return "", 0
	}
	path, _, _ := strings.Cut(s.lastTarget, "#")
	if path == "" {
		return "", 0
	}
	prefix := "read_file:" + path
	count := 0
	for _, k := range s.recent {
		if k == prefix || strings.HasPrefix(k, prefix+"#") {
			count++
		}
	}
	return path, count
}

func isExplorationToolCall(toolName string, args map[string]any) bool {
	switch toolName {
	case "read_file", "list_dir", "search", "glob", "code_search", "tool_help", "view_image",
		"lsp_definition", "lsp_references", "lsp_hover", "lsp_document_symbols",
		"web_fetch", "web_search", "git_status", "git_diff", "git_log", "git_branch_state", "git_merge_status":
		return true
	case "run_command":
		return isReadOnlyCommand(strings.ToLower(strings.TrimSpace(stringArg(args, "command"))))
	default:
		return false
	}
}

func allowsPlanSynthesis(toolName string) bool {
	switch toolName {
	case "update_plan", "think":
		return true
	default:
		return false
	}
}

func isSynthesisGuardOperation(operation string) bool {
	switch strings.ToLower(strings.TrimSpace(operation)) {
	case "plan", "analysis", "preview", "implement", "inspect", "overview", "review", "validate":
		return true
	default:
		return false
	}
}

func synthesisGuardBudget(mode string) int {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "analysis":
		return analysisExplorationBudget
	case "preview":
		return previewExplorationBudget
	case "implement":
		return implementExplorationBudget
	case "inspect":
		return inspectExplorationBudget
	case "overview":
		return overviewExplorationBudget
	case "review":
		return reviewExplorationBudget
	case "validate":
		return validateExplorationBudget
	case "chat":
		return chatExplorationBudget
	default:
		return planExplorationBudget
	}
}

func isReadOnlyCommand(command string) bool {
	if command == "" {
		return false
	}
	for _, prefix := range []string{
		"rg ", "grep ", "sed ", "cat ", "ls", "git status", "git diff", "git log", "git show", "git branch", "git grep", "go test", "npm test", "pnpm test", "yarn test",
	} {
		if strings.HasPrefix(command, prefix) {
			return true
		}
	}
	return false
}
