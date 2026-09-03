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

func TestSetScratchDirPersistsAndUpdatesInit(t *testing.T) {
	service, _ := workspaceTestService(t)
	service.ScratchDir = "/old/scratch"
	var saved string
	service.SaveScratchDir = func(dir string) (string, error) {
		saved = dir
		return "/resolved/scratch", nil
	}

	got, err := service.SetScratchDir("  ~/scratch  ")
	if err != nil {
		t.Fatal(err)
	}
	if saved != "~/scratch" || got != "/resolved/scratch" {
		t.Fatalf("saved = %q, returned = %q", saved, got)
	}
	if got := service.Init().ScratchDir; got != "/resolved/scratch" {
		t.Fatalf("Init scratch dir = %q", got)
	}
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

func TestWorkspaceFileManagerCreatesRenameCopiesAndDeletes(t *testing.T) {
	service, root := workspaceTestService(t)

	file, err := service.CreateWorkspaceFile("a.go", "package main\n")
	if err != nil {
		t.Fatalf("create file: %v", err)
	}
	if file.Path != "a.go" || file.Content != "package main\n" {
		t.Fatalf("created file = %#v", file)
	}
	if data, err := os.ReadFile(filepath.Join(root, "a.go")); err != nil || string(data) != "package main\n" {
		t.Fatalf("on-disk content = %q, err = %v", data, err)
	}
	if _, err := service.CreateWorkspaceFile("a.go", ""); err == nil {
		t.Fatal("creating an existing file should fail")
	}

	if err := service.CreateWorkspaceDir("sub"); err != nil {
		t.Fatalf("create dir: %v", err)
	}
	if _, err := service.CreateWorkspaceFile("sub/nested.go", "x"); err != nil {
		t.Fatalf("create file in dir: %v", err)
	}

	// Rename moves and re-parents.
	if err := service.RenameWorkspacePath("a.go", "sub/renamed.go"); err != nil {
		t.Fatalf("rename: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "a.go")); !os.IsNotExist(err) {
		t.Fatalf("old path still exists after rename: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "sub", "renamed.go")); err != nil {
		t.Fatalf("new path missing after rename: %v", err)
	}

	// Copy duplicates a file into a new name.
	if err := service.CopyWorkspacePath("sub/renamed.go", "copy.go"); err != nil {
		t.Fatalf("copy file: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "copy.go")); err != nil {
		t.Fatalf("copy missing: %v", err)
	}

	// Copy a directory tree.
	if err := service.CopyWorkspacePath("sub", "sub2"); err != nil {
		t.Fatalf("copy dir: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "sub2", "renamed.go")); err != nil {
		t.Fatalf("copied dir missing its file: %v", err)
	}

	// Delete removes a file; deleting a missing path is a no-op.
	if err := service.DeleteWorkspacePath("copy.go"); err != nil {
		t.Fatalf("delete file: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "copy.go")); !os.IsNotExist(err) {
		t.Fatalf("deleted path still exists: %v", err)
	}
	if err := service.DeleteWorkspacePath("nope.go"); err != nil {
		t.Fatalf("delete missing: %v", err)
	}
	if err := service.DeleteWorkspacePath("."); err == nil {
		t.Fatal("deleting the workspace root should fail")
	}
}

func TestScratchFilesRoundTrip(t *testing.T) {
	service, root := workspaceTestService(t)
	service.ScratchDir = t.TempDir()

	file, err := service.CreateScratchFile("idea.md", "hello\n")
	if err != nil {
		t.Fatalf("create scratch: %v", err)
	}
	if file.Path != "idea.md" {
		t.Fatalf("scratch path = %q", file.Path)
	}

	listed, err := service.ListScratch()
	if err != nil {
		t.Fatalf("list scratch: %v", err)
	}
	if len(listed) != 1 || listed[0].Name != "idea.md" {
		t.Fatalf("listed = %#v", listed)
	}

	read, err := service.ReadScratchFile("idea.md")
	if err != nil {
		t.Fatalf("read scratch: %v", err)
	}
	if read.Content != "hello\n" {
		t.Fatalf("scratch content = %q", read.Content)
	}

	saved, err := service.WriteScratchFile("idea.md", "hi\n", file.Version)
	if err != nil {
		t.Fatalf("write scratch: %v", err)
	}
	if saved.Content != "hi\n" {
		t.Fatalf("saved scratch = %q", saved.Content)
	}

	if err := service.RenameScratchFile("idea.md", "renamed.md"); err != nil {
		t.Fatalf("rename scratch: %v", err)
	}
	if _, err := service.ReadScratchFile("idea.md"); err == nil {
		t.Fatal("old scratch name still readable after rename")
	}
	if err := service.CopyScratchFile("renamed.md", "renamed-copy.md"); err != nil {
		t.Fatalf("copy scratch: %v", err)
	}
	if _, err := service.ReadScratchFile("renamed-copy.md"); err != nil {
		t.Fatalf("copied scratch missing: %v", err)
	}
	if err := service.DeleteScratchFile("renamed.md"); err != nil {
		t.Fatalf("delete scratch: %v", err)
	}

	// Path traversal in a name is rejected, never written outside the root.
	if _, err := service.CreateScratchFile("../escape.md", ""); err == nil {
		t.Fatal("traversing scratch name should be rejected")
	}
	if _, err := os.Stat(filepath.Join(root, "escape.md")); !os.IsNotExist(err) {
		t.Fatalf("traversing scratch wrote outside its root: %v", err)
	}
}
