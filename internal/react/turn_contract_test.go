package react

import (
	"testing"
	"time"

	"forge/internal/protocol"
)

func TestSessionSetTurnContractPersistsItemAndSnapshot(t *testing.T) {
	sink := &fakeDurableSink{}
	s := NewSession()
	s.SetDurableSink(sink)

	s.SetTurnContract(TurnContract{
		ID:         "contract-1",
		SourceTurn: 3,
		Intent:     TurnIntentImplement,
		RequiredActions: []ContractAction{{
			Kind:        ContractActionEdit,
			Description: "add durable model",
		}},
		RequiredArtifacts:    []ArtifactRequirement{{Path: "internal/react/turn_contract.go", Description: "runtime model"}},
		RequiredVerification: []VerificationRequirement{{Command: "go test ./internal/react", Description: "react tests"}},
		Evidence:             []EvidenceRecord{{Kind: EvidenceTest, Summary: "red test added"}},
		Gates:                []ContractGate{{Name: "tests", Status: ContractGatePending}},
		Status:               ContractStatusActive,
	})

	snap := s.Snapshot()
	if snap.TurnContract == nil || snap.TurnContract.ID != "contract-1" || snap.TurnContract.RequiredActions[0].Kind != ContractActionEdit {
		t.Fatalf("TurnContract snapshot = %#v", snap.TurnContract)
	}
	items := sink.Items()
	if len(items) != 1 || items[0].Kind != protocol.ItemTurnContract || items[0].TurnContract == nil {
		t.Fatalf("durable items = %#v, want turn_contract item", items)
	}
	if got := items[0].TurnContract; got.ID != "contract-1" || got.RequiredActions[0].Kind != string(ContractActionEdit) || got.Gates[0].Status != string(ContractGatePending) {
		t.Fatalf("TurnContract item = %#v", got)
	}
}

func TestSessionSetTurnContractDefaultsEmptyStatusToActive(t *testing.T) {
	sink := &fakeDurableSink{}
	s := NewSession()
	s.SetDurableSink(sink)

	s.SetTurnContract(TurnContract{ID: "contract-1"})

	snap := s.Snapshot()
	if snap.TurnContract == nil || snap.TurnContract.Status != ContractStatusActive {
		t.Fatalf("TurnContract snapshot = %#v, want active status", snap.TurnContract)
	}
	items := sink.Items()
	if len(items) != 1 || items[0].TurnContract == nil || items[0].TurnContract.Status != string(ContractStatusActive) {
		t.Fatalf("TurnContract item = %#v, want schema-valid active status", items)
	}
}

func TestSessionSetTurnContractWithEmptyIDDoesNotLeaveLiveContract(t *testing.T) {
	s := NewSession()
	s.SetTurnContract(TurnContract{Status: ContractStatusActive})

	if got := s.Snapshot().TurnContract; got != nil {
		t.Fatalf("TurnContract = %#v, want nil", got)
	}
	if items := s.Snapshot().Items; len(items) != 0 {
		t.Fatalf("items = %#v, want no persisted empty-ID contract", items)
	}
}

func TestSessionUpdateTurnContractMutatesAndPersistsLatestItem(t *testing.T) {
	sink := &fakeDurableSink{}
	s := NewSession()
	s.SetDurableSink(sink)
	s.SetTurnContract(TurnContract{ID: "contract-1", Status: ContractStatusActive})

	s.UpdateTurnContract(func(contract *TurnContract) {
		contract.Status = ContractStatusSatisfied
		contract.Evidence = append(contract.Evidence, EvidenceRecord{Kind: EvidenceTest, Summary: "go test passed"})
	})

	snap := s.Snapshot()
	if snap.TurnContract == nil || snap.TurnContract.Status != ContractStatusSatisfied || snap.TurnContract.Evidence[0].Summary != "go test passed" {
		t.Fatalf("TurnContract snapshot = %#v", snap.TurnContract)
	}
	items := sink.Items()
	if len(items) != 2 || items[1].Kind != protocol.ItemTurnContract || items[1].TurnContract == nil {
		t.Fatalf("durable items = %#v, want latest turn_contract item", items)
	}
	if got := items[1].TurnContract; got.Status != string(ContractStatusSatisfied) || got.Evidence[0].Summary != "go test passed" {
		t.Fatalf("latest TurnContract item = %#v", got)
	}
}

