package react

import (
	"strings"

	"forge/internal/config"
	"forge/internal/permissions"
)

func LoadApprovalConfig(cfg *config.Config) ApprovalConfig {
	out := ApprovalConfig{
		DefaultPolicy: ApprovalOnRequest,
		SandboxPolicy: SandboxWorkspaceWrite,
	}
	if cfg == nil {
		return normalizeApprovalConfig(out)
	}
	if policy := parseApprovalPolicy(cfg.Approval.DefaultPolicy); policy != "" {
		out.DefaultPolicy = policy
	}
	if sandbox := parseSandboxPolicy(cfg.Approval.SandboxPolicy); sandbox != "" {
		out.SandboxPolicy = sandbox
	}
	out.KnownSafeCommand = append(out.KnownSafeCommand, nonEmptyStrings(cfg.Approval.KnownSafePrefixes)...)
	out.ScopedRules = permissions.LoadConfigRules(cfg.Permissions)
	for _, rule := range cfg.Approval.Rules {
		decision := parseRuleDecision(rule.Decision)
		if decision == "" {
			continue
		}
		approvalRule := ApprovalRule{
			Tool:     strings.TrimSpace(rule.Tool),
			Decision: decision,
		}
		switch {
		case len(rule.CommandPrefix) > 0:
			prefix := nonEmptyStrings(rule.CommandPrefix)
			matcher, err := parseShellRulePrefix(prefix)
			if err != nil {
				continue
			}
			approvalRule.CommandPrefix = prefix
			approvalRule.matcher = matcher
			approvalRule.hasMatcher = true
		case strings.TrimSpace(rule.Command) != "":
			command := strings.TrimSpace(rule.Command)
			matcher, err := parseShellRule(command)
			if err != nil {
				continue
			}
			approvalRule.Command = command
			approvalRule.matcher = matcher
			approvalRule.hasMatcher = true
		}
		out.Rules = append(out.Rules, approvalRule)
	}
	return normalizeApprovalConfig(out)
}

func parseApprovalPolicy(value string) ApprovalPolicy {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case string(ApprovalNever):
		return ApprovalNever
	case string(ApprovalOnFailure):
		return ApprovalOnFailure
	case string(ApprovalOnRequest):
		return ApprovalOnRequest
	case string(ApprovalUnlessTrusted):
		return ApprovalUnlessTrusted
	default:
		return ""
	}
}

func parseSandboxPolicy(value string) SandboxPolicy {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case string(SandboxReadOnly):
		return SandboxReadOnly
	case string(SandboxWorkspaceWrite):
		return SandboxWorkspaceWrite
	case string(SandboxDangerFull):
		return SandboxDangerFull
	default:
		return ""
	}
}

func parseRuleDecision(value string) RuleDecision {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case string(DecisionAllow):
		return DecisionAllow
	case string(DecisionPrompt):
		return DecisionPrompt
	case string(DecisionForbidden):
		return DecisionForbidden
	default:
		return ""
	}
}

func nonEmptyStrings(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}
