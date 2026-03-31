package react

import (
	"strings"
	"testing"

	"forge/internal/agent/tools"
)

func TestApprovalDecisionRecordsRuleBasedPaths(t *testing.T) {
	cases := []struct {
		name              string
		decision          RuleDecision
		wantApproved      bool
		wantDecision      ApprovalDecision
		wantReasonContain string
	}{
		{
			name:              "allow",
			decision:          DecisionAllow,
			wantApproved:      true,
			wantDecision:      ApprovalDecisionAllow,
			wantReasonContain: "rule allowed",
		},
		{
			name:              "prompt",
			decision:          DecisionPrompt,
			wantApproved:      true,
			wantDecision:      ApprovalDecisionPrompt,
			wantReasonContain: "rule requires prompt",
		},
		{
			name:              "forbid",
			decision:          DecisionForbidden,
			wantApproved:      false,
			wantDecision:      ApprovalDecisionForbidden,
			wantReasonContain: "rule forbade",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			promptCalls := 0
			gate := NewApprovalGate("", ApprovalConfig{
				DefaultPolicy: ApprovalNever,
				SandboxPolicy: SandboxWorkspaceWrite,
				Rules: []ApprovalRule{
					{
						Tool:          "run_command",
						CommandPrefix: []string{"git", "status"},
						Decision:      tc.decision,
					},
				},
			}, func(action tools.Action) (bool, error) {
				promptCalls++
				return true, nil
			}, nil)

			approved, err := gate.Approve(tools.Action{
				Tool:    "run_command",
				Summary: "git status --short",
				Detail:  "git status --short",
			})
			if err != nil {
				t.Fatal(err)
			}
			if approved != tc.wantApproved {
				t.Fatalf("approved = %v, want %v", approved, tc.wantApproved)
			}
			if tc.decision == DecisionPrompt && promptCalls != 1 {
				t.Fatalf("prompt calls = %d, want 1", promptCalls)
			}
			if tc.decision != DecisionPrompt && promptCalls != 0 {
				t.Fatalf("prompt calls = %d, want 0", promptCalls)
			}

			updates := gate.ApprovalUpdates()
			if len(updates) != 1 {
				t.Fatalf("update count = %d, want 1", len(updates))
			}
			got := updates[0]
			if got.Decision != tc.wantDecision {
				t.Fatalf("decision = %q, want %q", got.Decision, tc.wantDecision)
			}
			if got.Source != ApprovalDecisionSourceRule {
				t.Fatalf("source = %q, want %q", got.Source, ApprovalDecisionSourceRule)
			}
			if !strings.Contains(got.Reason, tc.wantReasonContain) {
				t.Fatalf("reason = %q, want to contain %q", got.Reason, tc.wantReasonContain)
			}
		})
	}
}

