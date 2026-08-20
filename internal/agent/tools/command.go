package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
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
	// Multi-word invocations are specific enough to match anywhere in a command
	// line; they cannot collide with an ordinary filename or argument.
	interactiveCommandPattern = regexp.MustCompile(`(?i)\b(npm run dev|pnpm dev|yarn dev|npm run start|pnpm start|yarn start|next dev|tail -f|python(?:3)?\s+-i|rails console|python manage\.py shell)\b`)
	// Single-word programs only count when they are the program being run.
	// Matching them anywhere diverted ordinary commands that merely named one:
	// `cat vite.config.ts`, `grep watch src/`, `go test ./internal/top/...`.
	// The command then never ran and returned advice as a successful result,
	// so a model retrying it made no progress.
	interactiveInvocationPattern = regexp.MustCompile(`(?i)(^|[|&;]\s*)(\S*/)?(vite|top|htop|less|more|vim|nvim|nano|watch|irb)\b`)
	// A bare `node`, optionally with an interactive flag, is a REPL. Anything
	// with a script or other flag exits on its own.
	interactiveNodePattern = regexp.MustCompile(`(?i)(^|[|&;]\s*)(\S*/)?node(\s+(-i|--interactive))?\s*$`)
)

func NewRunCommand(workDir string, timeoutSecs int, manager *ExecSessionManager, approve ApprovalFunc, forcePrompt ...ApprovalFunc) Tool {
	return NewRunCommandWithWorkDirProvider(workDir, FixedWorkDirProvider(workDir), timeoutSecs, manager, approve, DefaultSecretPolicy(), forcePrompt...)
}

func NewRunCommandWithWorkDirProvider(fallbackWorkDir string, provider WorkDirProvider, timeoutSecs int, manager *ExecSessionManager, approve ApprovalFunc, policy SecretPolicy, forcePrompt ...ApprovalFunc) Tool {
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
		Timeout:     effectiveRunCommandTimeout("", timeoutSecs),
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
				activeWorkDir := currentWorkDir(provider, fallbackWorkDir)
				if err := os.MkdirAll(activeWorkDir, 0o755); err != nil {
					return "", err
				}
				sessionID, err := manager.Start(activeWorkDir, command)
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

			timeout := effectiveRunCommandTimeout(command, timeoutSecs)
			cmdCtx, cancel := context.WithTimeout(ctx, timeout)
			defer cancel()

			cmd := exec.CommandContext(cmdCtx, "sh", "-c", command)
			activeWorkDir := currentWorkDir(provider, fallbackWorkDir)
			if err := os.MkdirAll(activeWorkDir, 0o755); err != nil {
				return "", err
			}
			cmd.Dir = activeWorkDir
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
					return result + fmt.Sprintf("\ntimeout after %ds", int(timeout.Seconds())), nil
				}
				if exitErr, ok := err.(*exec.ExitError); ok {
					exitCode = exitErr.ExitCode()
				}
			}

			return result + fmt.Sprintf("\nexit %d", exitCode), nil
		},
	}
}

func effectiveRunCommandTimeout(command string, timeoutSecs int) time.Duration {
	base := time.Duration(timeoutSecs) * time.Second
	if base <= 0 {
		base = DefaultToolTimeout
	}
	if filesystemWideDiscoveryCommand(command) && base < 5*time.Minute {
		return 5 * time.Minute
	}
	return base
}

func filesystemWideDiscoveryCommand(command string) bool {
	normalized := strings.ToLower(strings.Join(strings.Fields(command), " "))
	if normalized == "" {
		return false
	}
	return strings.Contains(normalized, "find / ") || strings.Contains(normalized, "find / -") || strings.Contains(normalized, "find /dev") || strings.Contains(normalized, "find /system") || strings.Contains(normalized, "find /users")
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
	return interactiveCommandPattern.MatchString(normalized) ||
		interactiveInvocationPattern.MatchString(normalized) ||
		interactiveNodePattern.MatchString(normalized)
}
