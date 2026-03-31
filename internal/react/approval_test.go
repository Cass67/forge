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
	updates := gate.ApprovalUpdates()
	if len(updates) != 1 {
		t.Fatalf("update count = %d, want 1", len(updates))
	}
	if updates[0].Decision != ApprovalDecisionForbidden || updates[0].Source != ApprovalDecisionSourceRule {
		t.Fatalf("forbidden rule update = %#v", updates[0])
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
	updates := gate.ApprovalUpdates()
	if len(updates) != 1 {
		t.Fatalf("update count = %d, want 1", len(updates))
	}
	if updates[0].Decision != ApprovalDecisionAllow || updates[0].Source != ApprovalDecisionSourceTrusted {
		t.Fatalf("trusted approval update = %#v", updates[0])
	}
}

func TestApprovalGateUnlessTrustedPromptsWhenGuardianWarnsTrustedCommand(t *testing.T) {
	promptCalls := 0
	gate := NewApprovalGate("", ApprovalConfig{
		DefaultPolicy:    ApprovalUnlessTrusted,
		SandboxPolicy:    SandboxWorkspaceWrite,
		KnownSafeCommand: []string{"git status"},
	}, func(action tools.Action) (bool, error) {
		promptCalls++
		return true, nil
	}, nil)
	gate.SetGuardianReviewer(func(transcript string, action tools.Action) tools.GuardianReview {
		return tools.GuardianReview{
			Decision: tools.GuardianWarn,
			Reason:   "needs context",
		}
	})

	approved, err := gate.Approve(tools.Action{
		Tool:    "run_command",
		Summary: "git status --short",
		Detail:  "git status --short",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !approved {
		t.Fatal("expected warned trusted command to prompt and approve")
	}
	if promptCalls != 1 {
		t.Fatalf("prompt calls = %d, want 1", promptCalls)
	}

	updates := gate.ApprovalUpdates()
	if len(updates) != 1 {
		t.Fatalf("update count = %d, want 1", len(updates))
	}
	if updates[0].Decision != ApprovalDecisionPrompt || updates[0].Source != ApprovalDecisionSourceGuardian {
		t.Fatalf("guardian-warn trusted update = %#v", updates[0])
	}
}

func TestApprovalGateUnlessTrustedPromptsForEscapedWildcardSummary(t *testing.T) {
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
		Summary: `git \* status`,
		Detail:  `git \* status`,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !approved {
		t.Fatal("expected prompt approval to allow escaped wildcard summary")
	}
	if promptCalls != 1 {
		t.Fatalf("prompt calls = %d, want 1", promptCalls)
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
	updates := gate.ApprovalUpdates()
	if len(updates) != 1 {
		t.Fatalf("update count = %d, want 1", len(updates))
	}
	if updates[0].Decision != ApprovalDecisionPrompt || updates[0].Source != ApprovalDecisionSourcePolicy {
		t.Fatalf("prompt approval update = %#v", updates[0])
	}
}

func TestApprovalGateGuardianBlocksDestructiveAction(t *testing.T) {
	prompted := false
	gate := NewApprovalGate("", ApprovalConfig{
		DefaultPolicy: ApprovalNever,
		SandboxPolicy: SandboxWorkspaceWrite,
	}, func(action tools.Action) (bool, error) {
		prompted = true
		return true, nil
	}, nil)
	gate.SetGuardianReviewer(func(transcript string, action tools.Action) tools.GuardianReview {
		return tools.ReviewApprovalAction(transcript, action)
	})
	gate.SetGuardianContext(func() string { return "task: clean artifacts" })

	approved, err := gate.Approve(tools.Action{
		Tool:    "run_command",
		Summary: "rm -rf /",
		Detail:  "rm -rf /",
	})
	if err != nil {
		t.Fatal(err)
	}
	if approved {
		t.Fatal("expected guardian to block destructive action")
	}
	if prompted {
		t.Fatal("guardian block should happen before prompt")
	}
	updates := gate.ApprovalUpdates()
	if len(updates) != 1 {
		t.Fatalf("update count = %d, want 1", len(updates))
	}
	if updates[0].Decision != ApprovalDecisionForbidden || updates[0].Source != ApprovalDecisionSourceGuardian {
		t.Fatalf("guardian block update = %#v", updates[0])
	}
}

func TestApprovalGateGuardianWarningForcesPrompt(t *testing.T) {
	promptCalls := 0
	var event GuardianEvent
	gate := NewApprovalGate("", ApprovalConfig{
		DefaultPolicy: ApprovalNever,
		SandboxPolicy: SandboxWorkspaceWrite,
	}, func(action tools.Action) (bool, error) {
		promptCalls++
		if !strings.Contains(action.Summary, "[guardian]") {
			t.Fatalf("prompt summary = %q", action.Summary)
		}
		return true, nil
	}, nil)
	gate.SetGuardianReviewer(func(transcript string, action tools.Action) tools.GuardianReview {
		return tools.ReviewApprovalAction(transcript, action)
	})
	gate.SetGuardianContext(func() string { return "" })
	gate.SetGuardianObserver(func(ev GuardianEvent) { event = ev })

	approved, err := gate.Approve(tools.Action{
		Tool:    "run_command",
		Summary: "git merge feature/runtime",
		Detail:  "git merge feature/runtime",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !approved {
		t.Fatal("expected prompt approval to allow warned action")
	}
	if promptCalls != 1 {
		t.Fatalf("prompt calls = %d, want 1", promptCalls)
	}
	if event.Decision != tools.GuardianWarn {
		t.Fatalf("guardian event = %#v", event)
	}
	if event.Action.Tool != "run_command" {
		t.Fatalf("guardian event = %#v", event)
	}
	updates := gate.ApprovalUpdates()
	if len(updates) != 1 {
		t.Fatalf("update count = %d, want 1", len(updates))
	}
	if updates[0].Decision != ApprovalDecisionPrompt {
		t.Fatalf("guardian warning update = %#v", updates[0])
	}
}

func TestApprovalGateGuardianObserverReceivesBlockEvent(t *testing.T) {
	var event GuardianEvent
	gate := NewApprovalGate("", ApprovalConfig{
		DefaultPolicy: ApprovalNever,
		SandboxPolicy: SandboxWorkspaceWrite,
	}, func(action tools.Action) (bool, error) {
		return true, nil
	}, nil)
	gate.SetGuardianReviewer(func(transcript string, action tools.Action) tools.GuardianReview {
		return tools.ReviewApprovalAction(transcript, action)
	})
	gate.SetGuardianObserver(func(ev GuardianEvent) { event = ev })

	approved, err := gate.Approve(tools.Action{
		Tool:    "run_command",
		Summary: "rm -rf /",
		Detail:  "rm -rf /",
	})
	if err != nil {
		t.Fatal(err)
	}
	if approved {
		t.Fatal("expected blocked action")
	}
	if event.Decision != tools.GuardianBlock {
		t.Fatalf("guardian event = %#v", event)
	}
}

func TestCompactGuardianContextIncludesTaskAndPlanState(t *testing.T) {
	snapshot := SessionSnapshot{
		LastInput: "implement the runtime change",
		Mode:      ModeImplement,
		TaskState: &TaskState{
			Objective: "implement the runtime change",
			Operation: "implement",
		},
		PlanState: &PlanState{
			Steps: []PlanStep{
				{Step: "Patch runtime", Status: "blocked", Blocker: "need user confirmation"},
			},
		},
	}

	context := CompactGuardianContext(snapshot)
	for _, want := range []string{"mode=implement", "operation=implement", "objective=implement the runtime change", "active_step=Patch runtime", "blocker=need user confirmation"} {
		if !strings.Contains(context, want) {
			t.Fatalf("guardian context missing %q: %q", want, context)
		}
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
	updates := gate.ApprovalUpdates()
	if len(updates) != 1 {
		t.Fatalf("update count = %d, want 1", len(updates))
	}
	if updates[0].Decision != ApprovalDecisionPrompt || updates[0].Source != ApprovalDecisionSourceSandbox {
		t.Fatalf("sandbox escalation update = %#v", updates[0])
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

func TestApprovalGateRestoreDeletesMergedSafetyBranch(t *testing.T) {
	repo := setupApprovalRepo(t)
	ensureMainBranch(t, repo)

	var progress []string
	gate := NewApprovalGate(repo, ApprovalConfig{
		DefaultPolicy: ApprovalNever,
		SandboxPolicy: SandboxWorkspaceWrite,
	}, func(action tools.Action) (bool, error) {
		return true, nil
	}, func(msg string) { progress = append(progress, msg) })

	// Trigger branch creation by approving a mutation on main.
	if _, err := gate.Approve(tools.Action{Tool: "write_file", Summary: "write internal/app.go"}); err != nil {
		t.Fatal(err)
	}
	safetyBranch, err := gitutil.CurrentBranch(repo)
	if err != nil || safetyBranch == "main" {
		t.Fatalf("expected forge/* branch, got %q err %v", safetyBranch, err)
	}

	// Simulate a commit on the safety branch.
	if err := os.WriteFile(filepath.Join(repo, "app.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "add", "app.go")
	runGit(t, repo, "commit", "-m", "add app.go")

	// Merge the safety branch into main so Restore() can delete it.
	runGit(t, repo, "checkout", "main")
	runGit(t, repo, "merge", "--ff-only", safetyBranch)
	// Switch back to safety branch to simulate the session still being on it.
	runGit(t, repo, "checkout", safetyBranch)

	gate.Restore()

	// Should be back on main.
	current, err := gitutil.CurrentBranch(repo)
	if err != nil {
		t.Fatal(err)
	}
	if current != "main" {
		t.Fatalf("expected main after Restore, got %q", current)
	}

	// Safety branch should have been deleted.
	exists, err := gitutil.BranchExists(repo, safetyBranch)
	if err != nil {
		t.Fatal(err)
	}
	if exists {
		t.Fatalf("expected safety branch %q to be deleted after Restore", safetyBranch)
	}

	// Progress messages should include deletion notice.
	var deletedMsg string
	for _, msg := range progress {
		if strings.Contains(msg, "Deleted merged branch") {
			deletedMsg = msg
		}
	}
	if deletedMsg == "" {
		t.Errorf("expected 'Deleted merged branch' progress message, got: %v", progress)
	}
}

func TestApprovalGateRestoreKeepsUnmergedSafetyBranch(t *testing.T) {
	repo := setupApprovalRepo(t)
	ensureMainBranch(t, repo)

	gate := NewApprovalGate(repo, ApprovalConfig{
		DefaultPolicy: ApprovalNever,
		SandboxPolicy: SandboxWorkspaceWrite,
	}, func(action tools.Action) (bool, error) {
		return true, nil
	}, nil)

	// Trigger branch creation.
	if _, err := gate.Approve(tools.Action{Tool: "write_file", Summary: "write internal/app.go"}); err != nil {
		t.Fatal(err)
	}
	safetyBranch, err := gitutil.CurrentBranch(repo)
	if err != nil || safetyBranch == "main" {
		t.Fatalf("expected forge/* branch, got %q err %v", safetyBranch, err)
	}

	// Commit on the safety branch but do NOT merge into main.
	if err := os.WriteFile(filepath.Join(repo, "app.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "add", "app.go")
	runGit(t, repo, "commit", "-m", "add app.go")

	gate.Restore()

	// Should be back on main.
	current, err := gitutil.CurrentBranch(repo)
	if err != nil {
		t.Fatal(err)
	}
	if current != "main" {
		t.Fatalf("expected main after Restore, got %q", current)
	}

	// Safety branch should still exist since it was not merged.
	exists, err := gitutil.BranchExists(repo, safetyBranch)
	if err != nil {
		t.Fatal(err)
	}
	if !exists {
		t.Fatalf("expected unmerged safety branch %q to be kept", safetyBranch)
	}
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
