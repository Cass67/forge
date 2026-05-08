package reacttools

import (
	"context"
	"encoding/json"
	"fmt"

	agenttools "forge/internal/agent/tools"
	"forge/internal/react"
)

func NewAgentStatus(pool *react.AgentPool) agenttools.Tool {
	return agenttools.Tool{
		Name:        "agent_status",
		Description: "List child agents in the current session with their latest status.",
		Parameters:  nil,
		AutoApprove: true,
		Execute: func(ctx context.Context, args map[string]any) (string, error) {
			_ = ctx
			_ = args
			if pool == nil {
				return "", fmt.Errorf("agent pool unavailable")
			}
			payload := struct {
				Agents []react.AgentResult `json:"agents"`
			}{Agents: redactAgentResults(pool.Statuses())}
			encoded, err := json.Marshal(payload)
			if err != nil {
				return "", err
			}
			return string(encoded), nil
		},
	}
}
