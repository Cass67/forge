package harness

import (
	"fmt"
	"path/filepath"
	"strings"
)

const maxStrictActionRetries = 1

func normalizeObservation(step Step, class Classification, session SessionState, obs Observation) Observation {
	obs.Response = strings.TrimSpace(obs.Response)
	obs.Summary = strings.TrimSpace(obs.Summary)
	if obs.Lane == "" {
		obs.Lane = step.Lane
	}
	if obs.Outcome.Lane == "" {
		obs.Outcome.Lane = obs.Lane
	}
	if obs.Outcome.DeliverableKind == "" {
		obs.Outcome.DeliverableKind = resolveExpectedDeliverable(class, session, obs.Lane)
	}
	if len(obs.Progress) == 0 {
		obs.Progress = deriveProgressMilestones(obs)
	}

	if obs.Status == ObservationBlocked {
		if obs.Outcome.Kind == OutcomeNone {
			obs.Outcome.Kind = OutcomeBlocked
		}
		if obs.Outcome.DeliverableStatus == DeliverableUnknown {
			obs.Outcome.DeliverableStatus = DeliverableMissing
		}
		if obs.Outcome.Reason == "" {
			obs.Outcome.Reason = firstNonEmpty(obs.Summary, errorString(obs.Err), "observation blocked")
		}
		if obs.Summary == "" {
			obs.Summary = obs.Outcome.Reason
		}
		return obs
	}

	if outcome, ok := contractViolationOutcome(step, class, obs.Response, obs.Outcome.DeliverableKind); ok {
		obs.Status = ObservationBlocked
		obs.Response = ""
		obs.Summary = outcome.Reason
		obs.Outcome = outcome
		return obs
	}
	if outcome, ok := ungroundedSideEffectOutcome(step, class, obs); ok {
		obs.Status = ObservationBlocked
		obs.Response = ""
		obs.Summary = outcome.Reason
		obs.Outcome = outcome
		return obs
	}
	if outcome, ok := selectedDirectionMismatchOutcome(step, class, session, obs); ok {
		obs.Status = ObservationBlocked
		obs.Response = ""
		obs.Summary = outcome.Reason
		obs.Outcome = outcome
		return obs
	}

	status, reason := evaluateDeliverableStatus(step, class, session, obs)
	obs.Outcome.DeliverableStatus = status
	switch status {
	case DeliverableSatisfied:
		if shouldAwaitUserFeedback(step, obs.Outcome.DeliverableKind) {
			obs.Outcome.Kind = OutcomeAwaitingFeedback
			obs.Outcome.Reason = firstNonEmpty(reason, "preview deliverable satisfied; awaiting user feedback")
		} else {
			obs.Outcome.Kind = OutcomeComplete
			obs.Outcome.Reason = firstNonEmpty(reason, "deliverable satisfied")
		}
	case DeliverableNotRequired:
		obs.Outcome.Kind = OutcomeComplete
		obs.Outcome.Reason = firstNonEmpty(reason, "deliverable not required")
	default:
		obs.Status = ObservationBlocked
		obs.Response = ""
		if step.Lane == LaneStrictAction {
			obs.Outcome.Kind = OutcomeRetry
		} else {
			obs.Outcome.Kind = OutcomeBlocked
		}
		obs.Outcome.Reason = firstNonEmpty(reason, "deliverable missing")
		obs.Summary = obs.Outcome.Reason
	}
	return obs
}

func resolveExpectedDeliverable(class Classification, session SessionState, lane ExecutionLane) DeliverableKind {
	if class.ThreadIntent == TurnIntentCancelThread {
		return DeliverableAnswerOnly
	}
	if class.ThreadIntent == TurnIntentMetaQuestion {
		return DeliverableAnswerOnly
	}
	if session.HasActiveThread() &&
		session.ActiveThread().Deliverable != "" &&
		(class.ThreadIntent == TurnIntentContinueThread ||
			class.ThreadIntent == TurnIntentReplayThread ||
			class.ThreadIntent == TurnIntentRepairThread) {
		return session.ActiveThread().Deliverable
	}
	if lane == LaneStrictAction && previewDeliverableRequested(class, session) {
		return DeliverablePreviewAvailableAndRenderable
	}
	switch class.Family {
	case FamilyInspect:
		return DeliverableEvidenceBackedExplanation
	case FamilyImplement, FamilyDebug, FamilyVerify:
		return DeliverableWorkspaceChangeWithVerification
	case FamilyResearch:
		return DeliverableResearchSummaryWithSources
	default:
		return DeliverableAnswerOnly
	}
}

