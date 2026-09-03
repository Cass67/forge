//go:build !windows

package tools

import (
	"os/exec"
	"syscall"
	"time"
)

// setProcGroup puts the child in its own process group so that killing the
// group reaches grandchildren. PTY sessions are left alone: pty.Start sets
// Setsid, which already makes the child a group leader, and combining it with
// Setpgid fails with EINVAL.
func setProcGroup(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
}

// killProcGroup signals the whole process group, giving it a moment to exit on
// SIGTERM before SIGKILL. Without this, `sh -c "sleep 30 & wait"` leaves the
// sleep running after the session believes it cleaned up.
func killProcGroup(pid int) {
	pgid, err := syscall.Getpgid(pid)
	if err != nil {
		// Child already reaped, or never got its own group.
		_ = syscall.Kill(pid, syscall.SIGKILL)
		return
	}
	_ = syscall.Kill(-pgid, syscall.SIGTERM)
	for range 20 {
		time.Sleep(25 * time.Millisecond)
		if syscall.Kill(-pgid, 0) != nil {
			return
		}
	}
	_ = syscall.Kill(-pgid, syscall.SIGKILL)
}
