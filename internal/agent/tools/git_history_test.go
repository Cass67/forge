package tools

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestGitShowIncludesMessageAndPatch(t *testing.T) {
	dir := initGitRepo(t)
	mustWriteFile(t, filepath.Join(dir, "main.go"), "package main\n\nfunc main() {}\n")
	runGit(t, dir, "commit", "-am", "add main func")

	out := runTool(t, NewGitShow(dir), map[string]any{})
	if !strings.Contains(out, "add main func") || !strings.Contains(out, "func main()") {
		t.Fatalf("git_show missing message or patch:\n%s", out)
	}
}

func TestGitShowStatOmitsPatch(t *testing.T) {
	dir := initGitRepo(t)
	out := runTool(t, NewGitShow(dir), map[string]any{"stat": true})
	if !strings.Contains(out, "main.go") {
		t.Fatalf("stat missing the file:\n%s", out)
	}
	if strings.Contains(out, "+package main") {
		t.Fatalf("stat=true returned patch lines:\n%s", out)
	}
}

func TestGitShowScopesToPath(t *testing.T) {
	dir := initGitRepo(t)
	mustWriteFile(t, filepath.Join(dir, "a.go"), "package main\n")
	mustWriteFile(t, filepath.Join(dir, "b.go"), "package main\n")
	runGit(t, dir, "add", ".")
	runGit(t, dir, "commit", "-m", "two files")

	out := runTool(t, NewGitShow(dir), map[string]any{"path": "a.go"})
	if !strings.Contains(out, "a.go") || strings.Contains(out, "+++ b/b.go") {
		t.Fatalf("path scope not applied:\n%s", out)
	}
}

func TestGitShowBadRefReportsError(t *testing.T) {
	dir := initGitRepo(t)
	out := runTool(t, NewGitShow(dir), map[string]any{"ref": "no-such-ref"})
	if !strings.Contains(out, "error") {
		t.Fatalf("expected an error for a bad ref:\n%s", out)
	}
}

func TestGitBlameRangeReportsAuthorAndLine(t *testing.T) {
	dir := initGitRepo(t)
	mustWriteFile(t, filepath.Join(dir, "main.go"), "package main\n\nfunc main() {}\n")
	runGit(t, dir, "commit", "-am", "add main func")

	out := runTool(t, NewGitBlame(dir), map[string]any{"path": "main.go", "start_line": float64(3), "end_line": float64(3)})
	if !strings.Contains(out, "func main()") {
		t.Fatalf("blame missing the requested line:\n%s", out)
	}
	if strings.Contains(out, "package main") {
		t.Fatalf("blame ignored the line range:\n%s", out)
	}
	if !strings.Contains(out, "test") {
		t.Fatalf("blame missing the author:\n%s", out)
	}
}

func TestGitBlameRequiresPath(t *testing.T) {
	out := runTool(t, NewGitBlame(initGitRepo(t)), map[string]any{})
	if !strings.Contains(out, "path is required") {
		t.Fatalf("expected a path error, got:\n%s", out)
	}
}

func TestGitWorktreeListIsReadOnly(t *testing.T) {
	dir := initGitRepo(t)
	denied := func(Action) (bool, error) {
		t.Fatal("mode=list must not ask for approval")
		return false, nil
	}
	out := runTool(t, NewGitWorktree(dir, denied), map[string]any{"mode": "list"})
	if !strings.Contains(out, dir) {
		t.Fatalf("worktree list missing the main worktree:\n%s", out)
	}
}

func TestGitWorktreeAddCreatesBranchAfterApproval(t *testing.T) {
	dir := initGitRepo(t)
	target := filepath.Join(t.TempDir(), "wt")
	var asked Action
	approve := func(a Action) (bool, error) { asked = a; return true, nil }

	out := runTool(t, NewGitWorktree(dir, approve), map[string]any{"mode": "add", "path": target, "ref": "spike"})
	if asked.Tool != "git_worktree" || !strings.Contains(asked.Summary, "spike") {
		t.Fatalf("approval did not describe the add: %+v", asked)
	}
	if _, err := os.Stat(filepath.Join(target, "main.go")); err != nil {
		t.Fatalf("worktree not created: %v", err)
	}
	if !strings.Contains(out, "worktrees now:") || !strings.Contains(out, target) {
		t.Fatalf("result missing the updated worktree list:\n%s", out)
	}
	if !strings.Contains(gitOut(t, dir, "branch", "--list", "spike"), "spike") {
		t.Fatal("expected the new branch to exist")
	}
}

func TestGitWorktreeDeniedDoesNothing(t *testing.T) {
	dir := initGitRepo(t)
	target := filepath.Join(t.TempDir(), "wt")
	deny := func(Action) (bool, error) { return false, nil }

	out := runTool(t, NewGitWorktree(dir, deny), map[string]any{"mode": "add", "path": target})
	if !strings.Contains(out, "denied by user") {
		t.Fatalf("expected a denial, got:\n%s", out)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatal("worktree created despite denial")
	}
}

func TestGitWorktreeRejectsBadInput(t *testing.T) {
	dir := initGitRepo(t)
	approve := func(Action) (bool, error) {
		t.Fatal("invalid input must be rejected before approval")
		return false, nil
	}
	tool := NewGitWorktree(dir, approve)
	if out := runTool(t, tool, map[string]any{"mode": "add"}); !strings.Contains(out, "path is required") {
		t.Fatalf("expected a path error, got:\n%s", out)
	}
	if out := runTool(t, tool, map[string]any{"mode": "teleport"}); !strings.Contains(out, "unknown mode") {
		t.Fatalf("expected an unknown-mode error, got:\n%s", out)
	}
}

func TestGHPullRequestWithoutGHExplainsItself(t *testing.T) {
	dir := initGitRepo(t)
	t.Setenv("PATH", t.TempDir())
	out := runTool(t, NewGHPullRequest(dir), map[string]any{})
	if !strings.Contains(out, "gh auth login") || !strings.Contains(out, "not installed") {
		t.Fatalf("expected install guidance, got:\n%s", out)
	}
}

func TestGHPullRequestRejectsUnknownMode(t *testing.T) {
	if _, err := exec.LookPath("gh"); err != nil {
		t.Skip("gh not installed")
	}
	out := runTool(t, NewGHPullRequest(initGitRepo(t)), map[string]any{"mode": "merge"})
	if !strings.Contains(out, "unknown mode") {
		t.Fatalf("expected an unknown-mode error, got:\n%s", out)
	}
}

func TestGHFailureExplainsCommonExits(t *testing.T) {
	if got := ghFailure("view", 0, context.Canceled, "gh: To use GitHub CLI, run: gh auth login"); !strings.Contains(got, "not authenticated") {
		t.Fatalf("auth failure not recognised: %s", got)
	}
	if got := ghFailure("view", 0, context.Canceled, "no pull requests found for branch \"x\""); !strings.Contains(got, "pass number") {
		t.Fatalf("missing-PR failure not recognised: %s", got)
	}
	if got := ghFailure("list", 0, context.Canceled, "could not resolve to a Repository"); !strings.Contains(got, "no GitHub remote") {
		t.Fatalf("no-remote failure not recognised: %s", got)
	}
}
