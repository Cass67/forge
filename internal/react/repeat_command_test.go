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

	for range repeatToolCallBlockThreshold {
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

// Bookkeeping tools change nothing in the world, so interleaving one must not
// launder a loop: search X / update_plan / search X is still two search X.
func TestBookkeepingToolDoesNotResetRepeatChain(t *testing.T) {
	r := &Runner{}
	args := map[string]any{"pattern": "needle"}
	const same = "no matches"

	r.updateRepeatToolCallWorkflow("search", args, same)
	r.updateRepeatToolCallWorkflow("update_plan", map[string]any{"steps": "x"}, "plan updated")
	r.updateRepeatToolCallWorkflow("search", args, same)
	r.updateRepeatToolCallWorkflow("tool_help", map[string]any{"name": "search"}, "help text")
	r.updateRepeatToolCallWorkflow("search", args, same)

	if got := r.repeatWorkflow.streak; got < repeatToolCallThreshold {
		t.Fatalf("streak = %d, want >= %d: bookkeeping calls laundered the loop", got, repeatToolCallThreshold)
	}
}

// A mutating call must still reset the chain: after an edit the file changed,
// so re-reading or re-running is legitimate work rather than a repeat.
func TestMutatingToolResetsRepeatChain(t *testing.T) {
	r := &Runner{}
	args := map[string]any{"path": "main.go"}

	r.updateRepeatToolCallWorkflow("read_file", args, "v1")
	r.updateRepeatToolCallWorkflow("read_file", args, "v1")
	r.updateRepeatToolCallWorkflow("write_file", map[string]any{"path": "main.go"}, "written")
	r.updateRepeatToolCallWorkflow("read_file", args, "v1")

	if got := r.repeatWorkflow.streak; got != 1 {
		t.Fatalf("streak = %d, want 1: a mutating call must reset the chain", got)
	}
}

// The model gets nudged before it gets blocked, so a run that was one call from
// resolving is not cut off at the first sign of repetition.
func TestNudgeComesBeforeBlock(t *testing.T) {
	r := &Runner{}
	args := map[string]any{"command": "cd /tmp && npm exec -- node --version"}
	const same = "use exec_session_start instead of run_command"

	for range repeatToolCallThreshold {
		r.updateRepeatToolCallWorkflow("run_command", args, same)
	}
	if overlay := r.repeatWorkflow.overlayContent(0); overlay == "" {
		t.Fatal("no nudge at the nudge threshold")
	}
	if _, ok := r.blockRepeatedExplorationToolCall("run_command", args); ok {
		t.Fatal("blocked at the nudge threshold; the model should be warned first")
	}

	for range repeatToolCallBlockThreshold - repeatToolCallThreshold {
		r.updateRepeatToolCallWorkflow("run_command", args, same)
	}
	if _, ok := r.blockRepeatedExplorationToolCall("run_command", args); !ok {
		t.Fatal("not blocked after the nudge went unheeded")
	}
}
