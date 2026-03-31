package react

import (
	"strings"

	"forge/internal/agent/tools"
)

type ApprovalDecision string

const (
	ApprovalDecisionAllow     ApprovalDecision = "allow"
	ApprovalDecisionPrompt    ApprovalDecision = "prompt"
	ApprovalDecisionForbidden ApprovalDecision = "forbidden"
)

type ApprovalDecisionSource string

const (
	ApprovalDecisionSourceRule     ApprovalDecisionSource = "rule"
	ApprovalDecisionSourceSandbox  ApprovalDecisionSource = "sandbox"
	ApprovalDecisionSourceTrusted  ApprovalDecisionSource = "trusted"
	ApprovalDecisionSourcePolicy   ApprovalDecisionSource = "policy"
	ApprovalDecisionSourceGuardian ApprovalDecisionSource = "guardian"
)

type ApprovalUpdate struct {
	Decision ApprovalDecision
	Source   ApprovalDecisionSource
	Reason   string
}

func NewApprovalUpdate(decision ApprovalDecision, source ApprovalDecisionSource, detail string) ApprovalUpdate {
	return ApprovalUpdate{
		Decision: decision,
		Source:   source,
		Reason:   FormatApprovalReason(decision, source, detail),
	}
}

func FormatApprovalReason(decision ApprovalDecision, source ApprovalDecisionSource, detail string) string {
	head := approvalReasonHead(decision, source)
	detail = strings.TrimSpace(detail)
	if detail == "" {
		return head
	}
	return head + ": " + detail
}

func approvalReasonHead(decision ApprovalDecision, source ApprovalDecisionSource) string {
	switch source {
	case ApprovalDecisionSourceRule:
		switch decision {
		case ApprovalDecisionAllow:
			return "rule allowed"
		case ApprovalDecisionPrompt:
			return "rule requires prompt"
		case ApprovalDecisionForbidden:
			return "rule forbade"
		}
	case ApprovalDecisionSourceSandbox:
		if decision == ApprovalDecisionPrompt {
			return "sandbox denied; prompting for approval"
		}
		return "sandbox denied"
	case ApprovalDecisionSourceTrusted:
		return "trusted command auto-approved"
	case ApprovalDecisionSourcePolicy:
		switch decision {
		case ApprovalDecisionAllow:
			return "policy allowed"
		case ApprovalDecisionPrompt:
			return "policy requested prompt"
		case ApprovalDecisionForbidden:
			return "policy forbade"
		}
	case ApprovalDecisionSourceGuardian:
		switch decision {
		case ApprovalDecisionPrompt:
			return "guardian warning required prompt"
		case ApprovalDecisionForbidden:
			return "guardian blocked"
		case ApprovalDecisionAllow:
			return "guardian allowed"
		}
	}
	if trimmed := strings.TrimSpace(string(source)); trimmed != "" {
		return trimmed
	}
	if trimmed := strings.TrimSpace(string(decision)); trimmed != "" {
		return trimmed
	}
	return "approval decision"
}

func approvalUpdateDetail(action tools.Action) string {
	if summary := strings.TrimSpace(action.Summary); summary != "" {
		return summary
	}
	if tool := strings.TrimSpace(action.Tool); tool != "" {
		return tool
	}
	return "action"
}
