package tools

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func NewGitMergeStatus(workDir string) Tool {
	return Tool{
		Name:        "git_merge_status",
		Description: "Inspect merge/rebase/cherry-pick conflict state, list unresolved files, and suggest the next merge-resolution step.",
		Parameters:  []ParameterDef{},
		AutoApprove: true,
		Execute: func(ctx context.Context, args map[string]any) (string, error) {
			gitDir, err := resolveGitDir(ctx, workDir)
			if err != nil {
				return fmt.Sprintf("error resolving git dir: %v", err), nil
			}
			state, err := inspectGitMergeState(ctx, workDir, gitDir)
			if err != nil {
				return fmt.Sprintf("error inspecting git merge state: %v", err), nil
			}
			return state.render(), nil
		},
	}
}

type gitMergeState struct {
	operation string
	unmerged  []string
	staged    []string
	unstaged  []string
}

func resolveGitDir(ctx context.Context, workDir string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", "rev-parse", "--git-dir")
	cmd.Dir = workDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("%v: %s", err, strings.TrimSpace(string(out)))
	}
	path := strings.TrimSpace(string(out))
	if !filepath.IsAbs(path) {
		path = filepath.Join(workDir, path)
	}
	return filepath.Clean(path), nil
}

func inspectGitMergeState(ctx context.Context, workDir, gitDir string) (gitMergeState, error) {
	state := gitMergeState{operation: detectGitOperation(gitDir)}

	cmd := exec.CommandContext(ctx, "git", "status", "--porcelain")
	cmd.Dir = workDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return state, fmt.Errorf("%v: %s", err, strings.TrimSpace(string(out)))
	}

	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		entry := parsePorcelainPath(line)
		if entry == "" {
			continue
		}
		if isUnmergedPorcelain(line) {
			state.unmerged = append(state.unmerged, entry)
			continue
		}
		if isStagedPorcelain(line) {
			state.staged = append(state.staged, entry)
		}
		if isUnstagedPorcelain(line) {
			state.unstaged = append(state.unstaged, entry)
		}
	}
	return state, nil
}

func detectGitOperation(gitDir string) string {
	switch {
	case fileExists(filepath.Join(gitDir, "MERGE_HEAD")):
		return "merge"
	case fileExists(filepath.Join(gitDir, "CHERRY_PICK_HEAD")):
		return "cherry-pick"
	case dirExists(filepath.Join(gitDir, "rebase-merge")) || dirExists(filepath.Join(gitDir, "rebase-apply")):
		return "rebase"
	default:
		return "none"
	}
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func parsePorcelainPath(line string) string {
	if len(line) < 4 {
		return ""
	}
	path := strings.TrimSpace(line[3:])
	if idx := strings.Index(path, " -> "); idx >= 0 {
		path = strings.TrimSpace(path[idx+4:])
	}
	return path
}

func isUnmergedPorcelain(line string) bool {
	if len(line) < 2 {
		return false
	}
	x, y := line[0], line[1]
	if x == 'U' || y == 'U' {
		return true
	}
	pair := line[:2]
	return pair == "AA" || pair == "DD"
}

func isStagedPorcelain(line string) bool {
	if len(line) < 1 {
		return false
	}
	x := line[0]
	return x != ' ' && x != '?' && x != 'U'
}

func isUnstagedPorcelain(line string) bool {
	if len(line) < 2 {
		return false
	}
	y := line[1]
	return y != ' ' && y != '?'
}

func (s gitMergeState) render() string {
	var sb strings.Builder
	sb.WriteString("operation: ")
	sb.WriteString(s.operation)
	sb.WriteString("\n")
	sb.WriteString(renderMergeSection("unmerged_files", s.unmerged))
	sb.WriteString("\n")
	sb.WriteString(renderMergeSection("staged_files", s.staged))
	sb.WriteString("\n")
	sb.WriteString(renderMergeSection("unstaged_files", s.unstaged))
	sb.WriteString("\n")
	sb.WriteString("next_action: ")
	sb.WriteString(s.nextAction())
	return sb.String()
}

func renderMergeSection(name string, items []string) string {
	if len(items) == 0 {
		return name + ": none"
	}
	var sb strings.Builder
	sb.WriteString(name)
	sb.WriteString(":\n")
	for _, item := range items {
		sb.WriteString("- ")
		sb.WriteString(item)
		sb.WriteString("\n")
	}
	return strings.TrimRight(sb.String(), "\n")
}

func (s gitMergeState) nextAction() string {
	switch {
	case s.operation == "none":
		return "no merge in progress"
	case len(s.unmerged) > 0:
		return "resolve each unmerged file, stage it, then re-run git_merge_status"
	case len(s.unstaged) > 0:
		return "finish edits, stage the changed files, then re-run git_merge_status before committing"
	case len(s.staged) > 0:
		return "run any targeted validation you need, then commit when ready"
	default:
		return "verify the repository state and complete the in-progress operation"
	}
}
