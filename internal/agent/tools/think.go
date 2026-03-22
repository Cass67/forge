package tools

import "context"

func NewThink() Tool {
	return Tool{
		Name:        "think",
		Description: "Use this to think through a problem step by step before acting. Your thought is recorded but not shown to the user.",
		Parameters: []ParameterDef{
			{Name: "thought", Type: "string", Description: "your reasoning", Required: true},
		},
		AutoApprove: true,
		Execute: func(ctx context.Context, args map[string]any) (string, error) {
			return "ok", nil
		},
	}
}
