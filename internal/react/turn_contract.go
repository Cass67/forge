package react

import "forge/internal/protocol"

type TurnIntent string

const (
	TurnIntentImplement TurnIntent = "implement"
	TurnIntentVerify    TurnIntent = "verify"
)

type ContractActionKind string

const (
	ContractActionEdit   ContractActionKind = "edit"
	ContractActionRun    ContractActionKind = "run"
	ContractActionReport ContractActionKind = "report"
)

type ContractStatus string

const (
	ContractStatusActive    ContractStatus = "active"
	ContractStatusSatisfied ContractStatus = "satisfied"
	ContractStatusCleared   ContractStatus = "cleared"
)

type ContractGateStatus string

const (
	ContractGatePending ContractGateStatus = "pending"
	ContractGatePassed  ContractGateStatus = "passed"
	ContractGateFailed  ContractGateStatus = "failed"
)

type EvidenceKind string

const (
	EvidenceTest EvidenceKind = "test"
	EvidenceTool EvidenceKind = "tool"
	EvidenceNote EvidenceKind = "note"
)

type ContractAction struct {
	Kind        ContractActionKind `json:"kind"`
	Description string             `json:"description,omitempty"`
}

type ArtifactRequirement struct {
	Path        string `json:"path"`
	Description string `json:"description,omitempty"`
}

type VerificationRequirement struct {
	Command     string `json:"command"`
	Description string `json:"description,omitempty"`
}

type EvidenceRecord struct {
	Kind    EvidenceKind `json:"kind"`
	Summary string       `json:"summary,omitempty"`
}

type ContractGate struct {
	Name     string             `json:"name"`
	Status   ContractGateStatus `json:"status"`
	Evidence string             `json:"evidence,omitempty"`
}

type TurnContract struct {
	ID                   string                    `json:"id"`
	SourceTurn           int                       `json:"source_turn,omitempty"`
	Intent               TurnIntent                `json:"intent,omitempty"`
	RequiredActions      []ContractAction          `json:"required_actions,omitempty"`
	RequiredArtifacts    []ArtifactRequirement     `json:"required_artifacts,omitempty"`
	RequiredVerification []VerificationRequirement `json:"required_verification,omitempty"`
	Evidence             []EvidenceRecord          `json:"evidence,omitempty"`
	Gates                []ContractGate            `json:"gates,omitempty"`
	Status               ContractStatus            `json:"status,omitempty"`
	Reason               string                    `json:"reason,omitempty"`
}

func copyTurnContract(contract *TurnContract) *TurnContract {
	if contract == nil {
		return nil
	}
	copy := *contract
	copy.RequiredActions = append([]ContractAction(nil), contract.RequiredActions...)
	copy.RequiredArtifacts = append([]ArtifactRequirement(nil), contract.RequiredArtifacts...)
	copy.RequiredVerification = append([]VerificationRequirement(nil), contract.RequiredVerification...)
	copy.Evidence = append([]EvidenceRecord(nil), contract.Evidence...)
	copy.Gates = append([]ContractGate(nil), contract.Gates...)
	return &copy
}

func normalizeTurnContract(contract *TurnContract) *TurnContract {
	if contract == nil {
		return nil
	}
	if contract.Status != ContractStatusSatisfied && contract.Status != ContractStatusCleared {
		contract.Status = ContractStatusActive
	}
	if contract.Intent != "" && contract.Intent != TurnIntentVerify {
		contract.Intent = TurnIntentImplement
	}
	for i := range contract.RequiredActions {
		switch contract.RequiredActions[i].Kind {
		case ContractActionEdit, ContractActionRun, ContractActionReport:
		default:
			contract.RequiredActions[i].Kind = ContractActionReport
		}
	}
	for i := range contract.Evidence {
		switch contract.Evidence[i].Kind {
		case EvidenceTest, EvidenceTool, EvidenceNote:
		default:
			contract.Evidence[i].Kind = EvidenceNote
		}
	}
	for i := range contract.Gates {
		switch contract.Gates[i].Status {
		case ContractGatePending, ContractGatePassed, ContractGateFailed:
		default:
			contract.Gates[i].Status = ContractGatePending
		}
	}
	return contract
}

