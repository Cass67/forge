package tools

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
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
