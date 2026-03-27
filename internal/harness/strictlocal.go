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
}

type StrictAgentExecutor struct {
	Agent        StrictScopedAgent
	DefaultTools *tools.Registry
	WorkDir      string
	LoadedSkills []skills.Skill
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
	if e.DefaultTools != nil {
		e.Agent.SetTools(e.DefaultTools)
	}

	prevRole := strings.TrimSpace(e.Agent.Role())
	e.Agent.SetRole("strictlocal")
	defer e.Agent.SetRole(prevRole)

	e.Agent.SetSystem(agent.BuildStrictLocalSystemPrompt(e.WorkDir, e.DefaultTools, e.LoadedSkills))
	defer e.Agent.UseGeneratedSystem()

	if err := e.Agent.Run(ctx, userMessage); err != nil {
		return Observation{
			Status:   ObservationBlocked,
			Summary:  err.Error(),
			TopicKey: class.TopicKey,
			Err:      err,
		}, err
	}

	response := strings.TrimSpace(e.Agent.LastResponse())
	if err := validateLocalResponse(class, response); err != nil {
		return Observation{
			Status:   ObservationBlocked,
			Summary:  err.Error(),
			TopicKey: class.TopicKey,
			Err:      err,
		}, err
	}

	return Observation{
		Status:   ObservationComplete,
		Response: response,
		Summary:  response,
		TopicKey: class.TopicKey,
	}, nil
}

func buildStrictLocalTurnPrompt(class Classification, userMessage string, session SessionState) string {
	if class.PrefersVisibleExecution {
		return buildVisibleCollaborationTurnPrompt(class, userMessage, session)
	}
	return strings.TrimSpace(userMessage)
}
