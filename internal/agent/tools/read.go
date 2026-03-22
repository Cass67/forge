package tools

import (
	"context"
	"fmt"
	"os"
	"strings"
)

func NewReadFile(workDir string) Tool {
	return Tool{
		Name:        "read_file",
		Description: "Read a file's contents. Returns content with line numbers.",
		Parameters: []ParameterDef{
			{Name: "path", Type: "string", Description: "file path relative to working directory", Required: true},
			{Name: "start_line", Type: "int", Description: "first line to read (1-indexed)", Required: false},
			{Name: "end_line", Type: "int", Description: "last line to read", Required: false},
		},
		AutoApprove: true,
		Execute: func(ctx context.Context, args map[string]any) (string, error) {
			path, _ := args["path"].(string)
			resolved, err := ResolvePath(workDir, path)
			if err != nil {
				return "", err
			}

			data, err := os.ReadFile(resolved)
			if err != nil {
				return fmt.Sprintf("error: %v", err), nil
			}

			if len(data) > 200*1024 {
				return fmt.Sprintf("error: file is %d bytes — use start_line/end_line to read a section", len(data)), nil
			}

			if IsBinary(data) {
				return "error: binary file, cannot display", nil
			}

			lines := strings.Split(string(data), "\n")
			start := 1
			end := len(lines)

			if v, ok := args["start_line"].(float64); ok && v > 0 {
				start = int(v)
			}
			if v, ok := args["end_line"].(float64); ok && v > 0 {
				end = int(v)
			}

			if start < 1 {
				start = 1
			}
			if end > len(lines) {
				end = len(lines)
			}
			if start > end {
				return "error: start_line > end_line", nil
			}

			var sb strings.Builder
			for i := start; i <= end; i++ {
				if i <= len(lines) {
					sb.WriteString(fmt.Sprintf("%4d | %s\n", i, lines[i-1]))
				}
			}
			return sb.String(), nil
		},
	}
}
