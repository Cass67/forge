package harness

import (
	"context"
	"strings"
	"testing"

	"forge/internal/agent/tools"
)

type strictScopedAgent struct {
	response      string
	err           error
	runMessages   []string
	toolSets      []*tools.Registry
	systemValues  []string
	usedGenerated int
	role          string
	roleHistory   []string
}

func (s *strictScopedAgent) Run(_ context.Context, userMessage string) error {
	s.runMessages = append(s.runMessages, userMessage)
	return s.err
}

func (s *strictScopedAgent) LastResponse() string {
	return s.response
}

func (s *strictScopedAgent) SetTools(reg *tools.Registry) {
	s.toolSets = append(s.toolSets, reg)
}

func (s *strictScopedAgent) SetSystem(system string) {
	s.systemValues = append(s.systemValues, system)
}

func (s *strictScopedAgent) UseGeneratedSystem() {
	s.usedGenerated++
}

func (s *strictScopedAgent) SetRole(role string) {
	s.role = role
	s.roleHistory = append(s.roleHistory, role)
}

func (s *strictScopedAgent) Role() string {
	return s.role
}

func TestStrictAgentExecutorUsesStrictSystemPromptAndRole(t *testing.T) {
	defaultTools := tools.NewRegistry()
	defaultTools.Register(tools.Tool{Name: "preview_server_ensure", Description: "ensure preview"})
	agent := &strictScopedAgent{response: "Preview is live."}
	exec := StrictAgentExecutor{
		Agent:        agent,
		DefaultTools: defaultTools,
		WorkDir:      t.TempDir(),
	}

	obs, err := exec.Execute(context.Background(), UserTurn{Text: "show me the preview"}, Classification{
		Family:                  FamilyAnswer,
		PrefersVisibleExecution: true,
	}, SessionState{})
	if err != nil {
		t.Fatal(err)
	}
	if obs.Response != "Preview is live." {
		t.Fatalf("response = %q", obs.Response)
	}
	if len(agent.systemValues) != 1 {
		t.Fatalf("strict system values = %d", len(agent.systemValues))
	}
	if !strings.Contains(agent.systemValues[0], "strict visible collaboration turn") {
		t.Fatalf("strict system missing collaboration contract: %q", agent.systemValues[0])
	}
	if !strings.Contains(agent.systemValues[0], "preview_server_ensure") {
		t.Fatalf("strict system missing preview tool guidance: %q", agent.systemValues[0])
	}
	if agent.usedGenerated != 1 {
		t.Fatalf("UseGeneratedSystem count = %d", agent.usedGenerated)
	}
	if len(agent.roleHistory) == 0 || agent.roleHistory[0] != "strictlocal" {
		t.Fatalf("role history = %#v", agent.roleHistory)
	}
	if len(agent.toolSets) != 1 || agent.toolSets[0] != defaultTools {
		t.Fatalf("tool sets = %#v", agent.toolSets)
	}
}

func TestStrictAgentExecutorRejectsMalformedToolMarkup(t *testing.T) {
	defaultTools := tools.NewRegistry()
	agent := &strictScopedAgent{response: "<tool_call>\n{\"args\":{\"path\":\"themes_preview.html\"}}\n</tool_call>"}
	exec := StrictAgentExecutor{
		Agent:        agent,
		DefaultTools: defaultTools,
		WorkDir:      t.TempDir(),
	}

	obs, err := exec.Execute(context.Background(), UserTurn{Text: "show me the preview"}, Classification{
		Family:                  FamilyAnswer,
		PrefersVisibleExecution: true,
	}, SessionState{})
	if err == nil {
		t.Fatal("expected strict executor to fail closed on malformed tool markup")
	}
	if obs.Status != ObservationBlocked {
		t.Fatalf("status = %q", obs.Status)
	}
}
