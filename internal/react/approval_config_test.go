package react

import (
	"testing"

	"forge/internal/agent/tools"
	"forge/internal/config"
)

func TestLoadApprovalConfigApprovalRulesFromConfig(t *testing.T) {
	cfg := &config.Config{}
	cfg.Approval.DefaultPolicy = "unless_trusted"
	cfg.Approval.SandboxPolicy = "read_only"
	cfg.Approval.KnownSafePrefixes = []string{"git status", "go test"}
	cfg.Approval.Rules = []config.ApprovalRuleConfig{
		{
			Tool:          "run_command",
			CommandPrefix: []string{"git", "push"},
			Decision:      "forbidden",
		},
		{
			Tool:     "run_command",
			Command:  "git status --short",
			Decision: "allow",
		},
		{
			Tool:     "run_command",
			Command:  "git * status",
			Decision: "prompt",
		},
	}

	got := LoadApprovalConfig(cfg)
	if got.DefaultPolicy != ApprovalUnlessTrusted {
		t.Fatalf("DefaultPolicy = %q", got.DefaultPolicy)
	}
	if got.SandboxPolicy != SandboxReadOnly {
		t.Fatalf("SandboxPolicy = %q", got.SandboxPolicy)
	}
	if len(got.KnownSafeCommand) != 2 {
		t.Fatalf("KnownSafeCommand = %d, want 2", len(got.KnownSafeCommand))
	}
	if len(got.Rules) != 3 {
		t.Fatalf("Rules = %d, want 3", len(got.Rules))
	}
	if got.Rules[0].Decision != DecisionForbidden {
		t.Fatalf("Decision = %q", got.Rules[0].Decision)
	}
	if got.Rules[0].CommandPrefix[0] != "git" || got.Rules[0].CommandPrefix[1] != "push" {
		t.Fatalf("CommandPrefix = %#v", got.Rules[0].CommandPrefix)
	}
	if got.Rules[1].Command != "git status --short" {
		t.Fatalf("Rules[1].Command = %q", got.Rules[1].Command)
	}
	if got.Rules[2].Command != "git * status" {
		t.Fatalf("Rules[2].Command = %q", got.Rules[2].Command)
	}
}

func TestLoadApprovalConfigApprovalPreservesToolOnlyRules(t *testing.T) {
	cfg := &config.Config{}
	cfg.Approval.Rules = []config.ApprovalRuleConfig{
		{
			Tool:     "write_file",
			Decision: "forbidden",
		},
	}

	got := LoadApprovalConfig(cfg)
	if len(got.Rules) != 1 {
		t.Fatalf("Rules = %d, want 1", len(got.Rules))
	}
	if got.Rules[0].Tool != "write_file" {
		t.Fatalf("Tool = %q", got.Rules[0].Tool)
	}
	if got.Rules[0].Decision != DecisionForbidden {
		t.Fatalf("Decision = %q", got.Rules[0].Decision)
	}
	if got.Rules[0].Command != "" || len(got.Rules[0].CommandPrefix) != 0 {
		t.Fatalf("unexpected matcher on tool-only rule: %#v", got.Rules[0])
	}
}

func TestLoadApprovalConfigApprovalGateMatchesCommandRules(t *testing.T) {
	cfg := &config.Config{}
	cfg.Approval.DefaultPolicy = "on_request"
	cfg.Approval.SandboxPolicy = "workspace_write"
	cfg.Approval.Rules = []config.ApprovalRuleConfig{
		{
			Tool:     "run_command",
			Command:  "git status --short",
			Decision: "allow",
		},
		{
			Tool:     "run_command",
			Command:  "git * status",
			Decision: "forbidden",
		},
	}

	promptCalls := 0
	gate := NewApprovalGate("", LoadApprovalConfig(cfg), func(action tools.Action) (bool, error) {
		promptCalls++
		return true, nil
	}, nil)

	exactApproved, err := gate.Approve(tools.Action{
		Tool:    "run_command",
		Summary: "git status --short",
		Detail:  "git status --short",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !exactApproved {
		t.Fatal("expected exact command rule to allow")
	}

	wildcardApproved, err := gate.Approve(tools.Action{
		Tool:    "run_command",
		Summary: "git commit status",
		Detail:  "git commit status",
	})
	if err != nil {
		t.Fatal(err)
	}
	if wildcardApproved {
		t.Fatal("expected wildcard command rule to forbid")
	}
	if promptCalls != 0 {
		t.Fatalf("prompt calls = %d, want 0", promptCalls)
	}
}

func TestLoadApprovalConfigApprovalGateAppliesToolOnlyRules(t *testing.T) {
	cfg := &config.Config{}
	cfg.Approval.DefaultPolicy = "on_request"
	cfg.Approval.SandboxPolicy = "workspace_write"
	cfg.Approval.Rules = []config.ApprovalRuleConfig{
		{
			Tool:     "write_file",
			Decision: "forbidden",
		},
	}

	promptCalls := 0
	gate := NewApprovalGate("", LoadApprovalConfig(cfg), func(action tools.Action) (bool, error) {
		promptCalls++
		return true, nil
	}, nil)

	approved, err := gate.Approve(tools.Action{
		Tool:    "write_file",
		Summary: "write internal/app.go",
		Detail:  "write internal/app.go",
	})
	if err != nil {
		t.Fatal(err)
	}
	if approved {
		t.Fatal("expected tool-only rule to forbid")
	}
	if promptCalls != 0 {
		t.Fatalf("prompt calls = %d, want 0", promptCalls)
	}
}

func TestLoadApprovalConfigApprovalFallsBackOnInvalidValues(t *testing.T) {
	cfg := &config.Config{}
	cfg.Approval.DefaultPolicy = "sometimes"
	cfg.Approval.SandboxPolicy = "sandboxed"
	cfg.Approval.Rules = []config.ApprovalRuleConfig{
		{
			Tool:          "run_command",
			CommandPrefix: []string{"git", "push"},
			Decision:      "maybe",
		},
	}

	got := LoadApprovalConfig(cfg)
	if got.DefaultPolicy != ApprovalOnRequest {
		t.Fatalf("DefaultPolicy = %q, want %q", got.DefaultPolicy, ApprovalOnRequest)
	}
	if got.SandboxPolicy != SandboxWorkspaceWrite {
		t.Fatalf("SandboxPolicy = %q, want %q", got.SandboxPolicy, SandboxWorkspaceWrite)
	}
	if len(got.Rules) != 0 {
		t.Fatalf("Rules = %d, want 0", len(got.Rules))
	}
}
