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

	"forge/internal/plugin"
)

const pluginName = "sandbox"

func init() {
	plugin.Register(Plugin{})
}

// Plugin is the top-level sandbox plugin. Implements ToolProvider and SkillProvider.
type Plugin struct{}

func (Plugin) Name() string    { return pluginName }
func (Plugin) Version() string { return "0.1.0" }

func (Plugin) Tools() []plugin.Tool {
	return []plugin.Tool{
		{
			Name:        "sandbox_run",
			Description: "Run a shell command inside a Docker sandbox container. The container bind-mounts the current working directory, so any files the agent writes inside the container are persisted on disk. Use for isolated development, testing, or when you need a clean environment that doesn't affect the host.",
			Parameters: []plugin.Param{
				{Name: "command", Type: "string", Description: "Shell command to run inside the container", Required: true},
				{Name: "image", Type: "string", Description: "Docker image to use. Defaults to 'golang:1-alpine' for Go projects, 'node:20-alpine' for Node, 'python:3.12-alpine' for Python. Pick one matching your project.", Required: false, Default: "alpine:3.20"},
				{Name: "timeout_seconds", Type: "number", Description: "Max execution time. Default 120s, max 600s.", Required: false, Default: 120},
				{Name: "shell", Type: "string", Description: "Shell interpreter inside container. Default 'sh'. Use 'bash' for bash-specific syntax.", Required: false, Default: "sh"},
			},
			Execute: toolRun,
		},
		{
			Name:        "sandbox_status",
			Description: "Show the status of all active sandbox containers for this session.",
			Parameters:  []plugin.Param{},
			Execute:     toolStatus,
		},
		{
			Name:        "sandbox_stop",
			Description: "Stop and remove an active sandbox container. Use when done with a sandbox to free resources.",
			Parameters: []plugin.Param{
				{Name: "container_id", Type: "string", Description: "Container ID or short ID from sandbox_status", Required: true},
			},
			Execute: toolStop,
		},
	}
}

func (Plugin) Skills() []plugin.Skill {
	return []plugin.Skill{skill}
}

func (Plugin) Commands() []plugin.Command {
	return []plugin.Command{command}
}

func (Plugin) Agents() []plugin.Agent {
	return []plugin.Agent{agent}
}

// --- plugin-level registrations ---

var skill = plugin.Skill{
	Name:        "docker-sandbox",
	Description: "Run code in an isolated Docker container that bind-mounts the current project directory. Use when the user asks to run code in a sandbox, needs an isolated dev environment, wants to test without affecting the host, or needs to install packages in a clean container. The /sandbox slash command is an alias for sandbox_run.",
	Body:        skillBody,
}

var command = plugin.Command{
	Name:        "/sandbox",
	Description: "Run a command in a Docker sandbox. Subcommands: /sandbox on [image] (session mode: run_command executes in a persistent container), /sandbox off, /sandbox status, /sandbox build [dockerfile] (build the configured Dockerfile image). Otherwise alias for sandbox_run.",
	Handler: func(ctx context.Context, args string) (string, error) {
		fields := strings.Fields(args)
		if len(fields) == 0 {
			return sessionStatus(), nil
		}
		switch fields[0] {
		case "on":
			return cmdOn(ctx, strings.TrimSpace(strings.TrimPrefix(args, "on")))
		case "off":
			return cmdOff(ctx)
		case "status":
			return sessionStatus(), nil
		case "build":
			return cmdBuild(ctx, strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(args), "build")))
		}
		if on() {
			out, _, err := sandboxExec(ctx, "", args)
			return out, err
		}
		return toolRun(ctx, map[string]any{"command": args})
	},
}

var agent = plugin.Agent{
	Name:         "sandbox-dev",
	Description:  "Develop in an isolated Docker sandbox. Writes code that runs in a container with the project directory bind-mounted.",
	SystemPrompt: "You are a sandbox-aware developer. You write code that will be tested inside a Docker container. The container bind-mounts the project directory at /workspace. Use sandbox_run or /sandbox to execute code, sandbox_status to check containers, sandbox_stop to clean up.",
	Model:        "",
	Fallbacks:    nil,
	ModelFamily:  "",
	Tools:        []string{"sandbox_run", "sandbox_status", "sandbox_stop", "read", "write", "edit", "run_command", "list_dir", "search", "glob", "git_status", "git_diff", "git_log"},
}

// --- persistent state: track containers per session ---

var (
	mu         sync.Mutex
	containers = make(map[string]*containerState) // container_id -> state
)

