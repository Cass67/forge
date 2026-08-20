package tools

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func initGitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	// Pin the initial branch: without init.defaultBranch set, git names it
	// "master" and every test that expects "main" fails on that machine only.
	runGit(t, dir, "init", "-b", "main")
	runGit(t, dir, "config", "user.email", "test@test.com")
	runGit(t, dir, "config", "user.name", "test")
	mustWriteFile(t, filepath.Join(dir, "main.go"), "package main\n")
	runGit(t, dir, "add", ".")
	runGit(t, dir, "commit", "-m", "initial")
	return dir
}

func mustWriteFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func gitOut(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@test.com",
		"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@test.com",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v failed: %s\n%s", args, err, out)
	}
	return string(out)
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	_ = gitOut(t, dir, args...)
}

func TestGitStatus(t *testing.T) {
	dir := initGitRepo(t)
	os.WriteFile(filepath.Join(dir, "new.go"), []byte("package main\n"), 0o644)
	tool := NewGitStatus(dir)
	if tool.Name != "git_status" {
		t.Fatalf("expected name 'git_status', got %q", tool.Name)
	}
	if !tool.AutoApprove {
		t.Fatal("git_status should be auto-approved")
	}
	result, err := tool.Execute(context.Background(), map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "new.go") {
		t.Errorf("expected new.go in status, got: %s", result)
	}
}

func TestGitStatusClean(t *testing.T) {
	dir := initGitRepo(t)
	tool := NewGitStatus(dir)
	result, err := tool.Execute(context.Background(), map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(result) != "" {
		t.Errorf("expected clean status, got: %s", result)
	}
}

func TestGitDiff(t *testing.T) {
	dir := initGitRepo(t)
	os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n\nfunc hello() {}\n"), 0o644)
	tool := NewGitDiff(dir)
	if !tool.AutoApprove {
		t.Fatal("git_diff should be auto-approved")
	}
	result, err := tool.Execute(context.Background(), map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "hello") {
		t.Errorf("expected diff with hello, got: %s", result)
	}
}

func TestGitDiffRedactsSecretContent(t *testing.T) {
	dir := initGitRepo(t)
	secret := dummySecret()
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n// token="+secret+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := NewGitDiff(dir)
	result, err := tool.Execute(context.Background(), map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(result, secret) {
		t.Fatalf("git diff leaked secret: %s", result)
	}
	if !strings.Contains(result, "<REDACTED:github-pat>") {
		t.Fatalf("git diff missing redaction marker: %s", result)
	}
}

func TestGitDiffDefaultIsHEAD(t *testing.T) {
	dir := initGitRepo(t)
	os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n\nfunc changed() {}\n"), 0o644)
	tool := NewGitDiff(dir)
	result, err := tool.Execute(context.Background(), map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "changed") {
		t.Errorf("default diff should show changes vs HEAD, got: %s", result)
	}
}

func TestGitLog(t *testing.T) {
	dir := initGitRepo(t)
	tool := NewGitLog(dir)
	if !tool.AutoApprove {
		t.Fatal("git_log should be auto-approved")
	}
	result, err := tool.Execute(context.Background(), map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "initial") {
		t.Errorf("expected 'initial' commit in log, got: %s", result)
	}
}

func TestGitLogCount(t *testing.T) {
	dir := initGitRepo(t)
	os.WriteFile(filepath.Join(dir, "second.go"), []byte("package main\n"), 0o644)
	cmd := exec.Command("git", "add", ".")
	cmd.Dir = dir
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	cmd = exec.Command("git", "commit", "-m", "second commit")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@test.com",
		"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@test.com")
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}

	tool := NewGitLog(dir)
	result, err := tool.Execute(context.Background(), map[string]any{"count": float64(1)})
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(result), "\n")
	if len(lines) != 1 {
		t.Errorf("expected 1 log line, got %d: %s", len(lines), result)
	}
}

