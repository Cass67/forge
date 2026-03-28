package harness

import "strings"

func AdmitWorker(class Classification, _ SessionState) (WorkerKind, string, bool) {
	if class.PrefersVisibleExecution {
		return WorkerNone, "", false
	}
	workspaceInspect := strings.HasPrefix(class.TopicKey, "workspace:directory") || strings.HasPrefix(class.TopicKey, "workspace:repository")
	if class.Family == FamilyInspect &&
		workspaceInspect &&
		!class.WantsEvaluation &&
		!class.WantsInterpretation &&
		!class.WantsAction &&
		!inspectTaskNeedsImplementationGrounding(class) {
		return WorkerReader, "workspace inspection benefits from a bounded reader worker", true
	}
	if class.Family == FamilyImplement &&
		class.CanStayLocal {
		return WorkerEditor, "implementation work benefits from a bounded editor worker", true
	}
	if class.Family == FamilyResearch && class.NeedsExternalSources && !class.CanStayLocal {
		return WorkerResearcher, "external research benefits from an isolated researcher worker", true
	}
	return WorkerNone, "", false
}

func Decide(_ Classification, obs Observation) Decision {
	outcome := obs.Outcome
	if outcome.Kind == OutcomeNone {
		if obs.Status == ObservationBlocked || obs.Err != nil {
			outcome.Kind = OutcomeBlocked
			outcome.Reason = firstNonEmpty(strings.TrimSpace(outcome.Reason), strings.TrimSpace(obs.Summary), errorString(obs.Err), "observation blocked")
		} else {
			outcome.Kind = OutcomeComplete
			outcome.Reason = firstNonEmpty(strings.TrimSpace(outcome.Reason), strings.TrimSpace(obs.Summary), "observation complete")
		}
	}

	switch outcome.Kind {
	case OutcomeRetry:
		return Decision{
			FinalState: StateRetry,
			Outcome:    outcome.Kind,
			Reason:     firstNonEmpty(strings.TrimSpace(outcome.Reason), "retry observation"),
		}
	case OutcomeReplan:
		return Decision{
			FinalState: StateReplan,
			Outcome:    outcome.Kind,
			Reason:     firstNonEmpty(strings.TrimSpace(outcome.Reason), "replan observation"),
		}
	case OutcomeAwaitingFeedback:
		return Decision{
			FinalState: StateAwaitingFeedback,
			Outcome:    outcome.Kind,
			Reason:     firstNonEmpty(strings.TrimSpace(outcome.Reason), "awaiting user feedback"),
		}
	case OutcomeBlocked:
		return Decision{
			FinalState: StateBlocked,
			Outcome:    outcome.Kind,
			Reason:     firstNonEmpty(strings.TrimSpace(outcome.Reason), "observation blocked"),
		}
	default:
		return Decision{
			FinalState: StateComplete,
			Outcome:    OutcomeComplete,
			Reason:     firstNonEmpty(strings.TrimSpace(outcome.Reason), "observation complete"),
		}
	}
}
