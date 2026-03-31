package reacttools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	agenttools "forge/internal/agent/tools"
)

type askUserOption struct {
	Label       string `json:"label"`
	Description string `json:"description"`
}

func NewAskUserQuestion() agenttools.Tool {
	return agenttools.Tool{
		Name:        "ask_user_question",
		Description: "Ask the user a structured question with 2-3 clear options when you need a decision, preference, or clarification during planning or implementation.",
		Parameters: []agenttools.ParameterDef{
			{Name: "question", Type: "string", Description: "the question to ask the user", Required: true},
			{Name: "options_json", Type: "string", Description: "JSON array of options: [{\"label\":\"Option A\",\"description\":\"tradeoff\"}]", Required: true},
		},
		AutoApprove: true,
		Execute: func(ctx context.Context, args map[string]any) (string, error) {
			_ = ctx
			question, _ := args["question"].(string)
			question = strings.TrimSpace(question)
			if question == "" {
				return "", fmt.Errorf("question is required")
			}
			raw, _ := args["options_json"].(string)
			raw = strings.TrimSpace(raw)
			if raw == "" {
				return "", fmt.Errorf("options_json is required")
			}
			var options []askUserOption
			if err := json.Unmarshal([]byte(raw), &options); err != nil {
				return "", fmt.Errorf("invalid options_json: %w", err)
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