func TestGitLogPath(t *testing.T) {
	dir := initGitRepo(t)
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@test.com",
			"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@test.com")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v failed: %s\n%s", args, err, out)
		}
	}
	if err := os.MkdirAll(filepath.Join(dir, "internal", "tui"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "internal", "tui", "chatstats.go"), []byte("package tui\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", ".")
	run("commit", "-m", "touch tui stats")
	if err := os.WriteFile(filepath.Join(dir, "other.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", ".")
	run("commit", "-m", "touch other")

	tool := NewGitLog(dir)
	result, err := tool.Execute(context.Background(), map[string]any{"count": float64(10), "path": "internal/tui"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "touch tui stats") {
		t.Fatalf("expected scoped log to include tui commit, got: %s", result)
	}
	if strings.Contains(result, "touch other") {
		t.Fatalf("expected scoped log to omit unrelated commit, got: %s", result)
	}
}

func TestGitCommit(t *testing.T) {
	dir := initGitRepo(t)
	os.WriteFile(filepath.Join(dir, "new.go"), []byte("package main\n"), 0o644)
	runGit(t, dir, "add", "new.go")

	approved := false
	var capturedAction Action
	approve := func(a Action) (bool, error) {
		capturedAction = a
		approved = true
		return true, nil
	}

	tool := NewGitCommit(dir, approve)
	if tool.Name != "git_commit" {
		t.Fatalf("expected name 'git_commit', got %q", tool.Name)
	}
	if tool.AutoApprove {
		t.Fatal("git_commit should require approval")
	}

	result, err := tool.Execute(context.Background(), map[string]any{"message": "add new file"})
	if err != nil {
		t.Fatal(err)
	}
	if !approved {
		t.Fatal("approval function was not called")
	}
	if !strings.Contains(capturedAction.Summary, "add new file") {
		t.Errorf("approval summary should contain commit message, got: %s", capturedAction.Summary)
	}
	if !strings.Contains(capturedAction.Detail, "new.go") {
		t.Errorf("approval detail should show staged files, got: %s", capturedAction.Detail)
	}
	if !strings.Contains(result, "commit") || !strings.Contains(result, "new.go") {
		t.Errorf("expected commit confirmation, got: %s", result)
	}

	cmd := exec.Command("git", "log", "--oneline", "-1")
	cmd.Dir = dir
	out, _ := cmd.Output()
	if !strings.Contains(string(out), "add new file") {
		t.Errorf("commit not found in git log: %s", out)
	}
}

func TestGitCommitPreservesUnstagedChanges(t *testing.T) {
	dir := initGitRepo(t)
	mustWriteFile(t, filepath.Join(dir, "staged.go"), "package main\n")
	mustWriteFile(t, filepath.Join(dir, "unrelated.go"), "package main\n")
	runGit(t, dir, "add", "staged.go")

	var capturedAction Action
	tool := NewGitCommit(dir, func(a Action) (bool, error) {
		capturedAction = a
		return true, nil
	})
	result, err := tool.Execute(context.Background(), map[string]any{"message": "add staged file"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(capturedAction.Detail, "unrelated.go") || strings.Contains(result, "unrelated.go") {
		t.Fatalf("commit included unrelated unstaged file: approval=%q result=%q", capturedAction.Detail, result)
	}
	if committed := gitOut(t, dir, "show", "--name-only", "--format=", "HEAD"); strings.TrimSpace(committed) != "staged.go" {
		t.Fatalf("committed files = %q, want staged.go", committed)
	}
	if status := gitOut(t, dir, "status", "--short", "--", "unrelated.go"); !strings.Contains(status, "?? unrelated.go") {
		t.Fatalf("unrelated file status = %q, want untracked", status)
	}
}

func TestGitCommitRetriesAfterHookFixWithoutStagingUnrelatedFiles(t *testing.T) {
	dir := initGitRepo(t)
	mustWriteFile(t, filepath.Join(dir, "staged.txt"), "bad\n")
	mustWriteFile(t, filepath.Join(dir, "unrelated.txt"), "leave me alone\n")
	runGit(t, dir, "add", "staged.txt")

	hook := filepath.Join(dir, ".git", "hooks", "pre-commit")
	mustWriteFile(t, hook, "#!/bin/sh\nif grep -q bad staged.txt; then\n  printf 'good\\n' > staged.txt\n  echo 'files were modified by this hook'\n  exit 1\nfi\n")
	if err := os.Chmod(hook, 0o755); err != nil {
		t.Fatal(err)
	}

	tool := NewGitCommit(dir, func(Action) (bool, error) { return true, nil })
	first, err := tool.Execute(context.Background(), map[string]any{"message": "add staged file"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(first, "files were modified by this hook") {
		t.Fatalf("first commit result = %q, want hook failure", first)
	}

	runGit(t, dir, "add", "staged.txt")
	second, err := tool.Execute(context.Background(), map[string]any{"message": "add staged file"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(second, "commit ") {
		t.Fatalf("second commit result = %q, want success", second)
	}
	if content := gitOut(t, dir, "show", "HEAD:staged.txt"); content != "good\n" {
		t.Fatalf("committed content = %q, want hook fix", content)
	}
	if status := gitOut(t, dir, "status", "--short", "--", "unrelated.txt"); !strings.Contains(status, "?? unrelated.txt") {
		t.Fatalf("unrelated file status = %q, want untracked", status)
	}
}

func TestGitPushVerifiesRemoteContainsCommit(t *testing.T) {
	dir := initGitRepo(t)
	remote := filepath.Join(t.TempDir(), "remote.git")
	runGit(t, t.TempDir(), "init", "--bare", remote)
	runGit(t, dir, "branch", "-M", "main")
	runGit(t, dir, "remote", "add", "origin", remote)

	mustWriteFile(t, filepath.Join(dir, "FORGE_VS_CODEX.md"), "doc\n")
	runGit(t, dir, "add", "FORGE_VS_CODEX.md")
	commit := NewGitCommit(dir, func(Action) (bool, error) { return true, nil })
	if _, err := commit.Execute(context.Background(), map[string]any{"message": "add comparison"}); err != nil {
		t.Fatal(err)
	}

	push := NewGitPush(dir, func(Action) (bool, error) { return true, nil })
	result, err := push.Execute(context.Background(), map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "remote contains") {
		t.Fatalf("result = %q", result)
	}
}

func TestGitPushWithoutRemoteFailsBeforeApproval(t *testing.T) {
	dir := initGitRepo(t)
	approved := false
	push := NewGitPush(dir, func(Action) (bool, error) {
		approved = true
		return true, nil
	})
	result, err := push.Execute(context.Background(), map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	if approved {
		t.Fatal("git_push without a remote should not request approval")
	}
	if !strings.Contains(result, "no configured remote") {
		t.Fatalf("result = %q", result)
	}
}

func TestGitPushDenialDoesNotPush(t *testing.T) {
	dir := initGitRepo(t)
	remote := filepath.Join(t.TempDir(), "remote.git")
	runGit(t, t.TempDir(), "init", "--bare", remote)
	runGit(t, dir, "branch", "-M", "main")
	runGit(t, dir, "remote", "add", "origin", remote)
	mustWriteFile(t, filepath.Join(dir, "FORGE_VS_CODEX.md"), "doc\n")
	runGit(t, dir, "add", "FORGE_VS_CODEX.md")
	commit := NewGitCommit(dir, func(Action) (bool, error) { return true, nil })
	if _, err := commit.Execute(context.Background(), map[string]any{"message": "add comparison"}); err != nil {
		t.Fatal(err)
	}
	approved := false
	push := NewGitPush(dir, func(Action) (bool, error) {
		approved = true
		return false, nil
	})

	result, err := push.Execute(context.Background(), map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	if !approved {
		t.Fatal("approval function was not called")
	}
	if !strings.Contains(result, "denied") {
		t.Fatalf("result = %q", result)
	}
	if refs := gitOut(t, dir, "ls-remote", "origin", "refs/heads/main"); strings.TrimSpace(refs) != "" {
		t.Fatalf("remote refs = %q, want none", refs)
	}
}

func TestGitCommitApprovalRedactsSecretPaths(t *testing.T) {
	dir := initGitRepo(t)
	secret := dummySecret()
	secretPath := "token-" + secret + ".txt"
	if err := os.WriteFile(filepath.Join(dir, secretPath), []byte("placeholder\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "add", secretPath)

	var capturedAction Action
	tool := NewGitCommit(dir, func(a Action) (bool, error) {
		capturedAction = a
		return false, nil
	})
	if _, err := tool.Execute(context.Background(), map[string]any{"message": "add token file"}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(capturedAction.Detail, secret) {
		t.Fatalf("git commit approval detail leaked secret path: %s", capturedAction.Detail)
	}
	if !strings.Contains(capturedAction.Detail, "<REDACTED:github-pat>") {
		t.Fatalf("git commit approval detail missing redaction: %s", capturedAction.Detail)
	}
}

func TestGitCommitDenied(t *testing.T) {
	dir := initGitRepo(t)
	os.WriteFile(filepath.Join(dir, "new.go"), []byte("package main\n"), 0o644)
	runGit(t, dir, "add", "new.go")

	tool := NewGitCommit(dir, func(a Action) (bool, error) { return false, nil })
	result, err := tool.Execute(context.Background(), map[string]any{"message": "should not happen"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "denied") {
		t.Errorf("expected denied message, got: %s", result)
	}
}

func TestGitCommitNothingToCommit(t *testing.T) {
	dir := initGitRepo(t)

	tool := NewGitCommit(dir, func(a Action) (bool, error) { return true, nil })
	result, err := tool.Execute(context.Background(), map[string]any{"message": "empty"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "nothing to commit") {
		t.Errorf("expected nothing to commit, got: %s", result)
	}
}

func TestGitMergeStatusCleanRepo(t *testing.T) {
	dir := initGitRepo(t)

	tool := NewGitMergeStatus(dir)
	result, err := tool.Execute(context.Background(), map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "operation: none") {
		t.Fatalf("expected no merge operation, got: %s", result)
	}
	if !strings.Contains(result, "next_action: no merge in progress") {
		t.Fatalf("expected clean next action, got: %s", result)
	}
}

func TestGitMergeStatusReportsConflicts(t *testing.T) {
	dir := initGitRepo(t)
	writeFile := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	runWithEnv := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@test.com",
			"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@test.com",
		)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v failed: %s\n%s", args, err, out)
		}
	}
	currentBranchCmd := exec.Command("git", "branch", "--show-current")
	currentBranchCmd.Dir = dir
	currentBranch, err := currentBranchCmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	baseBranch := strings.TrimSpace(string(currentBranch))
	if baseBranch == "" {
		t.Fatal("expected current branch name")
	}

	runWithEnv("checkout", "-b", "feature")
	writeFile("main.go", "package main\n\nfunc feature() {}\n")
	runWithEnv("add", "main.go")
	runWithEnv("commit", "-m", "feature change")

	runWithEnv("checkout", baseBranch)
	writeFile("main.go", "package main\n\nfunc mainline() {}\n")
	runWithEnv("add", "main.go")
	runWithEnv("commit", "-m", "main change")

	cmd := exec.Command("git", "merge", "feature")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@test.com",
		"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@test.com",
	)
	if out, err := cmd.CombinedOutput(); err == nil {
		t.Fatalf("expected merge conflict, got success: %s", out)
	}

	tool := NewGitMergeStatus(dir)
	result, err := tool.Execute(context.Background(), map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "operation: merge") {
		t.Fatalf("expected merge operation, got: %s", result)
	}
	if !strings.Contains(result, "unmerged_files:\n- main.go") {
		t.Fatalf("expected conflicted file listing, got: %s", result)
	}
	if !strings.Contains(result, "next_action: resolve each unmerged file, stage it, then re-run git_merge_status") {
		t.Fatalf("expected conflict guidance, got: %s", result)
	}
}

func TestGitBranchStateReportsCurrentBranch(t *testing.T) {
	dir := initGitRepo(t)

	tool := NewGitBranchState(dir)
	result, err := tool.Execute(context.Background(), map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "current_branch: ") {
		t.Fatalf("expected current branch, got: %s", result)
	}
	if !strings.Contains(result, "head_contains_target: unknown") {
		t.Fatalf("expected unknown target containment without a target, got: %s", result)
	}
}

func TestGitBranchStateWithWorkDirProviderUsesActiveWorkspaceForTargetLookup(t *testing.T) {
	base := initGitRepo(t)
	active := initGitRepo(t)
	runGit(t, base, "branch", "base-only")
	t.Chdir(base)

	tool := NewGitBranchStateWithWorkDirProvider(base, func() string { return active })
	result, err := tool.Execute(context.Background(), map[string]any{"target_branch": "base-only"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(result, "target_exists: true") {
		t.Fatalf("git_branch_state used original cwd/base repo for target lookup:\n%s", result)
	}
	if !strings.Contains(result, "target_exists: false") {
		t.Fatalf("git_branch_state result = %q, want target_exists false", result)
	}
}

func TestGitBranchStateReportsTargetContainment(t *testing.T) {
	dir := initGitRepo(t)
	runWithEnv := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@test.com",
			"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@test.com",
		)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v failed: %s\n%s", args, err, out)
		}
	}

	runWithEnv("checkout", "-b", "feature")
	if err := os.WriteFile(filepath.Join(dir, "feature.txt"), []byte("feature\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runWithEnv("add", "feature.txt")
	runWithEnv("commit", "-m", "feature work")

	tool := NewGitBranchState(dir)
	result, err := tool.Execute(context.Background(), map[string]any{"target_branch": "main"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "target_branch: main") {
		t.Fatalf("expected target branch in output, got: %s", result)
	}
	if !strings.Contains(result, "head_contains_target: true") {
		t.Fatalf("expected head to contain target branch tip, got: %s", result)
	}
	if !strings.Contains(result, "target_contains_head: false") {
		t.Fatalf("expected reverse containment false, got: %s", result)
	}
}

func TestGitBranchStateSuggestsUpdatingTargetBranchWhenHeadIsElsewhere(t *testing.T) {
	dir := initGitRepo(t)
	runWithEnv := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@test.com",
			"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@test.com",
		)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v failed: %s\n%s", args, err, out)
		}
	}

	runWithEnv("checkout", "-b", "feature/go-rewrite")
	if err := os.WriteFile(filepath.Join(dir, "feature.txt"), []byte("feature\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runWithEnv("add", "feature.txt")
	runWithEnv("commit", "-m", "feature work")

	tool := NewGitBranchState(dir)
	result, err := tool.Execute(context.Background(), map[string]any{"target_branch": "main"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "next_action: switch to main and fast-forward or merge the current HEAD into it") {
		t.Fatalf("expected next_action guidance, got: %s", result)
	}
}

func TestGitMergeStatusIncludesConflictPreviews(t *testing.T) {
	dir := initGitRepo(t)
	runWithEnv := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@test.com",
			"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@test.com",
		)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v failed: %s\n%s", args, err, out)
		}
	}

	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n\nfunc shared() string {\n\treturn \"base\"\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runWithEnv("add", "main.go")
	runWithEnv("commit", "-m", "add base file")

	runWithEnv("checkout", "-b", "feature")
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n\nfunc shared() string {\n\treturn \"feature\"\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runWithEnv("add", "main.go")
	runWithEnv("commit", "-m", "feature change")

	runWithEnv("checkout", "main")
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n\nfunc shared() string {\n\treturn \"main\"\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runWithEnv("add", "main.go")
	runWithEnv("commit", "-m", "main change")

	cmd := exec.Command("git", "merge", "feature")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@test.com",
		"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@test.com",
	)
	if out, err := cmd.CombinedOutput(); err == nil {
		t.Fatalf("expected merge conflict, got success: %s", out)
	}

	tool := NewGitMergeStatus(dir)
	result, err := tool.Execute(context.Background(), map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "conflict_previews:") {
		t.Fatalf("expected conflict previews, got: %s", result)
	}
	if !strings.Contains(result, "<<<<<<< HEAD") || !strings.Contains(result, ">>>>>>> feature") {
		t.Fatalf("expected marker preview, got: %s", result)
	}
}
