package react

import (
	"encoding/json"
	"fmt"
	"strings"

	"forge/internal/protocol"
)

type TurnIntent string

const (
	TurnIntentAnswerOnly    TurnIntent = "answer_only"
	TurnIntentInspect       TurnIntent = "inspect"
	TurnIntentWriteArtifact TurnIntent = "write_artifact"
	TurnIntentEditCode      TurnIntent = "edit_code"
	TurnIntentImplement     TurnIntent = "implement"
	TurnIntentVerify        TurnIntent = "verify"
)

type ContractActionKind string

const (
	ContractActionEdit   ContractActionKind = "edit"
	ContractActionRead   ContractActionKind = "read"
	ContractActionCommit ContractActionKind = "commit"
	ContractActionPush   ContractActionKind = "push"
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
	EvidenceTest              EvidenceKind = "test"
	EvidenceTool              EvidenceKind = "tool"
	EvidenceNote              EvidenceKind = "note"
	EvidenceRead              EvidenceKind = "read"
	EvidenceWrite             EvidenceKind = "write"
	EvidenceVerification      EvidenceKind = "verification"
	EvidenceDelegation        EvidenceKind = "delegation"
	EvidenceDelegationFailure EvidenceKind = "delegation_failure"
	EvidenceModelViolation    EvidenceKind = "model_violation"
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
	switch contract.Intent {
	case "", TurnIntentAnswerOnly, TurnIntentInspect, TurnIntentWriteArtifact, TurnIntentEditCode, TurnIntentImplement, TurnIntentVerify:
	default:
		contract.Intent = TurnIntentImplement
	}
	for i := range contract.RequiredActions {
		switch contract.RequiredActions[i].Kind {
		case ContractActionEdit, ContractActionRead, ContractActionCommit, ContractActionPush, ContractActionRun, ContractActionReport:
		default:
			contract.RequiredActions[i].Kind = ContractActionReport
		}
	}
	for i := range contract.Evidence {
		switch contract.Evidence[i].Kind {
		case EvidenceTest, EvidenceTool, EvidenceNote, EvidenceRead, EvidenceWrite, EvidenceVerification, EvidenceDelegation, EvidenceDelegationFailure, EvidenceModelViolation:
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

func recordToolCallEvidence(contract *TurnContract, toolName string, args map[string]any) {
	if contract == nil {
		return
	}
	toolName = strings.TrimSpace(toolName)
	if toolName == "" {
		return
	}
}

func recordToolResultEvidence(contract *TurnContract, toolName string, args map[string]any, result string, isError bool) {
	if contract == nil {
		return
	}
	toolName = strings.TrimSpace(toolName)
	if toolName == "" {
		return
	}
	if toolName == "run_command" {
		command := strings.TrimSpace(stringArg(args, "command"))
		if isValidationCommand(strings.ToLower(command)) {
			passed := !contractToolResultFailed(toolName, result, isError) && isValidationPass(result)
			status := "failed"
			if passed {
				status = "passed"
			}
			contract.Evidence = append(contract.Evidence, EvidenceRecord{Kind: EvidenceVerification, Summary: fmt.Sprintf("verification %s: %s", status, command)})
			return
		}
		status := "failed"
		if !contractToolResultFailed(toolName, result, isError) && runCommandResultExitZero(result) {
			status = "passed"
		}
		if status == "passed" && runCommandLooksLikeFileMutation(command) {
			contract.Evidence = append(contract.Evidence, EvidenceRecord{Kind: EvidenceWrite, Summary: strings.TrimSpace("run_command write: " + command)})
			return
		}
		contract.Evidence = append(contract.Evidence, EvidenceRecord{Kind: EvidenceTool, Summary: strings.TrimSpace("run_command " + status + ": " + command)})
		return
	}
	if isDelegationEvidenceTool(toolName) {
		kind, summary := delegationEvidence(toolName, args, result, isError)
		contract.Evidence = append(contract.Evidence, EvidenceRecord{Kind: kind, Summary: summary})
		return
	}
	if isReadEvidenceTool(toolName) {
		if contractToolResultFailed(toolName, result, isError) {
			contract.Evidence = append(contract.Evidence, EvidenceRecord{Kind: EvidenceTool, Summary: toolEvidenceSummary("failed read", toolName, evidencePaths(args))})
			return
		}
		contract.Evidence = append(contract.Evidence, EvidenceRecord{Kind: EvidenceRead, Summary: toolEvidenceSummary("read", toolName, evidencePaths(args))})
		return
	}
	if isWriteEvidenceTool(toolName) {
		if contractToolResultFailed(toolName, result, isError) {
			contract.Evidence = append(contract.Evidence, EvidenceRecord{Kind: EvidenceTool, Summary: toolEvidenceSummary("failed write", toolName, evidencePaths(args))})
			return
		}
		contract.Evidence = append(contract.Evidence, EvidenceRecord{Kind: EvidenceWrite, Summary: toolEvidenceSummary("write", toolName, evidencePaths(args))})
		return
	}
	if isGitSideEffectTool(toolName) {
		status := "passed"
		if sideEffectGateStatusForToolResult(toolName, result, isError) != SideEffectGatePassed {
			status = "failed"
		}
		contract.Evidence = append(contract.Evidence, EvidenceRecord{Kind: EvidenceTool, Summary: strings.TrimSpace(toolName + " " + status + ": " + strings.TrimSpace(result))})
	}
}

func recordModelViolation(contract *TurnContract, reason, detail string) {
	if contract == nil {
		return
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return
	}
	detail = strings.TrimSpace(detail)
	summary := reason
	if detail != "" {
		summary += ": " + detail
	}
	contract.Evidence = append(contract.Evidence, EvidenceRecord{Kind: EvidenceModelViolation, Summary: summary})
}

func isReadEvidenceTool(toolName string) bool {
	switch strings.TrimSpace(toolName) {
	case "read_file", "list_dir", "search", "glob", "code_search", "read_output", "git_status", "git_diff", "git_log":
		return true
	default:
		return false
	}
}

func isDelegationEvidenceTool(toolName string) bool {
	switch strings.TrimSpace(toolName) {
	case "spawn_agent", "wait_agent", "get_agent_output", "agent_status":
		return true
	default:
		return false
	}
}

func delegationEvidence(toolName string, args map[string]any, result string, isError bool) (EvidenceKind, string) {
	id := strings.TrimSpace(stringArg(args, "id"))
	status := ""
	var decoded map[string]any
	if json.Unmarshal([]byte(strings.TrimSpace(result)), &decoded) == nil {
		if id == "" {
			id = strings.TrimSpace(stringArg(decoded, "id"))
		}
		status = strings.TrimSpace(stringArg(decoded, "status"))
	}
	failed := contractToolResultFailed(toolName, result, isError) || delegationStatusFailed(status)
	action := "delegation"
	kind := EvidenceDelegation
	if failed {
		action = "failed delegation"
		kind = EvidenceDelegationFailure
	}
	parts := []string{action + ":", strings.TrimSpace(toolName)}
	if id != "" {
		parts = append(parts, id)
	}
	if status != "" {
		parts = append(parts, "status="+status)
	}
	return kind, strings.Join(parts, " ")
}

func delegationStatusFailed(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "failed", "error", "cancelled", "canceled":
		return true
	default:
		return false
	}
}

func isWriteEvidenceTool(toolName string) bool {
	switch strings.TrimSpace(toolName) {
	case "write_file", "edit_file", "apply_patch", "artifact_write", "scratchpad_write":
		return true
	default:
		return false
	}
}

func runCommandLooksLikeFileMutation(command string) bool {
	lower := strings.ToLower(strings.TrimSpace(command))
	if lower == "" {
		return false
	}
	return strings.Contains(lower, "sed -i") || strings.Contains(lower, "cat >>")
}

func isGitSideEffectTool(toolName string) bool {
	switch strings.TrimSpace(toolName) {
	case "git_commit", "git_push":
		return true
	default:
		return false
	}
}

func evidencePaths(args map[string]any) []string {
	for _, key := range []string{"path", "file_path", "target_path"} {
		if value := normalizeEvidencePath(stringArg(args, key)); value != "" {
			return []string{value}
		}
	}
	var paths []string
	for _, path := range pathsFromPatch(stringArg(args, "patch")) {
		if path = normalizeEvidencePath(path); path != "" {
			paths = append(paths, path)
		}
	}
	return paths
}

func normalizeEvidencePath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	return strings.Join(strings.Fields(path), " ")
}

func toolEvidenceSummary(action, toolName string, paths []string) string {
	summary := strings.TrimSpace(action) + ": " + strings.TrimSpace(toolName)
	if len(paths) > 0 {
		summary += " " + strings.Join(paths, ", ")
	}
	return summary
}

func contractToolResultFailed(toolName, result string, isError bool) bool {
	if isError || sideEffectToolResultIsFailure(result) {
		return true
	}
	lower := strings.ToLower(strings.TrimSpace(result))
	return strings.HasPrefix(lower, "refused:") || strings.HasPrefix(lower, "refused ")
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
