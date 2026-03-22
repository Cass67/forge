package tools

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
)

func NewSearch(workDir string) Tool {
	return Tool{
		Name:        "search",
		Description: "Search for a pattern across files.",
		Parameters: []ParameterDef{
			{Name: "pattern", Type: "string", Description: "regex pattern", Required: true},
			{Name: "path", Type: "string", Description: "directory to search (default \".\")", Required: false},
			{Name: "glob", Type: "string", Description: "file pattern filter (e.g. \"*.go\")", Required: false},
		},
		AutoApprove: true,
		Execute: func(ctx context.Context, args map[string]any) (string, error) {
			pattern, _ := args["pattern"].(string)
			searchPath := "."
			if p, ok := args["path"].(string); ok && p != "" {
				searchPath = p
			}
			glob, _ := args["glob"].(string)

			result, err := searchRg(ctx, workDir, pattern, searchPath, glob)
			if err != nil {
				result, err = searchGrep(ctx, workDir, pattern, searchPath, glob)
				if err != nil {
					return fmt.Sprintf("search error: %v", err), nil
				}
			}

			if result == "" {
				return "no matches found", nil
			}

			lines := strings.Split(strings.TrimRight(result, "\n"), "\n")
			if len(lines) > 100 {
				truncated := strings.Join(lines[:100], "\n")
				return truncated + fmt.Sprintf("\n... %d more matches", len(lines)-100), nil
			}
			return result, nil
		},
	}
}

func searchRg(ctx context.Context, workDir, pattern, searchPath, glob string) (string, error) {
	args := []string{"-n", "--no-heading", pattern, searchPath}
	if glob != "" {
		args = []string{"-n", "--no-heading", "--glob", glob, pattern, searchPath}
	}
	cmd := exec.CommandContext(ctx, "rg", args...)
	cmd.Dir = workDir
	out, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 1 {
			return "", nil
		}
		return "", err
	}
	return string(out), nil
}

func searchGrep(ctx context.Context, workDir, pattern, searchPath, glob string) (string, error) {
	args := []string{"-rn", pattern, searchPath}
	if glob != "" {
		args = []string{"-rn", "--include", glob, pattern, searchPath}
	}
	cmd := exec.CommandContext(ctx, "grep", args...)
	cmd.Dir = workDir
	out, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 1 {
			return "", nil
		}
		return "", err
	}
	return string(out), nil
}
