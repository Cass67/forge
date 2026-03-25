package harness

import (
	"context"
	"strings"

	"forge/internal/agent/tools"
)

type AgentRunner interface {
	Run(ctx context.Context, userMessage string) error
	LastResponse() string
}

type ScopedAgentRunner interface {
	AgentRunner
	SetTools(reg *tools.Registry)
}

type LocalExecutor interface {
	Execute(ctx context.Context, turn UserTurn, class Classification) (Observation, error)
}

type AgentExecutor struct {
	Agent        ScopedAgentRunner
	DefaultTools *tools.Registry
	InspectTools *tools.Registry
}

func (e AgentExecutor) Execute(ctx context.Context, turn UserTurn, class Classification) (Observation, error) {
	userMessage := turn.Text
	if useReadOnlyInspectScope(class) {
		userMessage = buildInspectTurnPrompt(turn.Text)
		if e.InspectTools != nil {
			e.Agent.SetTools(e.InspectTools)
			if e.DefaultTools != nil {
				defer e.Agent.SetTools(e.DefaultTools)
			}
		}
	} else if e.DefaultTools != nil {
		e.Agent.SetTools(e.DefaultTools)
	}

	if err := e.Agent.Run(ctx, userMessage); err != nil {
		return Observation{
			Status:   ObservationBlocked,
			Response: "",
			Summary:  err.Error(),
			TopicKey: class.TopicKey,
			Err:      err,
		}, err
	}

	response := strings.TrimSpace(e.Agent.LastResponse())
	return Observation{
		Status:   ObservationComplete,
		Response: response,
		Summary:  response,
		TopicKey: class.TopicKey,
	}, nil
}

func useReadOnlyInspectScope(class Classification) bool {
	return class.Family == FamilyInspect && !class.WantsAction
}

func buildInspectTurnPrompt(userMessage string) string {
	userMessage = strings.TrimSpace(userMessage)
	return strings.TrimSpace(`HARNESS MODE: inspect
This is a read-only inspection turn.
Rules for this turn:
- inspect the actual workspace before answering
- prefer list_dir, read_file, glob, search, git_status, git_log, and git_diff
- do not ask the user to choose between multiple summary formats; provide the most useful walkthrough directly
- ground claims in tool results from this turn or prior evidence already in the conversation
- keep the scope tight and stop once you have enough evidence

USER REQUEST:
` + userMessage)
}
