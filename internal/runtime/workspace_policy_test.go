package runtime

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"

	"forge/internal/gitutil"
	"forge/internal/harness"
)

func TestWorkspacePolicySwitchesOffProtectedBranchForActionTurns(t *testing.T) {
	repo := setupWorkspacePolicyRepo(t)
	ensureProtectedBranch(t, repo)

	policy := newWorkspacePolicy(repo)
	class := harness.Classification{
		Family:      harness.FamilyImplement,
		WantsAction: true,
		TaskText:    "implement auth fix",
	}

	milestone, err := policy.EnsureExecutionContext(context.Background(), harness.UserTurn{
		Text: "implement auth fix",
		Turn: 7,
	}, class, harness.SessionState{})
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(milestone.Message) == "" {
		t.Fatalf("expected non-empty milestone, got %#v", milestone)
	}

	current, err := gitutil.CurrentBranch(repo)
	if err != nil {
		t.Fatal(err)
	}
	if current == "main" || current == "master" {
		t.Fatalf("expected non-protected branch after policy, got %q", current)
	}
	if !strings.HasPrefix(current, "forge/") {
		t.Fatalf("expected forge branch name, got %q", current)
	}
}

func TestWorkspacePolicyNoopForNonActionTurn(t *testing.T) {
	repo := setupWorkspacePolicyRepo(t)
	start := ensureProtectedBranch(t, repo)

	policy := newWorkspacePolicy(repo)
	class := harness.Classification{
		Family:      harness.FamilyInspect,
		WantsAction: false,
		TaskText:    "describe this repo",
	}

	milestone, err := policy.EnsureExecutionContext(context.Background(), harness.UserTurn{
		Text: "describe this repo",
		Turn: 3,
	}, class, harness.SessionState{})
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(milestone.Message) != "" {
		t.Fatalf("expected empty milestone for non-action turn, got %#v", milestone)
	}

	current, err := gitutil.CurrentBranch(repo)
	if err != nil {
		t.Fatal(err)
	}
	if current != start {
		t.Fatalf("branch changed for non-action turn: got %q want %q", current, start)
	}
}

func TestWorkspacePolicyNoopOutsideGitRepo(t *testing.T) {
	dir := t.TempDir()

	policy := newWorkspacePolicy(dir)
	class := harness.Classification{
		Family:      harness.FamilyImplement,
		WantsAction: true,
		TaskText:    "implement auth fix",
	}

	milestone, err := policy.EnsureExecutionContext(context.Background(), harness.UserTurn{
		Text: "implement auth fix",
		Turn: 4,
	}, class, harness.SessionState{})
	if err != nil {
		t.Fatalf("unexpected policy error outside git repo: %v", err)
	}
	if strings.TrimSpace(milestone.Message) != "" {
		t.Fatalf("expected empty milestone outside git repo, got %#v", milestone)
	}
}

func setupWorkspacePolicyRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	runGit(t, dir, "init")
	runGit(t, dir, "config", "user.email", "test@test.com")
	runGit(t, dir, "config", "user.name", "Test")
	if err := os.WriteFile(dir+"/README.md", []byte("hello"), 0o644); err != nil {
		t.Fatalf("write README: %v", err)
	}
	runGit(t, dir, "add", "README.md")
	runGit(t, dir, "commit", "-m", "init")
	return dir
}

func ensureProtectedBranch(t *testing.T, dir string) string {
	t.Helper()
	current, err := gitutil.CurrentBranch(dir)
	if err != nil {
		t.Fatalf("CurrentBranch: %v", err)
	}
	if current == "main" || current == "master" {
		return current
	}
	runGit(t, dir, "checkout", "-b", "main")
	return "main"
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, string(out))
	}
}