func previewDeliverableRequested(class Classification, session SessionState) bool {
	if class.ThreadIntent == TurnIntentReplayThread {
		return true
	}
	if class.ThreadIntent != TurnIntentSupersedeThread &&
		session.HasActiveThread() &&
		session.ActiveThread().Kind == ThreadPreviewCollaboration {
		return true
	}
	if class.ThreadIntent != TurnIntentSupersedeThread &&
		session.HasRecentPreview() &&
		class.PrefersVisibleExecution &&
		class.IsFollowUp {
		return true
	}
	if !class.PrefersVisibleExecution {
		return false
	}

	lower := strings.ToLower(firstNonEmpty(class.TaskText, class.TopicKey))
	for _, marker := range []string{
		"preview",
		"mockup",
		"mockups",
		"web page",
		"webpage",
		"browser",
		"localhost",
		"server",
		"url",
	} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func contractViolationOutcome(step Step, class Classification, response string, deliverable DeliverableKind) (ActionOutcome, bool) {
	if step.Lane == LaneWorkerSidecar {
		return ActionOutcome{}, false
	}
	if requiresConcreteResponse(step, class) && response == "" {
		return ActionOutcome{
			Lane:              step.Lane,
			Kind:              outcomeKindForContractViolation(step.Lane),
			DeliverableKind:   deliverable,
			DeliverableStatus: DeliverableMissing,
			Reason:            contractViolationReason(step.Lane, "no final response"),
		}, true
	}
	if containsToolCallMarkup(response) {
		return ActionOutcome{
			Lane:              step.Lane,
			Kind:              outcomeKindForContractViolation(step.Lane),
			DeliverableKind:   deliverable,
			DeliverableStatus: DeliverableMissing,
			Reason:            contractViolationReason(step.Lane, "malformed tool markup"),
		}, true
	}
	return ActionOutcome{}, false
}

func ungroundedSideEffectOutcome(step Step, _ Classification, obs Observation) (ActionOutcome, bool) {
	claimText := observedClaimText(obs)
	if claimText == "" {
		return ActionOutcome{}, false
	}
	if claimsBranchSideEffect(claimText) && !hasBranchWorkflowEvidence(obs.ToolCalls) {
		return claimEvidenceViolationOutcome(step, obs.Outcome.DeliverableKind, "response claimed branch creation or switching without supporting tool evidence"), true
	}
	if claimsCommitSideEffect(claimText) && !hasCommitEvidence(obs.ToolCalls) {
		return claimEvidenceViolationOutcome(step, obs.Outcome.DeliverableKind, "response claimed a commit without git_commit evidence"), true
	}
	if claimsPreviewLiveSideEffect(claimText) && !previewVerified(obs.Runtime.Preview) {
		return claimEvidenceViolationOutcome(step, obs.Outcome.DeliverableKind, "response claimed a live preview without verified preview runtime evidence"), true
	}
	return ActionOutcome{}, false
}

func observedClaimText(obs Observation) string {
	text := strings.TrimSpace(obs.Response)
	if summary := strings.TrimSpace(obs.Summary); summary != "" {
		if text == "" {
			text = summary
		} else {
			text += "\n" + summary
		}
	}
	return strings.ToLower(strings.TrimSpace(text))
}

func claimEvidenceViolationOutcome(step Step, deliverable DeliverableKind, reason string) ActionOutcome {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "response included an ungrounded side-effect claim"
	}
	return ActionOutcome{
		Lane:              step.Lane,
		Kind:              outcomeKindForContractViolation(step.Lane),
		DeliverableKind:   deliverable,
		DeliverableStatus: DeliverableMissing,
		Reason:            reason,
	}
}

func claimsBranchSideEffect(lower string) bool {
	if lower == "" {
		return false
	}
	for _, phrase := range []string{
		"branch created",
		"created branch",
		"created a branch",
		"created and switched",
		"switched to branch",
		"switched to a branch",
		"switched branches",
		"checked out branch",
		"checked out to",
	} {
		if strings.Contains(lower, phrase) {
			return true
		}
	}
	return false
}

func hasBranchWorkflowEvidence(calls []ObservedToolCall) bool {
	for _, call := range calls {
		if strings.TrimSpace(call.Name) != "run_command" {
			continue
		}
		cmd := toolCallCommand(call.Args)
		if cmd == "" {
			continue
		}
		if isGitBranchWorkflowCommand(strings.ToLower(cmd)) {
			return true
		}
	}
	return false
}

