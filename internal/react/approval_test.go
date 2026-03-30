package react

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"forge/internal/agent/tools"
	"forge/internal/gitutil"
)

func TestApprovalGateRuleForbidsCommand(t *testing.T) {
	prompted := false
	gate := NewApprovalGate("", ApprovalConfig{
		DefaultPolicy: ApprovalOnRequest,
		SandboxPolicy: SandboxWorkspaceWrite,
		Rules: []ApprovalRule{
			{
				Tool:          "run_command",
				CommandPrefix: []string{"git", "push"},
				Decision:      DecisionForbidden,
			},
		},
	}, func(action tools.Action) (bool, error) {
		prompted = true
		return true, nil
	}, nil)

	approved, err := gate.Approve(tools.Action{
		Tool:    "run_command",
		Summary: "git push origin main",
		Detail:  "git push origin main",
	})
	if err != nil {
		t.Fatal(err)
	}
	if approved {
		t.Fatal("expected command to be forbidden by explicit rule")
	}
	if prompted {
		t.Fatal("forbidden rule should not prompt")
	}
}

func TestApprovalGateUnlessTrustedSkipsPromptForKnownSafe(t *testing.T) {
	promptCalls := 0
	gate := NewApprovalGate("", ApprovalConfig{
		DefaultPolicy:    ApprovalUnlessTrusted,
		SandboxPolicy:    SandboxWorkspaceWrite,
		KnownSafeCommand: []string{"git status"},
	}, func(action tools.Action) (bool, error) {
		promptCalls++
		return true, nil
	}, nil)

	approved, err := gate.Approve(tools.Action{
		Tool:    "run_command",
		Summary: "git status --porcelain",
		Detail:  "git status --porcelain",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !approved {
		t.Fatal("expected known-safe command to auto-approve")
	}
	if promptCalls != 0 {
		t.Fatalf("prompt calls = %d, want 0", promptCalls)
	}
}

func TestApprovalGateOnRequestPromptsForMutations(t *testing.T) {
	promptCalls := 0
	gate := NewApprovalGate("", ApprovalConfig{
		DefaultPolicy: ApprovalOnRequest,
		SandboxPolicy: SandboxWorkspaceWrite,
	}, func(action tools.Action) (bool, error) {
		promptCalls++
		return true, nil
	}, nil)

	approved, err := gate.Approve(tools.Action{
		Tool:    "write_file",
		Summary: "write foo.txt",
		Detail:  "new file",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !approved {
		t.Fatal("expected prompt approval to allow write_file")
	}
	if promptCalls != 1 {
		t.Fatalf("prompt calls = %d, want 1", promptCalls)
	}
}

func TestApprovalGateReadOnlySandboxCanBeOverriddenOnFailure(t *testing.T) {
	promptCalls := 0
	gate := NewApprovalGate("", ApprovalConfig{
		DefaultPolicy: ApprovalOnFailure,
		SandboxPolicy: SandboxReadOnly,
	}, func(action tools.Action) (bool, error) {
		promptCalls++
		return true, nil
	}, nil)

	approved, err := gate.Approve(tools.Action{
		Tool:    "edit_file",
		Summary: "edit main.go",
		Detail:  "--- a/main.go",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !approved {
		t.Fatal("expected sandbox override approval to allow edit_file")
	}
	if promptCalls != 1 {
		t.Fatalf("prompt calls = %d, want 1", promptCalls)
	}
}

func TestApprovalGateSwitchesOffProtectedBranchForMutation(t *testing.T) {
	repo := setupApprovalRepo(t)
	ensureMainBranch(t, repo)

	gate := NewApprovalGate(repo, ApprovalConfig{
		DefaultPolicy: ApprovalNever,
		SandboxPolicy: SandboxWorkspaceWrite,
	}, func(action tools.Action) (bool, error) {
		return true, nil
	}, nil)

	approved, err := gate.Approve(tools.Action{
		Tool:    "write_file",
		Summary: "write internal/app.go",
		Detail:  "new file",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !approved {
		t.Fatal("expected mutation to be approved after branch transition")
	}

	branch, err := gitutil.CurrentBranch(repo)
	if err != nil {
		t.Fatal(err)
	}
	if branch == "main" || branch == "master" {
		t.Fatalf("expected non-protected branch, got %q", branch)
	}
	if !strings.HasPrefix(branch, "forge/") {
		t.Fatalf("expected forge/* branch, got %q", branch)
	}
}

func TestApprovalGateDoesNotSwitchBranchesForReadOnlyGitQueries(t *testing.T) {
	repo := setupApprovalRepo(t)
	ensureMainBranch(t, repo)

	gate := NewApprovalGate(repo, ApprovalConfig{
		DefaultPolicy: ApprovalNever,
		SandboxPolicy: SandboxWorkspaceWrite,
	}, func(action tools.Action) (bool, error) {
		return true, nil
	}, nil)

	approved, err := gate.Approve(tools.Action{
		Tool:    "run_command",
		Summary: "git branch -vv",
		Detail:  "git branch -vv",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !approved {
		t.Fatal("expected read-only git query to be approved")
	}

	branch, err := gitutil.CurrentBranch(repo)
	if err != nil {
		t.Fatal(err)
	}
	if branch != "main" {
		t.Fatalf("expected to stay on main for read-only query, got %q", branch)
	}
}

func setupApprovalRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	runGit(t, dir, "init")
	runGit(t, dir, "config", "user.email", "test@test.com")
	runGit(t, dir, "config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("write README: %v", err)
	}
	runGit(t, dir, "add", "README.md")
	runGit(t, dir, "commit", "-m", "init")
	return dir
}

func ensureMainBranch(t *testing.T, dir string) {
	t.Helper()
	current, err := gitutil.CurrentBranch(dir)
	if err != nil {
		t.Fatalf("CurrentBranch: %v", err)
	}
	if current == "main" {
		return
	}
	runGit(t, dir, "checkout", "-b", "main")
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.CommandContext(context.Background(), "git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, string(out))
	}
}
