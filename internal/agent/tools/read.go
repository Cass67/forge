package tools

import (
	"context"
	"fmt"
	"os"
	"strings"
)

func NewReadFile(workDir string, policies ...SecretPolicy) Tool {
	guard := newIgnoreGuard(workDir)
	secretPolicy := secretPolicyFromOptions(policies)
	return Tool{
		Name:        "read_file",
		Description: "Read a file's contents. Returns content with line numbers. Notebooks, zip/tar archives, SQLite databases, and PDFs are rendered rather than refused: read one without a member to see its cells, entry list, or schema, then pass member to open one entry or table.",
		Parameters: []ParameterDef{
			{Name: "path", Type: "string", Description: "file path relative to working directory", Required: true},
			{Name: "start_line", Type: "int", Description: "first line to read (1-indexed)", Required: false},
			{Name: "end_line", Type: "int", Description: "last line to read", Required: false},
			{Name: "member", Type: "string", Description: "entry inside an archive, or table inside a SQLite database", Required: false},
		},
		AutoApprove: true,
		Execute: func(ctx context.Context, args map[string]any) (string, error) {
			path, _ := args["path"].(string)
			resolved, err := ResolvePathAllowEscape(workDir, path)
			if err != nil {
				return "", err
			}
			if guard.blocked(resolved) {
				return fmt.Sprintf("error: %q is excluded by the secret-file policy (.ignore)", path), nil
			}

			if out, handled, err := readFormat(ctx, resolved, strings.TrimSpace(stringArg(args, "member"))); handled {
				if err != nil {
					return fmt.Sprintf("error: %v", err), nil
				}
				rendered, _ := secretPolicy.ApplyRead(out)
				return rendered, nil
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

			content, blocked := secretPolicy.ApplyRead(string(data))
			if blocked {
				return content, nil
			}

			lines := strings.Split(content, "\n")
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
			if annotated := annotateGitPathState(workDir, path); annotated != path {
				sb.WriteString("File status: " + annotated + "\n")
			}
			for i := start; i <= end; i++ {
				if i <= len(lines) {
					fmt.Fprintf(&sb, "%4d | %s\n", i, lines[i-1])
				}
			}
			return sb.String(), nil
		},
	}
}
