package harness

import "testing"

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
