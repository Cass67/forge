package harness

import "testing"

func TestAdmitWorkerAllowsExternalResearch(t *testing.T) {
	worker, reason, ok := AdmitWorker(Classification{
		Family:               FamilyResearch,
		NeedsExternalSources: true,
		CanStayLocal:         false,
	}, SessionState{})
	if !ok {
		t.Fatal("expected worker admission")
	}
	if worker != WorkerResearcher {
		t.Fatalf("worker = %q", worker)
	}
	if reason == "" {
		t.Fatal("expected admission reason")
	}
}

func TestAdmitWorkerUsesReaderForPlainInspectTurns(t *testing.T) {
	worker, reason, ok := AdmitWorker(Classification{
		Family:       FamilyInspect,
		CanStayLocal: true,
		TopicKey:     "workspace:repository",
	}, SessionState{})
	if !ok {
		t.Fatal("expected reader worker admission")
	}
	if worker != WorkerReader {
		t.Fatalf("worker = %q", worker)
	}
	if reason == "" {
		t.Fatal("expected admission reason")
	}
}

func TestAdmitWorkerKeepsImplementationGroundedWorkspaceInspectTurnsLocal(t *testing.T) {
	worker, reason, ok := AdmitWorker(Classification{
		Family:       FamilyInspect,
		CanStayLocal: true,
		TopicKey:     "workspace:repository",
		TaskText:     "explain how the harness routes preview follow-ups in this repo",
	}, SessionState{})
	if ok {
		t.Fatalf("unexpected worker admission: %q (%s)", worker, reason)
	}
}

func TestAdmitWorkerKeepsEvaluativeWorkspaceInspectTurnsLocal(t *testing.T) {
	worker, reason, ok := AdmitWorker(Classification{
		Family:          FamilyInspect,
		CanStayLocal:    true,
		WantsEvaluation: true,
		TopicKey:        "workspace:directory",
	}, SessionState{})
	if ok {
		t.Fatalf("unexpected worker admission: %q (%s)", worker, reason)
	}
}

func TestAdmitWorkerKeepsFocusedFileInspectTurnsLocal(t *testing.T) {
	worker, _, ok := AdmitWorker(Classification{
		Family:       FamilyInspect,
		CanStayLocal: true,
		TopicKey:     "files:python",
	}, SessionState{})
	if ok {
		t.Fatalf("unexpected worker admission: %q", worker)
	}
}

func TestAdmitWorkerUsesEditorForImplementationTurns(t *testing.T) {
	worker, reason, ok := AdmitWorker(Classification{
		Family:       FamilyImplement,
		CanStayLocal: true,
		TopicKey:     "workspace:directory",
		WantsAction:  true,
	}, SessionState{})
	if !ok {
		t.Fatal("expected editor worker admission")
	}
	if worker != WorkerEditor {
		t.Fatalf("worker = %q", worker)
	}
	if reason == "" {
		t.Fatal("expected admission reason")
	}
}

func TestAdmitWorkerKeepsVisibleCollaborationTurnsLocal(t *testing.T) {
	worker, reason, ok := AdmitWorker(Classification{
		Family:                  FamilyImplement,
		CanStayLocal:            true,
		PrefersVisibleExecution: true,
	}, SessionState{})
	if ok {
		t.Fatalf("unexpected worker admission: %q (%s)", worker, reason)
	}
}

func TestDecideCompletesSuccessfulObservation(t *testing.T) {
	got := Decide(Classification{Family: FamilyInspect}, Observation{Status: ObservationComplete})
	if got.FinalState != StateComplete {
		t.Fatalf("decision = %#v", got)
	}
}

func TestDecideUsesAwaitingFeedbackOutcome(t *testing.T) {
	got := Decide(Classification{Family: FamilyAnswer}, Observation{
		Status: ObservationComplete,
		Outcome: ActionOutcome{
			Lane:              LaneStrictAction,
			Kind:              OutcomeAwaitingFeedback,
			DeliverableKind:   DeliverablePreviewAvailableAndRenderable,
			DeliverableStatus: DeliverableSatisfied,
			Reason:            "preview deliverable satisfied; awaiting feedback",
		},
	})
	if got.FinalState != StateAwaitingFeedback {
		t.Fatalf("decision = %#v", got)
	}
}

func TestDecideUsesRetryOutcome(t *testing.T) {
	got := Decide(Classification{Family: FamilyImplement}, Observation{
		Status: ObservationBlocked,
		Outcome: ActionOutcome{
			Lane:              LaneStrictAction,
			Kind:              OutcomeRetry,
			DeliverableKind:   DeliverablePreviewAvailableAndRenderable,
			DeliverableStatus: DeliverableMissing,
			Reason:            "strict action finished without a verified preview",
		},
	})
	if got.FinalState != StateRetry {
		t.Fatalf("decision = %#v", got)
	}
}

func TestDecideBlocksErroredObservation(t *testing.T) {
	got := Decide(Classification{Family: FamilyInspect}, Observation{Status: ObservationBlocked, Summary: "boom"})
	if got.FinalState != StateBlocked {
		t.Fatalf("decision = %#v", got)
	}
}

func TestPolicyFallbackBlocksWorkerSkillFailures(t *testing.T) {
	got := Decide(Classification{Family: FamilyImplement}, Observation{
		Status:  ObservationBlocked,
		Summary: "required skill unavailable: test-driven-development",
	})
	if got.FinalState != StateBlocked {
		t.Fatalf("decision = %#v", got)
	}
}