// resetState clears the container state (for testing).
func resetState() {
	mu.Lock()
	defer mu.Unlock()
	containers = make(map[string]*containerState)
}

type containerState struct {
	ContainerID string
	Dir         string
	Image       string
	CreatedAt   time.Time
}

// --- tools ---

func toolRun(ctx context.Context, args map[string]any) (string, error) {
	command, _ := args["command"].(string)
	if strings.TrimSpace(command) == "" {
		return "", fmt.Errorf("command is required")
	}

	image, _ := args["image"].(string)
	if image == "" {
		image = detectImage(args)
	}

	timeout, _ := args["timeout_seconds"].(float64)
	if timeout <= 0 {
		timeout = 120
	}
	if timeout > 600 {
		timeout = 600
	}

	shell, _ := args["shell"].(string)
	if shell == "" {
		shell = "sh"
	}

	// Resolve the working directory to bind-mount
	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("failed to get working directory: %w", err)
	}
	absDir, err := filepath.Abs(cwd)
	if err != nil {
		return "", fmt.Errorf("failed to resolve working directory: %w", err)
	}

	// Container name derived from directory so sandbox_status/sandbox_stop can target it.
	dirHash := containerName(absDir)

	// Track the container only while it actually runs; docker --rm removes it
	// on exit, so state recorded after the run would be stale.
	mu.Lock()
	containers[dirHash] = &containerState{
		ContainerID: dirHash,
		Dir:         absDir,
		Image:       image,
		CreatedAt:   time.Now(),
	}
	mu.Unlock()
	defer func() {
		mu.Lock()
		delete(containers, dirHash)
		mu.Unlock()
	}()

	// One-shot container: bind-mount cwd at /workspace, run command, stream output back.
	dockerArgs := []string{
		"run",
		"--name", dirHash,
		"-v", mountSpec(absDir),
		"-w", "/workspace",
		"-e", "HOME=/workspace",
		"--rm",
	}
	dockerArgs = append(dockerArgs, hardeningArgs()...)
	dockerArgs = append(dockerArgs, image, shell, "-c", command)

	ctx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "docker", dockerArgs...)
	cmd.Dir = absDir // fallback dir if container dir differs

	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Sprintf("Command failed:\n%s\n\nDocker args: %v", strings.TrimSpace(string(output)), dockerArgs), err
	}

	result := fmt.Sprintf("Sandbox executed in container %s\nDirectory: %s\nImage: %s\nOutput:\n%s", dirHash, absDir, image, strings.TrimSpace(string(output)))
	return result, nil
}

func toolStatus(ctx context.Context, args map[string]any) (string, error) {
	mu.Lock()
	defer mu.Unlock()

	if len(containers) == 0 {
		return sessionStatus() + "\nNo one-shot sandboxes running.", nil
	}

	var lines []string
	lines = append(lines, fmt.Sprintf("Active sandboxes (%d):", len(containers)))
	for id, cs := range containers {
		lines = append(lines, fmt.Sprintf("  Container: %s", id))
		lines = append(lines, fmt.Sprintf("  Directory: %s", cs.Dir))
		lines = append(lines, fmt.Sprintf("  Image:     %s", cs.Image))
		lines = append(lines, fmt.Sprintf("  Created:   %s", cs.CreatedAt.Format(time.RFC3339)))
		lines = append(lines, "---")
	}
	return strings.Join(lines, "\n"), nil
}

func toolStop(ctx context.Context, args map[string]any) (string, error) {
	containerID, _ := args["container_id"].(string)
	if strings.TrimSpace(containerID) == "" {
		return "", fmt.Errorf("container_id is required")
	}

	// Find and remove from state
	mu.Lock()
	var found *containerState
	var foundKey string
	for k, cs := range containers {
		if strings.HasPrefix(cs.ContainerID, containerID) || strings.HasPrefix(k, containerID) {
			found = cs
			foundKey = k
			break
		}
	}
	if found != nil {
		delete(containers, foundKey)
	}
	mu.Unlock()

	if found == nil {
		// Try docker stop anyway in case it exists outside our tracking
		cmd := exec.CommandContext(ctx, "docker", "stop", containerID)
		_ = cmd.Run()
		return fmt.Sprintf("Container %s not tracked by plugin. Attempted docker stop.", containerID), nil
	}

	cmd := exec.CommandContext(ctx, "docker", "stop", found.ContainerID)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Sprintf("Failed to stop container %s: %s\n%s", found.ContainerID, err, string(out)), err
	}

	return fmt.Sprintf("Sandbox stopped and removed: %s (dir: %s)", found.ContainerID, found.Dir), nil
}

