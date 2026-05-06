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
