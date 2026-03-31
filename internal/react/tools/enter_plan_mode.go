package reacttools

import (
	"context"
	"fmt"
	"strings"

	agenttools "forge/internal/agent/tools"
	"forge/internal/react"
)

type planModeSession interface {
	SetMode(react.Mode)
	SetTaskState(react.TaskState)
}

func NewEnterPlanMode(session planModeSession) agenttools.Tool {
	return agenttools.Tool{
		Name:        "enter_plan_mode",
		Description: "Enter explicit plan mode for ambiguous or non-trivial implementation work. Use this when you need to explore the repo, understand patterns, and present an approach before coding.",
		Parameters: []agenttools.ParameterDef{
			{Name: "objective", Type: "string", Description: "implementation goal to plan for", Required: true},
			{Name: "required_verification", Type: "string", Description: "optional approval or planning outcome required before implementation", Required: false},
		},
		AutoApprove: true,
		Execute: func(ctx context.Context, args map[string]any) (string, error) {
			_ = ctx
			if session == nil {
				return "", fmt.Errorf("plan mode session unavailable")
			}
			objective, _ := args["objective"].(string)
			objective = strings.TrimSpace(objective)
			if objective == "" {
				return "", fmt.Errorf("objective is required")
			}
			requiredVerification, _ := args["required_verification"].(string)
			requiredVerification = strings.TrimSpace(requiredVerification)
			if requiredVerification == "" {
				requiredVerification = "explore the codebase, capture a meaningful plan, and get user approval before implementation"
			}
			session.SetTaskState(react.TaskState{
				Objective:            objective,
				Operation:            "plan",
				RequiredVerification: requiredVerification,
			})
			session.SetMode(react.ModePlan)
			return fmt.Sprintf("Entered plan mode.\nObjective: %s\nNext: inspect the repo, synthesize a plan, and confirm the implementation approach before coding.", objective), nil
		},
	}
}