// --- helpers ---

// detectImage picks a sensible default based on the project directory contents.
func detectImage(args map[string]any) string {
	cwd, err := os.Getwd()
	if err != nil {
		return "alpine:3.20"
	}

	entries, err := os.ReadDir(cwd)
	if err != nil {
		return "alpine:3.20"
	}

	for _, e := range entries {
		name := e.Name()
		switch {
		case name == "go.mod":
			return "golang:1-alpine"
		case name == "package.json":
			return "node:20-alpine"
		case name == "requirements.txt" || name == "pyproject.toml" || strings.HasSuffix(name, ".py"):
			return "python:3.12-alpine"
		case name == "Cargo.toml":
			return "rust:alpine"
		case name == "Gemfile":
			return "ruby:alpine"
		}
	}
	return "alpine:3.20"
}

// containerName creates a docker-safe name from an absolute path.
// Replaces non-alphanumeric chars with dashes, truncates to 64 chars.
func containerName(dir string) string {
	s := strings.ReplaceAll(dir, "/", "-")
	s = strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' {
			return r
		}
		return '-'
	}, s)
	// Collapse multiple dashes
	for strings.Contains(s, "--") {
		s = strings.ReplaceAll(s, "--", "-")
	}
	s = strings.Trim(s, "-")
	if len(s) > 64 {
		s = s[:64]
	}
	if s == "" {
		s = "sandbox"
	}
	return "forge-" + s
}

// --- skill body ---

const skillBody = `## Docker Sandbox

Run code in a Docker container that bind-mounts the current project directory at /workspace.

**This is convenience isolation, not a security boundary.** It keeps environment side effects
(installed packages, changed toolchains, stray build output) off your host. It does not contain
hostile code: the project directory is mounted into the container, the container shares the host
kernel, and a container escape or a mount-write is not defended against. Do not run code you
actively distrust and expect the host to be safe.

Containers run with ` + "`" + `--cap-drop=ALL --security-opt no-new-privileges --network none` + "`" + `, and the
project is mounted **read-only** by default. Set ` + "`" + `[plugins.settings] writable = true` + "`" + ` to mount
it read-write, which is what you need for builds or installs that must persist to the project.

## When to Use

- Testing without affecting the host environment
- Installing packages in a clean container
- Building or compiling in an isolated environment
- The user asks to "sandbox" something

## Commands

### /sandbox on [image] | off | status | build [dockerfile]
Session mode: ` + "`" + `on` + "`" + ` starts one persistent container (project dir bind-mounted at /workspace) and routes ALL run_command and terminal (exec_session) execution through it until ` + "`" + `off` + "`" + `. Config: ` + "`" + `[plugins.settings] default_on = true` + "`" + `, optional ` + "`" + `writable = true` + "`" + ` (read-write project mount; default is read-only), ` + "`" + `image = "..."` + "`" + ` or ` + "`" + `dockerfile = "path/to/Dockerfile"` + "`" + ` (built on demand, content-hash tagged; edits rebuild automatically). ` + "`" + `build` + "`" + ` forces a rebuild now. Image precedence: explicit image > dockerfile > auto-detect.

### /sandbox <command>
Alias for ` + "`" + `sandbox_run` + "`" + `. Runs a shell command in a Docker container with the current directory bind-mounted. When session mode is on, runs in the session container instead.

### sandbox_run
Parameters:
- ` + "`" + `command` + "`" + ` (required): Shell command to execute
- ` + "`" + `image` + "`" + ` (optional): Docker image. Auto-detected if project has go.mod, package.json, requirements.txt, etc.
- ` + "`" + `timeout_seconds` + "`" + ` (optional): Max execution time, default 120s, max 600s
- ` + "`" + `shell` + "`" + ` (optional): Shell interpreter, default "sh"

### sandbox_status
Show all active sandbox containers.

### sandbox_stop
Stop and remove a sandbox container.

## Images

Auto-detected from project files:
- go.mod → golang:1-alpine
- package.json → node:20-alpine
- requirements.txt / pyproject.toml → python:3.12-alpine
- Cargo.toml → rust:alpine
- Gemfile → ruby:alpine
- Default → alpine:3.20

## Notes

- The container bind-mounts the current working directory at /workspace
- Files written inside the container appear on the host immediately
- Containers use --rm so they're cleaned up after execution
- Each unique directory gets its own container name for identification
- Use short timeout for quick checks, longer for builds
`
