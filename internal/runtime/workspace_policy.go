package runtime

import (
	"context"
	"fmt"
	"os/exec"
	"strings"

	"forge/internal/gitutil"
	"forge/internal/harness"
)

type workspacePolicy struct {
	workDir string
}

func newWorkspacePolicy(workDir string) harness.WorkspacePolicy {
	if strings.TrimSpace(workDir) == "" {
		return nil
	}
	return &workspacePolicy{workDir: workDir}
}

func (p *workspacePolicy) EnsureExecutionContext(_ context.Context, turn harness.UserTurn, class harness.Classification, _ harness.SessionState) (harness.ProgressMilestone, error) {
	if p == nil || strings.TrimSpace(p.workDir) == "" {
		return harness.ProgressMilestone{}, nil
	}
	if !requiresProtectedBranchTransition(class) {
		return harness.ProgressMilestone{}, nil
	}
	isRepo, err := gitutil.IsRepository(p.workDir)
	if err != nil {
		return harness.ProgressMilestone{}, fmt.Errorf("detect git repository: %w", err)
	}
	if !isRepo {
		return harness.ProgressMilestone{}, nil
	}

	current, err := gitutil.CurrentBranch(p.workDir)
	if err != nil {
		return harness.ProgressMilestone{}, fmt.Errorf("detect current branch: %w", err)
	}
	if !isProtectedBranch(current) {
		return harness.ProgressMilestone{}, nil
	}

	target := policyBranchName(turn, class)
	if target == "" {
		target = "forge/task"
	}
	exists, err := gitutil.BranchExists(p.workDir, target)
	if err != nil {
		return harness.ProgressMilestone{}, fmt.Errorf("check branch %q: %w", target, err)
	}
	if exists {
		if err := checkoutBranch(p.workDir, target); err != nil {
			return harness.ProgressMilestone{}, fmt.Errorf("checkout branch %q: %w", target, err)
		}
	} else if err := gitutil.CheckoutNewBranch(p.workDir, target); err != nil {
		return harness.ProgressMilestone{}, fmt.Errorf("create branch %q: %w", target, err)
	}
	return harness.ProgressMilestone{
		Kind:    harness.ProgressMilestoneTool,
		Message: "Switched to branch " + target,
	}, nil
}

func requiresProtectedBranchTransition(class harness.Classification) bool {
	if !class.WantsAction {
		return false
	}
	switch class.Family {
	case harness.FamilyImplement, harness.FamilyDebug, harness.FamilyVerify:
		return true
	default:
		return false
	}
}

func isProtectedBranch(branch string) bool {
	branch = strings.TrimSpace(branch)
	return branch == "main" || branch == "master"
}

func policyBranchName(turn harness.UserTurn, class harness.Classification) string {
	base := firstNonEmpty(strings.TrimSpace(class.TaskText), strings.TrimSpace(turn.Text), strings.TrimSpace(class.TopicKey))
	slug := branchSlug(base)
	turnNum := turn.Turn
	if turnNum <= 0 {
		turnNum = 1
	}
	return fmt.Sprintf("forge/%s-%d", slug, turnNum)
}

func branchSlug(input string) string {
	input = strings.ToLower(strings.TrimSpace(input))
	if input == "" {
		return "task"
	}
	var b strings.Builder
	lastDash := false
	for _, r := range input {
		isAlphaNum := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
		if isAlphaNum {
			b.WriteRune(r)
			lastDash = false
		} else if !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
		if b.Len() >= 40 {
			break
		}
	}
	slug := strings.Trim(b.String(), "-")
	if slug == "" {
		return "task"
	}
	return slug
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func checkoutBranch(dir, branch string) error {
	branch = strings.TrimSpace(branch)
	if branch == "" {
		return fmt.Errorf("branch name is required")
	}
	cmd := exec.Command("git", "checkout", branch)
	cmd.Dir = dir
	return cmd.Run()
}
