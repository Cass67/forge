package runtime

import (
	"encoding/json"
	"regexp"
	"strings"

	"forge/internal/llm"
	reactruntime "forge/internal/react"
)

var (
	intentNarrationPattern   = regexp.MustCompile(`(?i)\b(i['’]ll|let me|first[, ]+i['’]ll|then[, ]+i['’]ll|next[, ]+i['’]ll)\b`)
	blockedClaimPattern      = regexp.MustCompile(`(?i)\b(blocked|unable to access|unable to inspect|produced no output|tooling.*(?:failed|unavailable)|can't access the repo|cannot access the repo)\b`)
	inspectionClaimPattern   = regexp.MustCompile(`(?i)\b(inspect(?:ed)?|review(?:ed)?|read|searched|located|analy[sz]ed|looked at|checked)\b`)
	changeClaimPattern       = regexp.MustCompile(`(?i)\b(fixed|updated|changed|implemented|added|wired|patched|refactored|edited)\b`)
	validationClaimPattern   = regexp.MustCompile(`(?i)\b(tested|validated|checks pass|tests pass|build passes|lint passes|compiled|verified (?:it|the fix|the change|the changes|it works|they work|working))\b`)
	repoGroundedInputPattern = regexp.MustCompile(`(?i)\b(repo|repository|code|codebase|project|worktree|branch|file|files|app|theme|tui|ui|test|tests|fix|implement|update|change|edit|style)\b`)
	validationCmdPattern     = regexp.MustCompile(`(?i)\b(go test|pytest|npm test|pnpm test|yarn test|cargo test|go build|cargo check|npm run build|pnpm build|yarn build|golangci-lint|go vet)\b`)
)

type turnEvidence struct {
	toolCalls  []llm.NativeToolCall
	hasRead    bool
	hasWrite   bool
	hasCheck   bool
	hasToolErr bool
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
	repoGrounded := repoGroundedInputPattern.MatchString(input) || repoGroundedInputPattern.MatchString(finalText)

	if repoGrounded && len(evidence.toolCalls) == 0 && !priorEvidence.hasAnyRepoEvidence() {
		return reactruntime.NewRetryableCompletionError(
			"non-compliant completion: repo-grounded turn finished without tool evidence",
			"You have not inspected the repository yet. Use repo tools first, then answer with concrete evidence instead of narrating intent.",
		)
	}
	if intentNarrationPattern.MatchString(finalText) && len(evidence.toolCalls) == 0 {
		return reactruntime.NewRetryableCompletionError(
			"non-compliant completion: intent narration without action",
			"Do the work now. Use tools to inspect the repository and only answer after you have concrete evidence.",
		)
	}
	if blockedClaimPattern.MatchString(finalText) && !evidence.hasToolErr {
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
	return nil
}

func currentTurnStartIndex(history []llm.Message) int {
	for i := len(history) - 1; i >= 0; i-- {
		if history[i].Role == llm.RoleUser {
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
	if name == "git_diff" || name == "git_status" {
		ev.hasCheck = true
	}
}

func validateGeneralTaskCompletion(snapshot reactruntime.SessionSnapshot, finalText string) error {
	if err := enforceCompletionEvidence(snapshot, finalText); err != nil {
		return err
	}
	return nil
}
