package tools

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
)

const gitOutputLimit = 50 * 1024

func truncateGitOutput(text string) string {
	if len(text) <= gitOutputLimit {
		return text
	}
	return text[:gitOutputLimit] + "\n... output truncated at 50KB"
}

func NewGitShow(workDir string) Tool {
	return NewGitShowWithWorkDirProvider(workDir, FixedWorkDirProvider(workDir))
}

func NewGitShowWithWorkDirProvider(fallbackWorkDir string, provider WorkDirProvider) Tool {
	secretPolicy := DefaultSecretPolicy()
	return Tool{
		Name:        "git_show",
		Description: "Show a single commit: message, metadata, and its diff. Pass a path to limit the diff to that file, or stat=true for the file list and diff stat only.",
		Parameters: []ParameterDef{
			{Name: "ref", Type: "string", Description: "commit-ish to show (default: HEAD)", Required: false},
			{Name: "path", Type: "string", Description: "optional repository-relative path to limit the diff to", Required: false},
			{Name: "stat", Type: "bool", Description: "show the diff stat instead of the full patch (default false)", Required: false},
		},
		AutoApprove: true,
		Execute: func(ctx context.Context, args map[string]any) (string, error) {
			ref := "HEAD"
			if r, _ := args["ref"].(string); strings.TrimSpace(r) != "" {
				ref = strings.TrimSpace(r)
			}
			gitArgs := []string{"show", "--no-color"}
			if stat, _ := args["stat"].(bool); stat {
				gitArgs = append(gitArgs, "--stat")
			}
			gitArgs = append(gitArgs, ref)
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
			return truncateGitOutput(result), nil
		},
	}
}

func NewGitBlame(workDir string) Tool {
	return NewGitBlameWithWorkDirProvider(workDir, FixedWorkDirProvider(workDir))
}

func NewGitBlameWithWorkDirProvider(fallbackWorkDir string, provider WorkDirProvider) Tool {
	secretPolicy := DefaultSecretPolicy()
	return Tool{
		Name:        "git_blame",
		Description: "Show which commit last touched each line of a file. Scope to a line range with start_line and end_line; blaming a whole large file is rarely what you want.",
		Parameters: []ParameterDef{
			{Name: "path", Type: "string", Description: "repository-relative path to blame", Required: true},
			{Name: "start_line", Type: "int", Description: "first line of the range (1-indexed)", Required: false},
			{Name: "end_line", Type: "int", Description: "last line of the range; defaults to start_line", Required: false},
			{Name: "ref", Type: "string", Description: "optional commit-ish to blame at (default: working tree)", Required: false},
		},
		AutoApprove: true,
		Execute: func(ctx context.Context, args map[string]any) (string, error) {
			path, _ := args["path"].(string)
			path = strings.TrimSpace(path)
			if path == "" {
				return "error: path is required", nil
			}
			gitArgs := []string{"blame"}
			start := intArg(args["start_line"], 0)
			end := intArg(args["end_line"], 0)
			if start > 0 {
				if end < start {
					end = start
				}
				gitArgs = append(gitArgs, "-L", fmt.Sprintf("%d,%d", start, end))
			}
			if ref, _ := args["ref"].(string); strings.TrimSpace(ref) != "" {
				gitArgs = append(gitArgs, strings.TrimSpace(ref))
			}
			gitArgs = append(gitArgs, "--", path)
			cmd := exec.CommandContext(ctx, "git", gitArgs...)
			cmd.Dir = currentWorkDir(provider, fallbackWorkDir)
			out, err := cmd.CombinedOutput()
			if err != nil {
				result, _ := secretPolicy.ApplyCommandOutput(fmt.Sprintf("error: %s\n%s", err, out))
				return result, nil
			}
			result, _ := secretPolicy.ApplyCommandOutput(string(out))
			return truncateGitOutput(result), nil
		},
	}
}

func NewGitWorktree(workDir string, approve ApprovalFunc) Tool {
	return NewGitWorktreeWithWorkDirProvider(workDir, FixedWorkDirProvider(workDir), approve)
}

