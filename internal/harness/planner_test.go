package harness

import "testing"

func TestPlanUsesLocalFirstStep(t *testing.T) {
	step := Plan(Classification{Family: FamilyInspect}, SessionState{})
	if step.Kind != StepLocal {
		t.Fatalf("step = %#v", step)
	}
	if step.Worker != WorkerNone {
		t.Fatalf("worker = %q", step.Worker)
	}
}
