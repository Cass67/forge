package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/pmezard/go-difflib/difflib"
)

func NewWriteFile(workDir string, approve ApprovalFunc, policies ...SecretPolicy) Tool {
	return NewWriteFileWithWorkDirProvider(workDir, FixedWorkDirProvider(workDir), approve, policies...)
}

func NewWriteFileWithWorkDirProvider(fallbackWorkDir string, provider WorkDirProvider, approve ApprovalFunc, policies ...SecretPolicy) Tool {
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

			activeWorkDir := currentWorkDir(provider, fallbackWorkDir)
			resolved, err := ResolvePath(activeWorkDir, path)
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

			_, ins, del := diffStat(detail)
			return fmt.Sprintf("wrote %s (%d lines, %d insertions(+), %d deletions(-))", path, len(strings.Split(content, "\n")), ins, del), nil
		},
	}
}

func simpleDiff(old, new_, path string) string {
	text, err := difflib.GetUnifiedDiffString(difflib.UnifiedDiff{
		A:        difflib.SplitLines(old),
		B:        difflib.SplitLines(new_),
		FromFile: "a/" + path,
		ToFile:   "b/" + path,
		Context:  3,
	})
	if err != nil {
		return ""
	}
	return text
}

// diffStat counts files, insertions, and deletions from a unified diff.
// It parses +++/--- headers for file count and +/- lines for insertions/deletions.
func diffStat(patch string) (files, insertions, deletions int) {
	seen := map[string]bool{}
	var inHeader bool
	for _, line := range strings.Split(patch, "\n") {
		if strings.HasPrefix(line, "--- ") || strings.HasPrefix(line, "+++ ") {
			// Extract filename after "a/" or "b/" prefix
			parts := strings.SplitN(line, " ", 2)
			if len(parts) == 2 {
				name := strings.TrimPrefix(parts[1], "a/")
				name = strings.TrimPrefix(name, "b/")
				if name != "" && name != "/dev/null" {
					seen[name] = true
				}
			}
			inHeader = true
			continue
		}
		if strings.HasPrefix(line, "diff ") || strings.HasPrefix(line, "index ") {
			inHeader = true
			continue
		}
		if inHeader && (strings.HasPrefix(line, "@@") || strings.HasPrefix(line, "---") || strings.HasPrefix(line, "+++")) {
			inHeader = false
		}
		if strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++") {
			insertions++
		}
		if strings.HasPrefix(line, "-") && !strings.HasPrefix(line, "---") {
			deletions++
		}
	}
	files = len(seen)
	return
}
