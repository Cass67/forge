package runtime

import (
	"encoding/json"
	"fmt"
	"path"
	"regexp"
	"sort"
	"strings"

	"forge/internal/llm"
	reactruntime "forge/internal/react"
)

var (
	intentNarrationPattern       = regexp.MustCompile(`(?i)\b(i['’]ll|first[, ]+i['’]ll|then[, ]+i['’]ll|next[, ]+i['’]ll|let me (?:inspect|check|read|search|look(?:\s+at)?|open|review|run|list|find|trace|debug|fix|implement|update|edit|verify))\b`)
	toolBlockedClaimPattern      = regexp.MustCompile(`(?i)\b(blocked because .*tools?|unable to access|unable to inspect|wasn['’]t able to inspect|produced no output|tool(?:-|\s*)call issues|tooling.*(?:failed|unavailable)|can['’]?t access the repo|cannot access the repo)\b`)
	repoInspectionFailurePattern = regexp.MustCompile(`(?i)\b(unable to access|unable to inspect|wasn['’]t able to inspect|tool(?:-|\s*)call issues|can['’]?t access the repo|cannot access the repo)\b`)
	noEvidenceClaimPattern       = regexp.MustCompile(`(?i)\b(don['’]t have (?:concrete )?evidence|do not have (?:concrete )?evidence|no repository contents were retrieved|no repo contents were retrieved|no files were inspected|couldn['’]t inspect|could not inspect|tool results? .* (?:aren['’]t visible|are not visible|not visible|not available)|outputs? .* (?:aren['’]t visible|are not visible|not visible|not available)|actual contents? .* not returned|readme(?:\.md)? .* (?:wasn['’]t visible|not visible|not returned)|root listing output .* (?:wasn['’]t visible|not visible|not returned)|can['’]t see what['’]s actually in (?:the )?repo|cannot see what['’]s actually in (?:the )?repo|cannot provide a concrete summary|can['’]t reliably state|not inferring beyond)\b`)
	completionClaimPattern       = regexp.MustCompile(`(?i)\b(done|complete|completed|finished|all set)\b`)
	inspectionClaimPattern       = regexp.MustCompile(`(?im)(?:(?:^|[.!?]\s+)\s*|(?:i|we)(?:['’]ve)?\s+)(inspected|reviewed|searched|located|analy[sz]ed|looked at|checked|read)\b`)
	// changeClaimPattern matches first-person change claims ("I fixed X", "we added X") and
	// sentence-leading past-tense claims ("Fixed the bug", "Updated the file").
	// Requires a first-person subject or sentence start to avoid false positives like
	// "X is implemented in Y" where the model is describing existing code, not claiming edits.
	changeClaimPattern       = regexp.MustCompile("(?im)(?:(?:^|[.!?]\\s+)\\s*|(?:i|we)(?:[''`]ve)?\\s+)(fixed|updated|changed|implemented|added|wired|patched|refactored|edited)\\b")
	validationClaimPattern   = regexp.MustCompile(`(?i)\b(tested|validated|checks pass|tests pass|build passes|lint passes|compiled|verified (?:it|the fix|the change|the changes|it works|they work|working))\b`)
	repoGroundedInputPattern = regexp.MustCompile(`(?i)\b(repo|repository|code|codebase|project|worktree|working directory|folder|directory|branch|file|files|app|theme|tui|ui|test|tests|fix|implement|update|change|edit|style)\b`)
	validationCmdPattern     = regexp.MustCompile(`(?i)\b(go test|go build|go vet|pytest|cargo test|cargo build|cargo check|npm test|npm run build|pnpm test|pnpm build|yarn test|yarn build|bun test|bun run build|golangci-lint)\b`)
	actionablePlanPattern    = regexp.MustCompile(`(?m)^\s*(?:[-*]|\d+\.)\s+\S+`)
	reviewFindingPattern     = regexp.MustCompile(`(?im)^\s*(?:[-*]|\d+\.)\s+(?:\[[Pp]\d\]\s+)?finding:`)
)

