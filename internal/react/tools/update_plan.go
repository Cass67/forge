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

// normalizePlanSteps enforces the one-in_progress rule by auto-promoting
// the first pending step when no active step is present. It rejects
// plans with multiple active steps. Active means in_progress or blocked.
func normalizePlanSteps(steps []react.PlanStep) ([]react.PlanStep, error) {
	if len(steps) == 0 {
		return nil, fmt.Errorf("at least one plan step is required")
	}
	active := 0
	firstPending := -1
	for i, step := range steps {
		switch step.Status {
		case "in_progress", "blocked":
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
		out := append([]react.PlanStep(nil), steps...)
		out[firstPending].Status = "in_progress"
		return out, nil
	}
	return steps, nil
}
