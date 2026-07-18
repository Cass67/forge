package sandbox

import (
	"context"
	"os/exec"
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
}

func (f *fakeDocker) run(ctx context.Context, args ...string) ([]byte, error) {
	f.calls = append(f.calls, args)
	if len(args) > 0 && args[0] == "inspect" {
		return []byte(f.inspectOut), nil
	}
	return []byte(f.out), f.err
}

func resetSession(t *testing.T) *fakeDocker {
	t.Helper()
	f := &fakeDocker{out: "ok", inspectOut: "true"}
	orig := dockerRunner
	dockerRunner = f.run
	sessionMu.Lock()
	manualOn, configOn, cfgImage, sess = false, false, "", nil
	sessionMu.Unlock()
	t.Cleanup(func() {
		dockerRunner = orig
		sessionMu.Lock()
		manualOn, configOn, cfgImage, sess = false, false, "", nil
		sessionMu.Unlock()
		agenttools.SetSandboxExecutor(nil)
	})
	return f
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