type turnEvidence struct {
	toolCalls  []llm.NativeToolCall
	hasRead    bool
	hasWrite   bool
	hasCheck   bool
	hasToolErr bool
}

type repoEvidenceAnchor struct {
	canonical string
	variants  []string
}

func enforceCompletionEvidence(snapshot reactruntime.SessionSnapshot, finalText string) error {
	input := strings.TrimSpace(snapshot.LastInput)
	finalText = strings.TrimSpace(finalText)
	if input == "" || finalText == "" || strings.HasPrefix(input, "/") || isPromptBoundaryQuestion(input) {
		return nil
	}

	start := currentTurnStartIndex(snapshot.History)
	evidence := collectEvidence(snapshot.History[start:])
	priorEvidence := collectEvidence(snapshot.History[:start])
	// Only treat the turn as repo-grounded based on the user's request.
	// Assistant phrasing like "I can read files" should not force tool use
	// for an otherwise casual greeting. Assistant-side repo claims are still
	// enforced by the inspection/change/validation checks below.
	repoGrounded := isRepoGroundedText(input)
	guardedTurn := shouldEnforceRepoGroundedCompletion(snapshot, evidence, priorEvidence)

	if repoGrounded && len(evidence.toolCalls) == 0 && !priorEvidence.hasAnyRepoEvidence() {
		return reactruntime.NewRetryableCompletionError(
			"non-compliant completion: repo-grounded turn finished without tool evidence",
			"You have not inspected the repository yet. Use repo tools first, then answer with concrete evidence instead of narrating intent.",
		)
	}
	if intentNarrationPattern.MatchString(finalText) && len(evidence.toolCalls) == 0 && guardedTurn {
		return reactruntime.NewRetryableCompletionError(
			"non-compliant completion: intent narration without action",
			buildIntentNarrationRetryPrompt(snapshot, evidence, priorEvidence),
		)
	}
	if combined := combineEvidence(priorEvidence, evidence); combined.hasAnyRepoEvidence() && guardedTurn &&
		(repoInspectionFailurePattern.MatchString(finalText) || noEvidenceClaimPattern.MatchString(finalText)) {
		return reactruntime.NewRetryableCompletionError(
			"non-compliant completion: contradicted gathered repo evidence",
			buildEvidenceAlreadyVisiblePrompt(snapshot.History, combined.toolCalls),
		)
	}
	if toolBlockedClaimPattern.MatchString(finalText) && guardedTurn && !evidence.hasToolErr && !currentPlanIsBlocked(snapshot.PlanState) {
		return reactruntime.NewRetryableCompletionError(
			"non-compliant completion: claimed tool blockage without a real tool error",
			"Do not claim tooling failure unless a tool actually failed in this turn. Continue with tools or report the real tool error.",
		)
	}
	if inspectionClaimPattern.MatchString(finalText) && !evidence.hasRead && !priorEvidence.hasRead {
		return reactruntime.NewRetryableCompletionError(
			"non-compliant completion: claimed inspection without repo reads",
			"Your answer claims repository inspection, but no repo read/search tool was used. Inspect the repo with tools before answering.",
		)
	}
	if changeClaimPattern.MatchString(finalText) && !evidence.hasWrite && !priorEvidence.hasWrite {
		return reactruntime.NewRetryableCompletionError(
			"non-compliant completion: claimed code changes without edits",
			"Your answer claims code changes, but no edit tool was used. Make the change first, then answer with the actual result.",
		)
	}
	if validationClaimPattern.MatchString(finalText) && !evidence.hasCheck && !priorEvidence.hasCheck {
		return reactruntime.NewRetryableCompletionError(
			"non-compliant completion: claimed validation without verification evidence",
			"Your answer claims verification, but no validation command ran. Run the relevant tests or checks first, then answer with the result.",
		)
	}
	if requiresPreviewVerification(snapshot) && !hasPreviewVerificationEvidence(snapshot.History) {
		return reactruntime.NewRetryableCompletionError(
			"non-compliant completion: preview mode finished without verified preview evidence",
			buildPreviewVerificationRetryPrompt(snapshot, evidence, priorEvidence),
		)
	}
	if anchors, required := requiredRepoAnchorEvidence(snapshot, snapshot.History, repoGrounded, combineEvidence(priorEvidence, evidence)); required > 0 {
		matched, missing := partitionRepoEvidenceAnchors(finalText, anchors)
		if len(matched) < required {
			return reactruntime.NewRetryableCompletionError(
				"non-compliant completion: answer omitted concrete repo anchors",
				buildRepoAnchorRetryPrompt(snapshot, required, matched, missing),
			)
		}
	}
	if requiresBriefRepoOverview(snapshot) && !looksLikeBriefRepoOverview(finalText) {
		return reactruntime.NewRetryableCompletionError(
			"non-compliant completion: repo overview answer was too exhaustive",
			buildBriefRepoOverviewRetryPrompt(snapshot.History, combineEvidence(priorEvidence, evidence).toolCalls),
		)
	}
	if requiresActionablePlan(snapshot) && !hasActionablePlan(snapshot, evidence, priorEvidence, finalText) {
		return reactruntime.NewRetryableCompletionError(
			"non-compliant completion: plan mode finished without an actionable plan",
			"This turn is in plan mode. Stop summarizing findings and provide an actionable plan now. Use update_plan or return a concrete step list with clear next actions.",
		)
	}
	if requiresReviewFindings(snapshot) && !looksLikeReviewFindings(finalText) {
		return reactruntime.NewRetryableCompletionError(
			"non-compliant completion: review mode finished without findings",
			"This turn is in review mode. Report findings first, ordered by severity when possible, and keep the summary secondary to the actual review issues.",
		)
	}
	if claimsCompletionWhilePlanStillActive(snapshot, finalText) {
		return reactruntime.NewRetryableCompletionError(
			"non-compliant completion: claimed completion while plan still has active work",
			"The current plan still has active work. Finish the remaining in_progress step, explicitly report the blocker, or update the plan state before claiming completion.",
		)
	}
	if requiresValidationEvidence(snapshot) && !evidence.hasCheck && !priorEvidence.hasCheck {
		return reactruntime.NewRetryableCompletionError(
			"non-compliant completion: validate mode finished without verification evidence",
			"This turn is in validate mode. Run the relevant tests or checks first, then answer with the concrete validation result.",
		)
	}
	return nil
}

