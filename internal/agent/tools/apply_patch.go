package tools

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

func NewApplyPatch(workDir string, approve ApprovalFunc, policies ...SecretPolicy) Tool {
	return NewApplyPatchWithWorkDirProvider(workDir, FixedWorkDirProvider(workDir), approve, policies...)
}

func NewApplyPatchWithWorkDirProvider(fallbackWorkDir string, provider WorkDirProvider, approve ApprovalFunc, policies ...SecretPolicy) Tool {
	var lastDiff string
	secretPolicy := secretPolicyFromOptions(policies)
	return Tool{
		Name: "apply_patch",
		Description: "Apply a patch across one or more files. Prefer this for multi-hunk edits, file creation, deletion, or moves.\n" +
			"Accepts either a unified diff (with ---/+++ headers and @@ hunks; paths relative to the workspace root) " +
			"or the *** Begin Patch / *** Update File: <path> / *** End Patch envelope.",
		Parameters: []ParameterDef{
			{Name: "patch", Type: "string", Description: "unified diff patch to apply", Required: true},
		},
		AutoApprove:      false,
		Concurrency:      ToolConcurrencySerial,
		MutatesWorkspace: true,
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
				lastDiff = ""
				return "apply_patch denied by user", nil
			}

			activeWorkDir := currentWorkDir(provider, fallbackWorkDir)
			if err := os.MkdirAll(activeWorkDir, 0o755); err != nil {
				lastDiff = ""
				return "", err
			}

			if isV4APatch(patch) {
				changes, err := parseV4A(patch, activeWorkDir)
				if err != nil {
					lastDiff = ""
					return fmt.Sprintf("apply_patch failed: %v", err), nil
				}
				diff, err := applyV4AChanges(changes, activeWorkDir)
				if err != nil {
					lastDiff = ""
					return fmt.Sprintf("apply_patch failed: %v", err), nil
				}
				lastDiff = diff
				_, ins, del := diffStat(diff)
				return fmt.Sprintf("applied patch (%d files, %d insertions(+), %d deletions(-))", len(changes), ins, del), nil
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
			check.Dir = activeWorkDir
			if out, err := check.CombinedOutput(); err != nil {
				lastDiff = ""
				return fmt.Sprintf("apply_patch failed: %s", strings.TrimSpace(string(out))), nil
			}

			apply := exec.CommandContext(ctx, "git", "apply", "--recount", "--whitespace=nowarn", tmpPath)
			apply.Dir = activeWorkDir
			if out, err := apply.CombinedOutput(); err != nil {
				lastDiff = ""
				return fmt.Sprintf("apply_patch failed: %s", strings.TrimSpace(string(out))), nil
			}

			files, ins, del := diffStat(patch)
			return fmt.Sprintf("applied patch (%d files, %d insertions(+), %d deletions(-))", files, ins, del), nil
		},
	}
}
