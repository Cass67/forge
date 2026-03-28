package react

import (
	"testing"

	"forge/internal/config"
)

func TestLoadApprovalConfigFromConfig(t *testing.T) {
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
	if len(got.Rules) != 1 {
		t.Fatalf("Rules = %d, want 1", len(got.Rules))
	}
	if got.Rules[0].Decision != DecisionForbidden {
		t.Fatalf("Decision = %q", got.Rules[0].Decision)
	}
}

func TestLoadApprovalConfigFallsBackOnInvalidValues(t *testing.T) {
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
