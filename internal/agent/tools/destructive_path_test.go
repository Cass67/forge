package tools

import (
	"os"
	"path/filepath"
	"testing"
)

func TestBlockedCommandPathTargets(t *testing.T) {
	home := t.TempDir()
	work := filepath.Join(home, "projects", "app")

	blocked := []string{
		"rm -rf /",
		"rm -rf /*",
		"rm -fr /",
		"rm -rf ~",
		"rm -rf $HOME",
		"rm -rf ${HOME}",
		"rm -rf /etc",
		"rm -rf /usr/lib",
		"rm -rf /System",
		"rm -rf /Users",
		"sudo rm -rf /",
		"echo hi && rm -rf /",
		"rm -r --force /",
		"mkfs.ext4 /dev/sda1",
		"dd if=/dev/zero of=/dev/sda",
	}
	for _, cmd := range blocked {
		if got := blockedCommand(cmd, work, home); got == "" {
			t.Errorf("blockedCommand(%q) = \"\", want it refused", cmd)
		}
	}

	// Ordinary destructive work inside the workspace must run untouched.
	allowed := []string{
		"rm -rf bobsdevdir",
		"rm -rf ./build",
		"rm -rf node_modules",
		"rm -rf dist/ coverage/",
		"rm -f main.o",
		"rm -rf " + filepath.Join(work, "tmp"),
		"rm -rf /tmp/scratch-build",
		"git clean -fdx",
		"echo rm -rf /",
		"grep -rn 'rm -rf /' docs/",
	}
	for _, cmd := range allowed {
		if got := blockedCommand(cmd, work, home); got != "" {
			t.Errorf("blockedCommand(%q) = %q, want it allowed", cmd, got)
		}
	}
}

// The approval hook and the force-prompt hook are the same function, so an
// auto-approving (--yolo) session must not be able to wave a catastrophic
// delete through. The refusal happens before approval is consulted.
func TestRunCommandRefusesCatastrophicDeleteEvenWhenAutoApproved(t *testing.T) {
	work := t.TempDir()
	autoApprove := func(Action) (bool, error) { return true, nil }
	tool := NewRunCommand(work, 30, nil, autoApprove)

	if _, err := tool.Execute(t.Context(), map[string]any{"command": "rm -rf /"}); err == nil {
		t.Fatal("rm -rf / was allowed under an auto-approving session")
	}

	// The same session must still be able to delete inside its workspace.
	victim := filepath.Join(work, "bobsdevdir")
	if err := os.MkdirAll(victim, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := tool.Execute(t.Context(), map[string]any{"command": "rm -rf bobsdevdir"}); err != nil {
		t.Fatalf("rm -rf bobsdevdir was refused: %v", err)
	}
	if _, err := os.Stat(victim); !os.IsNotExist(err) {
		t.Fatalf("bobsdevdir still present: %v", err)
	}
}