func shouldEnforceRepoGroundedCompletion(snapshot reactruntime.SessionSnapshot, evidence, priorEvidence turnEvidence) bool {
	if snapshot.TaskState != nil {
		return true
	}
	switch snapshot.Mode {
	case reactruntime.ModeInspect, reactruntime.ModePlan, reactruntime.ModeImplement, reactruntime.ModeValidate, reactruntime.ModeReview, reactruntime.ModePreview:
		return true
	}
	if len(evidence.toolCalls) > 0 {
		return true
	}
	input := normalizedIntentText(snapshot.LastInput)
	if input == "" {
		return false
	}
	if isRepoGroundedText(input) {
		return true
	}
	return priorEvidence.hasAnyRepoEvidence() && looksLikeRepoFollowUp(input)
}

func requiresValidationEvidence(snapshot reactruntime.SessionSnapshot) bool {
	if snapshot.Mode == reactruntime.ModeValidate {
		return true
	}
	if snapshot.TaskState == nil {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(snapshot.TaskState.Operation), "validate")
}

func requiresActionablePlan(snapshot reactruntime.SessionSnapshot) bool {
	if snapshot.Mode == reactruntime.ModePlan {
		return true
	}
	if snapshot.TaskState == nil {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(snapshot.TaskState.Operation), "plan")
}

func requiresReviewFindings(snapshot reactruntime.SessionSnapshot) bool {
	if snapshot.Mode == reactruntime.ModeReview {
		return true
	}
	if snapshot.TaskState == nil {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(snapshot.TaskState.Operation), "review")
}

func requiresPreviewVerification(snapshot reactruntime.SessionSnapshot) bool {
	if snapshot.Mode == reactruntime.ModePreview {
		return true
	}
	if snapshot.TaskState == nil {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(snapshot.TaskState.Operation), "preview")
}

