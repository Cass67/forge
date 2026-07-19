package tools

import (
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

func NewGitStatus(workDir string) Tool {
	return NewGitStatusWithWorkDirProvider(workDir, FixedWorkDirProvider(workDir))
}

func NewGitStatusWithWorkDirProvider(fallbackWorkDir string, provider WorkDirProvider) Tool {
	secretPolicy := DefaultSecretPolicy()
	return Tool{
		Name:        "git_status",
		Description: "Show working tree status (git status --porcelain).",
		Parameters:  []ParameterDef{},
		AutoApprove: true,
		Execute: func(ctx context.Context, args map[string]any) (string, error) {
			cmd := exec.CommandContext(ctx, "git", "status", "--porcelain")
			cmd.Dir = currentWorkDir(provider, fallbackWorkDir)
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
	return NewGitDiffWithWorkDirProvider(workDir, FixedWorkDirProvider(workDir))
}

func NewGitDiffWithWorkDirProvider(fallbackWorkDir string, provider WorkDirProvider) Tool {
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
			cmd.Dir = currentWorkDir(provider, fallbackWorkDir)
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
	return NewGitLogWithWorkDirProvider(workDir, FixedWorkDirProvider(workDir))
}

func NewGitLogWithWorkDirProvider(fallbackWorkDir string, provider WorkDirProvider) Tool {
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
			cmd.Dir = currentWorkDir(provider, fallbackWorkDir)
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
	return NewGitCommitWithWorkDirProvider(workDir, FixedWorkDirProvider(workDir), approve)
}
