//go:build !windows

package shellenv

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/creack/pty"
	"golang.org/x/sys/unix"
)

const probeChildEnv = "FORGE_SHELLENV_PROBE_CHILD"

// TestProbeLeavesTheForegroundProcessGroupAlone runs the shell probe in a
// child that owns a pty, the way forge-gui owns the terminal it was launched
// from. An interactive probe shell that grabs that terminal leaves it pointing
// at a dead process group once it exits, and Ctrl-C there stops working.
func TestProbeLeavesTheForegroundProcessGroupAlone(t *testing.T) {
	if os.Getenv(probeChildEnv) == "1" {
		_, _ = loginShellEnv(context.Background())
		_, _ = os.Stdout.WriteString("probe-done\n")
		return
	}

	cmd := exec.Command(os.Args[0], "-test.run=TestProbeLeavesTheForegroundProcessGroupAlone")
	cmd.Env = append(os.Environ(), probeChildEnv+"=1")
	ptmx, err := pty.Start(cmd)
	if err != nil {
		t.Skipf("no pty available: %v", err)
	}
	defer func() {
		_ = ptmx.Close()
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}()

	done := make(chan struct{})
	var out bytes.Buffer
	go func() {
		defer close(done)
		buf := make([]byte, 4096)
		for {
			n, err := ptmx.Read(buf)
			out.Write(buf[:n])
			if bytes.Contains(out.Bytes(), []byte("probe-done")) || err != nil {
				return
			}
		}
	}()

	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatalf("probe never finished, output: %s", out.String())
	}

	fg, err := unix.IoctlGetInt(int(ptmx.Fd()), unix.TIOCGPGRP)
	if err != nil {
		t.Fatalf("read foreground process group: %v", err)
	}
	if fg != cmd.Process.Pid {
		t.Fatalf("foreground process group is %d, want the child %d: the probe took the terminal", fg, cmd.Process.Pid)
	}
	if err := unix.Kill(fg, 0); err != nil && err != unix.EPERM {
		t.Fatalf("foreground process group %d is gone: %v", fg, err)
	}
}
