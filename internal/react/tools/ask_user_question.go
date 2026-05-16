package reacttools

import (
	"context"
	"fmt"
	"strings"

	agenttools "forge/internal/agent/tools"
	"forge/internal/llm"
)

type askUserOption struct {
	Label       string `json:"label"`
	Description string `json:"description"`
}

func NewAskUserQuestion() agenttools.Tool {
	additional := false
	return agenttools.Tool{
		Name:        "ask_user_question",
		Description: "Ask the user a structured question with 2-3 clear options when you need a decision, preference, or clarification during planning or implementation.",
		Parameters:  []agenttools.ParameterDef{},
		Schema: &llm.ToolSchema{
			Type: "object",
			Properties: map[string]*llm.ToolSchema{
				"question": {Type: "string", Description: "the question to ask the user"},
				"options": {
					Type: "array",
					Items: &llm.ToolSchema{
						Type: "object",
						Properties: map[string]*llm.ToolSchema{
							"label":       {Type: "string", Description: "short option label"},
							"description": {Type: "string", Description: "brief tradeoff or explanation"},
						},
						Required:             []string{"label"},
						AdditionalProperties: &additional,
					},
				},
			},
			Required:             []string{"question", "options"},
			AdditionalProperties: &additional,
		},
		AutoApprove: true,
		Execute: func(ctx context.Context, args map[string]any) (string, error) {
			_ = ctx
			question, _ := args["question"].(string)
			question = strings.TrimSpace(question)
			if question == "" {
				return "", fmt.Errorf("question is required")
			}
			options, err := askUserOptionsFromArgs(args)
			if err != nil {
				return "", err
			}
			if len(options) < 2 {
				return "", fmt.Errorf("at least two options are required")
			}
			var b strings.Builder
			b.WriteString("Question: ")
			b.WriteString(question)
			for i, opt := range options {
				label := strings.TrimSpace(opt.Label)
				if label == "" {
					return "", fmt.Errorf("option %d is missing a label", i+1)
				}
				fmt.Fprintf(&b, "\n%d. %s", i+1, label)
				if desc := strings.TrimSpace(opt.Description); desc != "" {
					b.WriteString(" — ")
					b.WriteString(desc)
				}
			}
			return b.String(), nil
		},
	}
}

func askUserOptionsFromArgs(args map[string]any) ([]askUserOption, error) {
	raw, ok := args["options"].([]any)
	if !ok {
		return nil, fmt.Errorf("options is required")
	}
	options := make([]askUserOption, 0, len(raw))
	for i, item := range raw {
		obj, ok := item.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("option %d must be an object", i+1)
		}
		label, _ := obj["label"].(string)
		description, _ := obj["description"].(string)
		options = append(options, askUserOption{Label: label, Description: description})
	}
	return options, nil
}
