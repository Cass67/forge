package harness

func Plan(class Classification, _ SessionState) Step {
	step := Step{
		Kind:    StepLocal,
		Worker:  WorkerNone,
		Reason:  "local-first default",
		Summary: "run locally",
	}

	if class.Family == FamilyResearch && class.NeedsExternalSources {
		step.Reason = "bootstrap kernel keeps research local until worker contracts land"
	}

	return step
}
