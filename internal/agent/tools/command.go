package tools

import (
	"context"
	"encoding/json"
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
	adHocPreviewServerPattern     = regexp.MustCompile(`(?i)(python(?:3)?\s+-m\s+http\.server|npx\s+http-server|python(?:3)?\s+-m\s+simplehttpserver|ruby\s+-run\s+-e\s+httpd|busybox\s+httpd)`)
	interactiveCommandPattern     = regexp.MustCompile(`(?i)\b(npm run dev|pnpm dev|yarn dev|npm run start|pnpm start|yarn start|vite|next dev|tail -f|top|htop|less|more|vim|nvim|nano|watch\b|python(?:3)?\s+-i|node\b|irb\b|rails console|python manage\.py shell)\b`)
)

func NewRunCommand(workDir string, timeoutSecs int, manager *ExecSessionManager, approve ApprovalFunc, forcePrompt ...ApprovalFunc) Tool {
	return newRunCommand(workDir, timeoutSecs, manager, approve, DefaultSecretPolicy(), forcePrompt...)
}

func NewRunCommandWithSecretPolicy(workDir string, timeoutSecs int, manager *ExecSessionManager, approve ApprovalFunc, policy SecretPolicy, forcePrompt ...ApprovalFunc) Tool {
	return newRunCommand(workDir, timeoutSecs, manager, approve, policy, forcePrompt...)
}

func newRunCommand(workDir string, timeoutSecs int, manager *ExecSessionManager, approve ApprovalFunc, policy SecretPolicy, forcePrompt ...ApprovalFunc) Tool {
	if manager == nil {
		manager = NewExecSessionManager()
	}
	secretPolicy := policy.WithDefaults()
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
			if looksLikeAdHocPreviewServer(command) {
				return "use preview_server_ensure instead of launching an ad-hoc web server with run_command", nil
			}
			if requiresExecSession(command) {
				return "use exec_session_start instead of run_command for interactive or long-running terminal work", nil
			}
			background := isBackgroundCommand(command)
			if background {
				command = stripBackgroundCommandSuffix(command)
			}

			approver := approve
			if isDestructive(command) && len(forcePrompt) > 0 && forcePrompt[0] != nil {
				approver = forcePrompt[0]
			}

			approved, err := approver(Action{
				Context: ctx,
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
			if background {
				sessionID, err := manager.Start(workDir, command)
				if err != nil {
					return "", err
				}
				payload, err := json.Marshal(execSessionStatus{
					Status:    "running",
					SessionID: sessionID,
					Command:   command,
				})
				if err != nil {
					return "", err
				}
				return string(payload), nil
			}

			timeout := time.Duration(timeoutSecs) * time.Second
			cmdCtx, cancel := context.WithTimeout(ctx, timeout)
			defer cancel()

			cmd := exec.CommandContext(cmdCtx, "sh", "-c", command)
			cmd.Dir = workDir
			out, err := cmd.CombinedOutput()

			result, blocked := secretPolicy.ApplyCommandOutput(string(out))
			if blocked {
				return result, nil
			}
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

func looksLikeAdHocPreviewServer(cmd string) bool {
	normalized := strings.ToLower(strings.TrimSpace(cmd))
	if normalized == "" {
		return false
	}
	return adHocPreviewServerPattern.MatchString(normalized)
}

func isBackgroundCommand(cmd string) bool {
	return strings.HasSuffix(strings.TrimSpace(cmd), "&")
}

func stripBackgroundCommandSuffix(cmd string) string {
	trimmed := strings.TrimSpace(cmd)
	trimmed = strings.TrimSuffix(trimmed, "&")
	return strings.TrimSpace(trimmed)
}

func requiresExecSession(cmd string) bool {
	normalized := strings.ToLower(strings.TrimSpace(cmd))
	if normalized == "" {
		return false
	}
	return interactiveCommandPattern.MatchString(normalized)
}
