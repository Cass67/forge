//go:build !windows

package shellenv

import (
	"os/exec"
	"syscall"
)

// detachFromTerminal puts the probe shell in its own session with no
// controlling terminal. An interactive shell (`-i`) turns on job control and
// calls tcsetpgrp to take the terminal it is attached to; when the probe then
// exits, the launching terminal is left with a foreground process group that
// no longer exists, and Ctrl-C there signals nothing for the rest of the
// session. Without a controlling terminal the probe cannot reach it at all.
func detachFromTerminal(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}
