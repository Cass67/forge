package tools

import (
	"context"
	"fmt"
	"os"
	"strings"
)

func NewEditFile(workDir string, approve ApprovalFunc, policies ...SecretPolicy) Tool {
	return NewEditFileWithWorkDirProvider(workDir, FixedWorkDirProvider(workDir), approve, policies...)
}

func NewEditFileWithWorkDirProvider(fallbackWorkDir string, provider WorkDirProvider, approve ApprovalFunc, policies ...SecretPolicy) Tool {
	var lastDiff string
	secretPolicy := secretPolicyFromOptions(policies)
	return Tool{
		Name:        "edit_file",
		Description: "Replace a block of a file. Preferred form: pass the anchors read_file printed beside the first and last line to replace (start_anchor/end_anchor) — no need to echo the old text back. Fallback form: pass old_text to search for.",
		Parameters: []ParameterDef{
			{Name: "path", Type: "string", Description: "file path", Required: true},
			{Name: "new_text", Type: "string", Description: "replacement text; empty deletes the block", Required: true},
			{Name: "start_anchor", Type: "string", Description: "anchor of the first line to replace, from read_file", Required: false},
			{Name: "end_anchor", Type: "string", Description: "anchor of the last line to replace; omit to replace just the start line", Required: false},
			{Name: "start_line", Type: "int", Description: "line number of start_anchor, only needed when the anchor is ambiguous", Required: false},
			{Name: "old_text", Type: "string", Description: "exact text to find (must be unique in file); alternative to anchors", Required: false},
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
			oldText, _ := args["old_text"].(string)
			newText, _ := args["new_text"].(string)
			checkedNewText, blocked := secretPolicy.ApplyWrite(newText)
			if blocked {
				return checkedNewText, nil
			}
			newText = checkedNewText

			resolved, err := ResolvePath(currentWorkDir(provider, fallbackWorkDir), path)
			if err != nil {
				return "", err
			}

			data, err := os.ReadFile(resolved)
			if err != nil {
				return fmt.Sprintf("error: %v", err), nil
			}

			content := string(data)

			if startAnchor, _ := args["start_anchor"].(string); strings.TrimSpace(startAnchor) != "" {
				endAnchor, _ := args["end_anchor"].(string)
				hintLine := 0
				if v, ok := args["start_line"].(float64); ok && v > 0 {
					hintLine = int(v)
				}
				lines := strings.Split(content, "\n")
				span, err := resolveAnchorSpan(lines, startAnchor, endAnchor, hintLine)
				if err != nil {
					return fmt.Sprintf("edit_file failed: %v", err), nil
				}
				newContent := strings.Join(replaceSpan(lines, span, newText), "\n")
				return applyEdit(ctx, approve, resolved, path, content, newContent, &lastDiff)
			}

			if oldText == "" {
				return "edit_file failed: pass start_anchor (preferred) or old_text", nil
			}

			count := strings.Count(content, oldText)
			if count == 0 {
				if newText != "" {
					if replacementCount := strings.Count(content, newText); replacementCount == 1 {
						return fmt.Sprintf("edit_file skipped: requested replacement is already present in %s", path), nil
					} else if replacementCount > 1 {
						return fmt.Sprintf("edit_file skipped: requested replacement is already present %d times in %s", replacementCount, path), nil
					}
				}
				return fmt.Sprintf("edit_file failed: old_text not found in %s", path), nil
			}
			if count > 1 {
				return fmt.Sprintf("edit_file failed: old_text matched %d locations in %s; provide more surrounding context to make the match unique", count, path), nil
			}

			newContent := strings.Replace(content, oldText, newText, 1)
			return applyEdit(ctx, approve, resolved, path, content, newContent, &lastDiff)
		},
	}
}

func applyEdit(ctx context.Context, approve ApprovalFunc, resolved, path, content, newContent string, lastDiff *string) (string, error) {
	diff := simpleDiff(content, newContent, path)
	*lastDiff = diff

	approved, err := approve(Action{
		Context: ctx,
		Tool:    "edit_file",
		Summary: fmt.Sprintf("edit %s", path),
		Detail:  diff,
		Path:    path,
	})
	if err != nil {
		return "", err
	}
	if !approved {
		*lastDiff = ""
		return "edit_file denied by user", nil
	}

	if err := os.WriteFile(resolved, []byte(newContent), 0o644); err != nil {
		*lastDiff = ""
		return fmt.Sprintf("error writing file: %v", err), nil
	}

	_, ins, del := diffStat(diff)
	return fmt.Sprintf("edited %s (%d insertions(+), %d deletions(-))", path, ins, del), nil
}
