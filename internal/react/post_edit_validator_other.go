//go:build !(aix || android || darwin || dragonfly || freebsd || illumos || linux || netbsd || openbsd || solaris)

package react

import "os/exec"

func configurePostEditValidatorCommand(cmd *exec.Cmd) {}

func killPostEditValidatorProcessGroup(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	_ = cmd.Process.Kill()
}
