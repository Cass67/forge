package reacttools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	agenttools "forge/internal/agent/tools"
	"forge/internal/react"
)

func NewSpawnAgent(pool *react.AgentPool) agenttools.Tool {
	return agenttools.Tool{
		Name:        "spawn_agent",
		Description: "Spawn a bounded child agent task. Role is optional advisory context.",
		Parameters: []agenttools.ParameterDef{
			{Name: "task_description", Type: "string", Description: "Task to delegate to the child agent", Required: true},
			{Name: "role", Type: "string", Description: "Optional role hint for the child agent", Required: false},
		},
		AutoApprove: true,
		Execute: func(ctx context.Context, args map[string]any) (string, error) {
			if pool == nil {
				return "", fmt.Errorf("agent pool unavailable")
			}
			task, _ := args["task_description"].(string)
			if strings.TrimSpace(task) == "" {
				return "", fmt.Errorf("task_description is required")
			}
			role, _ := args["role"].(string)
			if strings.TrimSpace(role) == "" {
				role = "default"
			}

			id, err := pool.Spawn(ctx, role, task)
			if err != nil {
				return "", err
			}
			result := react.AgentResult{
				ID:     id,
				Role:   role,
				Status: react.AgentStatusRunning,
			}
			encoded, err := json.Marshal(result)
			if err != nil {
				return "", err
			}
			return string(encoded), nil
		},
	}
}
