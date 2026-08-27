package reacttools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	agenttools "forge/internal/agent/tools"
	"forge/internal/llm"
	"forge/internal/react"
)

type planToolSession interface {
	SetPlanState(react.PlanState)
}

func NewUpdatePlan(session planToolSession) agenttools.Tool {
	additional := false
	return agenttools.Tool{
		Name:        "update_plan",
		Description: "Track a short task plan with step statuses. Use for multi-step work and keep exactly one in_progress step until finished.",
		Parameters:  []agenttools.ParameterDef{},
		Schema: &llm.ToolSchema{
			Type: "object",
			Properties: map[string]*llm.ToolSchema{
				"steps": {
					Type: "array",
					Items: &llm.ToolSchema{
						Type: "object",
						Properties: map[string]*llm.ToolSchema{
							"step":    {Type: "string", Description: "short task description"},
							"status":  {Type: "string", Enum: []string{"pending", "in_progress", "blocked", "completed"}},
							"blocker": {Type: "string", Description: "required when status is blocked"},
						},
						Required:             []string{"step", "status"},
						AdditionalProperties: &additional,
					},
				},
				"explanation": {Type: "string", Description: "optional short explanation for a plan change"},
			},
			Required:             []string{"steps"},
			AdditionalProperties: &additional,
		},
		AutoApprove: true,
		Execute: func(ctx context.Context, args map[string]any) (string, error) {
			_ = ctx
			if session == nil {
				return "", fmt.Errorf("plan session unavailable")
			}
			steps, err := planStepsFromArgs(args)
			if err != nil {
				return "", err
			}
			for i := range steps {
				steps[i].Step = strings.TrimSpace(steps[i].Step)
				steps[i].Status = normalizePlanStatus(steps[i].Status)
				steps[i].Blocker = strings.TrimSpace(steps[i].Blocker)
				if steps[i].Step == "" {
					return "", fmt.Errorf("plan step %d is missing step text", i+1)
				}
				switch steps[i].Status {
				case "pending", "in_progress", "blocked", "completed":
				default:
					return "", fmt.Errorf("plan step %d has invalid status %q", i+1, steps[i].Status)
				}
				if steps[i].Status == "blocked" && steps[i].Blocker == "" {
					return "", fmt.Errorf("plan step %d is blocked but missing blocker text", i+1)
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

func planStepsFromArgs(args map[string]any) ([]react.PlanStep, error) {
	rawSteps, ok := args["steps"]
	if !ok || rawSteps == nil {
		return nil, fmt.Errorf("steps is required")
	}
	data, err := json.Marshal(rawSteps)
	if err != nil {
		return nil, fmt.Errorf("invalid steps: %w", err)
	}
	var steps []react.PlanStep
	if err := json.Unmarshal(data, &steps); err != nil {
		return nil, fmt.Errorf("invalid steps: %w", err)
	}
	return steps, nil
}

// normalizePlanStatus lowercases the status and normalises spaces/hyphens to
// underscores so that "In Progress", "in-progress", "In_Progress" etc. all
// map to the canonical "in_progress" token. Common synonyms for the three
// canonical values are also mapped so that model-generated variants like
// "done", "finished", "complete", "started", "running" are accepted.
func normalizePlanStatus(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.ReplaceAll(s, " ", "_")
	s = strings.ReplaceAll(s, "-", "_")
	switch s {
	case "done", "finished", "complete", "completed_step", "success", "succeeded":
		return "completed"
	case "started", "running", "active", "doing", "wip", "in_progress_step":
		return "in_progress"
	case "blocked_on", "paused", "waiting_on_blocker":
		return "blocked"
	case "todo", "not_started", "queued", "waiting":
		return "pending"
	}
	return s
}

// normalizePlanSteps enforces the one-active-step rule. It keeps the first
// in_progress step and demotes later ones to pending, auto-promotes the first
// pending step when no active step exists, and rejects multiple blocked steps.
func normalizePlanSteps(steps []react.PlanStep) ([]react.PlanStep, error) {
	if len(steps) == 0 {
		return nil, fmt.Errorf("at least one plan step is required")
	}
	out := append([]react.PlanStep(nil), steps...)
	active := 0
	firstPending := -1
	for i, step := range out {
		switch step.Status {
		case "in_progress":
			if active > 0 {
				out[i].Status = "pending"
				if firstPending < 0 {
					firstPending = i
				}
				continue
			}
			active++
		case "blocked":
			active++
		case "pending":
			if firstPending < 0 {
				firstPending = i
			}
		}
	}
	if active > 1 {
		return nil, fmt.Errorf("plan must have exactly one active step while work remains")
	}
	// Auto-promote the first pending step when no active step exists yet.
	if active == 0 && firstPending >= 0 {
		out[firstPending].Status = "in_progress"
		return out, nil
	}
	return out, nil
}
