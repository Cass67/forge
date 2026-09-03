//go:build windows

package tools

import (
	"os"
	"os/exec"
)

func setProcGroup(_ *exec.Cmd) {}

func killProcGroup(pid int) {
	if p, err := os.FindProcess(pid); err == nil {
		_ = p.Kill()
	}
}
