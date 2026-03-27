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
	if session.HasActiveThread() &&
		session.ActiveThread().Deliverable != "" &&
		(class.ThreadIntent == TurnIntentContinueThread ||
			class.ThreadIntent == TurnIntentReplayThread ||
			class.ThreadIntent == TurnIntentRepairThread ||
			class.ThreadIntent == TurnIntentSupersedeThread) {
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
	if session.HasActiveThread() && session.ActiveThread().Kind == ThreadPreviewCollaboration {
		return true
	}
	if session.HasRecentPreview() && class.PrefersVisibleExecution && class.IsFollowUp {
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
