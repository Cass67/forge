package tools

import (
	"context"
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
