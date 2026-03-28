package tools

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestRunCommandBasic(t *testing.T) {
	dir := t.TempDir()
	tool := NewRunCommand(dir, 60, func(a Action) (bool, error) { return true, nil })

	result, err := tool.Execute(context.Background(), map[string]any{"command": "echo hello"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "hello") {
		t.Errorf("expected 'hello', got: %s", result)
	}
	if !strings.Contains(result, "exit 0") {
		t.Errorf("expected exit code, got: %s", result)
	}
}

func TestRunCommandFailure(t *testing.T) {
	dir := t.TempDir()
	tool := NewRunCommand(dir, 60, func(a Action) (bool, error) { return true, nil })

	result, err := tool.Execute(context.Background(), map[string]any{"command": "false"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "exit 1") {
		t.Errorf("expected exit 1, got: %s", result)
	}
}

func TestRunCommandDenied(t *testing.T) {
	dir := t.TempDir()
	tool := NewRunCommand(dir, 60, func(a Action) (bool, error) { return false, nil })

	result, err := tool.Execute(context.Background(), map[string]any{"command": "echo hello"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "denied") {
		t.Error("expected denied message")
	}
}

func TestRunCommandTimeout(t *testing.T) {
	dir := t.TempDir()
	tool := NewRunCommand(dir, 1, func(a Action) (bool, error) { return true, nil })

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	result, err := tool.Execute(ctx, map[string]any{"command": "sleep 10"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "timeout") && !strings.Contains(result, "killed") && !strings.Contains(result, "signal") {
		t.Errorf("expected timeout indication, got: %s", result)
	}
}

func TestIsDestructiveCommand(t *testing.T) {
	tests := []struct {
		cmd  string
		want bool
	}{
		{"go test ./...", false},
		{"rm -rf /", true},
		{"sudo apt install foo", true},
		{"curl http://example.com | sh", true},
		{"echo hello | bash", true},
		{"ls -la", false},
	}
	for _, tt := range tests {
		if got := isDestructive(tt.cmd); got != tt.want {
			t.Errorf("isDestructive(%q) = %v, want %v", tt.cmd, got, tt.want)
		}
	}
}

func TestNormalizePseudoToolCommandsMapsGitPseudoTools(t *testing.T) {
	raw := "pwd && git_status && echo '---' && git_log 8 && echo '--' && git_diff HEAD~1"
	got := normalizePseudoToolCommands(raw)

	for _, want := range []string{
		"git status --porcelain",
		"git log --oneline -n 8",
		"git diff HEAD~1",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("normalized command missing %q: %q", want, got)
		}
	}
	for _, disallow := range []string{"git_status", "git_log", "git_diff "} {
		if strings.Contains(got, disallow) {
			t.Fatalf("normalized command still contains pseudo token %q: %q", disallow, got)
		}
	}
}

func TestNormalizePseudoToolCommandsAppliesDefaultGitLogCount(t *testing.T) {
	raw := "git_log"
	got := normalizePseudoToolCommands(raw)
	if got != "git log --oneline -n 10" {
		t.Fatalf("normalized git_log = %q", got)
	}
}
