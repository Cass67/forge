package sandbox

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	agenttools "forge/internal/agent/tools"
)

type fakeDocker struct {
	calls [][]string
	out   string
	err   error
	// inspectOut controls what "docker inspect -f ..." returns.
	inspectOut string
	// imageInspectErr controls "docker image inspect" (nil = image exists).
	imageInspectErr error
}

func (f *fakeDocker) run(ctx context.Context, args ...string) ([]byte, error) {
	f.calls = append(f.calls, args)
	if len(args) > 0 && args[0] == "inspect" {
		return []byte(f.inspectOut), nil
	}
	if len(args) > 1 && args[0] == "image" && args[1] == "inspect" {
		return nil, f.imageInspectErr
	}
	return []byte(f.out), f.err
}

func resetSession(t *testing.T) *fakeDocker {
	t.Helper()
	f := &fakeDocker{out: "ok", inspectOut: "true"}
	orig := dockerRunner
	dockerRunner = f.run
	sessionMu.Lock()
	manualOn, configOn, cfgImage, cfgDockerfile, sess = false, false, "", "", nil
	sessionMu.Unlock()
	t.Cleanup(func() {
		dockerRunner = orig
		sessionMu.Lock()
		manualOn, configOn, cfgImage, cfgDockerfile, sess = false, false, "", "", nil
		sessionMu.Unlock()
		agenttools.SetSandboxExecutor(nil)
		agenttools.SetSandboxArgv(nil)
	})
	return f
}

