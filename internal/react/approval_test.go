package react

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"forge/internal/agent/tools"
	"forge/internal/gitutil"
)

func dummyApprovalSecret() string {
	return "ghp_" + strings.Repeat("a", 36)
}

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

func TestApprovalGateRedactsSecretSummaryAndDetailBeforePromptAndUpdates(t *testing.T) {
	secret := dummyApprovalSecret()
	var prompted tools.Action
	gate := NewApprovalGate("", ApprovalConfig{
		DefaultPolicy: ApprovalOnRequest,
		SandboxPolicy: SandboxWorkspaceWrite,
	}, func(action tools.Action) (bool, error) {
		prompted = action
		return true, nil
	}, nil)

	approved, err := gate.Approve(tools.Action{
		Tool:    "write_file",
		Summary: "write token " + secret,
		Detail:  "token=" + secret,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !approved {
		t.Fatal("expected approval")
	}
	if strings.Contains(prompted.Summary, secret) {
		t.Fatalf("prompt summary leaked secret: %s", prompted.Summary)
	}
	if strings.Contains(prompted.Detail, secret) {
		t.Fatalf("prompt detail leaked secret: %s", prompted.Detail)
	}
	updates := gate.ApprovalUpdates()
	for _, update := range updates {
		if strings.Contains(update.Reason, secret) {
			t.Fatalf("approval update leaked secret: %#v", update)
		}
	}
}

func TestApprovalGateDeniesPromptApprovalAfterContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	gate := NewApprovalGate("", ApprovalConfig{
		DefaultPolicy: ApprovalOnRequest,
		SandboxPolicy: SandboxWorkspaceWrite,
	}, func(action tools.Action) (bool, error) {
		cancel()
		return true, nil
	}, nil)

	approved, err := gate.Approve(tools.Action{
		Context: ctx,
		Tool:    "write_file",
		Summary: "write internal/app.go",
		Detail:  "diff",
		Path:    "internal/app.go",
	})
	if err != nil {
		t.Fatal(err)
	}
	if approved {
		t.Fatal("approval after context cancellation should be denied")
	}
}

func TestApprovalGatePromptReturnsWhenContextCancelledWhilePromptBlocked(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	promptStarted := make(chan struct{})
	releasePrompt := make(chan struct{})
	gate := NewApprovalGate("", ApprovalConfig{
		DefaultPolicy: ApprovalOnRequest,
		SandboxPolicy: SandboxWorkspaceWrite,
	}, func(action tools.Action) (bool, error) {
		close(promptStarted)
		<-releasePrompt
		return true, nil
	}, nil)
	defer close(releasePrompt)

	result := make(chan struct {
		approved bool
		err      error
	}, 1)
	go func() {
		approved, err := gate.Approve(tools.Action{
			Context: ctx,
			Tool:    "write_file",
			Summary: "write internal/app.go",
			Detail:  "diff",
			Path:    "internal/app.go",
		})
		result <- struct {
			approved bool
			err      error
		}{approved: approved, err: err}
	}()
	<-promptStarted
	cancel()

	select {
	case got := <-result:
		if got.err != nil {
			t.Fatal(got.err)
		}
		if got.approved {
			t.Fatal("approval should be denied after context cancellation")
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("approval did not return promptly after context cancellation")
	}
}

func TestApprovalGateDeniesSecondContextPromptWhenCancelledPromptStillOwnsPrompt(t *testing.T) {
	firstCtx, cancelFirst := context.WithCancel(context.Background())
	firstStarted := make(chan struct{})
	releaseFirst := make(chan struct{})
	secondEntered := make(chan struct{}, 1)
	var mu sync.Mutex
	promptCalls := 0
	gate := NewApprovalGate("", ApprovalConfig{
		DefaultPolicy: ApprovalOnRequest,
		SandboxPolicy: SandboxWorkspaceWrite,
	}, func(action tools.Action) (bool, error) {
		mu.Lock()
		promptCalls++
		call := promptCalls
		mu.Unlock()
		if call == 1 {
			close(firstStarted)
			<-releaseFirst
			return false, nil
		}
		secondEntered <- struct{}{}
		return true, nil
	}, nil)

	firstResult := make(chan bool, 1)
	go func() {
		approved, err := gate.Approve(tools.Action{Context: firstCtx, Tool: "write_file", Summary: "write a", Detail: "diff"})
		if err != nil {
			t.Errorf("first approve: %v", err)
		}
		firstResult <- approved
	}()
	<-firstStarted
	cancelFirst()
	select {
	case approved := <-firstResult:
		if approved {
			t.Fatal("cancelled first approval should be denied")
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("first approval did not return promptly after cancellation")
	}

	secondResult := make(chan bool, 1)
	go func() {
		approved, err := gate.Approve(tools.Action{Context: context.Background(), Tool: "write_file", Summary: "write b", Detail: "diff"})
		if err != nil {
			t.Errorf("second approve: %v", err)
		}
		secondResult <- approved
	}()
	select {
	case approved := <-secondResult:
		if approved {
			t.Fatal("second approval should be denied while first prompt still owns prompt")
		}
	case <-secondEntered:
		t.Fatal("second prompt entered while first prompt still blocked")
	case <-time.After(200 * time.Millisecond):
		t.Fatal("second approval blocked behind abandoned prompt")
	}
	close(releaseFirst)
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

func TestApprovalGateOnRequestSkipsPromptForKnownSafe(t *testing.T) {
	promptCalls := 0
	gate := NewApprovalGate("", ApprovalConfig{
		DefaultPolicy:    ApprovalOnRequest,
		SandboxPolicy:    SandboxWorkspaceWrite,
		KnownSafeCommand: []string{"go test"},
	}, func(action tools.Action) (bool, error) {
		promptCalls++
		return true, nil
	}, nil)

	approved, err := gate.Approve(tools.Action{Tool: "run_command", Summary: "go test ./..."})
	if err != nil || !approved {
		t.Fatalf("Approve() = (%v, %v), want (true, nil)", approved, err)
	}
	if promptCalls != 0 {
		t.Fatalf("prompt calls = %d, want 0", promptCalls)
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
	if len(updates) != 2 {
		t.Fatalf("update count = %d, want 2", len(updates))
	}
	if updates[0].Decision != ApprovalDecisionPrompt || updates[0].Source != ApprovalDecisionSourceGuardian {
		t.Fatalf("guardian-warn trusted prompt update = %#v", updates[0])
	}
	if updates[1].Decision != ApprovalDecisionAllow || updates[1].Source != ApprovalDecisionSourceUser {
		t.Fatalf("guardian-warn trusted result update = %#v", updates[1])
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
	if len(updates) != 2 {
		t.Fatalf("update count = %d, want 2", len(updates))
	}
	if updates[0].Decision != ApprovalDecisionPrompt || updates[0].Source != ApprovalDecisionSourcePolicy {
		t.Fatalf("prompt request update = %#v", updates[0])
	}
	if updates[1].Decision != ApprovalDecisionAllow || updates[1].Source != ApprovalDecisionSourceUser {
		t.Fatalf("prompt result update = %#v", updates[1])
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
	if len(updates) != 2 {
		t.Fatalf("update count = %d, want 2", len(updates))
	}
	if updates[0].Decision != ApprovalDecisionPrompt || updates[0].Source != ApprovalDecisionSourceGuardian {
		t.Fatalf("guardian warning prompt update = %#v", updates[0])
	}
	if updates[1].Decision != ApprovalDecisionAllow || updates[1].Source != ApprovalDecisionSourceUser {
		t.Fatalf("guardian warning result update = %#v", updates[1])
	}
}

func TestApprovalGateGuardianWarningDoesNotBypassForbiddenCommandRule(t *testing.T) {
	promptCalls := 0
	gate := NewApprovalGate("", ApprovalConfig{
		DefaultPolicy: ApprovalNever,
		SandboxPolicy: SandboxWorkspaceWrite,
		Rules: []ApprovalRule{
			{
				Tool:     "run_command",
				Command:  "git push *",
				Decision: DecisionForbidden,
			},
		},
	}, func(action tools.Action) (bool, error) {
		promptCalls++
		return true, nil
	}, nil)
	gate.SetGuardianReviewer(func(transcript string, action tools.Action) tools.GuardianReview {
		return tools.GuardianReview{
			Decision: tools.GuardianWarn,
			Reason:   "double-check remote target",
		}
	})

	approved, err := gate.Approve(tools.Action{
		Tool:    "run_command",
		Summary: "git push origin",
		Detail:  "git push origin",
	})
	if err != nil {
		t.Fatal(err)
	}
	if approved {
		t.Fatal("expected forbidden command rule to win even after guardian warning")
	}
	if promptCalls != 0 {
		t.Fatalf("prompt calls = %d, want 0", promptCalls)
	}

	updates := gate.ApprovalUpdates()
	if len(updates) != 1 {
		t.Fatalf("update count = %d, want 1", len(updates))
	}
	if updates[0].Decision != ApprovalDecisionForbidden || updates[0].Source != ApprovalDecisionSourceRule {
		t.Fatalf("forbidden rule update = %#v", updates[0])
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
	if len(updates) != 2 {
		t.Fatalf("update count = %d, want 2", len(updates))
	}
	if updates[0].Decision != ApprovalDecisionPrompt || updates[0].Source != ApprovalDecisionSourceSandbox {
		t.Fatalf("sandbox escalation prompt update = %#v", updates[0])
	}
	if updates[1].Decision != ApprovalDecisionAllow || updates[1].Source != ApprovalDecisionSourceUser {
		t.Fatalf("sandbox escalation result update = %#v", updates[1])
	}
}

func TestApprovalGateNeverSwitchesBranches(t *testing.T) {
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
		t.Fatal("expected mutation to be approved")
	}

	branch, err := gitutil.CurrentBranch(repo)
	if err != nil {
		t.Fatal(err)
	}
	if branch != "main" {
		t.Fatalf("gate must never switch branches, got %q", branch)
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
