package tools

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strings"
)

func NewGitCommitWithWorkDirProvider(fallbackWorkDir string, workDirProvider WorkDirProvider, approve ApprovalFunc) Tool {
	secretPolicy := DefaultSecretPolicy()
	var lastDiff string
	return Tool{
		Name:        "git_commit",
		Description: "Stage and commit the working-tree changes. Shows the user the file list and diff stat for approval before committing.",
		Parameters: []ParameterDef{
			{Name: "message", Type: "string", Description: "commit message", Required: true},
		},
		AutoApprove:      false,
		MutatesWorkspace: true,
		LastDiff: func() string {
			diff := lastDiff
			lastDiff = ""
			return diff
		},
		Execute: func(ctx context.Context, args map[string]any) (string, error) {
			lastDiff = ""
			workDir := currentWorkDir(workDirProvider, fallbackWorkDir)
			message, _ := args["message"].(string)
			if out, err := gitCombinedOutput(ctx, workDir, "add", "-A"); err != nil {
				result, _ := secretPolicy.ApplyCommandOutput(fmt.Sprintf("error staging changes: %s\n%s", err, out))
				return result, nil
			}

			staged, err := gitNulPaths(ctx, workDir, "diff", "--cached", "--name-only", "-z")
			if err != nil {
				result, _ := secretPolicy.ApplyCommandOutput(fmt.Sprintf("error checking staged files: %s", err))
				return result, nil
			}
			if len(staged) == 0 {
				return "nothing to commit", nil
			}

			stat, err := gitOutput(ctx, workDir, "git", "diff", "--cached", "--stat")
			if err != nil {
				result, _ := secretPolicy.ApplyCommandOutput(fmt.Sprintf("error generating staged diff stat: %s\n%s", err, stat))
				return result, nil
			}
			detail := fmt.Sprintf("Files to be committed:\n%s\n\nDiff stat:\n%s", strings.Join(staged, "\n"), stat)
			approved, err := approve(Action{
				Context: ctx,
				Tool:    "git_commit",
				Summary: secretPolicy.RedactApprovalDetail(fmt.Sprintf("git commit -m %q", message)),
				Detail:  secretPolicy.RedactApprovalDetail(detail),
			})
			if err != nil {
				return "", err
			}
			if !approved {
				return "git_commit denied by user", nil
			}

			out, err := gitCombinedOutput(ctx, workDir, "commit", "-m", message)
			if err != nil {
				result, _ := secretPolicy.ApplyCommandOutput(fmt.Sprintf("error committing: %s\n%s", err, out))
				return result, nil
			}
			commitFiles, err := gitOutputLines(ctx, workDir, "show", "--name-only", "--format=", "HEAD")
			if err != nil {
				result, _ := secretPolicy.ApplyCommandOutput(fmt.Sprintf("committed, but failed to verify commit files: %s", err))
				return result, nil
			}
			if !sameStringSet(staged, commitFiles) {
				return fmt.Sprintf("committed files did not match approved staged files; staged=%s committed=%s", strings.Join(staged, ", "), strings.Join(commitFiles, ", ")), nil
			}
			hash, err := gitOutput(ctx, workDir, "git", "rev-parse", "--short", "HEAD")
			if err != nil {
				result, _ := secretPolicy.ApplyCommandOutput(fmt.Sprintf("committed, but failed to read commit hash: %s", err))
				return result, nil
			}
			lastDiff = strings.TrimSpace(detail)
			result := fmt.Sprintf("commit %s created with files:\n%s", strings.TrimSpace(hash), strings.Join(commitFiles, "\n"))
			result, _ = secretPolicy.ApplyCommandOutput(result)
			return result, nil
		},
	}
}

func NewGitPush(workDir string, approve ApprovalFunc) Tool {
	return NewGitPushWithWorkDirProvider(workDir, FixedWorkDirProvider(workDir), approve)
}

