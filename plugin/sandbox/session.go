package sandbox

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
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
	sessionMu     sync.Mutex
	manualOn      bool
	configOn      bool
	cfgImage      string
	cfgDockerfile string
	cfgWritable   bool
	sess          *sessionState
)

// hardeningArgs are the docker flags applied to every sandbox container. They
// reduce what a container can do to the host if code inside it misbehaves;
// they do not make the sandbox a security boundary (see the plugin docs).
// --read-only is deliberately absent: HOME=/workspace means a read-only root
// breaks ordinary builds, and a flag that forces users to disable hardening is
// worse than not shipping it.
func hardeningArgs() []string {
	return []string{
		"--cap-drop=ALL",
		"--security-opt", "no-new-privileges",
		"--network", "none",
	}
}

// mountSpec returns the project bind-mount. Read-only unless the user opts in
// with writable = true, because the mount is the one route from container code
// back onto the host filesystem.
func mountSpec(dir string) string {
	if cfgWritable {
		return dir + ":/workspace:rw"
	}
	return dir + ":/workspace:ro"
}

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
	if v, ok := settings["dockerfile"].(string); ok {
		cfgDockerfile = v
	} else {
		cfgDockerfile = ""
	}
	cfgWritable = settings["writable"] == true
	syncExecutorLocked()
}

func on() bool {
	sessionMu.Lock()
	defer sessionMu.Unlock()
	return manualOn || configOn
}

// syncExecutorLocked installs or clears the run_command sandbox executor and
// the exec_session argv rewriter. Caller must hold sessionMu.
func syncExecutorLocked() {
	if manualOn || configOn {
		agenttools.SetSandboxExecutor(sandboxExec)
		agenttools.SetSandboxArgv(sandboxArgv)
	} else {
		agenttools.SetSandboxExecutor(nil)
		agenttools.SetSandboxArgv(nil)
	}
}

// sandboxArgv implements agenttools.SandboxArgvFunc: rewrites an exec_session
// command to run inside the persistent session container. Errors fail the
// session start (fail closed) so a broken sandbox never leaks onto the host.
func sandboxArgv(workDir, command string, tty bool) ([]string, bool, error) {
	if !on() {
		return nil, false, nil
	}
	s, err := ensureSession(context.Background())
	if err != nil {
		return nil, false, err
	}
	argv := []string{"docker", "exec", "-i"}
	if tty {
		argv = append(argv, "-t")
	}
	argv = append(argv, "-w", containerWorkDir(s, workDir), s.ContainerName, "sh", "-c", command)
	return argv, true, nil
}

func sessionContainerName(dir string) string {
	return fmt.Sprintf("%s-session-%d", containerName(dir), os.Getpid())
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

	image, err := resolveImageLocked(ctx, false)
	if err != nil {
		return nil, err
	}
	// Names are per-process, so a live collision with another forge instance
	// can't happen. A same-named container here is a leftover from a crashed
	// prior process that reused our PID: remove it and start clean rather than
	// inherit a polluted environment.
	_, _ = dockerRunner(ctx, "rm", "-f", name)
	runArgs := []string{"run", "-d",
		"--name", name,
		"--label", "forge.sandbox=1",
		"--label", "forge.pid=" + strconv.Itoa(os.Getpid()),
		"-v", mountSpec(dir),
		"-w", "/workspace",
		"-e", "HOME=/workspace",
	}
	runArgs = append(runArgs, hardeningArgs()...)
	runArgs = append(runArgs, image, "tail", "-f", "/dev/null")
	out, err := dockerRunner(ctx, runArgs...)
	if err != nil {
		return nil, fmt.Errorf("docker run: %w: %s", err, strings.TrimSpace(string(out)))
	}
	sess = &sessionState{ContainerName: name, Dir: dir, Image: image, StartedAt: time.Now()}
	return sess, nil
}

