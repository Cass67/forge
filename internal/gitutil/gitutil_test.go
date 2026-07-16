package gitutil

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func setupRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := Init(dir); err != nil {
		t.Fatalf("git init: %v", err)
	}
	// Configure user for commits in the test repo.
	run(dir, "git", "config", "user.email", "test@test.com")
	run(dir, "git", "config", "user.name", "Test")
	return dir
}

func TestInitCreatesGitDir(t *testing.T) {
	dir := t.TempDir()
	if err := Init(dir); err != nil {
		t.Fatalf("Init: %v", err)
	}
	info, err := os.Stat(filepath.Join(dir, ".git"))
	if err != nil {
		t.Fatalf("expected .git directory: %v", err)
	}
	if !info.IsDir() {
		t.Fatal("expected .git to be a directory")
	}
}

func TestCommitAllWithNewFiles(t *testing.T) {
	dir := setupRepo(t)

	os.WriteFile(filepath.Join(dir, "hello.txt"), []byte("hello"), 0o644)

	if err := CommitAll(dir, "add hello"); err != nil {
		t.Fatalf("CommitAll: %v", err)
	}

	log, err := Log(dir, 5)
	if err != nil {
		t.Fatalf("Log: %v", err)
	}
	if !strings.Contains(log, "add hello") {
		t.Errorf("expected commit message in log, got: %s", log)
	}
}

func TestCommitAllNoChangesIsNoop(t *testing.T) {
	dir := setupRepo(t)

	// Create initial commit so repo isn't empty.
	os.WriteFile(filepath.Join(dir, "init.txt"), []byte("init"), 0o644)
	CommitAll(dir, "initial")

	// Second call with no changes should not error.
	if err := CommitAll(dir, "should not appear"); err != nil {
		t.Fatalf("CommitAll with no changes: %v", err)
	}

	log, _ := Log(dir, 5)
	if strings.Contains(log, "should not appear") {
		t.Error("expected no-op commit to be skipped")
	}
}

func TestLogReturnsHistory(t *testing.T) {
	dir := setupRepo(t)

	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a"), 0o644)
	CommitAll(dir, "first")

	os.WriteFile(filepath.Join(dir, "b.txt"), []byte("b"), 0o644)
	CommitAll(dir, "second")

	log, err := Log(dir, 10)
	if err != nil {
		t.Fatalf("Log: %v", err)
	}
	if !strings.Contains(log, "first") || !strings.Contains(log, "second") {
		t.Errorf("expected both commits in log, got: %s", log)
	}
}

func TestGeneratePRTemplate(t *testing.T) {
	dir := setupRepo(t)

	os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main"), 0o644)
	CommitAll(dir, "pass 1 round 1: correctness")

	path, err := GeneratePRTemplate(dir, "build a web server", "claude-sonnet-4-6", "gpt-4o")
	if err != nil {
		t.Fatalf("GeneratePRTemplate: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read template: %v", err)
	}
	content := string(data)

	for _, want := range []string{
		"## Summary",
		"claude-sonnet-4-6",
		"gpt-4o",
		"build a web server",
		"## Test Plan",
		"correctness",
	} {
		if !strings.Contains(content, want) {
			t.Errorf("template missing %q", want)
		}
	}
}

func TestDiffStat(t *testing.T) {
	dir := setupRepo(t)

	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a"), 0o644)
	CommitAll(dir, "first")

	os.WriteFile(filepath.Join(dir, "b.txt"), []byte("b"), 0o644)
	CommitAll(dir, "second")

	stat, err := DiffStat(dir)
	if err != nil {
		t.Fatalf("DiffStat: %v", err)
	}
	if !strings.Contains(stat, "b.txt") {
		t.Errorf("expected b.txt in diffstat, got: %s", stat)
	}
}

func TestCurrentBranch(t *testing.T) {
	dir := setupRepo(t)
	os.WriteFile(filepath.Join(dir, "init.txt"), []byte("init"), 0o644)
	if err := CommitAll(dir, "initial"); err != nil {
		t.Fatalf("CommitAll: %v", err)
	}

	current, err := CurrentBranch(dir)
	if err != nil {
		t.Fatalf("CurrentBranch: %v", err)
	}
	if current != "master" && current != "main" {
		t.Fatalf("unexpected initial branch %q", current)
	}
}

func TestCurrentBranchInUnbornRepo(t *testing.T) {
	dir := setupRepo(t)

	current, err := CurrentBranch(dir)
	if err != nil {
		t.Fatalf("CurrentBranch: %v", err)
	}
	if current == "" || current == "HEAD" {
		t.Fatalf("expected symbolic branch name, got %q", current)
	}
}

func TestIsRepository(t *testing.T) {
	nonRepo := t.TempDir()
	isRepo, err := IsRepository(nonRepo)
	if err != nil {
		t.Fatalf("IsRepository(nonRepo): %v", err)
	}
	if isRepo {
		t.Fatal("expected non-repo directory to report false")
	}

	repo := setupRepo(t)
	isRepo, err = IsRepository(repo)
	if err != nil {
		t.Fatalf("IsRepository(repo): %v", err)
	}
	if !isRepo {
		t.Fatal("expected git repo to report true")
	}
}
