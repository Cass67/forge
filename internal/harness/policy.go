package harness

func AdmitWorker(class Classification, _ SessionState) (WorkerKind, string, bool) {
	if class.Family == FamilyInspect && !class.WantsEvaluation && !class.WantsInterpretation && !class.WantsAction {
		return WorkerReader, "plain inspection benefits from a bounded reader worker", true
	}
	if class.Family == FamilyResearch && class.NeedsExternalSources && !class.CanStayLocal {
		return WorkerResearcher, "external research benefits from an isolated researcher worker", true
	}
	return WorkerNone, "", false
}

func Decide(_ Classification, obs Observation) Decision {
	if obs.Status == ObservationBlocked || obs.Err != nil {
		return Decision{
			FinalState: StateBlocked,
			Reason:     "observation blocked",
		}
	}
	return Decision{
		FinalState: StateComplete,
		Reason:     "observation complete",
	}
}