func TestApprovalDecisionRecordsSandboxDeniedPromptEscalation(t *testing.T) {
	promptCalls := 0
	gate := NewApprovalGate("", ApprovalConfig{
		DefaultPolicy: ApprovalOnFailure,
		SandboxPolicy: SandboxReadOnly,
	}, func(action tools.Action) (bool, error) {
		promptCalls++
		return true, nil
	}, nil)

	approved, err := gate.Approve(tools.Action{
		Tool:    "write_file",
		Summary: "write internal/app.go",
		Detail:  "new file",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !approved {
		t.Fatal("expected sandbox escalation prompt to approve")
	}
	if promptCalls != 1 {
		t.Fatalf("prompt calls = %d, want 1", promptCalls)
	}

	updates := gate.ApprovalUpdates()
	if len(updates) != 1 {
		t.Fatalf("update count = %d, want 1", len(updates))
	}
	got := updates[0]
	if got.Decision != ApprovalDecisionPrompt {
		t.Fatalf("decision = %q, want %q", got.Decision, ApprovalDecisionPrompt)
	}
	if got.Source != ApprovalDecisionSourceSandbox {
		t.Fatalf("source = %q, want %q", got.Source, ApprovalDecisionSourceSandbox)
	}
	for _, want := range []string{"sandbox denied", "write internal/app.go"} {
		if !strings.Contains(got.Reason, want) {
			t.Fatalf("reason = %q, want to contain %q", got.Reason, want)
		}
	}
}

func TestApprovalDecisionRecordsSandboxForbiddenOutcome(t *testing.T) {
	gate := NewApprovalGate("", ApprovalConfig{
		DefaultPolicy: ApprovalNever,
		SandboxPolicy: SandboxReadOnly,
	}, func(action tools.Action) (bool, error) {
		t.Fatal("prompt should not be called for sandbox-forbidden action")
		return false, nil
	}, nil)

	approved, err := gate.Approve(tools.Action{
		Tool:    "write_file",
		Summary: "write internal/app.go",
		Detail:  "new file",
	})
	if err != nil {
		t.Fatal(err)
	}
	if approved {
		t.Fatal("expected sandbox-forbidden action to be rejected")
	}

	updates := gate.ApprovalUpdates()
	if len(updates) != 1 {
		t.Fatalf("update count = %d, want 1", len(updates))
	}
	got := updates[0]
	if got.Decision != ApprovalDecisionForbidden {
		t.Fatalf("decision = %q, want %q", got.Decision, ApprovalDecisionForbidden)
	}
	if got.Source != ApprovalDecisionSourceSandbox {
		t.Fatalf("source = %q, want %q", got.Source, ApprovalDecisionSourceSandbox)
	}
	if !strings.Contains(got.Reason, "sandbox denied") {
		t.Fatalf("reason = %q, want sandbox denial wording", got.Reason)
	}
}

func TestApprovalDecisionRecordsTrustedCommandAutoApproval(t *testing.T) {
	promptCalls := 0
	gate := NewApprovalGate("", ApprovalConfig{
		DefaultPolicy:    ApprovalUnlessTrusted,
		SandboxPolicy:    SandboxWorkspaceWrite,
		KnownSafeCommand: []string{"git status"},
	}, func(action tools.Action) (bool, error) {
		promptCalls++
		return true, nil
	}, nil)

	approved, err := gate.Approve(tools.Action{
		Tool:    "run_command",
		Summary: "git status --short",
		Detail:  "git status --short",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !approved {
		t.Fatal("expected trusted command to auto-approve")
	}
	if promptCalls != 0 {
		t.Fatalf("prompt calls = %d, want 0", promptCalls)
	}

	updates := gate.ApprovalUpdates()
	if len(updates) != 1 {
		t.Fatalf("update count = %d, want 1", len(updates))
	}
	got := updates[0]
	if got.Decision != ApprovalDecisionAllow {
		t.Fatalf("decision = %q, want %q", got.Decision, ApprovalDecisionAllow)
	}
	if got.Source != ApprovalDecisionSourceTrusted {
		t.Fatalf("source = %q, want %q", got.Source, ApprovalDecisionSourceTrusted)
	}
	if !strings.Contains(got.Reason, "trusted command auto-approved") {
		t.Fatalf("reason = %q, want trusted auto-approval wording", got.Reason)
	}
}

func TestApprovalDecisionRecordsGuardianWarnForTrustedCommand(t *testing.T) {
	promptCalls := 0
	gate := NewApprovalGate("", ApprovalConfig{
		DefaultPolicy:    ApprovalUnlessTrusted,
		SandboxPolicy:    SandboxWorkspaceWrite,
		KnownSafeCommand: []string{"git status"},
	}, func(action tools.Action) (bool, error) {
		promptCalls++
		return true, nil
	}, nil)
	gate.SetGuardianReviewer(func(transcript string, action tools.Action) tools.GuardianReview {
		return tools.GuardianReview{
			Decision: tools.GuardianWarn,
			Reason:   "needs context",
		}
	})

	approved, err := gate.Approve(tools.Action{
		Tool:    "run_command",
		Summary: "git status --short",
		Detail:  "git status --short",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !approved {
		t.Fatal("expected warned trusted command to prompt and approve")
	}
	if promptCalls != 1 {
		t.Fatalf("prompt calls = %d, want 1", promptCalls)
	}

	updates := gate.ApprovalUpdates()
	if len(updates) != 1 {
		t.Fatalf("update count = %d, want 1", len(updates))
	}
	got := updates[0]
	if got.Decision != ApprovalDecisionPrompt {
		t.Fatalf("decision = %q, want %q", got.Decision, ApprovalDecisionPrompt)
	}
	if got.Source != ApprovalDecisionSourceGuardian {
		t.Fatalf("source = %q, want %q", got.Source, ApprovalDecisionSourceGuardian)
	}
	if !strings.Contains(got.Reason, "guardian warning required prompt") {
		t.Fatalf("reason = %q, want guardian warning wording", got.Reason)
	}
}

func TestApprovalDecisionReasonFormatting(t *testing.T) {
	cases := []struct {
		name     string
		decision ApprovalDecision
		source   ApprovalDecisionSource
		detail   string
		want     string
	}{
		{
			name:     "rule allow",
			decision: ApprovalDecisionAllow,
			source:   ApprovalDecisionSourceRule,
			detail:   "matched git status",
			want:     "rule allowed: matched git status",
		},
		{
			name:     "rule prompt",
			decision: ApprovalDecisionPrompt,
			source:   ApprovalDecisionSourceRule,
			detail:   "matched git status",
			want:     "rule requires prompt: matched git status",
		},
		{
			name:     "rule forbid",
			decision: ApprovalDecisionForbidden,
			source:   ApprovalDecisionSourceRule,
			detail:   "matched git push",
			want:     "rule forbade: matched git push",
		},
		{
			name:     "sandbox prompt",
			decision: ApprovalDecisionPrompt,
			source:   ApprovalDecisionSourceSandbox,
			detail:   "write internal/app.go",
			want:     "sandbox denied; prompting for approval: write internal/app.go",
		},
		{
			name:     "trusted allow",
			decision: ApprovalDecisionAllow,
			source:   ApprovalDecisionSourceTrusted,
			detail:   "git status --short",
			want:     "trusted command auto-approved: git status --short",
		},
		{
			name:     "guardian forbid",
			decision: ApprovalDecisionForbidden,
			source:   ApprovalDecisionSourceGuardian,
			detail:   "action looks destructive and should not be auto-approved",
			want:     "guardian blocked: action looks destructive and should not be auto-approved",
		},
		{
			name:     "policy allow",
			decision: ApprovalDecisionAllow,
			source:   ApprovalDecisionSourcePolicy,
			detail:   "write foo.txt",
			want:     "policy allowed: write foo.txt",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := FormatApprovalReason(tc.decision, tc.source, tc.detail)
			if got != tc.want {
				t.Fatalf("FormatApprovalReason() = %q, want %q", got, tc.want)
			}
		})
	}
}