func hasActionablePlan(snapshot reactruntime.SessionSnapshot, evidence, priorEvidence turnEvidence, finalText string) bool {
	if snapshot.PlanState != nil && len(snapshot.PlanState.Steps) > 0 {
		return true
	}
	if hasToolCallNamed(evidence.toolCalls, "update_plan") || hasToolCallNamed(priorEvidence.toolCalls, "update_plan") {
		return true
	}
	return looksLikeActionablePlan(finalText)
}

func hasToolCallNamed(calls []llm.NativeToolCall, name string) bool {
	for _, call := range calls {
		if strings.EqualFold(strings.TrimSpace(call.Name), name) {
			return true
		}
	}
	return false
}

func looksLikeActionablePlan(finalText string) bool {
	return len(actionablePlanPattern.FindAllString(finalText, -1)) >= 2
}

func looksLikeReviewFindings(finalText string) bool {
	// Accept any structured list (≥2 bullets/items) as findings — "Finding:" label is not required.
	if len(actionablePlanPattern.FindAllString(finalText, -1)) >= 2 {
		return true
	}
	// Also accept explicit "Finding:" labels for legacy compatibility.
	return len(reviewFindingPattern.FindAllString(finalText, -1)) >= 1
}

func claimsCompletionWhilePlanStillActive(snapshot reactruntime.SessionSnapshot, finalText string) bool {
	if snapshot.Mode != reactruntime.ModeImplement {
		return false
	}
	if !completionClaimPattern.MatchString(finalText) || currentPlanIsBlocked(snapshot.PlanState) {
		return false
	}
	return currentPlanHasActiveWork(snapshot.PlanState)
}

func currentPlanHasActiveWork(state *reactruntime.PlanState) bool {
	if state == nil {
		return false
	}
	return state.HasActiveStep()
}

func currentPlanIsBlocked(state *reactruntime.PlanState) bool {
	if state == nil {
		return false
	}
	_, ok := state.BlockedStep()
	return ok
}

// currentTurnStartIndex returns the index after the last real user message.
// Budget-warning injections (prefixed with "[budget]") are not treated as
// turn boundaries so they don't orphan prior tool-call evidence.
func currentTurnStartIndex(history []llm.Message) int {
	for i := len(history) - 1; i >= 0; i-- {
		if history[i].Role == llm.RoleUser && !strings.HasPrefix(strings.TrimSpace(history[i].Content), "[budget]") {
			return i + 1
		}
	}
	return 0
}

func collectEvidence(history []llm.Message) turnEvidence {
	var ev turnEvidence
	for _, msg := range history {
		switch msg.Role {
		case llm.RoleAssistant:
			for _, call := range msg.ToolCalls {
				ev.toolCalls = append(ev.toolCalls, call)
				updateEvidenceForToolCall(&ev, call)
			}
		case llm.RoleTool:
			content := strings.ToLower(strings.TrimSpace(msg.Content))
			if strings.HasPrefix(content, "error:") || strings.HasPrefix(content, "blocked:") {
				ev.hasToolErr = true
			}
		}
	}
	return ev
}

func (ev turnEvidence) hasAnyRepoEvidence() bool {
	return ev.hasRead || ev.hasWrite || ev.hasCheck || ev.hasToolErr || len(ev.toolCalls) > 0
}

func updateEvidenceForToolCall(ev *turnEvidence, call llm.NativeToolCall) {
	name := strings.TrimSpace(call.Name)
	switch name {
	case "read_file", "search", "code_search", "list_dir", "glob", "git_status", "git_diff", "git_log", "git_branch_state", "git_merge_status", "artifact_read", "preview_server_status", "lsp_definition", "lsp_references", "lsp_hover", "lsp_document_symbols":
		ev.hasRead = true
	case "edit_file", "write_file", "apply_patch", "artifact_write":
		ev.hasWrite = true
	}
	if name == "preview_server_ensure" || name == "preview_server_status" {
		ev.hasCheck = true
	}
	if name == "run_command" {
		var args struct {
			Command string `json:"command"`
		}
		if err := json.Unmarshal([]byte(call.ArgsJSON), &args); err == nil && validationCmdPattern.MatchString(args.Command) {
			ev.hasCheck = true
		}
	}
}