func NewGitPushWithWorkDirProvider(fallbackWorkDir string, workDirProvider WorkDirProvider, approve ApprovalFunc) Tool {
	secretPolicy := DefaultSecretPolicy()
	return Tool{
		Name:             "git_push",
		Description:      "Push the current HEAD to the remote (origin by default) for the current branch, after user approval, then verify the remote advertises that commit.",
		AutoApprove:      false,
		MutatesWorkspace: true,
		Execute: func(ctx context.Context, args map[string]any) (string, error) {
			_ = args
			workDir := currentWorkDir(workDirProvider, fallbackWorkDir)
			remote, err := defaultGitRemote(ctx, workDir)
			if err != nil {
				return "git_push failed: no configured remote to push to", nil
			}
			current, err := gitOutput(ctx, workDir, "git", "branch", "--show-current")
			if err != nil || strings.TrimSpace(current) == "" {
				return "git_push failed: cannot determine current branch (detached HEAD?)", nil
			}
			targetBranch := strings.TrimSpace(current)

			head, err := gitOutput(ctx, workDir, "git", "rev-parse", "HEAD")
			if err != nil {
				result, _ := secretPolicy.ApplyCommandOutput(fmt.Sprintf("error reading HEAD: %s\n%s", err, head))
				return result, nil
			}
			head = strings.TrimSpace(head)
			remoteRef := "refs/heads/" + targetBranch
			detail := fmt.Sprintf("Current branch: %s\nHEAD: %s\nRemote: %s\nRemote ref: %s", targetBranch, head, remote, remoteRef)
			approved, err := approve(Action{
				Context: ctx,
				Tool:    "git_push",
				Summary: secretPolicy.RedactApprovalDetail(fmt.Sprintf("git push %s HEAD:%s", remote, remoteRef)),
				Detail:  secretPolicy.RedactApprovalDetail(detail),
			})
			if err != nil {
				return "", err
			}
			if !approved {
				return "git_push denied by user", nil
			}

			if out, err := gitCombinedOutput(ctx, workDir, "push", remote, "HEAD:"+remoteRef); err != nil {
				result, _ := secretPolicy.ApplyCommandOutput(fmt.Sprintf("error pushing: %s\n%s", err, out))
				return result, nil
			}
			remoteOut, err := gitOutput(ctx, workDir, "git", "ls-remote", remote, remoteRef)
			if err != nil {
				result, _ := secretPolicy.ApplyCommandOutput(fmt.Sprintf("pushed, but failed to verify remote: %s\n%s", err, remoteOut))
				return result, nil
			}
			remoteSHA := strings.Fields(remoteOut)
			if len(remoteSHA) == 0 || remoteSHA[0] != head {
				return fmt.Sprintf("pushed, but remote %s %s advertised %q instead of %s", remote, remoteRef, strings.TrimSpace(remoteOut), head), nil
			}
			result := fmt.Sprintf("remote contains %s at %s/%s", head, remote, targetBranch)
			result, _ = secretPolicy.ApplyCommandOutput(result)
			return result, nil
		},
	}
}

func defaultGitRemote(ctx context.Context, workDir string) (string, error) {
	out, err := gitOutput(ctx, workDir, "git", "remote")
	if err != nil {
		return "", err
	}
	remotes := strings.Fields(out)
	if len(remotes) == 0 {
		return "", fmt.Errorf("no remotes configured")
	}
	if slices.Contains(remotes, "origin") {
		return "origin", nil
	}
	return remotes[0], nil
}

func gitCombinedOutput(ctx context.Context, workDir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = workDir
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func gitNulPaths(ctx context.Context, workDir string, args ...string) ([]string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = workDir
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	parts := bytes.Split(out, []byte{0})
	paths := make([]string, 0, len(parts))
	for _, part := range parts {
		if len(part) == 0 {
			continue
		}
		paths = append(paths, filepath.ToSlash(string(part)))
	}
	sort.Strings(paths)
	return paths, nil
}

func gitOutputLines(ctx context.Context, workDir string, args ...string) ([]string, error) {
	out, err := gitOutput(ctx, workDir, append([]string{"git"}, args...)...)
	if err != nil {
		return nil, err
	}
	var lines []string
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			lines = append(lines, filepath.ToSlash(line))
		}
	}
	sort.Strings(lines)
	return lines, nil
}

func sameStringSet(a, b []string) bool {
	a = slices.Clone(a)
	b = slices.Clone(b)
	sort.Strings(a)
	sort.Strings(b)
	return slices.Equal(a, b)
}
