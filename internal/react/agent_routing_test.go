package react

import (
	"strings"
	"testing"
)

// wait_agent was the model's first wrong guess for a background command id.
// not_found alone sends it looking elsewhere; naming command_status stops it.
func TestAgentNotFoundRoutesNumericIDToCommandStatus(t *testing.T) {
	got := decorateAgentResultResumeState(AgentResult{ID: "1", Status: AgentStatusNotFound})
	if !strings.Contains(got.ResumeHint, "command_status") {
		t.Errorf("numeric agent id should be routed: %q", got.ResumeHint)
	}
}

func TestAgentNotFoundKeepsResumeHintForRealAgentIDs(t *testing.T) {
	got := decorateAgentResultResumeState(AgentResult{ID: "agent-7", Status: AgentStatusNotFound})
	if strings.Contains(got.ResumeHint, "command_status") {
		t.Errorf("a real agent id must not be misrouted: %q", got.ResumeHint)
	}
	if !strings.Contains(got.ResumeHint, "spawn a new agent") {
		t.Errorf("real agent ids keep the resume guidance: %q", got.ResumeHint)
	}
}
