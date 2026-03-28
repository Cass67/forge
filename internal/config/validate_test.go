package config

import "testing"

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
