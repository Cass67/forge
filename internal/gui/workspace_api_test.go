package gui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func workspaceTestService(t *testing.T) (*Service, string) {
	t.Helper()
	root := t.TempDir()
	return serviceAt(root), root
}

func TestTerminalEnvironmentSetsXtermCapabilities(t *testing.T) {
	got := terminalEnvironment([]string{"PATH=/bin", "TERM=dumb", "COLORTERM=old"})
	want := []string{"PATH=/bin", "TERM=xterm-256color", "COLORTERM=truecolor"}
	if len(got) != len(want) {
		t.Fatalf("terminal environment = %q, want %q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("terminal environment = %q, want %q", got, want)
		}
	}
}

func TestStartTerminalReattachesExistingSession(t *testing.T) {
	service, root := workspaceTestService(t)
	t.Cleanup(service.closeTerminals)

	if _, err := service.StartTerminal("terminal-1", 24, 80); err != nil {
		t.Fatal(err)
	}
	dir, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	key := service.terminalKey(dir, "terminal-1")
	service.mu.RLock()
	session := service.terminals[key]
	service.mu.RUnlock()
	if session == nil {
		t.Fatal("terminal session was not registered")
	}
	session.mu.Lock()
	session.buffer = append(session.buffer, []byte("preserved output")...)
	session.mu.Unlock()

	output, err := service.StartTerminal("terminal-1", 24, 80)
	if err != nil {
		t.Fatalf("reattach terminal: %v", err)
	}
	if !strings.Contains(output, "preserved output") {
		t.Fatalf("reattach output = %q", output)
	}
}

func TestWorkspacePathRejectsEscapeAndExternalSymlink(t *testing.T) {
	root := t.TempDir()
	if _, err := workspacePath(root, "../outside"); err == nil {
		t.Fatal("workspacePath accepted parent traversal")
	}

	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "external")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := workspacePath(root, "external"); err == nil {
		t.Fatal("workspacePath accepted a symlink outside the workspace")
	}
}

func TestWorkspaceReadWriteUsesOptimisticVersion(t *testing.T) {
	service, root := workspaceTestService(t)
	path := filepath.Join(root, "note.txt")
	if err := os.WriteFile(path, []byte("first\n"), 0o640); err != nil {
		t.Fatal(err)
	}

	file, err := service.ReadWorkspaceFile("note.txt")
	if err != nil {
		t.Fatal(err)
	}
	if file.Content != "first\n" || file.Version == "" {
		t.Fatalf("unexpected file response: %#v", file)
	}

	if err := os.WriteFile(path, []byte("external\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	if _, err := service.WriteWorkspaceFile("note.txt", "editor\n", file.Version); err == nil {
		t.Fatal("write succeeded with a stale version")
	}

	current, err := service.ReadWorkspaceFile("note.txt")
	if err != nil {
		t.Fatal(err)
	}
	saved, err := service.WriteWorkspaceFile("note.txt", "editor\n", current.Version)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Content != "editor\n" || saved.Version == current.Version {
		t.Fatalf("unexpected saved response: %#v", saved)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o640 {
		t.Fatalf("file mode changed to %v", info.Mode().Perm())
	}
}

func TestReadWorkspaceFileRejectsBinary(t *testing.T) {
	service, root := workspaceTestService(t)
	for name, content := range map[string][]byte{
		"nul.dat":  {'a', 0, 'b'},
		"utf8.dat": {0xff, 0xfe},
	} {
		if err := os.WriteFile(filepath.Join(root, name), content, 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := service.ReadWorkspaceFile(name); err == nil {
			t.Fatalf("binary file %q was accepted", name)
		}
	}
}

func TestListWorkspaceDirIgnoresDirectoryRemovedAfterTreeScan(t *testing.T) {
	service, root := workspaceTestService(t)
	cache := filepath.Join(root, ".mypy_cache")
	if err := os.Mkdir(cache, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(cache); err != nil {
		t.Fatal(err)
	}

	entries, err := service.ListWorkspaceDir(".mypy_cache")
	if err != nil {
		t.Fatalf("listing removed directory: %v", err)
	}
	if entries != nil {
		t.Fatalf("entries = %#v, want nil", entries)
	}
}
