package react

import (
	"slices"
	"strings"
	"testing"
	"time"

	"forge/internal/protocol"
)

func TestTurnContractRecordsReadEvidence(t *testing.T) {
	contract := &TurnContract{ID: "contract-1"}

	recordToolResultEvidence(contract, "read_file", map[string]any{"path": "README.md"}, "file contents", false)

	assertContractEvidence(t, contract, EvidenceRead, "read_file", "README.md")
}

func TestTurnContractDoesNotRecordSuccessfulReadEvidenceForFailedRead(t *testing.T) {
	contract := &TurnContract{ID: "contract-1"}

	recordToolResultEvidence(contract, "read_file", map[string]any{"path": "missing.md"}, "error: file not found", false)

	assertNoContractEvidence(t, contract, EvidenceRead)
	assertContractEvidence(t, contract, EvidenceTool, "failed read", "read_file", "missing.md")
}

func TestTurnContractRecordsWriteEvidenceWithoutMutatingArtifactGate(t *testing.T) {
	contract := &TurnContract{
		ID:    "contract-1",
		Gates: []ContractGate{{Name: "artifact", Status: ContractGatePending}},
	}

	recordToolResultEvidence(contract, "write_file", map[string]any{"path": "docs/plan.md"}, "wrote docs/plan.md", false)

	assertContractEvidence(t, contract, EvidenceWrite, "write_file", "docs/plan.md")
	assertContractGate(t, contract, "artifact", ContractGatePending)
}

func TestTurnContractRecordsVerificationEvidenceWithoutMutatingGate(t *testing.T) {
	contract := &TurnContract{
		ID:    "contract-1",
		Gates: []ContractGate{{Name: "verification", Status: ContractGatePending}},
	}

	recordToolResultEvidence(contract, "run_command", map[string]any{"command": "go test ./internal/react"}, "ok\nexit 0", false)

	assertContractEvidence(t, contract, EvidenceVerification, "go test ./internal/react", "passed")
	assertContractGate(t, contract, "verification", ContractGatePending)
}

func TestTurnContractRecordsFailedVerificationEvidenceWithoutMutatingGate(t *testing.T) {
	contract := &TurnContract{
		ID:    "contract-1",
		Gates: []ContractGate{{Name: "verification", Status: ContractGatePending}},
	}

	recordToolResultEvidence(contract, "run_command", map[string]any{"command": "go test ./internal/react"}, "FAIL\nexit 1", false)

	assertContractEvidence(t, contract, EvidenceVerification, "go test ./internal/react", "failed")
	assertContractGate(t, contract, "verification", ContractGatePending)
}

func TestTurnContractRecordsTextLevelToolResultFailures(t *testing.T) {
	contract := &TurnContract{ID: "contract-1"}

	recordToolResultEvidence(contract, "write_file", map[string]any{"path": "docs/plan.md"}, "blocked: outside requested scope", false)

	assertNoContractEvidence(t, contract, EvidenceWrite)
	assertContractEvidence(t, contract, EvidenceTool, "failed write", "write_file", "docs/plan.md")
}

func TestTurnContractRecordsDelegationEvidence(t *testing.T) {
	contract := &TurnContract{ID: "contract-1"}

	recordToolResultEvidence(contract, "wait_agent", map[string]any{"id": "agent-1"}, `{"id":"agent-1","status":"completed","result":"wrote docs/report.md"}`, false)

	assertContractEvidence(t, contract, EvidenceDelegation, "wait_agent", "agent-1", "completed")
	assertNoContractEvidence(t, contract, EvidenceWrite)
}

func TestTurnContractRecordsFailedDelegationEvidence(t *testing.T) {
	contract := &TurnContract{ID: "contract-1"}

	recordToolResultEvidence(contract, "get_agent_output", map[string]any{"id": "agent-1"}, "error: agent failed", true)

	assertContractEvidence(t, contract, EvidenceDelegationFailure, "get_agent_output", "agent-1", "failed")
	assertNoContractEvidence(t, contract, EvidenceRead)
	assertNoContractEvidence(t, contract, EvidenceWrite)
}

func TestTurnContractRecordsAllPatchPathEvidenceOnOneLine(t *testing.T) {
	contract := &TurnContract{ID: "contract-1"}
	patch := "*** Begin Patch\n*** Update File: docs/a.md\n@@\n-old\n+new\n*** Add File: docs/b.md\n+hi\n*** End Patch"

	recordToolResultEvidence(contract, "apply_patch", map[string]any{"patch": patch}, "applied patch", false)

	assertContractEvidence(t, contract, EvidenceWrite, "docs/a.md", "docs/b.md")
	if strings.Contains(contract.Evidence[0].Summary, "\n") {
		t.Fatalf("evidence summary contains newline: %q", contract.Evidence[0].Summary)
	}
}

func TestTurnContractRecordsModelViolationEvidence(t *testing.T) {
	contract := &TurnContract{ID: "contract-1"}

	recordModelViolation(contract, "unknown_tool", "bogus_tool")

	assertContractEvidence(t, contract, EvidenceModelViolation, "unknown_tool", "bogus_tool")
}

func TestTurnContractRecordsGitEvidenceUsingSideEffectGateSemantics(t *testing.T) {
	contract := &TurnContract{ID: "contract-1"}

	recordToolResultEvidence(contract, "git_commit", map[string]any{"message": "task 10"}, "created commit abc123", false)

	assertContractEvidence(t, contract, EvidenceTool, "git_commit", "failed")
	assertNoContractEvidenceSummary(t, contract, EvidenceTool, "git_commit passed")
}

