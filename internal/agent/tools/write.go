package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func NewWriteFile(workDir string, approve ApprovalFunc, policies ...SecretPolicy) Tool {
	var lastDiff string
	secretPolicy := secretPolicyFromOptions(policies)
	return Tool{
		Name:        "write_file",
		Description: "Create or overwrite a file.",
		Parameters: []ParameterDef{
			{Name: "path", Type: "string", Description: "file path", Required: true},
			{Name: "content", Type: "string", Description: "full file content", Required: true},
		},
		AutoApprove:      false,
		Concurrency:      ToolConcurrencySerial,
		MutatesWorkspace: true,
		LastDiff: func() string {
			d := lastDiff
			lastDiff = ""
			return d
		},
		Execute: func(ctx context.Context, args map[string]any) (string, error) {
			lastDiff = ""
			path, _ := args["path"].(string)
			content, _ := args["content"].(string)
			checkedContent, blocked := secretPolicy.ApplyWrite(content)
			if blocked {
				return checkedContent, nil
			}
			content = checkedContent

			resolved, err := ResolvePath(workDir, path)
			if err != nil {
				return "", err
			}

			var detail string
			existing, readErr := os.ReadFile(resolved)
			if readErr == nil {
				detail = simpleDiff(string(existing), content, path)
			} else {
				preview := content
				lines := strings.Split(preview, "\n")
				if len(lines) > 20 {
					preview = strings.Join(lines[:20], "\n") + "\n... (truncated)"
				}
				detail = fmt.Sprintf("new file: %s\n%s", path, preview)
			}

			lastDiff = detail

			approved, err := approve(Action{
				Context: ctx,
				Tool:    "write_file",
				Summary: fmt.Sprintf("write %s", path),
				Detail:  detail,
				Path:    path,
			})
			if err != nil {
				return "", err
			}
			if !approved {
				lastDiff = ""
				return "write_file denied by user", nil
			}

			if err := os.MkdirAll(filepath.Dir(resolved), 0o755); err != nil {
				return fmt.Sprintf("error creating directories: %v", err), nil
			}

			if err := os.WriteFile(resolved, []byte(content), 0o644); err != nil {
				lastDiff = ""
				return fmt.Sprintf("error writing file: %v", err), nil
			}

			return fmt.Sprintf("wrote %d bytes to %s", len(content), path), nil
		},
	}
}

func simpleDiff(old, new_, path string) string {
	oldLines := strings.Split(old, "\n")
	newLines := strings.Split(new_, "\n")
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("--- a/%s\n+++ b/%s\n", path, path))

	maxLen := len(oldLines)
	if len(newLines) > maxLen {
		maxLen = len(newLines)
	}

	for i := 0; i < maxLen; i++ {
		haveOld := i < len(oldLines)
		haveNew := i < len(newLines)
		switch {
		case haveOld && haveNew && oldLines[i] == newLines[i]:
			sb.WriteString(" " + oldLines[i] + "\n")
		case haveOld && haveNew:
			sb.WriteString("-" + oldLines[i] + "\n")
			sb.WriteString("+" + newLines[i] + "\n")
		case haveOld:
			sb.WriteString("-" + oldLines[i] + "\n")
		case haveNew:
			sb.WriteString("+" + newLines[i] + "\n")
		}
	}
	return sb.String()
}
