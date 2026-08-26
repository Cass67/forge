package tools

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

func NewGHPullRequest(workDir string) Tool {
	return NewGHPullRequestWithWorkDirProvider(workDir, FixedWorkDirProvider(workDir))
}

func NewGHPullRequestWithWorkDirProvider(fallbackWorkDir string, provider WorkDirProvider) Tool {
	secretPolicy := DefaultSecretPolicy()
	return Tool{
		Name:        "gh_pr",
		Description: "Read GitHub pull requests through the gh CLI. mode=list shows open PRs, mode=view shows one PR with its description and comments, mode=diff shows its patch. Read-only; it never creates, merges, or comments on anything.",
		Parameters: []ParameterDef{
			{Name: "mode", Type: "string", Description: "list, view, or diff (default: list)", Required: false},
			{Name: "number", Type: "int", Description: "pull request number, required for view and diff; omit on the current branch's PR", Required: false},
			{Name: "limit", Type: "int", Description: "how many PRs to list (default 20)", Required: false},
			{Name: "state", Type: "string", Description: "list filter: open, closed, merged, or all (default: open)", Required: false},
		},
		AutoApprove: true,
		Execute: func(ctx context.Context, args map[string]any) (string, error) {
			workDir := currentWorkDir(provider, fallbackWorkDir)
			if _, err := exec.LookPath("gh"); err != nil {
				return "gh_pr unavailable: the GitHub CLI (gh) is not installed or not on PATH; install it from https://cli.github.com and run `gh auth login`", nil
			}

			mode := strings.ToLower(strings.TrimSpace(stringArg(args, "mode")))
			if mode == "" {
				mode = "list"
			}
			number := intArg(args["number"], 0)

			var ghArgs []string
			switch mode {
			case "list":
				state := strings.ToLower(strings.TrimSpace(stringArg(args, "state")))
				if state == "" {
					state = "open"
				}
				ghArgs = []string{"pr", "list", "--state", state, "--limit", fmt.Sprint(intArg(args["limit"], 20))}
			case "view":
				ghArgs = []string{"pr", "view", "--comments"}
				if number > 0 {
					ghArgs = append(ghArgs, fmt.Sprint(number))
				}
			case "diff":
				ghArgs = []string{"pr", "diff"}
				if number > 0 {
					ghArgs = append(ghArgs, fmt.Sprint(number))
				}
			default:
				return fmt.Sprintf("error: unknown mode %q; use list, view, or diff", mode), nil
			}

			cmd := exec.CommandContext(ctx, "gh", ghArgs...)
			cmd.Dir = workDir
			out, err := cmd.CombinedOutput()
			if err != nil {
				result, _ := secretPolicy.ApplyCommandOutput(ghFailure(mode, number, err, string(out)))
				return result, nil
			}
			text := strings.TrimSpace(string(out))
			if text == "" {
				return fmt.Sprintf("gh pr %s returned nothing", mode), nil
			}
			result, _ := secretPolicy.ApplyCommandOutput(text)
			return truncateGitOutput(result), nil
		},
	}
}

// ghFailure turns gh's exit codes into something the model can act on. The two
// that matter are "not logged in" and "this branch has no PR", which otherwise
// look identical to a transport error.
func ghFailure(mode string, number int, err error, out string) string {
	out = strings.TrimSpace(out)
	lower := strings.ToLower(out)
	switch {
	case strings.Contains(lower, "auth login") || strings.Contains(lower, "not logged"):
		return "gh_pr failed: gh is not authenticated; run `gh auth login` and retry\n" + out
	case strings.Contains(lower, "no pull requests found") || strings.Contains(lower, "no default remote"):
		if number == 0 && mode != "list" {
			return "gh_pr failed: the current branch has no open pull request; pass number to target one explicitly\n" + out
		}
		return "gh_pr failed: " + out
	case strings.Contains(lower, "could not resolve to a repository") || strings.Contains(lower, "not a git repository"):
		return "gh_pr failed: this directory has no GitHub remote gh can resolve\n" + out
	}
	if _, ok := errors.AsType[*exec.ExitError](err); ok && out != "" {
		return "gh_pr failed: " + out
	}
	return fmt.Sprintf("gh_pr failed: %s\n%s", err, out)
}