// resolveImageLocked picks the session image: explicit image setting wins,
// then a configured Dockerfile (built on demand), then project auto-detect.
// Caller must hold sessionMu.
func resolveImageLocked(ctx context.Context, forceBuild bool) (string, error) {
	if cfgImage != "" {
		return cfgImage, nil
	}
	if cfgDockerfile != "" {
		return builtImageLocked(ctx, forceBuild)
	}
	return detectImage(nil), nil
}

// builtImageLocked builds the configured Dockerfile into a content-hash-tagged
// image, skipping the build when that tag already exists (unless forced).
// Editing the Dockerfile changes the hash, so rebuilds happen automatically.
// Caller must hold sessionMu.
func builtImageLocked(ctx context.Context, force bool) (string, error) {
	content, err := os.ReadFile(cfgDockerfile)
	if err != nil {
		return "", fmt.Errorf("read dockerfile %s: %w", cfgDockerfile, err)
	}
	tag := fmt.Sprintf("forge-sandbox:%x", sha256.Sum256(content))[:len("forge-sandbox:")+12]
	if !force {
		if _, err := dockerRunner(ctx, "image", "inspect", tag); err == nil {
			return tag, nil
		}
	}
	out, err := dockerRunner(ctx, "build", "-t", tag, "-f", cfgDockerfile, filepath.Dir(cfgDockerfile))
	if err != nil {
		return "", fmt.Errorf("docker build %s: %w: %s", cfgDockerfile, err, strings.TrimSpace(string(out)))
	}
	return tag, nil
}

// cmdBuild handles "/sandbox build [dockerfile]": force-build the image now.
func cmdBuild(ctx context.Context, dockerfileArg string) (string, error) {
	sessionMu.Lock()
	defer sessionMu.Unlock()
	if dockerfileArg != "" {
		cfgDockerfile = dockerfileArg
	}
	if cfgDockerfile == "" {
		return "", fmt.Errorf("no dockerfile configured: /sandbox build <path> or set [plugins.settings] dockerfile")
	}
	cfgImage = "" // a built image outranks a stale explicit image setting
	tag, err := builtImageLocked(ctx, true)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("Sandbox image built: %s (from %s)\nSession containers will use it. Restart with /sandbox off && /sandbox on if one is already running.", tag, cfgDockerfile), nil
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
	return fmt.Sprintf("Sandbox ON\nContainer: %s\nImage: %s\nDirectory: %s (mounted at /workspace)\nAll run_command and terminal (exec_session) commands now execute inside this container. /sandbox off to stop.", s.ContainerName, s.Image, s.Dir), nil
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

// reapOrphans removes sandbox containers whose owning forge process is gone.
// A crashed instance never runs its session-end cleanup, so without this its
// container would leak. Cross-instance safe: containers owned by a still-live
// forge PID are left untouched. Called at session start.
func reapOrphans(ctx context.Context) {
	out, err := dockerRunner(ctx, "ps", "-a",
		"--filter", "label=forge.sandbox=1",
		"--format", `{{.Label "forge.pid"}} {{.ID}}`)
	if err != nil {
		return
	}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		pid, err := strconv.Atoi(fields[0])
		if err != nil || pidAlive(pid) {
			continue
		}
		_, _ = dockerRunner(ctx, "rm", "-f", fields[1])
	}
}

// pidAlive reports whether a process exists. signal 0 does no work but still
// checks the target: nil means alive, ESRCH means gone.
// ignores PID reuse — a reused PID just delays reaping one container.
func pidAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return p.Signal(syscall.Signal(0)) == nil
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
			Point: plugin.PointSessionStart,
			Handler: func(ctx context.Context, _ plugin.HookEvent) []plugin.HookResult {
				reapOrphans(ctx)
				return nil
			},
		},
		{
			Point: plugin.PointSessionEnd,
			Handler: func(ctx context.Context, _ plugin.HookEvent) []plugin.HookResult {
				cleanupSession(ctx)
				return nil
			},
		},
	}
}