func validateGeneralTaskCompletion(snapshot reactruntime.SessionSnapshot, finalText string) error {
	if err := enforceCompletionEvidence(snapshot, finalText); err != nil {
		return err
	}
	return nil
}

func buildIntentNarrationRetryPrompt(snapshot reactruntime.SessionSnapshot, evidence, priorEvidence turnEvidence) string {
	combined := combineEvidence(priorEvidence, evidence)
	summary := summarizeToolCalls(combined.toolCalls)
	if summary == "" {
		return "Use repo tools now. Your next assistant message must include one or more tool calls. A single brief sentence before the tool calls is fine, but the same message must include the tool calls. After tool results arrive, answer from that evidence."
	}
	if requiresBriefRepoOverview(snapshot) {
		return fmt.Sprintf(
			"You already gathered repo evidence in this session: %s. Do not narrate next steps. Answer directly from that evidence with one short paragraph or 2-4 concrete bullets, cite the main inspected paths, and stop there unless the user asks for more detail.",
			summary,
		)
	}
	return fmt.Sprintf(
		"You already gathered repo evidence in this session: %s. Do not narrate next steps. Answer directly from that evidence in 3-6 concrete bullets or a short paragraph, cite the files, paths, or symbols you inspected, and only mention verification if you actually ran it.",
		summary,
	)
}

func buildPreviewVerificationRetryPrompt(snapshot reactruntime.SessionSnapshot, evidence, priorEvidence turnEvidence) string {
	combined := combineEvidence(priorEvidence, evidence)
	switch {
	case hasPreviewWriteEvidence(snapshot.History):
		return "This turn is in preview mode and the preview content is already written. Call preview_server_ensure now, then answer with the verified preview URL."
	case combined.hasRead:
		return fmt.Sprintf(
			"This turn is in preview mode and you already gathered enough context: %s. Stop researching, create or update the preview content, call preview_server_ensure, and answer with the verified preview URL.",
			summarizeToolCalls(combined.toolCalls),
		)
	default:
		return "This turn is in preview mode. Create or update the preview content, call preview_server_ensure, and answer with the verified preview URL."
	}
}

func combineEvidence(a, b turnEvidence) turnEvidence {
	combined := turnEvidence{
		toolCalls:  append([]llm.NativeToolCall{}, a.toolCalls...),
		hasRead:    a.hasRead || b.hasRead,
		hasWrite:   a.hasWrite || b.hasWrite,
		hasCheck:   a.hasCheck || b.hasCheck,
		hasToolErr: a.hasToolErr || b.hasToolErr,
	}
	combined.toolCalls = append(combined.toolCalls, b.toolCalls...)
	return combined
}

func summarizeToolCalls(calls []llm.NativeToolCall) string {
	if len(calls) == 0 {
		return ""
	}
	summaries := make([]string, 0, 4)
	seen := make(map[string]struct{}, len(calls))
	for _, call := range calls {
		entry := summarizeToolCall(call)
		if entry == "" {
			continue
		}
		if _, ok := seen[entry]; ok {
			continue
		}
		seen[entry] = struct{}{}
		summaries = append(summaries, entry)
		if len(summaries) == 4 {
			break
		}
	}
	return strings.Join(summaries, ", ")
}

func summarizeToolCall(call llm.NativeToolCall) string {
	name := strings.TrimSpace(call.Name)
	if name == "" {
		return ""
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(call.ArgsJSON), &payload); err != nil {
		return name
	}
	for _, key := range []string{"path", "query", "command"} {
		if raw, ok := payload[key]; ok {
			if value := strings.TrimSpace(fmt.Sprint(raw)); value != "" {
				return fmt.Sprintf("%s(%s)", name, value)
			}
		}
	}
	return name
}

