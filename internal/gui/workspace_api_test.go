package gui

import (
	"os"
	"path/filepath"
	"testing"

	"forge/internal/tui"
)

func workspaceTestService(t *testing.T) (*Service, string) {
	t.Helper()
	root := t.TempDir()
	return &Service{cfg: tui.ChatLiveConfig{WorkDir: root}, ready: true}, root
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