func claimsCommitSideEffect(lower string) bool {
	if lower == "" {
		return false
	}
	for _, phrase := range []string{
		"commit created",
		"created commit",
		"created a commit",
		"changes committed",
		"committed the changes",
		"i committed",
		"we committed",
	} {
		if strings.Contains(lower, phrase) {
			return true
		}
	}
	return false
}

func hasCommitEvidence(calls []ObservedToolCall) bool {
	for _, call := range calls {
		if strings.TrimSpace(call.Name) == "git_commit" {
			return true
		}
	}
	return false
}

func claimsPreviewLiveSideEffect(lower string) bool {
	if lower == "" {
		return false
	}
	if strings.Contains(lower, "preview is live") ||
		strings.Contains(lower, "preview is running") ||
		strings.Contains(lower, "preview available at") ||
		strings.Contains(lower, "verified preview") {
		return true
	}
	if strings.Contains(lower, "http://127.0.0.1") &&
		(strings.Contains(lower, "preview") || strings.Contains(lower, "localhost")) &&
		(strings.Contains(lower, "live") || strings.Contains(lower, "running") || strings.Contains(lower, "available")) {
		return true
	}
	return false
}

func toolCallCommand(args map[string]any) string {
	if len(args) == 0 {
		return ""
	}
	for _, key := range []string{"command", "cmd"} {
		if value, ok := args[key]; ok {
			if cmd, ok := value.(string); ok {
				return strings.TrimSpace(cmd)
			}
		}
	}
	return ""
}

