package tools

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
)

func NewGitBranchState(workDir string) Tool {
	return Tool{
		Name:        "git_branch_state",
		Description: "Inspect current branch, optional target branch containment, and whether the repository is in an in-progress git operation.",
		Parameters: []ParameterDef{
			{Name: "target_branch", Type: "string", Description: "optional branch name to compare against HEAD", Required: false},
		},
		AutoApprove: true,
		Execute: func(ctx context.Context, args map[string]any) (string, error) {
			targetBranch, _ := args["target_branch"].(string)
			state, err := inspectGitBranchState(ctx, workDir, strings.TrimSpace(targetBranch))
			if err != nil {
				return fmt.Sprintf("error inspecting git branch state: %v", err), nil
			}
			return state.render(), nil
		},
	}
}

type gitBranchState struct {
	currentBranch      string
	headCommit         string
	operation          string
	targetBranch       string
	targetExists       bool
	headContainsTarget string
	targetContainsHead string
}

func inspectGitBranchState(ctx context.Context, workDir, targetBranch string) (gitBranchState, error) {
	gitDir, err := resolveGitDir(ctx, workDir)
	if err != nil {
		return gitBranchState{}, err
	}
	currentBranch, err := gitOutput(ctx, workDir, "git", "branch", "--show-current")
	if err != nil {
		return gitBranchState{}, err
	}
	headCommit, err := gitOutput(ctx, workDir, "git", "rev-parse", "HEAD")
	if err != nil {
		return gitBranchState{}, err
	}
	state := gitBranchState{
		currentBranch:      strings.TrimSpace(currentBranch),
		headCommit:         strings.TrimSpace(headCommit),
		operation:          detectGitOperation(gitDir),
		targetBranch:       targetBranch,
		headContainsTarget: "unknown",
		targetContainsHead: "unknown",
	}
	if targetBranch == "" {
		return state, nil
	}

	if err := exec.CommandContext(ctx, "git", "rev-parse", "--verify", "--quiet", targetBranch).Run(); err == nil {
		state.targetExists = true
		state.headContainsTarget = boolString(gitMergeBaseIsAncestor(ctx, workDir, targetBranch, "HEAD"))
		state.targetContainsHead = boolString(gitMergeBaseIsAncestor(ctx, workDir, "HEAD", targetBranch))
	}
	return state, nil
}

func gitOutput(ctx context.Context, workDir string, argv ...string) (string, error) {
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = workDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("%v: %s", err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

func gitMergeBaseIsAncestor(ctx context.Context, workDir, a, b string) bool {
	cmd := exec.CommandContext(ctx, "git", "merge-base", "--is-ancestor", a, b)
	cmd.Dir = workDir
	return cmd.Run() == nil
}

func boolString(v bool) string {
	if v {
		return "true"
	}
	return "false"
}

func (s gitBranchState) render() string {
	var sb strings.Builder
	sb.WriteString("current_branch: ")
	sb.WriteString(emptyToUnknown(s.currentBranch))
	sb.WriteString("\n")
	sb.WriteString("head_commit: ")
	sb.WriteString(emptyToUnknown(s.headCommit))
	sb.WriteString("\n")
	sb.WriteString("operation: ")
	sb.WriteString(emptyToUnknown(s.operation))
	sb.WriteString("\n")
	if strings.TrimSpace(s.targetBranch) == "" {
		sb.WriteString("target_branch: none\n")
		sb.WriteString("target_exists: unknown\n")
		sb.WriteString("head_contains_target: unknown\n")
		sb.WriteString("target_contains_head: unknown")
		return sb.String()
	}
	sb.WriteString("target_branch: ")
	sb.WriteString(s.targetBranch)
	sb.WriteString("\n")
	sb.WriteString("target_exists: ")
	sb.WriteString(boolString(s.targetExists))
	sb.WriteString("\n")
	sb.WriteString("head_contains_target: ")
	sb.WriteString(s.headContainsTarget)
	sb.WriteString("\n")
	sb.WriteString("target_contains_head: ")
	sb.WriteString(s.targetContainsHead)
	return sb.String()
}

func emptyToUnknown(value string) string {
	if strings.TrimSpace(value) == "" {
		return "unknown"
	}
	return strings.TrimSpace(value)
}