func turnContractToProtocol(contract TurnContract) protocol.TurnContractItem {
	out := protocol.TurnContractItem{
		ID:                   contract.ID,
		SourceTurn:           contract.SourceTurn,
		Intent:               string(contract.Intent),
		Status:               string(contract.Status),
		Reason:               contract.Reason,
		RequiredActions:      make([]protocol.ContractActionItem, 0, len(contract.RequiredActions)),
		RequiredArtifacts:    make([]protocol.ArtifactRequirementItem, 0, len(contract.RequiredArtifacts)),
		RequiredVerification: make([]protocol.VerificationRequirementItem, 0, len(contract.RequiredVerification)),
		Evidence:             make([]protocol.EvidenceRecordItem, 0, len(contract.Evidence)),
		Gates:                make([]protocol.ContractGateItem, 0, len(contract.Gates)),
	}
	for _, action := range contract.RequiredActions {
		out.RequiredActions = append(out.RequiredActions, protocol.ContractActionItem{Kind: string(action.Kind), Description: action.Description})
	}
	for _, artifact := range contract.RequiredArtifacts {
		out.RequiredArtifacts = append(out.RequiredArtifacts, protocol.ArtifactRequirementItem{Path: artifact.Path, Description: artifact.Description})
	}
	for _, verification := range contract.RequiredVerification {
		out.RequiredVerification = append(out.RequiredVerification, protocol.VerificationRequirementItem{Command: verification.Command, Description: verification.Description})
	}
	for _, evidence := range contract.Evidence {
		out.Evidence = append(out.Evidence, protocol.EvidenceRecordItem{Kind: string(evidence.Kind), Summary: evidence.Summary})
	}
	for _, gate := range contract.Gates {
		out.Gates = append(out.Gates, protocol.ContractGateItem{Name: gate.Name, Status: string(gate.Status), Evidence: gate.Evidence})
	}
	return out
}

func turnContractFromProtocol(item protocol.TurnContractItem) TurnContract {
	out := TurnContract{
		ID:                   item.ID,
		SourceTurn:           item.SourceTurn,
		Intent:               TurnIntent(item.Intent),
		Status:               ContractStatus(item.Status),
		Reason:               item.Reason,
		RequiredActions:      make([]ContractAction, 0, len(item.RequiredActions)),
		RequiredArtifacts:    make([]ArtifactRequirement, 0, len(item.RequiredArtifacts)),
		RequiredVerification: make([]VerificationRequirement, 0, len(item.RequiredVerification)),
		Evidence:             make([]EvidenceRecord, 0, len(item.Evidence)),
		Gates:                make([]ContractGate, 0, len(item.Gates)),
	}
	for _, action := range item.RequiredActions {
		out.RequiredActions = append(out.RequiredActions, ContractAction{Kind: ContractActionKind(action.Kind), Description: action.Description})
	}
	for _, artifact := range item.RequiredArtifacts {
		out.RequiredArtifacts = append(out.RequiredArtifacts, ArtifactRequirement{Path: artifact.Path, Description: artifact.Description})
	}
	for _, verification := range item.RequiredVerification {
		out.RequiredVerification = append(out.RequiredVerification, VerificationRequirement{Command: verification.Command, Description: verification.Description})
	}
	for _, evidence := range item.Evidence {
		out.Evidence = append(out.Evidence, EvidenceRecord{Kind: EvidenceKind(evidence.Kind), Summary: evidence.Summary})
	}
	for _, gate := range item.Gates {
		out.Gates = append(out.Gates, ContractGate{Name: gate.Name, Status: ContractGateStatus(gate.Status), Evidence: gate.Evidence})
	}
	return out
}
