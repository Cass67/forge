package reacttools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	agenttools "forge/internal/agent/tools"
	"forge/internal/react"
)

type planToolSession interface {
	SetPlanState(react.PlanState)
}

func NewUpdatePlan(session planToolSession) agenttools.Tool {
	return agenttools.Tool{
		Name:        "update_plan",
		Description: "Track a short task plan with step statuses. Use for multi-step work and keep exactly one in_progress step until finished.",
		Parameters: []agenttools.ParameterDef{
			{Name: "steps_json", Type: "string", Description: "JSON array of plan steps: [{\"step\":\"Inspect files\",\"status\":\"in_progress\"}]", Required: true},
			{Name: "explanation", Type: "string", Description: "optional short explanation for a plan change", Required: false},
		},
		AutoApprove: true,
		Execute: func(ctx context.Context, args map[string]any) (string, error) {
			_ = ctx
			if session == nil {
				return "", fmt.Errorf("plan session unavailable")
			}
			raw, _ := args["steps_json"].(string)
			raw = strings.TrimSpace(raw)
			if raw == "" {
				return "", fmt.Errorf("steps_json is required")
			}
			var steps []react.PlanStep
			if err := json.Unmarshal([]byte(raw), &steps); err != nil {
				return "", fmt.Errorf("invalid steps_json: %w", err)
			}
			for i := range steps {
				steps[i].Step = strings.TrimSpace(steps[i].Step)
				steps[i].Status = strings.TrimSpace(steps[i].Status)
				if steps[i].Step == "" {
					return "", fmt.Errorf("plan step %d is missing step text", i+1)
				}
				switch steps[i].Status {
				case "pending", "in_progress", "completed":
				default:
					return "", fmt.Errorf("plan step %d has invalid status %q", i+1, steps[i].Status)
				}
			}
			var normErr error
			steps, normErr = normalizePlanSteps(steps)
			if normErr != nil {
				return "", normErr
			}
			explanation, _ := args["explanation"].(string)
			state := react.PlanState{
				Explanation: strings.TrimSpace(explanation),
				Steps:       steps,
			}
			session.SetPlanState(state)
			return react.FormatPlanState(state), nil
		},
	}
}

// normalizePlanSteps enforces the one-in_progress rule by auto-promoting
// the first pending step when no in_progress step is present. It rejects
// plans with multiple in_progress steps.
func normalizePlanSteps(steps []react.PlanStep) ([]react.PlanStep, error) {
	if len(steps) == 0 {
		return nil, fmt.Errorf("at least one plan step is required")
	}
	inProgress := 0
	firstPending := -1
	for i, step := range steps {
		switch step.Status {
		case "in_progress":
			inProgress++
		case "pending":
			if firstPending < 0 {
				firstPending = i
			}
		}
	}
	if inProgress > 1 {
		return nil, fmt.Errorf("plan must have exactly one in_progress step while work remains")
	}
	// Auto-promote the first pending step when none is in_progress yet.
	if inProgress == 0 && firstPending >= 0 {
		out := append([]react.PlanStep(nil), steps...)
		out[firstPending].Status = "in_progress"
		return out, nil
	}
	return steps, nil
}
