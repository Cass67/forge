package harness

import (
	"context"
	"strings"
	"testing"

	"forge/internal/agent/tools"
)

type stubScopedAgent struct {
	response    string
	err         error
	runMessages []string
	toolSets    []*tools.Registry
}

func (s *stubScopedAgent) Run(_ context.Context, userMessage string) error {
	s.runMessages = append(s.runMessages, userMessage)
	return s.err
}

func (s *stubScopedAgent) LastResponse() string {
	return s.response
}

func (s *stubScopedAgent) SetTools(reg *tools.Registry) {
	s.toolSets = append(s.toolSets, reg)
}

func TestAgentExecutorScopesInspectTurnsToReadOnlyTools(t *testing.T) {
	defaultTools := tools.NewRegistry()
	inspectTools := tools.NewRegistry()
	agent := &stubScopedAgent{response: "Directory contains cmd and internal."}
	exec := AgentExecutor{
		Agent:        agent,
		DefaultTools: defaultTools,
		InspectTools: inspectTools,
	}

	obs, err := exec.Execute(context.Background(), UserTurn{Text: "talk about this directory"}, Classification{
		Family:   FamilyInspect,
		TopicKey: "workspace:directory",
	})
	if err != nil {
		t.Fatal(err)
	}
	if obs.Response != "Directory contains cmd and internal." {
		t.Fatalf("response = %q", obs.Response)
	}
	if len(agent.toolSets) != 2 {
		t.Fatalf("tool set swaps = %d, want 2", len(agent.toolSets))
	}
	if agent.toolSets[0] != inspectTools {
		t.Fatal("expected inspect turn to install read-only tools first")
	}
	if agent.toolSets[1] != defaultTools {
		t.Fatal("expected inspect turn to restore default tools afterwards")
	}
	if len(agent.runMessages) != 1 {
		t.Fatalf("run messages = %d", len(agent.runMessages))
	}
	msg := agent.runMessages[0]
	if !strings.Contains(msg, "HARNESS MODE: inspect") {
		t.Fatalf("inspect prompt missing harness mode: %q", msg)
	}
	if !strings.Contains(msg, "provide the most useful walkthrough directly") {
		t.Fatalf("inspect prompt missing direct-answer guidance: %q", msg)
	}
	if !strings.Contains(msg, "talk about this directory") {
		t.Fatalf("inspect prompt missing original user request: %q", msg)
	}
}

func TestAgentExecutorLeavesNonInspectTurnsOnDefaultTools(t *testing.T) {
	defaultTools := tools.NewRegistry()
	inspectTools := tools.NewRegistry()
	agent := &stubScopedAgent{response: "implemented"}
	exec := AgentExecutor{
		Agent:        agent,
		DefaultTools: defaultTools,
		InspectTools: inspectTools,
	}

	obs, err := exec.Execute(context.Background(), UserTurn{Text: "implement the auth fix"}, Classification{
		Family:   FamilyImplement,
		TopicKey: "path:internal/auth.go",
	})
	if err != nil {
		t.Fatal(err)
	}
	if obs.Response != "implemented" {
		t.Fatalf("response = %q", obs.Response)
	}
	if len(agent.toolSets) != 1 {
		t.Fatalf("tool set swaps = %d, want 1", len(agent.toolSets))
	}
	if agent.toolSets[0] != defaultTools {
		t.Fatal("expected non-inspect turn to keep default tools")
	}
	if len(agent.runMessages) != 1 || agent.runMessages[0] != "implement the auth fix" {
		t.Fatalf("run messages = %#v", agent.runMessages)
	}
}

func TestAgentExecutorFocusedInspectPromptRequestsSerialSampledEvidence(t *testing.T) {
	defaultTools := tools.NewRegistry()
	inspectTools := tools.NewRegistry()
	agent := &stubScopedAgent{response: "sampled a few Python files"}
	exec := AgentExecutor{
		Agent:        agent,
		DefaultTools: defaultTools,
		InspectTools: inspectTools,
	}

	_, err := exec.Execute(context.Background(), UserTurn{Text: "check the py files and let me know if they are up to scratch"}, Classification{
		Family:   FamilyInspect,
		TopicKey: "files:python",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(agent.runMessages) != 1 {
		t.Fatalf("run messages = %d", len(agent.runMessages))
	}
	msg := agent.runMessages[0]
	if !strings.Contains(msg, "INSPECT SCOPE: focused-files") {
		t.Fatalf("inspect prompt missing focused-files scope: %q", msg)
	}
	if !strings.Contains(msg, "each working turn must emit exactly one tool call and no prose") {
		t.Fatalf("inspect prompt missing serial tool-call guidance: %q", msg)
	}
	if !strings.Contains(msg, "sample a small representative set of matching files") {
		t.Fatalf("inspect prompt missing bounded sampling guidance: %q", msg)
	}
}
