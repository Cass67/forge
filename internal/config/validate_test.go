package config

import (
	"strings"
	"testing"
)

func TestValidate_DefaultConfigHasNoIssues(t *testing.T) {
	var cfg Config
	setDefaults(&cfg)
	issues := cfg.Validate()
	if len(issues) != 0 {
		t.Fatalf("expected no validation issues, got %v", issues)
	}
}

func TestValidate_InvalidFields(t *testing.T) {
	var cfg Config
	setDefaults(&cfg)
	cfg.Models.Writer = ""
	cfg.Session.RoundsPerPass = 0
	cfg.Log.Level = "verbose"
	cfg.Retry.MaxAttempts = 0
	cfg.Chat.AutoSkills = "maybe"
	cfg.Approval.DefaultPolicy = "sometimes"
	cfg.Approval.SandboxPolicy = "sandboxed"
	cfg.Approval.Rules = []ApprovalRuleConfig{
		{Tool: "run_command", Decision: "maybe"},
	}

	issues := cfg.Validate()
	if len(issues) < 8 {
		t.Fatalf("expected multiple validation issues, got %v", issues)
	}
}

func TestValidateApprovalRulesRequireOnlyOneExplicitMatcherShape(t *testing.T) {
	var cfg Config
	setDefaults(&cfg)
	cfg.Approval.Rules = []ApprovalRuleConfig{
		{
			Tool:          "run_command",
			CommandPrefix: []string{"git", "push"},
			Command:       "git push",
			Decision:      "forbidden",
		},
		{
			Tool:     "write_file",
			Decision: "allow",
		},
	}

	issues := cfg.Validate()
	if !hasIssueContaining(issues, "approval.rules[0]", "exactly one of command_prefix or command") {
		t.Fatalf("expected exclusive matcher issue for rule 0, got %v", issues)
	}
	if hasIssueContaining(issues, "approval.rules[1]", "exactly one of command_prefix or command") {
		t.Fatalf("tool-only rule should remain valid, got %v", issues)
	}
}

