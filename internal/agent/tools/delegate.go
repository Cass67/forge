package tools

import (
	"context"
	"fmt"
)

// DelegateFunc is called by the delegate tool to spawn a sub-agent.
// role is one of: scout, builder, doctor, architect.
// task is the structured delegation prompt.
type DelegateFunc func(ctx context.Context, role, task string) (string, error)

// NewDelegate creates a tool that delegates tasks to specialist sub-agents.
func NewDelegate(fn DelegateFunc) Tool {
	return Tool{
		Name:        "delegate",
		Description: "Delegate a task to a specialist agent. Roles: scout (codebase/web research, read-only), builder (implement code changes), doctor (debug/diagnose, read-only), architect (plan multi-step work, read-only).",
		Parameters: []ParameterDef{
			{Name: "role", Type: "string", Description: "Agent role: scout, builder, doctor, architect", Required: true},
			{Name: "task", Type: "string", Description: "Structured task: TASK, OUTCOME, CONTEXT, MUST NOT sections", Required: true},
		},
		AutoApprove: true,
		Execute: func(ctx context.Context, args map[string]any) (string, error) {
			role, _ := args["role"].(string)
			task, _ := args["task"].(string)
			if role == "" || task == "" {
				return "", fmt.Errorf("role and task are required")
			}
			validRoles := map[string]bool{"scout": true, "builder": true, "doctor": true, "architect": true}
			if !validRoles[role] {
				return "", fmt.Errorf("unknown role %q (valid: scout, builder, doctor, architect)", role)
			}
			return fn(ctx, role, task)
		},
	}
}
