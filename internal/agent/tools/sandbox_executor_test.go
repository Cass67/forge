package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestWithSandboxExecutorRoutesThroughExecutor(t *testing.T) {
	dir := t.TempDir()
	approveAll := func(a Action) (bool, error) { return true, nil }
	tool := WithSandboxExecutor(NewRunCommand(dir, 60, nil, approveAll), approveAll, FixedWorkDirProvider(dir), dir, DefaultSecretPolicy())

	var gotWorkDir, gotCommand string
	SetSandboxExecutor(func(ctx context.Context, workDir, command string) (string, bool, error) {
		gotWorkDir, gotCommand = workDir, command
		return "sandbox output\nexit 0", true, nil
	})
	defer SetSandboxExecutor(nil)

	result, err := tool.Execute(context.Background(), map[string]any{"command": "echo hello"})
	if err != nil {
		t.Fatal(err)
	}
	if gotCommand != "echo hello" {
		t.Fatalf("executor got command %q", gotCommand)
	}
	if gotWorkDir != dir {
		t.Fatalf("executor got workDir %q, want %q", gotWorkDir, dir)
	}
	if !strings.Contains(result, "sandbox output") {
		t.Fatalf("expected sandbox output, got %q", result)
	}
}

func TestWithSandboxExecutorRoutesBackgroundThroughSessionSandboxArgv(t *testing.T) {
	tests := []struct {
		name string
		args map[string]any
	}{
		{
			name: "explicit background flag",
			args: map[string]any{"command": "printf original", "run_in_background": true},
		},
		{
			name: "background suffix",
			args: map[string]any{"command": "printf original &"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			approveAll := func(a Action) (bool, error) { return true, nil }
			manager := NewExecSessionManager()
			tool := WithSandboxExecutor(NewRunCommand(dir, 60, manager, approveAll), approveAll, FixedWorkDirProvider(dir), dir, DefaultSecretPolicy())

			executorCalled := false
			SetSandboxExecutor(func(ctx context.Context, workDir, command string) (string, bool, error) {
				executorCalled = true
				return "", true, nil
			})
			var gotCommand string
			var gotTTY bool
			SetSandboxArgv(func(workDir, command string, tty bool) ([]string, bool, error) {
				gotCommand = command
				gotTTY = tty
				return []string{"sh", "-c", "printf sandbox-background"}, true, nil
			})
			t.Cleanup(func() {
				SetSandboxExecutor(nil)
				SetSandboxArgv(nil)
			})

			result, err := tool.Execute(context.Background(), tt.args)
			if err != nil {
				t.Fatal(err)
			}
			if executorCalled {
				t.Fatal("foreground sandbox executor must not handle background command")
			}
			if gotCommand != "printf original" {
				t.Fatalf("sandbox argv got command %q", gotCommand)
			}
			if !gotTTY {
				t.Fatal("background run_command session must request a TTY")
			}

			var status execSessionStatus
			if err := json.Unmarshal([]byte(result), &status); err != nil {
				t.Fatalf("expected session status, got %q: %v", result, err)
			}
			if status.Status != "running" || status.SessionID == 0 {
				t.Fatalf("background handle = %+v", status)
			}
		})
	}
}

func TestWithSandboxExecutorDeclinedFallsThroughToHost(t *testing.T) {
	dir := t.TempDir()
	approveAll := func(a Action) (bool, error) { return true, nil }
	tool := WithSandboxExecutor(NewRunCommand(dir, 60, nil, approveAll), approveAll, FixedWorkDirProvider(dir), dir, DefaultSecretPolicy())

	SetSandboxExecutor(func(ctx context.Context, workDir, command string) (string, bool, error) {
		return "", false, nil
	})
	defer SetSandboxExecutor(nil)

	result, err := tool.Execute(context.Background(), map[string]any{"command": "echo host"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "host") {
		t.Fatalf("expected host execution output, got %q", result)
	}
}

func TestWithSandboxExecutorNilUsesHost(t *testing.T) {
	dir := t.TempDir()
	approveAll := func(a Action) (bool, error) { return true, nil }
	tool := WithSandboxExecutor(NewRunCommand(dir, 60, nil, approveAll), approveAll, FixedWorkDirProvider(dir), dir, DefaultSecretPolicy())

	SetSandboxExecutor(nil)
	result, err := tool.Execute(context.Background(), map[string]any{"command": "echo plain"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "plain") {
		t.Fatalf("expected host execution output, got %q", result)
	}
}

func TestWithSandboxExecutorApprovalDenial(t *testing.T) {
	dir := t.TempDir()
	tool := WithSandboxExecutor(NewRunCommand(dir, 60, nil, func(a Action) (bool, error) { return true, nil }),
		func(a Action) (bool, error) { return false, nil }, FixedWorkDirProvider(dir), dir, DefaultSecretPolicy())

	called := false
	SetSandboxExecutor(func(ctx context.Context, workDir, command string) (string, bool, error) {
		called = true
		return "", true, nil
	})
	defer SetSandboxExecutor(nil)

	result, err := tool.Execute(context.Background(), map[string]any{"command": "echo nope"})
	if err != nil {
		t.Fatal(err)
	}
	if called {
		t.Fatal("executor must not run when approval denied")
	}
	if !strings.Contains(result, "denied") {
		t.Fatalf("expected denial message, got %q", result)
	}
}