func requiredRepoAnchorEvidence(snapshot reactruntime.SessionSnapshot, history []llm.Message, repoGrounded bool, evidence turnEvidence) ([]repoEvidenceAnchor, int) {
	if (!repoGrounded && !requiresBriefRepoOverview(snapshot)) || !evidence.hasRead || !requiresConcreteInspectAnchors(snapshot) {
		return nil, 0
	}
	anchors := collectRepoEvidenceAnchors(history)
	if len(anchors) == 0 {
		return nil, 0
	}
	required := 1
	if len(anchors) >= 2 {
		required = 2
	}
	return anchors, required
}

func requiresConcreteInspectAnchors(snapshot reactruntime.SessionSnapshot) bool {
	if snapshot.Mode == reactruntime.ModeInspect || snapshot.Mode == reactruntime.ModeReview {
		return true
	}
	if snapshot.TaskState == nil {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(snapshot.TaskState.Operation)) {
	case "overview", "inspect", "review", "analysis":
		return true
	default:
		return false
	}
}

func requiresBriefRepoOverview(snapshot reactruntime.SessionSnapshot) bool {
	if snapshot.TaskState != nil && strings.EqualFold(strings.TrimSpace(snapshot.TaskState.Operation), "overview") {
		return true
	}
	return isWorkspaceOverviewRequest(snapshot.LastInput)
}

func looksLikeBriefRepoOverview(finalText string) bool {
	if strings.TrimSpace(finalText) == "" {
		return false
	}
	if len(actionablePlanPattern.FindAllString(finalText, -1)) > 5 {
		return false
	}
	if len(finalText) > 1400 {
		return false
	}
	nonEmptyLines := 0
	for _, line := range strings.Split(finalText, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		nonEmptyLines++
	}
	return nonEmptyLines <= 9
}

func hasPreviewVerificationEvidence(history []llm.Message) bool {
	callByID := make(map[string]struct{})
	for _, msg := range history {
		switch msg.Role {
		case llm.RoleAssistant:
			for _, call := range msg.ToolCalls {
				switch strings.TrimSpace(call.Name) {
				case "preview_server_ensure", "preview_server_status":
					callByID[call.ID] = struct{}{}
				}
			}
		case llm.RoleTool:
			if _, ok := callByID[msg.ToolCallID]; !ok {
				continue
			}
			if previewToolResultLooksVerified(msg.Content) {
				return true
			}
		}
	}
	return false
}

func previewToolResultLooksVerified(result string) bool {
	lower := strings.ToLower(strings.TrimSpace(result))
	if lower == "" {
		return false
	}
	if !strings.Contains(lower, "http://127.0.0.1:") && !strings.Contains(lower, `"url"`) {
		return false
	}
	return strings.Contains(lower, `"status":"live"`) || strings.Contains(lower, `"status":"running"`)
}

