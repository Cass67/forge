package tools

import (
	"context"
	"fmt"
	"strings"
)

func NewToolHelp(reg *Registry) Tool {
	return Tool{
		Name:        "tool_help",
		Description: "Reveal specialized tools on demand so the prompt only carries what is needed.",
		Parameters: []ParameterDef{
			{Name: "query", Type: "string", Description: "Capability needed, tool name, or short task description", Required: true},
		},
		AutoApprove: true,
		Execute: func(ctx context.Context, args map[string]any) (string, error) {
			_ = ctx
			query, _ := args["query"].(string)
			query = strings.TrimSpace(query)
			if query == "" {
				return "No specialized tools matched. Ask for a capability like web search, fetching a URL, or committing changes.", nil
			}
			revealed := reg.RevealMatchingTools(query)
			if len(revealed) == 0 {
				hidden := reg.hiddenToolNames()
				if len(hidden) == 0 {
					return "No additional hidden tools are available.", nil
				}
				return "No specialized tools matched that request. Try a need like web search, fetch a URL, or create a git commit.", nil
			}
			names := make([]string, 0, len(revealed))
			for _, tool := range revealed {
				names = append(names, tool.Name)
			}
			return fmt.Sprintf("Revealed specialized tools for this session:\n\n%s", reg.DescribeNamedTools(names)), nil
		},
	}
}