func NewGitWorktreeWithWorkDirProvider(fallbackWorkDir string, provider WorkDirProvider, approve ApprovalFunc) Tool {
	secretPolicy := DefaultSecretPolicy()
	return Tool{
		Name:        "git_worktree",
		Description: "List, add, or remove git worktrees. mode=list is read-only; add and remove touch the filesystem and ask the user first.",
		Parameters: []ParameterDef{
			{Name: "mode", Type: "string", Description: "list, add, or remove (default: list)", Required: false},
			{Name: "path", Type: "string", Description: "worktree directory, required for add and remove", Required: false},
			{Name: "ref", Type: "string", Description: "branch or commit to check out for add; a new branch is created when it does not exist", Required: false},
		},
		AutoApprove:      false,
		MutatesWorkspace: true,
		Execute: func(ctx context.Context, args map[string]any) (string, error) {
			workDir := currentWorkDir(provider, fallbackWorkDir)
			mode := strings.ToLower(strings.TrimSpace(stringArg(args, "mode")))
			if mode == "" {
				mode = "list"
			}
			path := strings.TrimSpace(stringArg(args, "path"))

			switch mode {
			case "list":
				out, err := gitOutput(ctx, workDir, "git", "worktree", "list")
				if err != nil {
					result, _ := secretPolicy.ApplyCommandOutput(fmt.Sprintf("error listing worktrees: %s", err))
					return result, nil
				}
				result, _ := secretPolicy.ApplyCommandOutput(out)
				return strings.TrimSpace(result), nil
			case "add", "remove":
				if path == "" {
					return fmt.Sprintf("error: path is required for mode=%s", mode), nil
				}
			default:
				return fmt.Sprintf("error: unknown mode %q; use list, add, or remove", mode), nil
			}

			gitArgs := []string{"worktree", mode}
			summary := ""
			if mode == "add" {
				ref := strings.TrimSpace(stringArg(args, "ref"))
				if ref != "" && !gitRefExists(ctx, workDir, ref) {
					gitArgs = append(gitArgs, "-b", ref, path)
					summary = fmt.Sprintf("git worktree add -b %s %s", ref, path)
				} else {
					gitArgs = append(gitArgs, path)
					if ref != "" {
						gitArgs = append(gitArgs, ref)
					}
					summary = "git " + strings.Join(gitArgs, " ")
				}
			} else {
				gitArgs = append(gitArgs, path)
				summary = "git " + strings.Join(gitArgs, " ")
			}

			detail := fmt.Sprintf("Repository: %s\nWorktree path: %s", workDir, filepath.Clean(path))
			if mode == "remove" {
				detail += "\n\nRemoving a worktree deletes its working directory."
			}
			approved, err := approve(Action{
				Context: ctx,
				Tool:    "git_worktree",
				Summary: secretPolicy.RedactApprovalDetail(summary),
				Detail:  secretPolicy.RedactApprovalDetail(detail),
				Path:    path,
			})
			if err != nil {
				return "", err
			}
			if !approved {
				return "git_worktree denied by user", nil
			}

			out, err := gitCombinedOutput(ctx, workDir, gitArgs...)
			if err != nil {
				result, _ := secretPolicy.ApplyCommandOutput(fmt.Sprintf("error running %s: %s\n%s", summary, err, out))
				return result, nil
			}
			listing, listErr := gitOutput(ctx, workDir, "git", "worktree", "list")
			if listErr != nil {
				result, _ := secretPolicy.ApplyCommandOutput(strings.TrimSpace(out))
				return result, nil
			}
			result, _ := secretPolicy.ApplyCommandOutput(fmt.Sprintf("%s\n\nworktrees now:\n%s", strings.TrimSpace(out), strings.TrimSpace(listing)))
			return strings.TrimSpace(result), nil
		},
	}
}

func stringArg(args map[string]any, name string) string {
	v, _ := args[name].(string)
	return v
}

func gitRefExists(ctx context.Context, workDir, ref string) bool {
	cmd := exec.CommandContext(ctx, "git", "rev-parse", "--verify", "--quiet", ref)
	cmd.Dir = workDir
	return cmd.Run() == nil
}
