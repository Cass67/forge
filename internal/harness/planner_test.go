package harness

import "testing"

func TestPlanUsesReaderWorkerForPlainInspectTurns(t *testing.T) {
	step := Plan(Classification{Family: FamilyInspect, TopicKey: "workspace:directory"}, SessionState{})
	if step.Kind != StepWorker {
		t.Fatalf("step = %#v", step)
	}
	if step.Worker != WorkerReader {
		t.Fatalf("worker = %q", step.Worker)
	}
}

func TestPlanUsesEditorWorkerForImplementationTurns(t *testing.T) {
	step := Plan(Classification{
		Family:       FamilyImplement,
		TopicKey:     "workspace:directory",
		WantsAction:  true,
		CanStayLocal: true,
	}, SessionState{})
	if step.Kind != StepWorker {
		t.Fatalf("step = %#v", step)
	}
	if step.Worker != WorkerEditor {
		t.Fatalf("worker = %q", step.Worker)
	}
}
