package tools

import (
	"context"
	"sync"
)

// SandboxExecutor routes run_command shell execution through an alternate
// backend (e.g. a Docker container). workDir is the host working directory;
// the executor maps it into the sandbox itself. Output should follow the
// run_command convention of ending with "\nexit N". handled=false means
// fall through to host execution.
type SandboxExecutor func(ctx context.Context, workDir, command string) (output string, handled bool, err error)

// SandboxArgvFunc rewrites an exec_session command into an argv that runs it
// inside the sandbox (e.g. docker exec). handled=false means run on the host.
// A non-nil error fails the session start: when a sandbox is active we fail
// closed rather than silently falling back to host execution.
type SandboxArgvFunc func(workDir, command string, tty bool) (argv []string, handled bool, err error)

var (
	sandboxMu     sync.RWMutex
	sandboxFn     SandboxExecutor
	sandboxArgvFn SandboxArgvFunc
)

// SetSandboxExecutor installs the executor run_command delegates foreground
// commands to. Pass nil to restore host execution. Background (&) commands
// stay on the host; PTY sessions route via SetSandboxArgv.
// no background routing, add when needed.
func SetSandboxExecutor(ex SandboxExecutor) {
	sandboxMu.Lock()
	sandboxFn = ex
	sandboxMu.Unlock()
}

// SetSandboxArgv installs the argv rewriter exec_session starts consult.
// Pass nil to restore host execution.
func SetSandboxArgv(fn SandboxArgvFunc) {
	sandboxMu.Lock()
	sandboxArgvFn = fn
	sandboxMu.Unlock()
}

// CurrentSandboxArgv returns the installed argv rewriter, or nil.
func CurrentSandboxArgv() SandboxArgvFunc {
	sandboxMu.RLock()
	defer sandboxMu.RUnlock()
	return sandboxArgvFn
}

// CurrentSandboxExecutor returns the installed executor, or nil.
func CurrentSandboxExecutor() SandboxExecutor {
	sandboxMu.RLock()
	defer sandboxMu.RUnlock()
	return sandboxFn
}

// WithSandboxExecutor wraps a run_command Tool so that, when a sandbox
// executor is installed, foreground commands are approved and then routed
// through it. When no executor is installed (or it declines), the wrapped
// tool behaves exactly as before. Executor output passes through the same
// secret policy and 50KB truncation as host output.
func WithSandboxExecutor(t Tool, approve ApprovalFunc, provider WorkDirProvider, fallbackWorkDir string, policy SecretPolicy) Tool {
	inner := t.Execute
	secretPolicy := policy.WithDefaults()
	t.Execute = func(ctx context.Context, args map[string]any) (string, error) {
		ex := CurrentSandboxExecutor()
		if ex == nil {
			return inner(ctx, args)
		}
		command, _ := args["command"].(string)
		approved, err := approve(Action{
			Context: ctx,
			Tool:    t.Name,
			Summary: command,
			Detail:  command,
		})
		if err != nil {
			return "", err
		}
		if !approved {
			return "run_command denied by user", nil
		}
		out, handled, execErr := ex(ctx, currentWorkDir(provider, fallbackWorkDir), command)
		if !handled {
			return inner(ctx, args)
		}
		result, blocked := secretPolicy.ApplyCommandOutput(out)
		if blocked {
			return result, nil
		}
		if len(result) > 50*1024 {
			result = result[:50*1024] + "\n... output truncated at 50KB"
		}
		return result, execErr
	}
	return t
}
