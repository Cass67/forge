package tools

import (
	"encoding/json"
	"strings"
	"testing"
)

// A background command hands back a session id. Without naming the tool that
// consumes it, a model guesses: observed wait_agent -> read_output -> sleep 20.
func TestBackgroundStatusNamesTheFollowUpTool(t *testing.T) {
	payload, err := json.Marshal(execSessionStatus{
		Status: "running", SessionID: 7, Command: "npm test", Next: nextForSession(7),
	})
	if err != nil {
		t.Fatal(err)
	}
	got := string(payload)
	for _, want := range []string{"command_status", "session_id", "7", "Do not sleep"} {
		if !strings.Contains(got, want) {
			t.Errorf("background result missing %q:\n%s", want, got)
		}
	}
}

// The hint must warn off the two subsystems that do not take this id, because
// those are exactly the ones a model reaches for.
func TestNextHintWarnsOffTheWrongSubsystems(t *testing.T) {
	got := nextForSession(3)
	if !strings.Contains(got, "read_output") || !strings.Contains(got, "wait_agent") {
		t.Errorf("hint should name the tools that do NOT take this id: %s", got)
	}
	if !strings.Contains(got, "command_status") {
		t.Errorf("hint must name the tool that does: %s", got)
	}
}
