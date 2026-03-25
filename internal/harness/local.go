package harness

import (
	"context"
	"strings"
)

type AgentRunner interface {
	Run(ctx context.Context, userMessage string) error
	LastResponse() string
}

type LocalExecutor interface {
	Execute(ctx context.Context, turn UserTurn, class Classification) (Observation, error)
}

type AgentExecutor struct {
	Agent AgentRunner
}

func (e AgentExecutor) Execute(ctx context.Context, turn UserTurn, class Classification) (Observation, error) {
	if err := e.Agent.Run(ctx, turn.Text); err != nil {
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