func TestSessionUpdateTurnContractDefaultsEmptyStatusToActive(t *testing.T) {
	sink := &fakeDurableSink{}
	s := NewSession()
	s.SetDurableSink(sink)
	s.SetTurnContract(TurnContract{ID: "contract-1", Status: ContractStatusActive})

	s.UpdateTurnContract(func(contract *TurnContract) {
		contract.Status = ""
	})

	snap := s.Snapshot()
	if snap.TurnContract == nil || snap.TurnContract.Status != ContractStatusActive {
		t.Fatalf("TurnContract snapshot = %#v, want active status", snap.TurnContract)
	}
	items := sink.Items()
	if len(items) != 2 || items[1].TurnContract == nil || items[1].TurnContract.Status != string(ContractStatusActive) {
		t.Fatalf("latest TurnContract item = %#v, want schema-valid active status", items)
	}
}

func TestSessionUpdateTurnContractWithoutActiveContractDoesNotCreateEmptyContract(t *testing.T) {
	s := NewSession()
	called := false

	s.UpdateTurnContract(func(contract *TurnContract) {
		called = true
		contract.Status = ContractStatusActive
	})

	if called {
		t.Fatal("UpdateTurnContract callback ran without active contract")
	}
	if got := s.Snapshot().TurnContract; got != nil {
		t.Fatalf("TurnContract = %#v, want nil", got)
	}
	if items := s.Snapshot().Items; len(items) != 0 {
		t.Fatalf("items = %#v, want no persisted empty contract", items)
	}
}

func TestSessionClearTurnContractClearsSnapshotAndReplay(t *testing.T) {
	s := NewSession()
	s.SetTurnContract(TurnContract{ID: "contract-1", Status: ContractStatusActive})
	s.ClearTurnContract("user changed task")

	if got := s.Snapshot().TurnContract; got != nil {
		t.Fatalf("TurnContract = %#v, want nil", got)
	}
	restored, err := NewSessionFromItems(s.Snapshot().Items)
	if err != nil {
		t.Fatal(err)
	}
	if got := restored.Snapshot().TurnContract; got != nil {
		t.Fatalf("restored TurnContract = %#v, want nil", got)
	}
}

func TestSessionClearTurnContractPersistsSchemaValidClearItem(t *testing.T) {
	sink := &fakeDurableSink{}
	s := NewSession()
	s.SetDurableSink(sink)
	s.SetTurnContract(TurnContract{ID: "contract-1", Status: ContractStatusActive})

	s.ClearTurnContract("user changed task")

	items := sink.Items()
	if len(items) != 2 || items[1].TurnContract == nil {
		t.Fatalf("items = %#v, want clear turn_contract item", items)
	}
	got := items[1].TurnContract
	if got.ID == "" || got.Status != string(ContractStatusCleared) || got.Reason != "user changed task" {
		t.Fatalf("clear TurnContract item = %#v, want non-empty id, cleared status, reason", got)
	}
}

