package workspace

import (
	"os"
	"path/filepath"
	"testing"
)

func TestUsableRejectsTheFilesystemRoot(t *testing.T) {
	// A bundled app launched from Finder inherits "/" as its working
	// directory, which must never become the agent's workspace.
	if Usable("/") {
		t.Error(`Usable("/") = true, want false`)
	}
	if Usable("") || Usable(filepath.Join(t.TempDir(), "missing")) {
		t.Error("Usable accepted a path that is not a directory")
	}
	if !Usable(t.TempDir()) {
		t.Error("Usable rejected a writable temp directory")
	}
}

func TestRegistryRemembersPinsAndOrders(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	older, newer, pinned := t.TempDir(), t.TempDir(), t.TempDir()

	r := LoadRegistry()
	for _, dir := range []string{older, newer, pinned} {
		if err := r.Remember(dir); err != nil {
			t.Fatalf("Remember(%s): %v", dir, err)
		}
	}
	if err := r.SetPinned(pinned, true); err != nil {
		t.Fatalf("SetPinned: %v", err)
	}
	// Touching the oldest makes it most recent among the unpinned.
	if err := r.Remember(older); err != nil {
		t.Fatalf("Remember: %v", err)
	}

	list := r.List()
	if len(list) != 3 {
		t.Fatalf("List() = %d entries, want 3", len(list))
	}
	if list[0].Path != pinned || !list[0].Pinned {
		t.Errorf("pinned workspace should sort first, got %+v", list[0])
	}
	if list[1].Path != older {
		t.Errorf("most recent unpinned = %s, want %s", list[1].Path, older)
	}
	if got := r.MostRecent(); got != pinned {
		t.Errorf("MostRecent() = %s, want the pinned workspace", got)
	}

	// The list survives a reload, which is the point of the registry.
	if reloaded := LoadRegistry().List(); len(reloaded) != 3 || reloaded[0].Path != pinned {
		t.Fatalf("registry did not persist: %+v", reloaded)
	}

	if err := r.Forget(older); err != nil {
		t.Fatalf("Forget: %v", err)
	}
	for _, e := range LoadRegistry().List() {
		if e.Path == older {
			t.Fatal("Forget did not remove the workspace")
		}
	}
	if _, err := os.Stat(older); err != nil {
		t.Fatal("Forget deleted the directory itself")
	}
}

// Directories that have since been deleted must not linger in the list.
func TestRegistryDropsMissingDirectories(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	gone := filepath.Join(t.TempDir(), "gone")
	if err := os.Mkdir(gone, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	r := LoadRegistry()
	if err := r.Remember(gone); err != nil {
		t.Fatalf("Remember: %v", err)
	}
	if err := os.Remove(gone); err != nil {
		t.Fatalf("rm: %v", err)
	}
	if list := r.List(); len(list) != 0 {
		t.Fatalf("List() = %+v, want the missing directory dropped", list)
	}
}
