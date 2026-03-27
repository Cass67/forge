package harness

import (
	"context"
	"fmt"
	"strings"

	"forge/internal/agent"
	"forge/internal/agent/tools"
	"forge/internal/skills"
)

type StrictScopedAgent interface {
	ScopedAgentRunner
	SetSystem(string)
	UseGeneratedSystem()
	SetRole(string)
	Role() string
	InjectSkill(skills.Skill)
	EmitProgress(string)
}

type StrictAgentExecutor struct {
	Agent          StrictScopedAgent
	DefaultTools   *tools.Registry
	InspectTools   *tools.Registry
	PreviewRuntime *tools.PreviewRuntime
	WorkDir        string
	LoadedSkills   []skills.Skill
	AutoSkillsMode string
}

func (e StrictAgentExecutor) Execute(ctx context.Context, turn UserTurn, class Classification, session SessionState) (Observation, error) {
	if e.Agent == nil {
		err := fmt.Errorf("strict local agent unavailable")
		return Observation{
			Status:   ObservationBlocked,
			Summary:  err.Error(),
			TopicKey: class.TopicKey,
			Err:      err,
		}, err
	}

	userMessage := buildStrictLocalTurnPrompt(class, turn.Text, session)
	selectedTools := e.DefaultTools
	if useReadOnlyInspectScope(class) && e.InspectTools != nil {
		selectedTools = e.InspectTools
		if e.DefaultTools != nil {
			defer e.Agent.SetTools(e.DefaultTools)
		}
	}
	if selectedTools != nil {
		e.Agent.SetTools(selectedTools)
	}

	prevRole := strings.TrimSpace(e.Agent.Role())
	e.Agent.SetRole("strictlocal")
	defer e.Agent.SetRole(prevRole)

	e.Agent.SetSystem(agent.BuildStrictLocalSystemPrompt(e.WorkDir, selectedTools, e.LoadedSkills))
	defer e.Agent.UseGeneratedSystem()

	skillRuntime := skills.NewRuntime(e.LoadedSkills)
	applyStrictLocalSkillContext(e.Agent, skillRuntime, turn.Text, e.AutoSkillsMode)
	if progress := strictLocalProgressMessage(class, skillRuntime.UseRecords()); progress != "" {
		e.Agent.EmitProgress(progress)
	}

	if err := e.Agent.Run(ctx, userMessage); err != nil {
		return Observation{
			Status:    ObservationBlocked,
			Summary:   err.Error(),
			TopicKey:  class.TopicKey,
			SkillUses: skillRuntime.UseRecords(),
			Err:       err,
		}, err
	}

	response := strings.TrimSpace(e.Agent.LastResponse())
	if err := validateLocalResponse(class, response); err != nil {
		return Observation{
			Status:    ObservationBlocked,
			Summary:   err.Error(),
			TopicKey:  class.TopicKey,
			SkillUses: skillRuntime.UseRecords(),
			Err:       err,
		}, err
	}

	return Observation{
		Status:    ObservationComplete,
		Response:  response,
		Summary:   response,
		TopicKey:  class.TopicKey,
		Runtime:   captureLocalRuntimeSnapshot(e.PreviewRuntime, e.Agent),
		SkillUses: skillRuntime.UseRecords(),
	}, nil
}

func applyStrictLocalSkillContext(agent StrictScopedAgent, runtime *skills.Runtime, input, autoMode string) {
	if agent == nil || runtime == nil {
		return
	}

	requiredName := skills.RequiredForInput(input)
	injected := make(map[string]struct{})
	if requiredName != "" {
		required, ok := runtime.ResolveRequired(input)
		if !ok {
			runtime.RecordSkillUse(requiredName, "strictlocal", "required_missing")
		} else {
			agent.InjectSkill(required)
			runtime.RecordSkillUse(required.Name, "strictlocal", "required_applied")
			injected[required.Name] = struct{}{}
		}
	}

	if skills.NormalizeAutoMode(autoMode) != skills.AutoSkillsAuto {
		return
	}
	autoSkill, ok := runtime.ResolveAuto(input)
	if !ok {
		return
	}
	if _, seen := injected[autoSkill.Name]; seen {
		return
	}
	agent.InjectSkill(autoSkill)
	runtime.RecordSkillUse(autoSkill.Name, "strictlocal", "auto_applied")
}

func strictLocalProgressMessage(class Classification, uses []skills.UseRecord) string {
	if names := appliedSkillNames(uses); len(names) > 0 {
		return "Using " + humanJoin(names) + " guidance while working through this request"
	}

	switch class.Family {
	case FamilyInspect:
		return "Inspecting the workspace"
	case FamilyDebug:
		return "Investigating the issue"
	case FamilyVerify:
		return "Checking the result"
	case FamilyResearch:
		return "Gathering current references"
	default:
		return "Working through the request"
	}
}

func appliedSkillNames(uses []skills.UseRecord) []string {
	names := make([]string, 0, len(uses))
	for _, use := range uses {
		switch strings.TrimSpace(use.Outcome) {
		case "required_applied", "auto_applied":
			names = append(names, strings.TrimSpace(use.Name))
		}
	}
	return names
}

func humanJoin(items []string) string {
	trimmed := make([]string, 0, len(items))
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item != "" {
			trimmed = append(trimmed, item)
		}
	}
	switch len(trimmed) {
	case 0:
		return ""
	case 1:
		return trimmed[0]
	case 2:
		return trimmed[0] + " and " + trimmed[1]
	default:
		return strings.Join(trimmed[:len(trimmed)-1], ", ") + ", and " + trimmed[len(trimmed)-1]
	}
}

func buildStrictLocalTurnPrompt(class Classification, userMessage string, session SessionState) string {
	if useReadOnlyInspectScope(class) {
		return buildInspectTurnPrompt(class, userMessage, session)
	}
	if class.PrefersVisibleExecution {
		return buildVisibleCollaborationTurnPrompt(class, userMessage, session)
	}
	return strings.TrimSpace(userMessage)
}
