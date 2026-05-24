package react

import (
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
	EvidenceTest           EvidenceKind = "test"
	EvidenceTool           EvidenceKind = "tool"
	EvidenceNote           EvidenceKind = "note"
	EvidenceRead           EvidenceKind = "read"
	EvidenceWrite          EvidenceKind = "write"
	EvidenceVerification   EvidenceKind = "verification"
	EvidenceModelViolation EvidenceKind = "model_violation"
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
		case EvidenceTest, EvidenceTool, EvidenceNote, EvidenceRead, EvidenceWrite, EvidenceVerification, EvidenceModelViolation:
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

func deriveTurnContractFromInput(turn int, input string, nowDate string) *TurnContract {
	normalized := normalizeToolIntentText(input)
	contract := &TurnContract{
		ID:         fmt.Sprintf("contract-%d", turn),
		SourceTurn: turn,
		Intent:     TurnIntentAnswerOnly,
		Status:     ContractStatusActive,
	}
	if normalized == "" {
		return contract
	}
	if turnInputSuggestsWritePlan(normalized) {
		contract.Intent = TurnIntentWriteArtifact
		contract.RequiredActions = append(contract.RequiredActions, ContractAction{Kind: ContractActionEdit, Description: "write requested artifact"})
		contract.RequiredArtifacts = append(contract.RequiredArtifacts, ArtifactRequirement{Path: turnContractPlanPath(input, nowDate), Description: "requested plan artifact"})
		contract.Gates = append(contract.Gates, ContractGate{Name: "artifact", Status: ContractGatePending})
		return contract
	}
	if turnInputSuggestsInspection(normalized) {
		contract.Intent = TurnIntentInspect
		contract.RequiredActions = append(contract.RequiredActions, ContractAction{Kind: ContractActionRead, Description: "inspect requested context"})
		return contract
	}
	if turnInputSuggestsImplementation(normalized) {
		contract.Intent = TurnIntentEditCode
		contract.RequiredActions = append(contract.RequiredActions, ContractAction{Kind: ContractActionEdit, Description: "modify code as requested"})
		if turnInputMentionsVerification(normalized) {
			contract.RequiredVerification = append(contract.RequiredVerification, VerificationRequirement{Description: "verification requested by user"})
		}
		addGitActionsToTurnContract(contract, normalized)
		return contract
	}
	if turnInputSuggestsGitCommit(normalized) || turnInputSuggestsGitPush(normalized) {
		contract.Intent = TurnIntentEditCode
		addGitActionsToTurnContract(contract, normalized)
		return contract
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
	case "read_file", "list_dir", "search", "glob", "code_search", "read_output", "scratchpad_read", "git_status", "git_diff", "git_log":
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

func turnInputSuggestsWritePlan(input string) bool {
	if inputNegatesFileWrite(input) || turnInputLooksLikeQuestion(input) {
		return false
	}
	return containsToolPhrase(input, "write a plan", "write plan", "create a plan", "draft a plan") || strings.HasPrefix(input, "write docs/plans/")
}

func turnInputLooksLikeQuestion(input string) bool {
	return strings.Contains(input, "?") || strings.HasPrefix(input, "how do i ") || strings.HasPrefix(input, "how can i ")
}

func turnContractPlanPath(input string, nowDate string) string {
	for _, path := range extractMarkdownAndNamedPaths(input) {
		return path
	}
	nowDate = strings.TrimSpace(nowDate)
	if nowDate == "" {
		nowDate = "runtime-current-date"
	}
	return "docs/plans/" + nowDate + "-plan.md"
}

func turnInputSuggestsInspection(input string) bool {
	return containsToolPhrase(input, "look at the repo", "inspect the repo", "inspect repo", "read the repo", "review the repo")
}

func turnInputSuggestsImplementation(input string) bool {
	return containsToolPhrase(input, "implement this", "implement it", "make the change", "fix this")
}

func turnInputMentionsVerification(input string) bool {
	return containsToolPhrase(input, "test", "tests", "verify", "verification", "check", "lint")
}

func turnInputSuggestsGitCommit(input string) bool {
	return inputSuggestsGitCommit(input)
}

func turnInputSuggestsGitPush(input string) bool {
	return inputSuggestsGitPush(input)
}

func addGitActionsToTurnContract(contract *TurnContract, input string) {
	if turnInputSuggestsGitCommit(input) {
		contract.RequiredActions = append(contract.RequiredActions, ContractAction{Kind: ContractActionCommit, Description: "commit requested changes"})
	}
	if turnInputSuggestsGitPush(input) {
		contract.RequiredActions = append(contract.RequiredActions, ContractAction{Kind: ContractActionPush, Description: "push requested changes"})
	}
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
