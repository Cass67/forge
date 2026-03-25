package harness

import "testing"

func TestSessionBeginTurnAndApplyCarriesRecentEvidence(t *testing.T) {
	session := NewSession()
	turn1 := session.BeginTurn("describe this directory")
	if turn1.Turn != 1 {
		t.Fatalf("turn1 = %#v", turn1)
	}

	class := Classification{
		Family:   FamilyInspect,
		TopicKey: "workspace:directory",
	}
	session.Apply(class, Observation{
		Status:   ObservationComplete,
		Response: "Directory contains cmd, internal, and docs.",
		Summary:  "directory overview",
		TopicKey: "workspace:directory",
	})

	turn2 := session.BeginTurn("what do you think?")
	if turn2.Turn != 2 {
		t.Fatalf("turn2 = %#v", turn2)
	}

	state := session.Snapshot()
	if !state.HasRecentEvidence() {
		t.Fatalf("expected recent evidence: %#v", state)
	}
	if state.LastEvidence.TopicKey != "workspace:directory" {
		t.Fatalf("last evidence = %#v", state.LastEvidence)
	}
	if state.LastResponse != "Directory contains cmd, internal, and docs." {
		t.Fatalf("last response = %q", state.LastResponse)
	}
}

func TestSessionRecentEvidenceExpiresAfterOneTurn(t *testing.T) {
	session := NewSession()
	_ = session.BeginTurn("describe this directory")
	session.Apply(Classification{
		Family:   FamilyInspect,
		TopicKey: "workspace:directory",
	}, Observation{
		Status:   ObservationComplete,
		Response: "overview",
		TopicKey: "workspace:directory",
	})
	_ = session.BeginTurn("thanks")
	_ = session.BeginTurn("what do you think?")

	state := session.Snapshot()
	if state.HasRecentEvidence() {
		t.Fatalf("expected evidence to expire after one turn: %#v", state)
	}
}
