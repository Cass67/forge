package harness

import (
	"context"
	"strings"
	"testing"

	"forge/internal/agent/tools"
	"forge/internal/skills"
)

type strictScopedAgent struct {
	response       string
	err            error
	runMessages    []string
	toolSets       []*tools.Registry
	systemValues   []string
	injectedSkills []string
	progressLines  []string
	usedGenerated  int
	role           string
	roleHistory    []string
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

func (s *strictScopedAgent) InjectSkill(skill skills.Skill) {
	s.injectedSkills = append(s.injectedSkills, skill.Name)
}

func (s *strictScopedAgent) EmitProgress(msg string) {
	s.progressLines = append(s.progressLines, strings.TrimSpace(msg))
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
	if obs.Status != ObservationBlocked {
		t.Fatalf("status = %q", obs.Status)
	}
	if obs.Outcome.Kind != OutcomeRetry {
		t.Fatalf("outcome = %#v", obs.Outcome)
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

func TestStrictAgentExecutorUsesInspectPromptAndToolsForVisibleInspectTurns(t *testing.T) {
	defaultTools := tools.NewRegistry()
	defaultTools.Register(tools.Tool{Name: "preview_server_ensure", Description: "ensure preview"})
	inspectTools := tools.NewRegistry()
	inspectTools.Register(tools.Tool{Name: "read_file", Description: "read file"})

	agent := &strictScopedAgent{response: "Repository looks healthy."}
	exec := StrictAgentExecutor{
		Agent:        agent,
		DefaultTools: defaultTools,
		InspectTools: inspectTools,
		WorkDir:      t.TempDir(),
	}

	obs, err := exec.Execute(context.Background(), UserTurn{Text: "take a look at this repo and update me at every step"}, Classification{
		Family:                  FamilyInspect,
		PrefersVisibleExecution: true,
		WantsEvaluation:         true,
	}, SessionState{})
	if err != nil {
		t.Fatal(err)
	}
	if obs.Response != "Repository looks healthy." {
		t.Fatalf("response = %q", obs.Response)
	}
	if len(agent.runMessages) != 1 {
		t.Fatalf("run messages = %d", len(agent.runMessages))
	}
	prompt := agent.runMessages[0]
	if !strings.Contains(prompt, "HARNESS MODE: inspect") {
		t.Fatalf("inspect prompt missing harness mode: %q", prompt)
	}
	if !strings.Contains(prompt, "inspect the actual workspace before answering") {
		t.Fatalf("inspect prompt missing evidence-first rule: %q", prompt)
	}
	if !strings.Contains(prompt, "each working turn must emit exactly one tool call and no prose") {
		t.Fatalf("inspect prompt missing one-tool rule: %q", prompt)
	}
	if len(agent.toolSets) == 0 || agent.toolSets[0] != inspectTools {
		t.Fatalf("tool sets = %#v", agent.toolSets)
	}
	if len(agent.systemValues) != 1 {
		t.Fatalf("strict system values = %d", len(agent.systemValues))
	}
	if !strings.Contains(agent.systemValues[0], "read_file") {
		t.Fatalf("strict inspect system missing inspect tools: %q", agent.systemValues[0])
	}
	if strings.Contains(agent.systemValues[0], "preview_server_ensure") {
		t.Fatalf("strict inspect system should not advertise preview-only tools: %q", agent.systemValues[0])
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
	if err != nil {
		t.Fatalf("unexpected transport error: %v", err)
	}
	if obs.Status != ObservationBlocked {
		t.Fatalf("status = %q", obs.Status)
	}
	if obs.Outcome.Kind != OutcomeRetry {
		t.Fatalf("outcome = %#v", obs.Outcome)
	}
}

func TestStrictAgentExecutorRejectsProsePrefixedMalformedToolMarkup(t *testing.T) {
	defaultTools := tools.NewRegistry()
	agent := &strictScopedAgent{response: "Checking now.\n<tool_call>\n{\"args\":{\"path\":\"themes_preview.html\"}}\n</tool_call>"}
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
		t.Fatalf("unexpected transport error: %v", err)
	}
	if obs.Status != ObservationBlocked {
		t.Fatalf("status = %q", obs.Status)
	}
	if obs.Outcome.Kind != OutcomeRetry {
		t.Fatalf("outcome = %#v", obs.Outcome)
	}
}

func TestStrictAgentExecutorUsesInjectedSkillContext(t *testing.T) {
	defaultTools := tools.NewRegistry()
	defaultTools.Register(tools.Tool{Name: "read_file", Description: "read file"})

	agent := &strictScopedAgent{response: "Designed the new status panel."}
	exec := StrictAgentExecutor{
		Agent:        agent,
		DefaultTools: defaultTools,
		WorkDir:      t.TempDir(),
		LoadedSkills: []skills.Skill{{
			Name:        "brainstorming",
			Description: "plan before implementation",
			Body:        "Plan first.",
			Source:      "/tmp/brainstorming/SKILL.md",
		}},
	}

	obs, err := exec.Execute(context.Background(), UserTurn{Text: "design a new release status panel and update me as you go"}, Classification{
		Family:                  FamilyImplement,
		PrefersVisibleExecution: true,
	}, SessionState{})
	if err != nil {
		t.Fatal(err)
	}
	if obs.Status != ObservationComplete {
		t.Fatalf("observation = %#v", obs)
	}
	if len(agent.injectedSkills) != 1 || agent.injectedSkills[0] != "brainstorming" {
		t.Fatalf("injected skills = %#v", agent.injectedSkills)
	}
	if len(obs.SkillUses) != 1 || obs.SkillUses[0].Name != "brainstorming" || obs.SkillUses[0].Outcome != "required_applied" {
		t.Fatalf("skill uses = %#v", obs.SkillUses)
	}
	if len(agent.progressLines) == 0 || !strings.Contains(agent.progressLines[0], "brainstorming") {
		t.Fatalf("progress lines = %#v", agent.progressLines)
	}
}

func TestStrictLocalPreviewIdeatePhaseConstrainsToolsToArtifactFlow(t *testing.T) {
	defaultTools := tools.NewRegistry()
	defaultTools.Register(tools.Tool{Name: "artifact_write", Description: "write artifact"})
	defaultTools.Register(tools.Tool{Name: "artifact_read", Description: "read artifact"})
	defaultTools.Register(tools.Tool{Name: "preview_server_ensure", Description: "ensure preview"})
	defaultTools.Register(tools.Tool{Name: "preview_server_status", Description: "preview status"})
	defaultTools.Register(tools.Tool{Name: "list_dir", Description: "list"})
	defaultTools.Register(tools.Tool{Name: "read_file", Description: "read"})
	defaultTools.Register(tools.Tool{Name: "search", Description: "search"})
	defaultTools.Register(tools.Tool{Name: "edit_file", Description: "edit"})
	defaultTools.Register(tools.Tool{Name: "write_file", Description: "write"})
	defaultTools.Register(tools.Tool{Name: "run_command", Description: "run"})

	agent := &strictScopedAgent{response: "Updated mockups are ready in preview."}
	exec := StrictAgentExecutor{
		Agent:        agent,
		DefaultTools: defaultTools,
		WorkDir:      t.TempDir(),
	}

	_, err := exec.Execute(context.Background(), UserTurn{Text: "thats nice but i need this for a tui"}, Classification{
		Family:                  FamilyImplement,
		PrefersVisibleExecution: true,
		ThreadIntent:            TurnIntentContinueThread,
	}, SessionState{
		Threads: ThreadLedger{
			Active: ThreadState{
				ID:     "thread-1",
				Kind:   ThreadPreviewCollaboration,
				Status: ThreadAwaitingUserFeedback,
				Phase:  ThreadPhaseIdeate,
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(agent.toolSets) == 0 {
		t.Fatalf("tool sets = %#v", agent.toolSets)
	}
	selected := agent.toolSets[0]
	if _, ok := selected.Get("artifact_write"); !ok {
		t.Fatalf("expected artifact_write in constrained toolset")
	}
	if _, ok := selected.Get("preview_server_ensure"); !ok {
		t.Fatalf("expected preview_server_ensure in constrained toolset")
	}
	if _, ok := selected.Get("edit_file"); ok {
		t.Fatalf("unexpected edit_file in constrained toolset")
	}
	if _, ok := selected.Get("write_file"); ok {
		t.Fatalf("unexpected write_file in constrained toolset")
	}
	if _, ok := selected.Get("run_command"); ok {
		t.Fatalf("unexpected run_command in constrained toolset")
	}
}

func TestStrictAgentExecutorPreviewFollowUpExplicitApplyKeepsCodeEditTools(t *testing.T) {
	defaultTools := tools.NewRegistry()
	defaultTools.Register(tools.Tool{Name: "artifact_write", Description: "write artifact"})
	defaultTools.Register(tools.Tool{Name: "preview_server_ensure", Description: "ensure preview"})
	defaultTools.Register(tools.Tool{Name: "edit_file", Description: "edit"})
	defaultTools.Register(tools.Tool{Name: "write_file", Description: "write"})
	defaultTools.Register(tools.Tool{Name: "run_command", Description: "run"})

	agent := &strictScopedAgent{response: "Applied theme updates in app code."}
	exec := StrictAgentExecutor{
		Agent:        agent,
		DefaultTools: defaultTools,
		WorkDir:      t.TempDir(),
	}

	_, err := exec.Execute(context.Background(), UserTurn{Text: "apply this theme to internal/tui/chattheme.go"}, Classification{
		Family:                  FamilyImplement,
		PrefersVisibleExecution: true,
		ThreadIntent:            TurnIntentContinueThread,
	}, SessionState{
		Threads: ThreadLedger{
			Active: ThreadState{
				ID:     "thread-1",
				Kind:   ThreadPreviewCollaboration,
				Status: ThreadAwaitingUserFeedback,
				Phase:  ThreadPhaseIdeate,
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(agent.toolSets) == 0 {
		t.Fatalf("tool sets = %#v", agent.toolSets)
	}
	selected := agent.toolSets[0]
	if _, ok := selected.Get("edit_file"); !ok {
		t.Fatalf("expected edit_file in explicit apply toolset")
	}
	if _, ok := selected.Get("write_file"); !ok {
		t.Fatalf("expected write_file in explicit apply toolset")
	}
	if _, ok := selected.Get("run_command"); !ok {
		t.Fatalf("expected run_command in explicit apply toolset")
	}
}

func TestStrictLocalPreviewApplyPhaseKeepsCodeEditTools(t *testing.T) {
	defaultTools := tools.NewRegistry()
	defaultTools.Register(tools.Tool{Name: "artifact_write", Description: "write artifact"})
	defaultTools.Register(tools.Tool{Name: "preview_server_ensure", Description: "ensure preview"})
	defaultTools.Register(tools.Tool{Name: "edit_file", Description: "edit"})
	defaultTools.Register(tools.Tool{Name: "write_file", Description: "write"})
	defaultTools.Register(tools.Tool{Name: "run_command", Description: "run"})

	agent := &strictScopedAgent{response: "Applied theme updates in app code."}
	exec := StrictAgentExecutor{
		Agent:        agent,
		DefaultTools: defaultTools,
		WorkDir:      t.TempDir(),
	}

	_, err := exec.Execute(context.Background(), UserTurn{Text: "go ahead and implement it"}, Classification{
		Family:                  FamilyImplement,
		PrefersVisibleExecution: true,
		ThreadIntent:            TurnIntentContinueThread,
	}, SessionState{
		Threads: ThreadLedger{
			Active: ThreadState{
				ID:     "thread-1",
				Kind:   ThreadPreviewCollaboration,
				Status: ThreadAwaitingUserFeedback,
				Phase:  ThreadPhaseApply,
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(agent.toolSets) == 0 {
		t.Fatalf("tool sets = %#v", agent.toolSets)
	}
	selected := agent.toolSets[0]
	if _, ok := selected.Get("edit_file"); !ok {
		t.Fatalf("expected edit_file in apply-phase toolset")
	}
	if _, ok := selected.Get("write_file"); !ok {
		t.Fatalf("expected write_file in apply-phase toolset")
	}
	if _, ok := selected.Get("run_command"); !ok {
		t.Fatalf("expected run_command in apply-phase toolset")
	}
}