func TestValidateApprovalRulesRejectMalformedMatchers(t *testing.T) {
	var cfg Config
	setDefaults(&cfg)
	cfg.Approval.Rules = []ApprovalRuleConfig{
		{
			Tool:          "run_command",
			CommandPrefix: []string{" ", "\t"},
			Decision:      "allow",
		},
		{
			Tool:     "run_command",
			Command:  `git \`,
			Decision: "prompt",
		},
	}

	issues := cfg.Validate()
	if !hasIssueContaining(issues, "approval.rules[0].command_prefix", "must not be empty") {
		t.Fatalf("expected empty command_prefix issue, got %v", issues)
	}
	if !hasIssueContaining(issues, "approval.rules[1].command", "ends with escape") {
		t.Fatalf("expected malformed command issue, got %v", issues)
	}
}

func TestValidatePermissionsRules(t *testing.T) {
	var cfg Config
	setDefaults(&cfg)
	cfg.Permissions.Project.Rules = []PermissionRuleConfig{
		{Behavior: "", Tool: "run_command", Pattern: "go test:*"},
		{Behavior: "maybe", Tool: "run_command", Pattern: "go test:*"},
		{Behavior: "allow", Tool: "", Pattern: "go test:*"},
		{Behavior: "deny", Tool: "write_file", Pattern: "../*.go"},
		{Behavior: "allow", Tool: "unknown_tool", Pattern: ""},
	}

	issues := cfg.Validate()
	if !hasIssueContaining(issues, "permissions.project.rules[0].behavior", "must not be empty") {
		t.Fatalf("expected empty behavior issue, got %v", issues)
	}
	if !hasIssueContaining(issues, "permissions.project.rules[1].behavior", "must be one of allow, ask, deny") {
		t.Fatalf("expected invalid behavior issue, got %v", issues)
	}
	if !hasIssueContaining(issues, "permissions.project.rules[2].tool", "must not be empty") {
		t.Fatalf("expected empty tool issue, got %v", issues)
	}
	if !hasIssueContaining(issues, "permissions.project.rules[3].pattern", "must not contain ..") {
		t.Fatalf("expected traversal pattern issue, got %v", issues)
	}
	if !hasIssueContaining(issues, "permissions.project.rules[4].tool", "unknown permission tool") {
		t.Fatalf("expected unknown tool issue, got %v", issues)
	}
}

func TestValidatePermissionsAutoConfig(t *testing.T) {
	var cfg Config
	setDefaults(&cfg)
	cfg.Permissions.Auto.Posture = "aggressive"
	cfg.Permissions.Auto.FailureBehavior = "allow"
	cfg.Permissions.Auto.MaxConsecutiveDenials = -1
	cfg.Permissions.Auto.MaxTotalDenials = 0

	issues := cfg.Validate()
	if !hasIssueContaining(issues, "permissions.auto.posture", "must be one of conservative, balanced") {
		t.Fatalf("expected posture issue, got %v", issues)
	}
	if !hasIssueContaining(issues, "permissions.auto.failure_behavior", "must be one of ask, deny") {
		t.Fatalf("expected failure behavior issue, got %v", issues)
	}
	if !hasIssueContaining(issues, "permissions.auto.max_consecutive_denials", "must be at least 1") {
		t.Fatalf("expected max consecutive issue, got %v", issues)
	}
	if !hasIssueContaining(issues, "permissions.auto.max_total_denials", "must be at least 1") {
		t.Fatalf("expected max total issue, got %v", issues)
	}
}

func TestValidateSecuritySecretsPolicy(t *testing.T) {
	var cfg Config
	setDefaults(&cfg)
	cfg.Security.Secrets.Read = "maybe"
	cfg.Security.Secrets.Write = "nope"
	cfg.Security.Secrets.CommandOutput = "hide"
	cfg.Security.Secrets.ApprovalDetail = "mask"

	issues := cfg.Validate()
	if !hasIssueContaining(issues, "security.secrets.read", "must be one of allow, redact, ask, block") {
		t.Fatalf("expected read policy issue, got %v", issues)
	}
	if !hasIssueContaining(issues, "security.secrets.write", "must be one of allow, redact, ask, block") {
		t.Fatalf("expected write policy issue, got %v", issues)
	}
	if !hasIssueContaining(issues, "security.secrets.command_output", "must be one of allow, redact, ask, block") {
		t.Fatalf("expected command_output policy issue, got %v", issues)
	}
	if !hasIssueContaining(issues, "security.secrets.approval_detail", "must be one of allow, redact, ask, block") {
		t.Fatalf("expected approval_detail policy issue, got %v", issues)
	}
}

func TestValidatePluginConfig(t *testing.T) {
	var cfg Config
	setDefaults(&cfg)
	cfg.Plugins = []PluginConfig{
		{
			ID:         "bad id",
			Command:    []string{"node", ""},
			Env:        map[string]string{"1BAD": "value"},
			InheritEnv: []string{""},
		},
		{
			ID:               "demo",
			Kind:             "unknown",
			Command:          []string{"node"},
			StartupTimeoutMS: -1,
			RequestTimeoutMS: -1,
		},
		{
			ID:      "demo",
			Command: []string{"node"},
		},
	}

	issues := cfg.Validate()
	if !hasIssueContaining(issues, "plugins[0].id", "letters") {
		t.Fatalf("expected invalid plugin id issue, got %v", issues)
	}
	if !hasIssueContaining(issues, "plugins[0].command[1]", "must not be empty") {
		t.Fatalf("expected empty command token issue, got %v", issues)
	}
	if !hasIssueContaining(issues, "plugins[0].env", "invalid environment variable") {
		t.Fatalf("expected invalid env issue, got %v", issues)
	}
	if !hasIssueContaining(issues, "plugins[1].startup_timeout_ms", ">= 0") {
		t.Fatalf("expected startup timeout issue, got %v", issues)
	}
	if !hasIssueContaining(issues, "plugins[1].kind", "forge-stdio") {
		t.Fatalf("expected plugin kind issue, got %v", issues)
	}
	if !hasIssueContaining(issues, "plugins[1].request_timeout_ms", ">= 0") {
		t.Fatalf("expected request timeout issue, got %v", issues)
	}
	if !hasIssueContaining(issues, "plugins[2].id", "duplicate") {
		t.Fatalf("expected duplicate plugin id issue, got %v", issues)
	}
}

func hasIssueContaining(issues []ValidationIssue, field, substring string) bool {
	for _, issue := range issues {
		if issue.Field == field && strings.Contains(issue.Message, substring) {
			return true
		}
	}
	return false
}
