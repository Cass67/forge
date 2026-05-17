package reacttools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	agenttools "forge/internal/agent/tools"
	"forge/internal/react"
)

func NewKillAgent(pool *react.AgentPool) agenttools.Tool {
	return agenttools.Tool{
		Name:        "kill_agent",
		Description: "Cancel a running child agent by id and mark it killed in session state.",
		Parameters: []agenttools.ParameterDef{
			{Name: "id", Type: "string", Description: "Child agent id from spawn_agent or agent_status", Required: true},
		},
		AutoApprove: true,
		Concurrency: agenttools.ToolConcurrencySerial,
		Execute: func(ctx context.Context, args map[string]any) (string, error) {
			if pool == nil {
				return "", fmt.Errorf("agent pool unavailable")
			}
			id, _ := args["id"].(string)
			if strings.TrimSpace(id) == "" {
				return "", fmt.Errorf("kill_agent requires a non-empty child agent id")
			}
			result, err := pool.Kill(ctx, id)
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