func isGitBranchWorkflowCommand(lower string) bool {
	if !strings.Contains(lower, "git") {
		return false
	}
	for _, marker := range []string{
		"git checkout",
		"git switch",
		"git branch",
		"git worktree add",
		"checkout -b",
		"switch -c",
	} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func selectedDirectionMismatchOutcome(step Step, class Classification, session SessionState, obs Observation) (ActionOutcome, bool) {
	if step.Lane != LaneStrictAction {
		return ActionOutcome{}, false
	}
	if !session.HasActiveThread() {
		return ActionOutcome{}, false
	}
	active := session.ActiveThread()
	if active.Kind != ThreadPreviewCollaboration || active.Phase != ThreadPhaseApply {
		return ActionOutcome{}, false
	}
	selected := strings.ToLower(strings.TrimSpace(active.SelectedDirection))
	if selected == "" {
		return ActionOutcome{}, false
	}
	switch class.ThreadIntent {
	case TurnIntentReplayThread, TurnIntentMetaQuestion, TurnIntentCancelThread:
		return ActionOutcome{}, false
	}
	if selectedDirectionMentioned(selected, obs) {
		return ActionOutcome{}, false
	}
	reason := fmt.Sprintf("selected direction mismatch: expected %q in apply result", active.SelectedDirection)
	return claimEvidenceViolationOutcome(step, obs.Outcome.DeliverableKind, reason), true
}

func selectedDirectionMentioned(selected string, obs Observation) bool {
	selected = strings.TrimSpace(strings.ToLower(selected))
	if selected == "" {
		return true
	}
	if strings.Contains(strings.ToLower(firstNonEmpty(obs.Response, obs.Summary)), selected) {
		return true
	}
	switch artifact := obs.Artifact.(type) {
	case EditorResult:
		for _, change := range artifact.Changes {
			if strings.Contains(strings.ToLower(change.Path), selected) ||
				strings.Contains(strings.ToLower(change.Summary), selected) {
				return true
			}
		}
	}
	return false
}

func requiresConcreteResponse(step Step, class Classification) bool {
	if step.Lane == LaneWorkerSidecar {
		return false
	}
	return step.Lane == LaneStrictAction || class.Family != FamilyAnswer
}

func outcomeKindForContractViolation(lane ExecutionLane) OutcomeKind {
	if lane == LaneStrictAction {
		return OutcomeRetry
	}
	return OutcomeBlocked
}

func contractViolationReason(lane ExecutionLane, detail string) string {
	prefix := "local action turn produced "
	if lane == LaneStrictAction {
		prefix = "strict action turn produced "
	}
	return prefix + strings.TrimSpace(detail)
}

func evaluateDeliverableStatus(step Step, class Classification, _ SessionState, obs Observation) (DeliverableStatus, string) {
	workerText := firstNonEmpty(obs.Response, obs.Summary)
	switch obs.Outcome.DeliverableKind {
	case DeliverablePreviewAvailableAndRenderable:
		if previewVerified(obs.Runtime.Preview) {
			return DeliverableSatisfied, "preview deliverable satisfied; verified preview available"
		}
		return DeliverableMissing, "strict action finished without a verified preview"
	case DeliverableEvidenceBackedExplanation:
		if workerText != "" {
			return DeliverableSatisfied, "evidence-backed explanation delivered"
		}
		if result, ok := obs.Artifact.(ReaderResult); ok && len(result.Evidence) > 0 {
			return DeliverableSatisfied, "evidence-backed explanation delivered"
		}
		return DeliverableMissing, "turn finished without an evidence-backed explanation"
	case DeliverableWorkspaceChangeWithVerification:
		if obs.Response != "" || workerText != "" || !obs.Runtime.Artifact.IsZero() {
			switch result := obs.Artifact.(type) {
			case EditorResult:
				if len(result.Changes) > 0 {
					return DeliverableSatisfied, "workspace deliverable satisfied"
				}
			case VerifierResult:
				if len(result.Checks) > 0 || len(result.Failures) > 0 {
					return DeliverableSatisfied, "workspace deliverable satisfied"
				}
			default:
				return DeliverableSatisfied, "workspace deliverable satisfied"
			}
		}
		if !obs.Runtime.Artifact.IsZero() {
			return DeliverableSatisfied, "workspace deliverable satisfied"
		}
		return DeliverableMissing, "turn finished without a concrete result"
	case DeliverableResearchSummaryWithSources:
		if result, ok := obs.Artifact.(ResearcherResult); ok {
			if workerText != "" && len(result.Sources) > 0 {
				return DeliverableSatisfied, "research deliverable satisfied"
			}
			return DeliverableMissing, "research turn finished without sources"
		}
		if workerText != "" && (step.Lane != LaneWorkerSidecar || class.NeedsExternalSources) {
			return DeliverableSatisfied, "research deliverable satisfied"
		}
		return DeliverableMissing, "research turn finished without sources"
	case DeliverableAnswerOnly:
		if workerText != "" {
			return DeliverableSatisfied, "answer delivered"
		}
		return DeliverableMissing, "answer turn produced no final response"
	default:
		if workerText != "" {
			return DeliverableNotRequired, "response available"
		}
		return DeliverableMissing, "deliverable missing"
	}
}

func previewVerified(preview PreviewSnapshot) bool {
	return strings.TrimSpace(preview.Status) != "" &&
		strings.TrimSpace(preview.Status) != "stopped" &&
		strings.TrimSpace(preview.URL) != ""
}

func shouldAwaitUserFeedback(step Step, deliverable DeliverableKind) bool {
	return step.Lane == LaneStrictAction && deliverable == DeliverablePreviewAvailableAndRenderable
}

func deriveProgressMilestones(obs Observation) []ProgressMilestone {
	milestones := make([]ProgressMilestone, 0, 3)
	if names := appliedSkillNames(obs.SkillUses); len(names) > 0 {
		milestones = append(milestones, ProgressMilestone{
			Kind:    ProgressMilestoneSkill,
			Message: "Using " + humanJoin(names),
		})
	}
	if !obs.Runtime.Artifact.IsZero() {
		label := filepath.Base(firstNonEmpty(obs.Runtime.Artifact.Path, obs.Runtime.Artifact.Handle))
		if label != "" {
			milestones = append(milestones, ProgressMilestone{
				Kind:    ProgressMilestoneChange,
				Message: fmt.Sprintf("Prepared %s", label),
			})
		}
	}
	if previewVerified(obs.Runtime.Preview) {
		milestones = append(milestones, ProgressMilestone{
			Kind:    ProgressMilestonePreview,
			Message: "Verified preview at " + obs.Runtime.Preview.URL,
		})
	}
	return milestones
}

func exhaustObservationRetry(obs Observation) Observation {
	if obs.Outcome.Kind != OutcomeRetry {
		return obs
	}
	obs.Status = ObservationBlocked
	obs.Outcome.Kind = OutcomeBlocked
	reason := strings.TrimSpace(obs.Outcome.Reason)
	if reason == "" {
		reason = "strict action retry budget exhausted"
	} else if !strings.Contains(strings.ToLower(reason), "after retry") {
		reason += " after retry"
	}
	obs.Outcome.Reason = reason
	obs.Summary = reason
	obs.Response = ""
	return obs
}
