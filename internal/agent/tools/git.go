package tools

import (
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

func NewGitStatus(workDir string) Tool {
	secretPolicy := DefaultSecretPolicy()
	return Tool{
		Name:        "git_status",
		Description: "Show working tree status (git status --porcelain).",
		Parameters:  []ParameterDef{},
		AutoApprove: true,
		Execute: func(ctx context.Context, args map[string]any) (string, error) {
			cmd := exec.CommandContext(ctx, "git", "status", "--porcelain")
			cmd.Dir = workDir
			out, err := cmd.CombinedOutput()
			if err != nil {
				result, _ := secretPolicy.ApplyCommandOutput(fmt.Sprintf("error: %s\n%s", err, out))
				return result, nil
			}
			result, _ := secretPolicy.ApplyCommandOutput(string(out))
			return result, nil
		},
	}
}

func NewGitDiff(workDir string) Tool {
	secretPolicy := DefaultSecretPolicy()
	return Tool{
		Name:        "git_diff",
		Description: "Show changes in the working tree. Default compares against HEAD (staged + unstaged). Pass a different ref to compare against that instead.",
		Parameters: []ParameterDef{
			{Name: "ref", Type: "string", Description: "git ref to diff against (default: HEAD)", Required: false},
		},
		AutoApprove: true,
		Execute: func(ctx context.Context, args map[string]any) (string, error) {
			ref := "HEAD"
			if r, ok := args["ref"].(string); ok && r != "" {
				ref = r
			}
			gitArgs := []string{"diff", ref}
			cmd := exec.CommandContext(ctx, "git", gitArgs...)
			cmd.Dir = workDir
			out, err := cmd.CombinedOutput()
			if err != nil {
				result, _ := secretPolicy.ApplyCommandOutput(fmt.Sprintf("error: %s\n%s", err, out))
				return result, nil
			}
			result, _ := secretPolicy.ApplyCommandOutput(string(out))
			if len(result) > 50*1024 {
				result = result[:50*1024] + "\n... output truncated at 50KB"
			}
			return result, nil
		},
	}
}

func NewGitLog(workDir string) Tool {
	secretPolicy := DefaultSecretPolicy()
	return Tool{
		Name:        "git_log",
		Description: "Show recent commit history (git log --oneline).",
		Parameters: []ParameterDef{
			{Name: "count", Type: "int", Description: "number of commits to show (default 10)", Required: false},
			{Name: "path", Type: "string", Description: "optional repository-relative path to scope history, e.g. internal/tui", Required: false},
		},
		AutoApprove: true,
		Execute: func(ctx context.Context, args map[string]any) (string, error) {
			count := 10
			if v, ok := args["count"].(float64); ok && v > 0 {
				count = int(v)
			}
			gitArgs := []string{"log", "--oneline", "-n", strconv.Itoa(count)}
			if path, _ := args["path"].(string); strings.TrimSpace(path) != "" {
				gitArgs = append(gitArgs, "--", strings.TrimSpace(path))
			}
			cmd := exec.CommandContext(ctx, "git", gitArgs...)
			cmd.Dir = workDir
			out, err := cmd.CombinedOutput()
			if err != nil {
				result, _ := secretPolicy.ApplyCommandOutput(fmt.Sprintf("error: %s\n%s", err, out))
				return result, nil
			}
			result, _ := secretPolicy.ApplyCommandOutput(string(out))
			return result, nil
		},
	}
}

func NewGitCommit(workDir string, approve ApprovalFunc) Tool {
	secretPolicy := DefaultSecretPolicy()
	return Tool{
		Name:        "git_commit",
		Description: "Stage all changes and commit. Shows you what will be committed for approval.",
		Parameters: []ParameterDef{
			{Name: "message", Type: "string", Description: "commit message", Required: true},
		},
		AutoApprove: false,
		Execute: func(ctx context.Context, args map[string]any) (string, error) {
			message, _ := args["message"].(string)

			statusCmd := exec.CommandContext(ctx, "git", "status", "--porcelain")
			statusCmd.Dir = workDir
			statusOut, err := statusCmd.Output()
			if err != nil {
				return fmt.Sprintf("error checking status: %s", err), nil
			}

			if len(strings.TrimSpace(string(statusOut))) == 0 {
				return "nothing to commit", nil
			}

			summary := secretPolicy.RedactApprovalDetail(fmt.Sprintf("git commit -m %q", message))
			detail := secretPolicy.RedactApprovalDetail(fmt.Sprintf("Files to be committed:\n%s", statusOut))
			approved, err := approve(Action{
				Context: ctx,
				Tool:    "git_commit",
				Summary: summary,
				Detail:  detail,
			})
			if err != nil {
				return "", err
			}
			if !approved {
				return "git_commit denied by user", nil
			}

			addCmd := exec.CommandContext(ctx, "git", "add", "-A")
			addCmd.Dir = workDir
			if out, err := addCmd.CombinedOutput(); err != nil {
				result, _ := secretPolicy.ApplyCommandOutput(fmt.Sprintf("error staging: %s\n%s", err, out))
				return result, nil
			}

			commitCmd := exec.CommandContext(ctx, "git", "commit", "-m", message)
			commitCmd.Dir = workDir
			out, err := commitCmd.CombinedOutput()
			if err != nil {
				result, _ := secretPolicy.ApplyCommandOutput(fmt.Sprintf("error committing: %s\n%s", err, out))
				return result, nil
			}
			result, _ := secretPolicy.ApplyCommandOutput(string(out))
			return result, nil
		},
	}
}
