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
	run := func(args ...string) {
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
	run("init")
	run("config", "user.email", "test@test.com")
	run("config", "user.name", "test")
	os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n"), 0o644)
	run("add", ".")
	run("commit", "-m", "initial")
	return dir
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
	if !strings.Contains(result, "add new file") {
		t.Errorf("expected commit confirmation, got: %s", result)
	}

	cmd := exec.Command("git", "log", "--oneline", "-1")
	cmd.Dir = dir
	out, _ := cmd.Output()
	if !strings.Contains(string(out), "add new file") {
		t.Errorf("commit not found in git log: %s", out)
	}
}

func TestGitCommitApprovalRedactsSecretPaths(t *testing.T) {
	dir := initGitRepo(t)
	secret := dummySecret()
	secretPath := "token-" + secret + ".txt"
	if err := os.WriteFile(filepath.Join(dir, secretPath), []byte("placeholder\n"), 0o644); err != nil {
		t.Fatal(err)
	}

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