func hasPreviewWriteEvidence(history []llm.Message) bool {
	for _, msg := range history {
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

func collectRepoEvidenceAnchors(history []llm.Message) []repoEvidenceAnchor {
	callByID := make(map[string]llm.NativeToolCall)
	var anchors []repoEvidenceAnchor
	seen := map[string]struct{}{}
	addAnchor := func(canonical string, variants ...string) {
		canonical = strings.TrimSpace(canonical)
		if canonical == "" || canonical == "." || canonical == "/" {
			return
		}
		if _, ok := seen[canonical]; ok {
			return
		}
		normalized := normalizeAnchorVariants(append([]string{canonical}, variants...))
		if len(normalized) == 0 {
			return
		}
		seen[canonical] = struct{}{}
		anchors = append(anchors, repoEvidenceAnchor{canonical: canonical, variants: normalized})
	}

	for _, msg := range history {
		switch msg.Role {
		case llm.RoleAssistant:
			for _, call := range msg.ToolCalls {
				callByID[call.ID] = call
				addAnchorsFromToolCall(call, addAnchor)
			}
		case llm.RoleTool:
			call, ok := callByID[msg.ToolCallID]
			if !ok {
				continue
			}
			if strings.EqualFold(strings.TrimSpace(call.Name), "list_dir") {
				addAnchorsFromListDirResult(msg.Content, addAnchor)
			}
		}
	}
	return anchors
}

func addAnchorsFromToolCall(call llm.NativeToolCall, add func(string, ...string)) {
	var payload map[string]any
	if err := json.Unmarshal([]byte(call.ArgsJSON), &payload); err != nil {
		return
	}
	switch strings.TrimSpace(call.Name) {
	case "read_file", "edit_file", "write_file", "artifact_read", "artifact_write", "glob":
		addPathAnchor(fmt.Sprint(payload["path"]), add)
	case "lsp_definition", "lsp_references", "lsp_hover", "lsp_document_symbols":
		addPathAnchor(fmt.Sprint(payload["path"]), add)
	}
}

func addAnchorsFromListDirResult(result string, add func(string, ...string)) {
	var candidates []string
	seen := map[string]struct{}{}
	for _, line := range strings.Split(result, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "...") || strings.HasPrefix(strings.ToLower(line), "error:") || strings.HasPrefix(strings.ToLower(line), "blocked:") {
			continue
		}
		lower := strings.ToLower(line)
		if _, ok := seen[lower]; ok {
			continue
		}
		seen[lower] = struct{}{}
		candidates = append(candidates, line)
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		return repoAnchorPriority(candidates[i]) > repoAnchorPriority(candidates[j])
	})
	for idx, candidate := range candidates {
		if idx >= 8 {
			return
		}
		addPathAnchor(candidate, add)
	}
}

func repoAnchorPriority(raw string) int {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return -100
	}
	trimmed := strings.TrimSuffix(raw, "/")
	base := strings.ToLower(path.Base(trimmed))
	score := 50

	if strings.HasPrefix(raw, ".") || strings.HasPrefix(base, ".") {
		score -= 40
	}

	switch base {
	case "readme", "readme.md":
		score += 100
	case "go.mod", "package.json", "pyproject.toml", "cargo.toml", "pom.xml", "build.gradle", "build.gradle.kts", "composer.json", "gemfile", "justfile":
		score += 90
	case "cmd", "internal", "src", "pkg", "lib", "app", "server", "client":
		score += 80
	case "docs", "test", "tests":
		score += 75
	case "architecture.md", "build.md", "contributing.md", "local_tooling.md", "makefile":
		score += 70
	case "go.sum":
		score += 55
	}

	if strings.HasSuffix(raw, "/") {
		score += 5
	}

	return score
}

func addPathAnchor(raw string, add func(string, ...string)) {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "." || raw == "/" || raw == "<nil>" {
		return
	}
	trimmed := strings.TrimSuffix(raw, "/")
	base := path.Base(trimmed)
	var variants []string
	if raw != trimmed {
		variants = append(variants, raw)
		if includeBareDirectoryVariant(base) {
			variants = append(variants, trimmed)
		}
		add(raw, variants...)
		return
	}
	if trimmed != "" {
		variants = append(variants, trimmed)
	}
	if base != "" && base != "." && base != trimmed {
		variants = append(variants, base)
	}
	ext := path.Ext(base)
	if ext != "" {
		stem := strings.TrimSuffix(base, ext)
		if stem != "" && stem != base && allowStemVariant(base, stem) {
			variants = append(variants, stem)
		}
	}
	add(raw, variants...)
}

func includeBareDirectoryVariant(base string) bool {
	switch strings.ToLower(strings.TrimSpace(base)) {
	case "app", "bin", "build", "cmd", "doc", "docs", "lib", "pkg", "src", "test", "tests":
		return false
	default:
		return true
	}
}

func allowStemVariant(base, stem string) bool {
	base = strings.ToLower(strings.TrimSpace(base))
	stem = strings.ToLower(strings.TrimSpace(stem))
	return base == "readme.md" && stem == "readme"
}

func normalizeAnchorVariants(values []string) []string {
	seen := map[string]struct{}{}
	var variants []string
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || value == "." || value == "/" {
			continue
		}
		lower := strings.ToLower(value)
		if _, ok := seen[lower]; ok {
			continue
		}
		seen[lower] = struct{}{}
		variants = append(variants, lower)
	}
	return variants
}

