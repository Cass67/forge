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
		Name: "run_command",
		Description: "Execute a shell command. Set run_in_background for anything " +
			"that does not exit on its own -- dev servers, watchers, REPLs, TUIs: " +
			"the call returns a session handle immediately and you read its output " +
			"with read_output. Foreground commands must finish within the timeout.",
		Parameters: []ParameterDef{
			{Name: "command", Type: "string", Description: "command to run", Required: true},
			{Name: "run_in_background", Type: "boolean", Description: "run without waiting for the command to exit"},
		},
		AutoApprove: false,
		Timeout:     effectiveRunCommandTimeout("", timeoutSecs),
		Execute: func(ctx context.Context, args map[string]any) (string, error) {
			command, _ := args["command"].(string)
			command = normalizePseudoToolCommands(command)
			// Whether a command is interactive or long-running is the model's
			// to declare, not this tool's to infer. Guessing it from the text
			// refused ordinary commands that merely named a program, and the
			// refusal came back as a successful result, so a retry changed
			// nothing. Anything that does run is bounded by the tool timeout.
			// Refused before approval so --yolo cannot wave it through: the
			// approval hook and the force-prompt hook are the same function,
			// so an auto-approving session had no protection at all. This is
			// a decidable question about which path the command resolves to,
			// not a guess about how dangerous it looks, and it is an error
			// rather than advice so the model must change the command.
			if target := blockedDestructiveTarget(command, currentWorkDir(provider, fallbackWorkDir), userHomeDir()); target != "" {
				return "", fmt.Errorf("refusing to run: %q would destroy %s, which is outside the workspace and not recoverable; target a path inside the working directory instead", command, target)
			}

			background := isBackgroundCommand(command) || boolArg(args, "run_in_background")
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

// boolArg reads a boolean tool argument, tolerating the string form some
// providers emit for boolean parameters.
func boolArg(args map[string]any, key string) bool {
	switch v := args[key].(type) {
	case bool:
		return v
	case string:
		return strings.EqualFold(strings.TrimSpace(v), "true")
	default:
		return false
	}
}

// userHomeDir returns the home directory, or "" when it cannot be determined,
// in which case home-directory protection is simply not applied.
func userHomeDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return home
}

func isBackgroundCommand(cmd string) bool {
	return strings.HasSuffix(strings.TrimSpace(cmd), "&")
}

func stripBackgroundCommandSuffix(cmd string) string {
	trimmed := strings.TrimSpace(cmd)
	trimmed = strings.TrimSuffix(trimmed, "&")
	return strings.TrimSpace(trimmed)
}
