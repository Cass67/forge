package reacttools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	agenttools "forge/internal/agent/tools"
	"forge/internal/react"
)

func NewWaitAgent(pool *react.AgentPool) agenttools.Tool {
	return newAgentOutputTool(
		"wait_agent",
		"Wait for a spawned child agent to complete; if it is still running when the wait window ends, return its current status.",
		pool,
	)
}

func NewGetAgentOutput(pool *react.AgentPool) agenttools.Tool {
	return newAgentOutputTool(
		"get_agent_output",
		"Get a spawned child agent's latest output. Alias for wait_agent; if the agent is still running when the wait window ends, return its current status.",
		pool,
	)
}

func newAgentOutputTool(name, description string, pool *react.AgentPool) agenttools.Tool {
	return agenttools.Tool{
		Name:        name,
		Description: description,
		Parameters: []agenttools.ParameterDef{
			{Name: "id", Type: "string", Description: "Child agent id from spawn_agent", Required: true},
			{Name: "timeout_seconds", Type: "int", Description: "How long to wait before returning current status (default 30)", Required: false},
		},
		AutoApprove: true,
		Execute: func(ctx context.Context, args map[string]any) (string, error) {
			if pool == nil {
				return "", fmt.Errorf("agent pool unavailable")
			}
			id, _ := args["id"].(string)
			if strings.TrimSpace(id) == "" {
				return "", fmt.Errorf("wait_agent requires a non-empty child agent id")
			}
			timeout := 30 * time.Second
			if value, ok := args["timeout_seconds"].(float64); ok {
				if value <= 0 {
					timeout = 30 * time.Second
				} else {
					timeout = time.Duration(value * float64(time.Second))
				}
			}
			result, err := pool.Wait(ctx, id, timeout)
			if err != nil {
				return "", err
			}
			encoded, err := json.Marshal(redactAgentResult(result))
			if err != nil {
				return "", err
			}
			return string(encoded), nil
		},
	}
}
