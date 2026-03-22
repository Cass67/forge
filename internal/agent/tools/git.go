package tools

import (
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

func NewGitStatus(workDir string) Tool {
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
				return fmt.Sprintf("error: %s\n%s", err, out), nil
			}
			return string(out), nil
		},
	}
}

func NewGitDiff(workDir string) Tool {
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
				return fmt.Sprintf("error: %s\n%s", err, out), nil
			}
			result := string(out)
			if len(result) > 50*1024 {
				result = result[:50*1024] + "\n... output truncated at 50KB"
			}
			return result, nil
		},
	}
}

func NewGitLog(workDir string) Tool {
	return Tool{
		Name:        "git_log",
		Description: "Show recent commit history (git log --oneline).",
		Parameters: []ParameterDef{
			{Name: "count", Type: "int", Description: "number of commits to show (default 10)", Required: false},
		},
		AutoApprove: true,
		Execute: func(ctx context.Context, args map[string]any) (string, error) {
			count := 10
			if v, ok := args["count"].(float64); ok && v > 0 {
				count = int(v)
			}
			cmd := exec.CommandContext(ctx, "git", "log", "--oneline", "-n", strconv.Itoa(count))
			cmd.Dir = workDir
			out, err := cmd.CombinedOutput()
			if err != nil {
				return fmt.Sprintf("error: %s\n%s", err, out), nil
			}
			return string(out), nil
		},
	}
}

func NewGitCommit(workDir string, approve ApprovalFunc) Tool {
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

			approved, err := approve(Action{
				Tool:    "git_commit",
				Summary: fmt.Sprintf("git commit -m %q", message),
				Detail:  fmt.Sprintf("Files to be committed:\n%s", statusOut),
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
				return fmt.Sprintf("error staging: %s\n%s", err, out), nil
			}

			commitCmd := exec.CommandContext(ctx, "git", "commit", "-m", message)
			commitCmd.Dir = workDir
			out, err := commitCmd.CombinedOutput()
			if err != nil {
				return fmt.Sprintf("error committing: %s\n%s", err, out), nil
			}
			return string(out), nil
		},
	}
}