func TestSessionConcurrentUpdateTurnContractPreservesBothMutations(t *testing.T) {
	s := NewSession()
	s.SetTurnContract(TurnContract{ID: "contract-1", Status: ContractStatusActive})
	entered := make(chan struct{})
	release := make(chan struct{})
	firstDone := make(chan struct{})
	secondDone := make(chan struct{})

	go func() {
		defer close(firstDone)
		s.UpdateTurnContract(func(contract *TurnContract) {
			contract.Evidence = append(contract.Evidence, EvidenceRecord{Kind: EvidenceNote, Summary: "first"})
			close(entered)
			<-release
		})
	}()
	<-entered
	go func() {
		defer close(secondDone)
		s.UpdateTurnContract(func(contract *TurnContract) {
			contract.RequiredActions = append(contract.RequiredActions, ContractAction{Kind: ContractActionRun})
		})
	}()
	select {
	case <-secondDone:
		t.Fatal("second update completed while first update callback was still active")
	case <-time.After(20 * time.Millisecond):
	}
	close(release)
	<-firstDone
	<-secondDone

	got := s.Snapshot().TurnContract
	if got == nil || len(got.Evidence) != 1 || got.Evidence[0].Summary != "first" || len(got.RequiredActions) != 1 || got.RequiredActions[0].Kind != ContractActionRun {
		t.Fatalf("TurnContract = %#v, want both update mutations", got)
	}
}

func TestSessionSnapshotItemsTurnContractIsAliasSafe(t *testing.T) {
	s := NewSession()
	s.SetTurnContract(TurnContract{
		ID:                   "contract-1",
		RequiredActions:      []ContractAction{{Kind: ContractActionEdit}},
		RequiredArtifacts:    []ArtifactRequirement{{Path: "a.go"}},
		RequiredVerification: []VerificationRequirement{{Command: "go test ./internal/react"}},
		Evidence:             []EvidenceRecord{{Kind: EvidenceTest, Summary: "initial"}},
		Gates:                []ContractGate{{Name: "tests", Status: ContractGatePending}},
		Status:               ContractStatusActive,
	})

	snap := s.Snapshot()
	snap.Items[0].TurnContract.ID = "changed"
	snap.Items[0].TurnContract.RequiredActions[0].Kind = "changed"
	snap.Items[0].TurnContract.RequiredArtifacts[0].Path = "changed.go"
	snap.Items[0].TurnContract.RequiredVerification[0].Command = "changed"
	snap.Items[0].TurnContract.Evidence[0].Summary = "changed"
	snap.Items[0].TurnContract.Gates[0].Name = "changed"

	next := s.Snapshot()
	if next.Items[0].TurnContract.ID != "contract-1" || next.Items[0].TurnContract.RequiredActions[0].Kind != string(ContractActionEdit) || next.Items[0].TurnContract.Gates[0].Name != "tests" {
		t.Fatalf("snapshot item mutation leaked into session items: %#v", next.Items[0].TurnContract)
	}
	if next.TurnContract == nil || next.TurnContract.ID != "contract-1" || next.TurnContract.Gates[0].Name != "tests" {
		t.Fatalf("snapshot item mutation leaked into latest contract: %#v", next.TurnContract)
	}
}

func TestSessionRestoresLatestActiveTurnContract(t *testing.T) {
	s, err := NewSessionFromItems([]protocol.Item{
		{
			Version:  protocol.CurrentItemVersion,
			ThreadID: "thread-1",
			Seq:      1,
			Kind:     protocol.ItemTurnContract,
			TurnContract: &protocol.TurnContractItem{
				ID:         "contract-1",
				SourceTurn: 1,
				Status:     string(ContractStatusActive),
			},
		},
		{
			Version:  protocol.CurrentItemVersion,
			ThreadID: "thread-1",
			Seq:      2,
			Kind:     protocol.ItemTurnContract,
			TurnContract: &protocol.TurnContractItem{
				ID:     "contract-2",
				Intent: string(TurnIntentVerify),
				RequiredVerification: []protocol.VerificationRequirementItem{{
					Command: "go test ./internal/react",
				}},
				Status: string(ContractStatusActive),
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	got := s.Snapshot().TurnContract
	if got == nil || got.ID != "contract-2" || got.Intent != TurnIntentVerify || got.RequiredVerification[0].Command != "go test ./internal/react" {
		t.Fatalf("TurnContract = %#v", got)
	}
}
