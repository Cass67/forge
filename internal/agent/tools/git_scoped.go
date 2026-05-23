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

const gitCommitRequiresScopeMessage = "blocked: git_commit requires an active side-effect intent with allowed_paths; use scoped git transaction tools after the runtime captures the requested target files."

const gitPushRequiresScopeMessage = "blocked: git_push requires an active side-effect intent with remote and target_branch."

type GitScope struct {
	AllowedPaths  []string
	TargetBranch  string
	Remote        string
	RequireBranch bool
}

type GitScopeProvider func() GitScope

func NewGitCommitScoped(workDir string, approve ApprovalFunc, scope GitScopeProvider) Tool {
	return NewGitCommitScopedWithWorkDirProvider(workDir, FixedWorkDirProvider(workDir), approve, scope)
}

func NewGitCommitScopedWithWorkDirProvider(fallbackWorkDir string, workDirProvider WorkDirProvider, approve ApprovalFunc, scope GitScopeProvider) Tool {
	secretPolicy := DefaultSecretPolicy()
	var lastDiff string
	return Tool{
		Name:        "git_commit",
		Description: "Commit only files in the active side-effect intent allowlist. Shows you what will be committed for approval.",
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
			if scope == nil {
				return gitCommitRequiresScopeMessage, nil
			}
			gitScope := scope()
			allowed, rejected := normalizeGitAllowedPaths(gitScope.AllowedPaths)
			if len(rejected) > 0 {
				return "blocked: active side-effect intent allowed_paths contains unsafe paths: " + strings.Join(rejected, ", "), nil
			}
			if len(allowed) == 0 {
				return gitCommitRequiresScopeMessage, nil
			}

			workDir := currentWorkDir(workDirProvider, fallbackWorkDir)
			if gitScope.RequireBranch && strings.TrimSpace(gitScope.TargetBranch) != "" {
				current, err := gitOutput(ctx, workDir, "git", "branch", "--show-current")
				if err != nil {
					result, _ := secretPolicy.ApplyCommandOutput(fmt.Sprintf("error checking branch: %s\n%s", err, current))
					return result, nil
				}
				if strings.TrimSpace(current) != strings.TrimSpace(gitScope.TargetBranch) {
					return fmt.Sprintf("blocked: current branch %q does not match required target branch %q", strings.TrimSpace(current), strings.TrimSpace(gitScope.TargetBranch)), nil
				}
			}

			message, _ := args["message"].(string)
			stagedBefore, err := gitNulPaths(ctx, workDir, "diff", "--cached", "--name-only", "-z")
			if err != nil {
				result, _ := secretPolicy.ApplyCommandOutput(fmt.Sprintf("error checking staged files: %s", err))
				return result, nil
			}
			if outside := pathsOutsideAllowlist(stagedBefore, allowed); len(outside) > 0 {
				return "blocked: pre-staged files outside active side-effect intent allowlist: " + strings.Join(outside, ", "), nil
			}

			addArgs := append([]string{"add", "--"}, allowed...)
			if out, err := gitCombinedOutput(ctx, workDir, addArgs...); err != nil {
				result, _ := secretPolicy.ApplyCommandOutput(fmt.Sprintf("error staging scoped paths: %s\n%s", err, out))
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
			if outside := pathsOutsideAllowlist(staged, allowed); len(outside) > 0 {
				return "blocked: staged files outside active side-effect intent allowlist: " + strings.Join(outside, ", "), nil
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
				result, _ := secretPolicy.ApplyCommandOutput(fmt.Sprintf("blocked: committed, but failed to verify commit files: %s", err))
				return result, nil
			}
			if !sameStringSet(staged, commitFiles) {
				return fmt.Sprintf("blocked: committed files did not match staged allowlist files; staged=%s committed=%s", strings.Join(staged, ", "), strings.Join(commitFiles, ", ")), nil
			}
			hash, err := gitOutput(ctx, workDir, "git", "rev-parse", "--short", "HEAD")
			if err != nil {
				result, _ := secretPolicy.ApplyCommandOutput(fmt.Sprintf("blocked: committed, but failed to read commit hash: %s", err))
				return result, nil
			}
			lastDiff = strings.TrimSpace(detail)
			result := fmt.Sprintf("commit %s created with files:\n%s", strings.TrimSpace(hash), strings.Join(commitFiles, "\n"))
			result, _ = secretPolicy.ApplyCommandOutput(result)
			return result, nil
		},
	}
}

func NewGitPushScoped(workDir string, approve ApprovalFunc, scope GitScopeProvider) Tool {
	return NewGitPushScopedWithWorkDirProvider(workDir, FixedWorkDirProvider(workDir), approve, scope)
}

