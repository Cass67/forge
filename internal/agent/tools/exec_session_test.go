//go:build !windows

package tools

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestExecSessionKillReapsGrandchildren pins the audit finding: killing only the
// `sh` process left its background children running. The child writes its PID to
// a file, so the test can signal-probe it directly after the session is stopped.
func TestExecSessionKillReapsGrandchildren(t *testing.T) {
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "child.pid")

	m := NewExecSessionManager()
	// Non-PTY path: this is the branch that sets its own process group.
	id, err := m.start(dir, fmt.Sprintf("sh -c 'echo $$ > %s; sleep 30' & wait", pidFile), 80, 24, false)
	if err != nil {
		t.Fatalf("start session: %v", err)
	}

	var childPID int
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		b, readErr := os.ReadFile(pidFile)
		if readErr == nil {
			if pid, convErr := strconv.Atoi(strings.TrimSpace(string(b))); convErr == nil && pid > 0 {
				childPID = pid
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	if childPID == 0 {
		t.Fatal("background child never reported its pid")
	}

	if err := syscall.Kill(childPID, 0); err != nil {
		t.Fatalf("child %d should be alive before stop: %v", childPID, err)
	}

	if _, err := m.Stop(id); err != nil {
		t.Fatalf("stop session: %v", err)
	}

	deadline = time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if syscall.Kill(childPID, 0) != nil {
			return // child is gone, which is the point of the test
		}
		time.Sleep(20 * time.Millisecond)
	}
	_ = syscall.Kill(childPID, syscall.SIGKILL) // don't leak it out of the test
	t.Fatalf("child %d survived session stop (orphaned)", childPID)
}