func assertContractEvidence(t *testing.T, contract *TurnContract, kind EvidenceKind, parts ...string) {
	t.Helper()
	for _, evidence := range contract.Evidence {
		if evidence.Kind != kind {
			continue
		}
		matched := true
		for _, part := range parts {
			if !strings.Contains(evidence.Summary, part) {
				matched = false
				break
			}
		}
		if matched {
			return
		}
	}
	t.Fatalf("evidence = %#v, want kind %s containing %q", contract.Evidence, kind, parts)
}

func assertNoContractEvidence(t *testing.T, contract *TurnContract, kind EvidenceKind) {
	t.Helper()
	for _, evidence := range contract.Evidence {
		if evidence.Kind == kind {
			t.Fatalf("evidence = %#v, want no kind %s", contract.Evidence, kind)
		}
	}
}

func assertNoContractEvidenceSummary(t *testing.T, contract *TurnContract, kind EvidenceKind, part string) {
	t.Helper()
	for _, evidence := range contract.Evidence {
		if evidence.Kind == kind && strings.Contains(evidence.Summary, part) {
			t.Fatalf("evidence = %#v, want no kind %s containing %q", contract.Evidence, kind, part)
		}
	}
}

func assertContractGate(t *testing.T, contract *TurnContract, name string, status ContractGateStatus) {
	t.Helper()
	for _, gate := range contract.Gates {
		if gate.Name == name && gate.Status == status {
			return
		}
	}
	t.Fatalf("gates = %#v, want %s=%s", contract.Gates, name, status)
}

func contractHasAction(contract *TurnContract, action ContractActionKind) bool {
	return slices.ContainsFunc(contract.RequiredActions, func(got ContractAction) bool {
		return got.Kind == action
	})
}

func contractHasArtifact(contract *TurnContract, path string) bool {
	return slices.ContainsFunc(contract.RequiredArtifacts, func(got ArtifactRequirement) bool {
		return got.Path == path
	})
}

func contractHasGate(contract *TurnContract, name string) bool {
	return slices.ContainsFunc(contract.Gates, func(got ContractGate) bool {
		return got.Name == name && got.Status == ContractGatePending
	})
}

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

func TestRunCommandFileMutationRecordsWriteEvidence(t *testing.T) {
	contract := &TurnContract{ID: "contract-1", Status: ContractStatusActive}

	recordToolResultEvidence(contract, "run_command", map[string]any{
		"command": "sed -i '' 's/window-save-state = always/window-save-state = never/' ~/Library/Application\\ Support/com.mitchellh.ghostty/macos.ghostty",
	}, "\nexit 0", false)

	if !contractHasEvidence(contract, EvidenceWrite, "run_command", "sed -i") {
		t.Fatalf("TurnContract evidence = %#v, want run_command write evidence", contract.Evidence)
	}
	if !turnContractActionSatisfied(contract, ContractActionEdit) {
		t.Fatalf("TurnContract evidence = %#v, want edit action satisfied", contract.Evidence)
	}
}

func TestRunCommandAppendHeredocRecordsWriteEvidence(t *testing.T) {
	contract := &TurnContract{ID: "contract-1", Status: ContractStatusActive}

	recordToolResultEvidence(contract, "run_command", map[string]any{
		"command": "cat >> ~/Library/Application\\ Support/com.mitchellh.ghostty/config.ghostty << 'EOF'\nconfig-file = macos.ghostty\nEOF",
	}, "\nexit 0", false)

	if !contractHasEvidence(contract, EvidenceWrite, "run_command", "cat >>") {
		t.Fatalf("TurnContract evidence = %#v, want append write evidence", contract.Evidence)
	}
}

func TestRunCommandFailedMutationDoesNotRecordWriteEvidence(t *testing.T) {
	contract := &TurnContract{ID: "contract-1", Status: ContractStatusActive}

	recordToolResultEvidence(contract, "run_command", map[string]any{
		"command": "sed -i '' 's/old/new/' file.txt",
	}, "sed: file.txt: No such file or directory\nexit 1", false)

	if contractHasEvidenceKind(contract, EvidenceWrite) {
		t.Fatalf("TurnContract evidence = %#v, failed command must not count as write", contract.Evidence)
	}
}

func TestRunCommandReadOnlyDoesNotRecordWriteEvidence(t *testing.T) {
	contract := &TurnContract{ID: "contract-1", Status: ContractStatusActive}

	recordToolResultEvidence(contract, "run_command", map[string]any{
		"command": "grep \"window-save-state\" ~/Library/Application\\ Support/com.mitchellh.ghostty/macos.ghostty",
	}, "window-save-state = never\n\nexit 0", false)

	if contractHasEvidenceKind(contract, EvidenceWrite) {
		t.Fatalf("TurnContract evidence = %#v, read-only command must not count as write", contract.Evidence)
	}
}

func TestRunCommandValidationTimeoutRecordsFailedVerification(t *testing.T) {
	contract := &TurnContract{ID: "contract-1", Status: ContractStatusActive}

	recordToolResultEvidence(contract, "run_command", map[string]any{
		"command": "go test ./...",
	}, "timeout after 120s", false)

	if !contractHasEvidence(contract, EvidenceVerification, "verification failed", "go test ./...") {
		t.Fatalf("TurnContract evidence = %#v, want failed verification", contract.Evidence)
	}
	if contractHasEvidence(contract, EvidenceVerification, "verification passed") {
		t.Fatalf("TurnContract evidence = %#v, timeout must not pass verification", contract.Evidence)
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
