package sandbox

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	agenttools "forge/internal/agent/tools"
	"forge/internal/plugin"
)

// --- session mode: one long-lived container per working directory ---
//
// /sandbox on   -> start container, route run_command through docker exec
// /sandbox off  -> stop container, restore host execution
// default_on    -> [plugins.settings] default_on = true enables at session start

var (
	sessionMu sync.Mutex
	manualOn  bool
	configOn  bool
	cfgImage  string
	sess      *sessionState
)

type sessionState struct {
	ContainerName string
	Dir           string
	Image         string
	StartedAt     time.Time
}

// dockerRunner runs docker and returns combined output. Stubbed in tests.
var dockerRunner = func(ctx context.Context, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, "docker", args...).CombinedOutput()
}

// Configure implements plugin.Configurable. nil/empty settings = defaults (off).
func (Plugin) Configure(settings map[string]any) {
	sessionMu.Lock()
	defer sessionMu.Unlock()
	configOn = settings["default_on"] == true
	if v, ok := settings["image"].(string); ok {
		cfgImage = v
	} else {
		cfgImage = ""
	}
	syncExecutorLocked()
}

func on() bool {
	sessionMu.Lock()
	defer sessionMu.Unlock()
	return manualOn || configOn
}

// syncExecutorLocked installs or clears the run_command sandbox executor.
// Caller must hold sessionMu.
func syncExecutorLocked() {
	if manualOn || configOn {
		agenttools.SetSandboxExecutor(sandboxExec)
	} else {
		agenttools.SetSandboxExecutor(nil)
	}
}

func sessionContainerName(dir string) string {
	return containerName(dir) + "-session"
}

// sandboxExec implements agenttools.SandboxExecutor: runs the command inside
// the persistent session container via docker exec.
func sandboxExec(ctx context.Context, workDir, command string) (string, bool, error) {
	if !on() {
		return "", false, nil
	}
	s, err := ensureSession(ctx)
	if err != nil {
		return fmt.Sprintf("sandbox session start failed: %v", err), true, err
	}
	work := containerWorkDir(s, workDir)

	ctx, cancel := context.WithTimeout(ctx, 600*time.Second)
	defer cancel()
	out, runErr := dockerRunner(ctx, "exec", "-w", work, s.ContainerName, "sh", "-c", command)
	result := strings.TrimSpace(string(out))
	if ctx.Err() == context.DeadlineExceeded {
		return result + "\ntimeout after 600s", true, nil
	}
	exitCode := 0
	if runErr != nil {
		if exitErr, ok := runErr.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			return result, true, fmt.Errorf("docker exec failed: %w", runErr)
		}
	}
	return result + fmt.Sprintf("\nexit %d", exitCode), true, nil
}

// containerWorkDir maps a host workDir into the container. The session dir is
// bind-mounted at /workspace; subdirectories map below it, anything outside
// falls back to /workspace.
func containerWorkDir(s *sessionState, workDir string) string {
	rel, err := filepath.Rel(s.Dir, workDir)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "/workspace"
	}
	if rel == "." {
		return "/workspace"
	}
	return "/workspace/" + filepath.ToSlash(rel)
}

// ensureSession returns the running session container, (re)starting it if needed.
func ensureSession(ctx context.Context) (*sessionState, error) {
	sessionMu.Lock()
	defer sessionMu.Unlock()

	cwd, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	dir, err := filepath.Abs(cwd)
	if err != nil {
		return nil, err
	}
	name := sessionContainerName(dir)

	if sess != nil && sess.ContainerName == name {
		out, err := dockerRunner(ctx, "inspect", "-f", "{{.State.Running}}", name)
		if err == nil && strings.TrimSpace(string(out)) == "true" {
			return sess, nil
		}
		sess = nil
	}

	image := cfgImage
	if image == "" {
		image = detectImage(nil)
	}
	out, err := dockerRunner(ctx, "run", "-d",
		"--name", name,
		"-v", dir+":/workspace:rw",
		"-w", "/workspace",
		"-e", "HOME=/workspace",
		image, "tail", "-f", "/dev/null")
	if err != nil {
		return nil, fmt.Errorf("docker run: %w: %s", err, strings.TrimSpace(string(out)))
	}
	sess = &sessionState{ContainerName: name, Dir: dir, Image: image, StartedAt: time.Now()}
	return sess, nil
}

// cmdOn handles "/sandbox on [image]".
func cmdOn(ctx context.Context, imageArg string) (string, error) {
	sessionMu.Lock()
	manualOn = true
	if imageArg != "" {
		cfgImage = imageArg
	}
	syncExecutorLocked()
	sessionMu.Unlock()

	s, err := ensureSession(ctx)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("Sandbox ON\nContainer: %s\nImage: %s\nDirectory: %s (mounted at /workspace)\nAll run_command shell commands now execute inside this container. /sandbox off to stop.", s.ContainerName, s.Image, s.Dir), nil
}

// cmdOff handles "/sandbox off": stop container, restore host execution.
func cmdOff(ctx context.Context) (string, error) {
	sessionMu.Lock()
	manualOn = false
	configOn = false
	syncExecutorLocked()
	s := sess
	sess = nil
	sessionMu.Unlock()

	if s == nil {
		return "Sandbox OFF. No container was running.", nil
	}
	out, err := dockerRunner(ctx, "rm", "-f", s.ContainerName)
	if err != nil {
		return "", fmt.Errorf("docker rm: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return fmt.Sprintf("Sandbox OFF\nContainer removed: %s\nrun_command executes on the host again.", s.ContainerName), nil
}

// sessionStatus handles "/sandbox" with no args and "/sandbox status".
func sessionStatus() string {
	sessionMu.Lock()
	defer sessionMu.Unlock()
	if !manualOn && !configOn {
		return "Sandbox session OFF. /sandbox on to start, /sandbox <cmd> for a one-shot container."
	}
	src := "manual"
	if configOn && !manualOn {
		src = "config default_on"
	}
	if sess == nil {
		return "Sandbox session ON (" + src + "). Container starts on first command."
	}
	return fmt.Sprintf("Sandbox session ON (%s)\nContainer: %s\nImage: %s\nDirectory: %s\nStarted: %s",
		src, sess.ContainerName, sess.Image, sess.Dir, sess.StartedAt.Format(time.RFC3339))
}

// cleanupSession tears down the session container on session end.
func cleanupSession(ctx context.Context) {
	sessionMu.Lock()
	s := sess
	sess = nil
	manualOn = false
	syncExecutorLocked()
	sessionMu.Unlock()
	if s != nil {
		_, _ = dockerRunner(ctx, "rm", "-f", s.ContainerName)
	}
}

// Hooks implements plugin.HookProvider: tear the session container down when
// the chat session ends so no containers are orphaned.
func (Plugin) Hooks() []plugin.Hook {
	return []plugin.Hook{
		{
			Point: plugin.PointSessionEnd,
			Handler: func(ctx context.Context, _ plugin.HookEvent) []plugin.HookResult {
				cleanupSession(ctx)
				return nil
			},
		},
	}
}
