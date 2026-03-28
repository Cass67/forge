package tools

import (
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

var (
	pseudoGitStatusCommandPattern = regexp.MustCompile(`(^|(?:&&|\|\||;|\|)\s*)git_status(\s|$)`)
	pseudoGitLogCommandPattern    = regexp.MustCompile(`(^|(?:&&|\|\||;|\|)\s*)git_log(?:\s+([0-9]+))?(\s|$)`)
	pseudoGitDiffCommandPattern   = regexp.MustCompile(`(^|(?:&&|\|\||;|\|)\s*)git_diff(?:\s+([^\s;&|]+))?(\s|$)`)
)

func NewRunCommand(workDir string, timeoutSecs int, approve ApprovalFunc, forcePrompt ...ApprovalFunc) Tool {
	return Tool{
		Name:        "run_command",
		Description: "Execute a shell command.",
		Parameters: []ParameterDef{
			{Name: "command", Type: "string", Description: "command to run", Required: true},
		},
		AutoApprove: false,
		Execute: func(ctx context.Context, args map[string]any) (string, error) {
			command, _ := args["command"].(string)
			command = normalizePseudoToolCommands(command)

			approver := approve
			if isDestructive(command) && len(forcePrompt) > 0 && forcePrompt[0] != nil {
				approver = forcePrompt[0]
			}

			approved, err := approver(Action{
				Tool:    "run_command",
				Summary: command,
				Detail:  command,
			})
			if err != nil {
				return "", err
			}
			if !approved {
				return "run_command denied by user", nil
			}

			timeout := time.Duration(timeoutSecs) * time.Second
			cmdCtx, cancel := context.WithTimeout(ctx, timeout)
			defer cancel()

			cmd := exec.CommandContext(cmdCtx, "sh", "-c", command)
			cmd.Dir = workDir
			out, err := cmd.CombinedOutput()

			result := string(out)
			if len(result) > 50*1024 {
				result = result[:50*1024] + "\n... output truncated at 50KB"
			}

			exitCode := 0
			if err != nil {
				if cmdCtx.Err() == context.DeadlineExceeded {
					return result + fmt.Sprintf("\ntimeout after %ds", timeoutSecs), nil
				}
				if exitErr, ok := err.(*exec.ExitError); ok {
					exitCode = exitErr.ExitCode()
				}
			}

			return result + fmt.Sprintf("\nexit %d", exitCode), nil
		},
	}
}

func normalizePseudoToolCommands(command string) string {
	command = pseudoGitStatusCommandPattern.ReplaceAllString(command, `${1}git status --porcelain${2}`)
	command = pseudoGitLogCommandPattern.ReplaceAllStringFunc(command, func(match string) string {
		parts := pseudoGitLogCommandPattern.FindStringSubmatch(match)
		prefix := parts[1]
		count := strings.TrimSpace(parts[2])
		suffix := parts[3]
		if count == "" {
			count = "10"
		}
		return prefix + "git log --oneline -n " + count + suffix
	})
	command = pseudoGitDiffCommandPattern.ReplaceAllStringFunc(command, func(match string) string {
		parts := pseudoGitDiffCommandPattern.FindStringSubmatch(match)
		prefix := parts[1]
		ref := strings.TrimSpace(parts[2])
		suffix := parts[3]
		if ref == "" {
			return prefix + "git diff" + suffix
		}
		return prefix + "git diff " + ref + suffix
	})
	return command
}

func isDestructive(cmd string) bool {
	lower := strings.ToLower(cmd)
	patterns := []string{
		"rm -rf /",
		"sudo ",
		"| sh", "| bash", "| zsh",
		"chmod 777",
		"mkfs",
		"> /dev/",
		"dd if=",
	}
	for _, p := range patterns {
		if strings.Contains(lower, p) {
			return true
		}
	}
	return false
}
