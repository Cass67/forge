package harness

import "testing"

func TestPlanUsesReaderWorkerForPlainInspectTurns(t *testing.T) {
	step := Plan(Classification{Family: FamilyInspect}, SessionState{})
	if step.Kind != StepWorker {
		t.Fatalf("step = %#v", step)
	}
	if step.Worker != WorkerReader {
		t.Fatalf("worker = %q", step.Worker)
	}
}
