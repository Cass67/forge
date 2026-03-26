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

func TestAdmitWorkerKeepsEvaluativeInspectTurnsLocal(t *testing.T) {
	worker, _, ok := AdmitWorker(Classification{
		Family:          FamilyInspect,
		CanStayLocal:    true,
		WantsEvaluation: true,
	}, SessionState{})
	if ok {
		t.Fatalf("unexpected worker admission: %q", worker)
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

func TestDecideCompletesSuccessfulObservation(t *testing.T) {
	got := Decide(Classification{Family: FamilyInspect}, Observation{Status: ObservationComplete})
	if got.FinalState != StateComplete {
		t.Fatalf("decision = %#v", got)
	}
}

func TestDecideBlocksErroredObservation(t *testing.T) {
	got := Decide(Classification{Family: FamilyInspect}, Observation{Status: ObservationBlocked, Summary: "boom"})
	if got.FinalState != StateBlocked {
		t.Fatalf("decision = %#v", got)
	}
}