func NewGitPushScopedWithWorkDirProvider(fallbackWorkDir string, workDirProvider WorkDirProvider, approve ApprovalFunc, scope GitScopeProvider) Tool {
	secretPolicy := DefaultSecretPolicy()
	return Tool{
		Name:             "git_push",
		Description:      "Push the current HEAD to the active side-effect intent remote and target branch, then verify the remote advertises that commit.",
		AutoApprove:      false,
		MutatesWorkspace: true,
		Execute: func(ctx context.Context, args map[string]any) (string, error) {
			_ = args
			if scope == nil {
				return gitPushRequiresScopeMessage, nil
			}
			gitScope := scope()
			remote := strings.TrimSpace(gitScope.Remote)
			targetBranch := strings.TrimSpace(gitScope.TargetBranch)
			if remote == "" || targetBranch == "" {
				return gitPushRequiresScopeMessage, nil
			}
			if !validGitRemoteName(remote) {
				return "blocked: git_push remote must be a configured remote name", nil
			}
			if !validGitTargetBranch(targetBranch) {
				return "blocked: git_push target branch is not a safe branch name", nil
			}

			workDir := currentWorkDir(workDirProvider, fallbackWorkDir)
			if remoteURL, err := gitOutput(ctx, workDir, "git", "remote", "get-url", remote); err != nil || strings.TrimSpace(remoteURL) == "" {
				return fmt.Sprintf("blocked: git_push remote %q is not a configured remote", remote), nil
			}
			currentBranch := ""
			if gitScope.RequireBranch {
				current, err := gitOutput(ctx, workDir, "git", "branch", "--show-current")
				if err != nil {
					result, _ := secretPolicy.ApplyCommandOutput(fmt.Sprintf("error checking branch: %s\n%s", err, current))
					return result, nil
				}
				currentBranch = strings.TrimSpace(current)
				if currentBranch != targetBranch {
					return fmt.Sprintf("blocked: current branch %q does not match required target branch %q", currentBranch, targetBranch), nil
				}
			} else if current, err := gitOutput(ctx, workDir, "git", "branch", "--show-current"); err == nil {
				currentBranch = strings.TrimSpace(current)
			}

			head, err := gitOutput(ctx, workDir, "git", "rev-parse", "HEAD")
			if err != nil {
				result, _ := secretPolicy.ApplyCommandOutput(fmt.Sprintf("error reading HEAD: %s\n%s", err, head))
				return result, nil
			}
			head = strings.TrimSpace(head)
			remoteRef := "refs/heads/" + targetBranch
			detail := fmt.Sprintf("Current branch: %s\nHEAD: %s\nRemote: %s\nTarget branch: %s\nRemote ref: %s\nAllowed paths:\n%s", currentBranch, head, remote, targetBranch, remoteRef, strings.Join(gitScope.AllowedPaths, "\n"))
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
				result, _ := secretPolicy.ApplyCommandOutput(fmt.Sprintf("blocked: pushed, but failed to verify remote: %s\n%s", err, remoteOut))
				return result, nil
			}
			remoteSHA := strings.Fields(remoteOut)
			if len(remoteSHA) == 0 || remoteSHA[0] != head {
				return fmt.Sprintf("blocked: pushed, but remote %s %s advertised %q instead of %s", remote, remoteRef, strings.TrimSpace(remoteOut), head), nil
			}
			result := fmt.Sprintf("remote contains %s at %s/%s", head, remote, targetBranch)
			result, _ = secretPolicy.ApplyCommandOutput(result)
			return result, nil
		},
	}
}

func normalizeGitAllowedPaths(paths []string) ([]string, []string) {
	seen := map[string]bool{}
	var out []string
	var rejected []string
	for _, path := range paths {
		original := strings.TrimSpace(path)
		path = strings.TrimSpace(path)
		if path == "" || filepath.IsAbs(path) || strings.ContainsAny(path, "*?[]") || strings.Contains(path, "\\") || strings.Contains(path, ":") || strings.Contains(path, "://") {
			if original != "" {
				rejected = append(rejected, original)
			}
			continue
		}
		path = filepath.ToSlash(filepath.Clean(filepath.ToSlash(path)))
		if path == "." || path == "" || strings.HasPrefix(path, "../") || path == ".." {
			rejected = append(rejected, original)
			continue
		}
		invalid := false
		for _, segment := range strings.Split(path, "/") {
			if segment == ".." || segment == "" {
				invalid = true
				break
			}
		}
		if invalid {
			rejected = append(rejected, original)
			continue
		}
		if seen[path] {
			continue
		}
		seen[path] = true
		out = append(out, path)
	}
	sort.Strings(out)
	sort.Strings(rejected)
	return out, rejected
}

func validGitRemoteName(remote string) bool {
	remote = strings.TrimSpace(remote)
	if remote == "" || strings.HasPrefix(remote, "-") || strings.Contains(remote, ":") || strings.Contains(remote, "/") || strings.Contains(remote, "\\") {
		return false
	}
	for _, r := range remote {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.' {
			continue
		}
		return false
	}
	return true
}

func validGitTargetBranch(branch string) bool {
	branch = strings.TrimSpace(branch)
	if branch == "" || strings.HasPrefix(branch, "-") || strings.HasPrefix(branch, "/") || strings.HasSuffix(branch, "/") || strings.HasPrefix(branch, "refs/") {
		return false
	}
	if branch == "@" || strings.Contains(branch, "@{") || strings.Contains(branch, "//") {
		return false
	}
	if strings.ContainsAny(branch, "\\~^:?*[") || strings.Contains(branch, "..") || strings.HasSuffix(branch, ".") || strings.HasSuffix(branch, ".lock") {
		return false
	}
	for _, segment := range strings.Split(branch, "/") {
		if segment == "" || strings.HasPrefix(segment, ".") || strings.HasSuffix(segment, ".lock") {
			return false
		}
	}
	return true
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

func pathsOutsideAllowlist(paths, allowed []string) []string {
	var outside []string
	for _, path := range paths {
		if !pathInAllowlist(path, allowed) {
			outside = append(outside, path)
		}
	}
	return outside
}

func pathInAllowlist(path string, allowed []string) bool {
	path = filepath.ToSlash(path)
	for _, allowedPath := range allowed {
		if path == allowedPath || strings.HasPrefix(path, allowedPath+"/") {
			return true
		}
	}
	return false
}

func sameStringSet(a, b []string) bool {
	a = slices.Clone(a)
	b = slices.Clone(b)
	sort.Strings(a)
	sort.Strings(b)
	return slices.Equal(a, b)
}
