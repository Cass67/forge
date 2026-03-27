package harness

import "fmt"

func Plan(class Classification, session SessionState) Step {
	if class.PrefersVisibleExecution {
		return Step{
			Kind:    StepStrictLocal,
			Worker:  WorkerNone,
			Reason:  "visible collaboration uses strict local execution",
			Summary: "run strict local collaboration",
		}
	}
	if worker, reason, ok := AdmitWorker(class, session); ok {
		return Step{
			Kind:    StepWorker,
			Worker:  worker,
			Reason:  reason,
			Summary: fmt.Sprintf("run hidden %s worker", worker),
		}
	}
	step := Step{
		Kind:    StepLocal,
		Worker:  WorkerNone,
		Reason:  "local-first default",
		Summary: "run locally",
	}
	return step
}
