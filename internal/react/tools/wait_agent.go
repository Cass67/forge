package reacttools

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	agenttools "forge/internal/agent/tools"
	"forge/internal/react"
)

func NewWaitAgent(pool *react.AgentPool) agenttools.Tool {
	return agenttools.Tool{
		Name:        "wait_agent",
		Description: "Wait for a spawned child agent to complete and return its result.",
		Parameters: []agenttools.ParameterDef{
			{Name: "id", Type: "string", Description: "Child agent id from spawn_agent", Required: true},
			{Name: "timeout_seconds", Type: "int", Description: "How long to wait before timing out (default 30)", Required: false},
		},
		AutoApprove: true,
		Execute: func(ctx context.Context, args map[string]any) (string, error) {
			if pool == nil {
				return "", fmt.Errorf("agent pool unavailable")
			}
			id, _ := args["id"].(string)
			timeout := 30 * time.Second
			if value, ok := args["timeout_seconds"].(float64); ok && value > 0 {
				timeout = time.Duration(int(value)) * time.Second
			}
			result, err := pool.Wait(ctx, id, timeout)
			if err != nil {
				return "", err
			}
			encoded, err := json.Marshal(result)
			if err != nil {
				return "", err
			}
			return string(encoded), nil
		},
	}
}
