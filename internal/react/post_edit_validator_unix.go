//go:build aix || android || darwin || dragonfly || freebsd || illumos || linux || netbsd || openbsd || solaris

package react

import (
	"os/exec"
	"syscall"
)

func configurePostEditValidatorCommand(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func killPostEditValidatorProcessGroup(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
}
