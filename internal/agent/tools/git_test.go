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
