package react

import (
	"strings"
	"testing"
)

// A command that keeps returning the identical result is making no progress.
// The guard existed but only considered read-only commands, so a rejected
// command could repeat indefinitely.
func TestRepeatedIdenticalCommandIsBlocked(t *testing.T) {
	r := &Runner{}
	args := map[string]any{"command": "cd /tmp && npm exec -- node --version"}
	const sameResult = "use exec_session_start instead of run_command for interactive or long-running terminal work"

	for range repeatToolCallThreshold {
		r.updateRepeatToolCallWorkflow("run_command", args, sameResult)
	}

	blocked, ok := r.blockRepeatedExplorationToolCall("run_command", args)
	if !ok {
		t.Fatal("identical run_command returning an identical result was not blocked")
	}
	if !strings.Contains(blocked, "run_command") {
		t.Fatalf("block message = %q", blocked)
	}
}

// Re-running a build or test command while fixing errors is normal work, not
// thrash: the output changes each time, so it must not be blocked.
func TestRepeatedCommandWithChangingOutputIsNotBlocked(t *testing.T) {
	r := &Runner{}
	args := map[string]any{"command": "go build ./..."}

	outputs := []string{"error: 3 problems", "error: 2 problems", "error: 1 problem"}
	for _, out := range outputs {
		r.updateRepeatToolCallWorkflow("run_command", args, out)
	}

	if _, ok := r.blockRepeatedExplorationToolCall("run_command", args); ok {
		t.Fatal("a repeated build command with changing output was blocked as thrash")
	}
}

// Re-reading a file after editing it must not be blocked: the content changed,
// so the read is new information rather than a repeat.
func TestRereadAfterChangeIsNotBlocked(t *testing.T) {
	r := &Runner{}
	args := map[string]any{"path": "main.go"}

	r.updateRepeatToolCallWorkflow("read_file", args, "package main // v1")
	r.updateRepeatToolCallWorkflow("read_file", args, "package main // v1")
	r.updateRepeatToolCallWorkflow("read_file", args, "package main // v2")

	if _, ok := r.blockRepeatedExplorationToolCall("read_file", args); ok {
		t.Fatal("re-reading a file whose content changed was blocked as thrash")
	}
}
