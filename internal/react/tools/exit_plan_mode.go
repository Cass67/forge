package reacttools

import (
	"context"
	"fmt"
	"strings"

	agenttools "forge/internal/agent/tools"
	"forge/internal/react"
)

type exitPlanModeSession interface {
	SetMode(react.Mode)
	SetTaskState(react.TaskState)
}

func NewExitPlanMode(session exitPlanModeSession) agenttools.Tool {
	return agenttools.Tool{
		Name:        "exit_plan_mode",
		Description: "Exit plan mode after the plan is ready and transition back into implementation with explicit objective and verification requirements.",
		Parameters: []agenttools.ParameterDef{
			{Name: "implementation_objective", Type: "string", Description: "approved implementation objective to execute", Required: true},
			{Name: "required_verification", Type: "string", Description: "verification expected before implementation can be considered complete", Required: false},
		},
		AutoApprove: true,
		Execute: func(ctx context.Context, args map[string]any) (string, error) {
			_ = ctx
			if session == nil {
				return "", fmt.Errorf("plan mode session unavailable")
			}
			objective, _ := args["implementation_objective"].(string)
			objective = strings.TrimSpace(objective)
			if objective == "" {
				return "", fmt.Errorf("implementation_objective is required")
			}
			requiredVerification, _ := args["required_verification"].(string)
			requiredVerification = strings.TrimSpace(requiredVerification)
			if requiredVerification == "" {
				requiredVerification = "inspect the relevant code, make the approved change, and run the relevant verification before claiming completion"
			}
			session.SetTaskState(react.TaskState{
				Objective:            objective,
				Operation:            "implement",
				RequiredVerification: requiredVerification,
			})
			session.SetMode(react.ModeImplement)
			return fmt.Sprintf("Exited plan mode.\nImplementation objective: %s\nNext: inspect the relevant code, implement the approved plan, and verify the result.", objective), nil
		},
	}
}
