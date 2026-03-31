package tools

import "testing"

func TestReviewApprovalActionBlocksDestructiveCommand(t *testing.T) {
	review := ReviewApprovalAction("task: clean build artifacts", Action{
		Tool:    "run_command",
		Summary: "rm -rf /",
		Detail:  "rm -rf /",
	})
	if review.Decision != GuardianBlock {
		t.Fatalf("decision = %q, want block", review.Decision)
	}
}

func TestReviewApprovalActionWarnsOnMutatingCommandWithoutContext(t *testing.T) {
	review := ReviewApprovalAction("", Action{
		Tool:    "run_command",
		Summary: "git merge feature/runtime",
		Detail:  "git merge feature/runtime",
	})
	if review.Decision != GuardianWarn {
		t.Fatalf("decision = %q, want warn", review.Decision)
	}
}

func TestReviewApprovalActionAllowsEditWithDetail(t *testing.T) {
	review := ReviewApprovalAction("task: patch runtime flow", Action{
		Tool:    "edit_file",
		Summary: "edit internal/runtime/chat.go",
		Detail:  "--- a/internal/runtime/chat.go",
	})
	if review.Decision != GuardianAllow {
		t.Fatalf("decision = %q, want allow", review.Decision)
	}
}
