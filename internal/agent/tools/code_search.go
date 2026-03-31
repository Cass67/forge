package tools

import (
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

func NewCodeSearch(workDir string) Tool {
	return Tool{
		Name:        "code_search",
		Description: "Search code with ripgrep using fast literal matching and small contextual snippets. Prefer this over raw shell grep for code lookup.",
		Parameters: []ParameterDef{
			{Name: "query", Type: "string", Description: "literal query to search for", Required: true},
			{Name: "path", Type: "string", Description: "base directory to search (default .)", Required: false},
			{Name: "glob", Type: "string", Description: "optional file glob like *.go or **/*.ts", Required: false},
			{Name: "context_lines", Type: "int", Description: "lines of context around matches (default 2)", Required: false},
		},
		AutoApprove: true,
		Execute: func(ctx context.Context, args map[string]any) (string, error) {
			query, _ := args["query"].(string)
			query = strings.TrimSpace(query)
			if query == "" {
				return "code_search failed: query is required", nil
			}
			basePath, _ := args["path"].(string)
			if strings.TrimSpace(basePath) == "" {
				basePath = "."
			}
			resolved, err := ResolvePathAllowEscape(workDir, basePath)
			if err != nil {
				return "", err
			}
			contextLines := 2
			if value, ok := args["context_lines"].(float64); ok && value >= 0 {
				contextLines = int(value)
			}
			glob, _ := args["glob"].(string)

			argv := []string{"-n", "-S", "-F", "-m", "25", "-C", strconv.Itoa(contextLines)}
			if strings.TrimSpace(glob) != "" {
				argv = append(argv, "-g", glob)
			}
			argv = append(argv, query, resolved)

			cmd := exec.CommandContext(ctx, "rg", argv...)
			out, err := cmd.CombinedOutput()
			if err != nil {
				if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 1 {
					return fmt.Sprintf("no matches for %q", query), nil
				}
				return fmt.Sprintf("code_search failed: %s", strings.TrimSpace(string(out))), nil
			}
			result := string(out)
			if len(result) > 32*1024 {
				result = result[:32*1024] + "\n... output truncated"
			}
			return result, nil
		},
	}
}
