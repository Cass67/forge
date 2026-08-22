//go:build windows

package shellenv

import "os/exec"

// Windows has no controlling terminal to lose.
func detachFromTerminal(cmd *exec.Cmd) {}
