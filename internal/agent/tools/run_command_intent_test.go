package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// Commands that merely name an interactive program are ordinary one-shot work
// and must run. Inferring intent from the text refused these, and the refusal
// was returned as a successful result, so retrying it changed nothing.
func TestRunCommandRunsCommandsThatMentionInteractivePrograms(t *testing.T) {
	dir := t.TempDir()
	tool := NewRunCommand(dir, 30, nil, approveAll)

	for _, cmd := range []string{
		"echo npm exec -- node --version",
		"echo node --version",
		"echo cat vite.config.ts",
		"echo grep watch src/",
		"echo which node",
	} {
		out, err := tool.Execute(context.Background(), map[string]any{"command": cmd})
		if err != nil {
			t.Fatalf("%q returned error: %v", cmd, err)
		}
		if strings.Contains(out, "instead of run_command") {
			t.Fatalf("%q was refused rather than run: %s", cmd, out)
		}
	}
}

// Long-running work is declared by the model, not guessed by the tool.
func TestRunCommandBackgroundIsDeclaredByParameter(t *testing.T) {
	dir := t.TempDir()
	tool := NewRunCommand(dir, 30, nil, approveAll)

	out, err := tool.Execute(context.Background(), map[string]any{
		"command":           "sleep 30",
		"run_in_background": true,
	})
	if err != nil {
		t.Fatal(err)
	}
	var status execSessionStatus
	if err := json.Unmarshal([]byte(out), &status); err != nil {
		t.Fatalf("background call did not return a session handle: %s", out)
	}
	if status.Status != "running" || status.SessionID == 0 {
		t.Fatalf("background handle = %+v", status)
	}
}

// A foreground command still runs in the foreground and returns its output.
func TestRunCommandForegroundReturnsOutput(t *testing.T) {
	dir := t.TempDir()
	tool := NewRunCommand(dir, 30, nil, approveAll)

	out, err := tool.Execute(context.Background(), map[string]any{"command": "echo hello"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "hello") {
		t.Fatalf("output = %q", out)
	}
}
