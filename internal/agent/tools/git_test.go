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
	runGit(t, dir, "init")
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

	approved := false
	var capturedAction Action
	approve := func(a Action) (bool, error) {
		capturedAction = a
		approved = true
		return true, nil
	}

	tool := NewGitCommitScoped(dir, approve, func() GitScope {
		return GitScope{AllowedPaths: []string{"new.go"}}
	})
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

func TestGitCommitScopedStagesOnlyAllowedPaths(t *testing.T) {
	dir := initGitRepo(t)
	mustWriteFile(t, filepath.Join(dir, "FORGE_VS_CODEX.md"), "doc\n")
	mustWriteFile(t, filepath.Join(dir, "unrelated.go"), "package main\n")

	scope := func() GitScope {
		return GitScope{AllowedPaths: []string{"FORGE_VS_CODEX.md"}, TargetBranch: "main"}
	}
	tool := NewGitCommitScoped(dir, func(Action) (bool, error) { return true, nil }, scope)
	result, err := tool.Execute(context.Background(), map[string]any{"message": "add comparison"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "commit") {
		t.Fatalf("result = %q", result)
	}

	out := gitOut(t, dir, "show", "--name-only", "--format=", "HEAD")
	if !strings.Contains(out, "FORGE_VS_CODEX.md") || strings.Contains(out, "unrelated.go") {
		t.Fatalf("commit files = %q", out)
	}
	status := gitOut(t, dir, "status", "--porcelain")
	if !strings.Contains(status, "unrelated.go") {
		t.Fatalf("unrelated file should remain dirty, status=%q", status)
	}
}

func TestGitPushScopedVerifiesRemoteContainsCommit(t *testing.T) {
	dir := initGitRepo(t)
	remote := filepath.Join(t.TempDir(), "remote.git")
	runGit(t, t.TempDir(), "init", "--bare", remote)
	runGit(t, dir, "branch", "-M", "main")
	runGit(t, dir, "remote", "add", "origin", remote)

	mustWriteFile(t, filepath.Join(dir, "FORGE_VS_CODEX.md"), "doc\n")
	scope := func() GitScope {
		return GitScope{AllowedPaths: []string{"FORGE_VS_CODEX.md"}, TargetBranch: "main", Remote: "origin", RequireBranch: true}
	}
	commit := NewGitCommitScoped(dir, func(Action) (bool, error) { return true, nil }, scope)
	if _, err := commit.Execute(context.Background(), map[string]any{"message": "add comparison"}); err != nil {
		t.Fatal(err)
	}

	push := NewGitPushScoped(dir, func(Action) (bool, error) { return true, nil }, scope)
	result, err := push.Execute(context.Background(), map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "remote contains") {
		t.Fatalf("result = %q", result)
	}
}

func TestGitPushScopedRequiresRemoteAndTargetBranchBeforeApproval(t *testing.T) {
	dir := initGitRepo(t)
	for _, tc := range []struct {
		name  string
		scope GitScope
	}{
		{name: "missing remote", scope: GitScope{TargetBranch: "main"}},
		{name: "missing target", scope: GitScope{Remote: "origin"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			approved := false
			push := NewGitPushScoped(dir, func(Action) (bool, error) {
				approved = true
				return true, nil
			}, func() GitScope { return tc.scope })
			result, err := push.Execute(context.Background(), map[string]any{})
			if err != nil {
				t.Fatal(err)
			}
			if approved {
				t.Fatal("git_push should block before approval")
			}
			if !strings.Contains(result, "blocked") {
				t.Fatalf("result = %q", result)
			}
		})
	}
}

func TestGitPushScopedRequiresCurrentBranchWhenConfigured(t *testing.T) {
	dir := initGitRepo(t)
	remote := filepath.Join(t.TempDir(), "remote.git")
	runGit(t, t.TempDir(), "init", "--bare", remote)
	runGit(t, dir, "branch", "-M", "feature")
	runGit(t, dir, "remote", "add", "origin", remote)
	approved := false
	push := NewGitPushScoped(dir, func(Action) (bool, error) {
		approved = true
		return true, nil
	}, func() GitScope {
		return GitScope{Remote: "origin", TargetBranch: "main", RequireBranch: true}
	})

	result, err := push.Execute(context.Background(), map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	if approved {
		t.Fatal("git_push should block branch mismatch before approval")
	}
	if !strings.Contains(result, "blocked") || !strings.Contains(result, "does not match") {
		t.Fatalf("result = %q", result)
	}
}

func TestGitPushScopedRejectsNonBranchTargetBeforeApproval(t *testing.T) {
	dir := initGitRepo(t)
	remote := filepath.Join(t.TempDir(), "remote.git")
	runGit(t, t.TempDir(), "init", "--bare", remote)
	runGit(t, dir, "branch", "-M", "main")
	runGit(t, dir, "remote", "add", "origin", remote)
	approved := false
	push := NewGitPushScoped(dir, func(Action) (bool, error) {
		approved = true
		return true, nil
	}, func() GitScope { return GitScope{TargetBranch: "refs/tags/pwn", Remote: "origin"} })

	result, err := push.Execute(context.Background(), map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	if approved {
		t.Fatal("git_push with non-branch target should not request approval")
	}
	if !strings.Contains(result, "blocked") || !strings.Contains(result, "target branch") {
		t.Fatalf("result = %q", result)
	}
	if out := gitOut(t, dir, "ls-remote", "origin", "refs/tags/pwn"); strings.TrimSpace(out) != "" {
		t.Fatalf("remote tag was mutated: %q", out)
	}
}

func TestGitPushScopedRejectsUnsafeRemoteBeforeApproval(t *testing.T) {
	dir := initGitRepo(t)
	approved := false
	push := NewGitPushScoped(dir, func(Action) (bool, error) {
		approved = true
		return true, nil
	}, func() GitScope { return GitScope{TargetBranch: "main", Remote: "https://example.com/repo.git"} })

	result, err := push.Execute(context.Background(), map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	if approved {
		t.Fatal("git_push with unsafe remote should not request approval")
	}
	if !strings.Contains(result, "blocked") || !strings.Contains(result, "remote") {
		t.Fatalf("result = %q", result)
	}
}

func TestGitPushScopedRejectsUnconfiguredRemoteBeforeApproval(t *testing.T) {
	dir := initGitRepo(t)
	approved := false
	push := NewGitPushScoped(dir, func(Action) (bool, error) {
		approved = true
		return true, nil
	}, func() GitScope { return GitScope{TargetBranch: "main", Remote: "evil"} })

	result, err := push.Execute(context.Background(), map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	if approved {
		t.Fatal("git_push with unconfigured remote should not request approval")
	}
	if !strings.Contains(result, "blocked") || !strings.Contains(result, "configured remote") {
		t.Fatalf("result = %q", result)
	}
}

func TestGitPushScopedDenialDoesNotPush(t *testing.T) {
	dir := initGitRepo(t)
	remote := filepath.Join(t.TempDir(), "remote.git")
	runGit(t, t.TempDir(), "init", "--bare", remote)
	runGit(t, dir, "branch", "-M", "main")
	runGit(t, dir, "remote", "add", "origin", remote)
	mustWriteFile(t, filepath.Join(dir, "FORGE_VS_CODEX.md"), "doc\n")
	scope := func() GitScope {
		return GitScope{AllowedPaths: []string{"FORGE_VS_CODEX.md"}, TargetBranch: "main", Remote: "origin"}
	}
	commit := NewGitCommitScoped(dir, func(Action) (bool, error) { return true, nil }, scope)
	if _, err := commit.Execute(context.Background(), map[string]any{"message": "add comparison"}); err != nil {
		t.Fatal(err)
	}
	approved := false
	push := NewGitPushScoped(dir, func(Action) (bool, error) {
		approved = true
		return false, nil
	}, scope)

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

func TestGitCommitScopedRejectsPreStagedUnrelatedFile(t *testing.T) {
	dir := initGitRepo(t)
	mustWriteFile(t, filepath.Join(dir, "FORGE_VS_CODEX.md"), "doc\n")
	mustWriteFile(t, filepath.Join(dir, "AI-1.md"), "agent report\n")
	runGit(t, dir, "add", "AI-1.md")

	scope := func() GitScope { return GitScope{AllowedPaths: []string{"FORGE_VS_CODEX.md"}} }
	tool := NewGitCommitScoped(dir, func(Action) (bool, error) { return true, nil }, scope)
	result, err := tool.Execute(context.Background(), map[string]any{"message": "add comparison"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "blocked") || !strings.Contains(result, "AI-1.md") {
		t.Fatalf("result = %q", result)
	}
}

func TestGitCommitRequiresScope(t *testing.T) {
	dir := initGitRepo(t)
	mustWriteFile(t, filepath.Join(dir, "new.go"), "package main\n")

	approved := false
	tool := NewGitCommit(dir, func(Action) (bool, error) {
		approved = true
		return true, nil
	})
	result, err := tool.Execute(context.Background(), map[string]any{"message": "add new file"})
	if err != nil {
		t.Fatal(err)
	}
	if approved {
		t.Fatal("legacy git_commit without scope should not request approval")
	}
	if !strings.Contains(result, "blocked") || !strings.Contains(result, "requires an active side-effect intent") {
		t.Fatalf("result = %q", result)
	}
}

func TestGitCommitScopedRequiresAllowedPaths(t *testing.T) {
	dir := initGitRepo(t)
	mustWriteFile(t, filepath.Join(dir, "new.go"), "package main\n")
	approved := false
	tool := NewGitCommitScoped(dir, func(Action) (bool, error) {
		approved = true
		return true, nil
	}, func() GitScope { return GitScope{} })

	result, err := tool.Execute(context.Background(), map[string]any{"message": "add new file"})
	if err != nil {
		t.Fatal(err)
	}
	if approved {
		t.Fatal("git_commit with empty scope should not request approval")
	}
	if !strings.Contains(result, "blocked") || !strings.Contains(result, "requires an active side-effect intent") {
		t.Fatalf("result = %q", result)
	}
}

func TestGitCommitScopedRejectsUnsafeAllowedPath(t *testing.T) {
	dir := initGitRepo(t)
	mustWriteFile(t, filepath.Join(dir, "FORGE_VS_CODEX.md"), "doc\n")
	approved := false
	tool := NewGitCommitScoped(dir, func(Action) (bool, error) {
		approved = true
		return true, nil
	}, func() GitScope { return GitScope{AllowedPaths: []string{"FORGE_VS_CODEX.md", "../bad.md"}} })

	result, err := tool.Execute(context.Background(), map[string]any{"message": "add comparison"})
	if err != nil {
		t.Fatal(err)
	}
	if approved {
		t.Fatal("git_commit with unsafe allowed path should not request approval")
	}
	if !strings.Contains(result, "blocked") || !strings.Contains(result, "unsafe") || !strings.Contains(result, "../bad.md") {
		t.Fatalf("result = %q", result)
	}
	if staged := gitOut(t, dir, "diff", "--cached", "--name-only"); strings.TrimSpace(staged) != "" {
		t.Fatalf("staged files = %q, want none", staged)
	}
}

func TestGitCommitScopedRejectsPathspecMetacharacters(t *testing.T) {
	dir := initGitRepo(t)
	mustWriteFile(t, filepath.Join(dir, "one.go"), "package main\n")
	mustWriteFile(t, filepath.Join(dir, "two.go"), "package main\n")
	approved := false
	tool := NewGitCommitScoped(dir, func(Action) (bool, error) {
		approved = true
		return true, nil
	}, func() GitScope { return GitScope{AllowedPaths: []string{"*.go"}} })

	result, err := tool.Execute(context.Background(), map[string]any{"message": "add go files"})
	if err != nil {
		t.Fatal(err)
	}
	if approved {
		t.Fatal("git_commit with pathspec metacharacters should not request approval")
	}
	if !strings.Contains(result, "blocked") || !strings.Contains(result, "unsafe") || !strings.Contains(result, "*.go") {
		t.Fatalf("result = %q", result)
	}
	if staged := gitOut(t, dir, "diff", "--cached", "--name-only"); strings.TrimSpace(staged) != "" {
		t.Fatalf("staged files = %q, want none", staged)
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
	tool := NewGitCommitScoped(dir, func(a Action) (bool, error) {
		capturedAction = a
		return false, nil
	}, func() GitScope { return GitScope{AllowedPaths: []string{secretPath}} })
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

	tool := NewGitCommitScoped(dir, func(a Action) (bool, error) { return false, nil }, func() GitScope {
		return GitScope{AllowedPaths: []string{"new.go"}}
	})
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

	tool := NewGitCommitScoped(dir, func(a Action) (bool, error) { return true, nil }, func() GitScope {
		return GitScope{AllowedPaths: []string{"main.go"}}
	})
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