func TestDockerfileSettingBuildsImage(t *testing.T) {
	f := resetSession(t)
	f.imageInspectErr = exec.ErrNotFound // image does not exist yet
	df := filepath.Join(t.TempDir(), "Dockerfile")
	if err := os.WriteFile(df, []byte("FROM ubuntu:24.04\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	Plugin{}.Configure(map[string]any{"default_on": true, "dockerfile": df})

	s, err := ensureSession(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(s.Image, "forge-sandbox:") {
		t.Fatalf("expected content-hash tag, got %s", s.Image)
	}
	var built, ran bool
	for _, c := range f.calls {
		if c[0] == "build" {
			built = true
			if c[3] != "-f" || c[4] != df {
				t.Fatalf("build args: %v", c)
			}
		}
		if c[0] == "run" && slices.Contains(c, s.Image) {
			ran = true
		}
	}
	if !built || !ran {
		t.Fatalf("built=%v ran=%v calls=%v", built, ran, f.calls)
	}
}

func TestDockerfileBuildSkippedWhenImageExists(t *testing.T) {
	f := resetSession(t)
	f.imageInspectErr = nil // image already exists
	df := filepath.Join(t.TempDir(), "Dockerfile")
	if err := os.WriteFile(df, []byte("FROM ubuntu:24.04\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	Plugin{}.Configure(map[string]any{"default_on": true, "dockerfile": df})

	if _, err := ensureSession(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, c := range f.calls {
		if c[0] == "build" {
			t.Fatalf("build should be skipped when image exists: %v", f.calls)
		}
	}
}

func TestCmdBuildForcesRebuild(t *testing.T) {
	f := resetSession(t)
	f.imageInspectErr = nil // image exists, build must still run
	df := filepath.Join(t.TempDir(), "Dockerfile")
	if err := os.WriteFile(df, []byte("FROM ubuntu:24.04\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := cmdBuild(context.Background(), df)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "forge-sandbox:") {
		t.Fatalf("cmdBuild output: %s", out)
	}
	var built bool
	for _, c := range f.calls {
		if c[0] == "build" {
			built = true
		}
	}
	if !built {
		t.Fatalf("expected forced build, calls=%v", f.calls)
	}
}

func TestExplicitImageOutranksDockerfile(t *testing.T) {
	resetSession(t)
	Plugin{}.Configure(map[string]any{"default_on": true, "image": "ubuntu:24.04", "dockerfile": "/nope/Dockerfile"})
	s, err := ensureSession(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if s.Image != "ubuntu:24.04" {
		t.Fatalf("explicit image should win, got %s", s.Image)
	}
}

func TestSandboxArgvRoutesPTYThroughDockerExec(t *testing.T) {
	resetSession(t)
	sessionMu.Lock()
	manualOn = true
	syncExecutorLocked()
	sessionMu.Unlock()

	if agenttools.CurrentSandboxArgv() == nil {
		t.Fatal("argv rewriter not installed when session on")
	}
	argv, handled, err := sandboxArgv("/somewhere", "top", true)
	if err != nil || !handled {
		t.Fatalf("handled=%v err=%v", handled, err)
	}
	joined := strings.Join(argv, " ")
	if argv[0] != "docker" || argv[1] != "exec" || !strings.Contains(joined, "-t") {
		t.Fatalf("expected docker exec -t argv, got %v", argv)
	}
	if argv[len(argv)-1] != "top" || argv[len(argv)-2] != "-c" {
		t.Fatalf("command not passed via sh -c: %v", argv)
	}

	argv, _, _ = sandboxArgv("/somewhere", "top", false)
	if strings.Contains(strings.Join(argv, " "), " -t ") {
		t.Fatalf("non-tty should not allocate a TTY: %v", argv)
	}
}

func TestSandboxArgvFailsClosed(t *testing.T) {
	f := resetSession(t)
	f.err = exec.ErrNotFound
	f.inspectOut = "false"
	sessionMu.Lock()
	manualOn = true
	syncExecutorLocked()
	sessionMu.Unlock()

	_, handled, err := sandboxArgv("/somewhere", "top", true)
	if err == nil {
		t.Fatal("docker failure should surface an error, not fall back to host")
	}
	if handled {
		t.Fatal("failed start must not report handled")
	}
}

func TestSandboxArgvClearedWhenOff(t *testing.T) {
	resetSession(t)
	sessionMu.Lock()
	manualOn = true
	syncExecutorLocked()
	manualOn = false
	syncExecutorLocked()
	sessionMu.Unlock()
	if agenttools.CurrentSandboxArgv() != nil {
		t.Fatal("argv rewriter should clear when session off")
	}
}

func TestConfigureDefaultOn(t *testing.T) {
	resetSession(t)
	Plugin{}.Configure(map[string]any{"default_on": true, "image": "golang:1-alpine"})
	if agenttools.CurrentSandboxExecutor() == nil {
		t.Fatal("default_on=true should install executor")
	}
	if !on() {
		t.Fatal("session should be on")
	}
	Plugin{}.Configure(nil)
	if agenttools.CurrentSandboxExecutor() != nil {
		t.Fatal("nil settings should clear executor")
	}
	if on() {
		t.Fatal("session should be off after Configure(nil)")
	}
}

func TestCmdOnOffFlow(t *testing.T) {
	f := resetSession(t)
	out, err := cmdOn(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Sandbox ON") {
		t.Fatalf("cmdOn output: %s", out)
	}
	if agenttools.CurrentSandboxExecutor() == nil {
		t.Fatal("executor not installed after on")
	}
	var ranRun bool
	for _, c := range f.calls {
		if len(c) >= 2 && c[0] == "run" && c[1] == "-d" {
			ranRun = true
		}
	}
	if !ranRun {
		t.Fatalf("docker run -d not called: %v", f.calls)
	}

	out, err = cmdOff(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Sandbox OFF") {
		t.Fatalf("cmdOff output: %s", out)
	}
	if agenttools.CurrentSandboxExecutor() != nil {
		t.Fatal("executor still installed after off")
	}
	var ranRm bool
	for _, c := range f.calls {
		if len(c) >= 2 && c[0] == "rm" && c[1] == "-f" {
			ranRm = true
		}
	}
	if !ranRm {
		t.Fatalf("docker rm -f not called: %v", f.calls)
	}
}

func TestSandboxExecFormatsOutput(t *testing.T) {
	f := resetSession(t)
	if _, err := cmdOn(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
	f.out = "hello world\n"
	out, handled, err := sandboxExec(context.Background(), "", "echo hello world")
	if err != nil || !handled {
		t.Fatalf("out=%q handled=%v err=%v", out, handled, err)
	}
	if out != "hello world\nexit 0" {
		t.Fatalf("output = %q", out)
	}
	last := f.calls[len(f.calls)-1]
	if last[0] != "exec" || last[1] != "-w" || last[2] != "/workspace" {
		t.Fatalf("docker exec args: %v", last)
	}
}

func TestSandboxExecExitCode(t *testing.T) {
	f := resetSession(t)
	if _, err := cmdOn(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
	f.out = "boom"
	f.err = exec.Command("sh", "-c", "exit 3").Run() // real *exec.ExitError with code 3
	out, handled, err := sandboxExec(context.Background(), "", "false")
	if err != nil || !handled {
		t.Fatalf("out=%q handled=%v err=%v", out, handled, err)
	}
	if !strings.HasSuffix(out, "exit 3") {
		t.Fatalf("output = %q, want exit 3 suffix", out)
	}
}

func TestEnsureSessionRestartsDeadContainer(t *testing.T) {
	f := resetSession(t)
	if _, err := cmdOn(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
	runs := 0
	for _, c := range f.calls {
		if len(c) > 0 && c[0] == "run" {
			runs++
		}
	}
	f.inspectOut = "false" // container died
	if _, err := ensureSession(context.Background()); err != nil {
		t.Fatal(err)
	}
	runs2 := 0
	for _, c := range f.calls {
		if len(c) > 0 && c[0] == "run" {
			runs2++
		}
	}
	if runs2 != runs+1 {
		t.Fatalf("expected restart, runs before=%d after=%d", runs, runs2)
	}
}

func TestContainerWorkDirMapping(t *testing.T) {
	s := &sessionState{Dir: "/home/user/proj"}
	cases := map[string]string{
		"/home/user/proj":     "/workspace",
		"/home/user/proj/sub": "/workspace/sub",
		"/etc":                "/workspace",
	}
	for in, want := range cases {
		if got := containerWorkDir(s, in); got != want {
			t.Errorf("containerWorkDir(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSlashRouting(t *testing.T) {
	resetSession(t)
	cmd := Plugin{}.Commands()[0]
	out, err := cmd.Handler(context.Background(), "status")
	if err != nil || !strings.Contains(out, "OFF") {
		t.Fatalf("status off: %q err=%v", out, err)
	}
	if _, err := cmd.Handler(context.Background(), "on"); err != nil {
		t.Fatal(err)
	}
	out, _ = cmd.Handler(context.Background(), "status")
	if !strings.Contains(out, "ON") {
		t.Fatalf("status on: %q", out)
	}
	out, err = cmd.Handler(context.Background(), "echo routed")
	if err != nil || !strings.Contains(out, "ok") {
		t.Fatalf("routed cmd: %q err=%v", out, err)
	}
	out, _ = cmd.Handler(context.Background(), "off")
	if !strings.Contains(out, "OFF") {
		t.Fatalf("off: %q", out)
	}
}
