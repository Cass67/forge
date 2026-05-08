package tools

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func NewApplyPatch(workDir string, approve ApprovalFunc, policies ...SecretPolicy) Tool {
	var lastDiff string
	secretPolicy := secretPolicyFromOptions(policies)
	return Tool{
		Name:        "apply_patch",
		Description: "Apply a unified diff patch across one or more files. Prefer this for multi-hunk edits, file creation, deletion, or moves.",
		Parameters: []ParameterDef{
			{Name: "patch", Type: "string", Description: "unified diff patch to apply", Required: true},
		},
		AutoApprove: false,
		LastDiff: func() string {
			diff := lastDiff
			lastDiff = ""
			return diff
		},
		Execute: func(ctx context.Context, args map[string]any) (string, error) {
			lastDiff = ""
			patch, _ := args["patch"].(string)
			patch = strings.TrimSpace(patch)
			if patch == "" {
				return "apply_patch failed: patch is required", nil
			}
			checkedPatch, blocked := secretPolicy.ApplyWrite(patch)
			if blocked {
				return checkedPatch, nil
			}
			patch = checkedPatch
			lastDiff = patch

			approved, err := approve(Action{
				Context: ctx,
				Tool:    "apply_patch",
				Summary: "apply unified patch",
				Detail:  patch,
			})
			if err != nil {
				return "", err
			}
			if !approved {
				return "apply_patch denied by user", nil
			}

			tmpFile, err := os.CreateTemp("", "forge-apply-patch-*.diff")
			if err != nil {
				return "", err
			}
			tmpPath := tmpFile.Name()
			defer func() { _ = os.Remove(tmpPath) }()
			if _, err := tmpFile.WriteString(patch + "\n"); err != nil {
				_ = tmpFile.Close()
				return "", err
			}
			if err := tmpFile.Close(); err != nil {
				return "", err
			}

			check := exec.CommandContext(ctx, "git", "apply", "--check", "--recount", "--whitespace=nowarn", tmpPath)
			check.Dir = workDir
			if out, err := check.CombinedOutput(); err != nil {
				return fmt.Sprintf("apply_patch failed: %s", strings.TrimSpace(string(out))), nil
			}

			apply := exec.CommandContext(ctx, "git", "apply", "--recount", "--whitespace=nowarn", tmpPath)
			apply.Dir = workDir
			if out, err := apply.CombinedOutput(); err != nil {
				return fmt.Sprintf("apply_patch failed: %s", strings.TrimSpace(string(out))), nil
			}

			return fmt.Sprintf("applied patch from %s", filepath.Base(tmpPath)), nil
		},
	}
}