func partitionRepoEvidenceAnchors(finalText string, anchors []repoEvidenceAnchor) ([]repoEvidenceAnchor, []repoEvidenceAnchor) {
	lower := strings.ToLower(finalText)
	var matched []repoEvidenceAnchor
	var missing []repoEvidenceAnchor
	for _, anchor := range anchors {
		found := false
		for _, variant := range anchor.variants {
			if variant != "" && strings.Contains(lower, variant) {
				found = true
				break
			}
		}
		if found {
			matched = append(matched, anchor)
			continue
		}
		missing = append(missing, anchor)
	}
	return matched, missing
}

func buildRepoAnchorRetryPrompt(snapshot reactruntime.SessionSnapshot, required int, matched, missing []repoEvidenceAnchor) string {
	styleInstruction := "Summarize only what those specific paths show."
	if requiresBriefRepoOverview(snapshot) {
		styleInstruction = "Keep the overview brief: use 2-4 concrete bullets or a short paragraph, and summarize only what those specific paths show."
	}
	if len(matched) == 0 {
		return fmt.Sprintf(
			"Your answer is too generic for the repo evidence you already gathered. Cite at least %d concrete inspected file or path references such as %s. If a path was only seen in list_dir(.), it is valid to say it is present at the repo root; do not invent file contents. \".\" or \"repo root\" do not count as concrete repo anchors. %s",
			required,
			summarizeRepoEvidenceAnchors(missing, required+2),
			styleInstruction,
		)
	}
	needed := required - len(matched)
	if needed < 1 {
		needed = 1
	}
	return fmt.Sprintf(
		"Your last answer only cited %s. \".\" or \"repo root\" do not count as concrete repo anchors. Cite at least %d additional inspected file or path references such as %s. If a path was only seen in list_dir(.), it is valid to say it is present at the repo root; do not invent file contents. %s",
		summarizeRepoEvidenceAnchors(matched, len(matched)),
		needed,
		summarizeRepoEvidenceAnchors(missing, needed+2),
		styleInstruction,
	)
}

func buildBriefRepoOverviewRetryPrompt(history []llm.Message, calls []llm.NativeToolCall) string {
	summary := summarizeToolCalls(calls)
	if summary == "" {
		summary = "the repo evidence you already gathered"
	}
	return fmt.Sprintf(
		"This is a casual repo-overview turn. You already gathered enough evidence: %s. Stop exploring and answer briefly: either one short paragraph or 2-4 concrete bullets. Anchor the overview to the main inspected paths and do not expand into a full architecture or tooling report unless the user asks.",
		summary,
	)
}

func buildEvidenceAlreadyVisiblePrompt(history []llm.Message, calls []llm.NativeToolCall) string {
	anchors := collectRepoEvidenceAnchors(history)
	anchorExamples := summarizeRepoEvidenceAnchors(anchors, 4)
	return fmt.Sprintf(
		"You already gathered repo evidence in this session: %s. The tool outputs are already visible in this conversation. Do not claim you could not inspect the repository, that the outputs are missing, or that you need them pasted again. Answer directly from the evidence you already have, citing concrete paths such as %s. If a path was only seen in list_dir(.), it is valid to say it is present at the repo root; do not invent file contents. \".\" or \"repo root\" do not count as concrete repo anchors.",
		summarizeToolCalls(calls),
		anchorExamples,
	)
}

func summarizeRepoEvidenceAnchors(anchors []repoEvidenceAnchor, limit int) string {
	if len(anchors) == 0 || limit <= 0 {
		return "the inspected repo paths"
	}
	var parts []string
	for _, anchor := range anchors {
		if anchor.canonical == "" {
			continue
		}
		parts = append(parts, anchor.canonical)
		if len(parts) >= limit {
			break
		}
	}
	if len(parts) == 0 {
		return "the inspected repo paths"
	}
	return strings.Join(parts, ", ")
}
