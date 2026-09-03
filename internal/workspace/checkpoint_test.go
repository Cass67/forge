package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCheckpointRestoreReturnsTrackedFileToOriginalContent(t *testing.T) {
	ctx := context.Background()
	root := initTestGitRepo(t)
	writeFile(t, filepath.Join(root, "tracked.txt"), "original\n")
	runGit(t, root, "add", "tracked.txt")
	runGit(t, root, "commit", "-m", "initial")

	manager := NewCheckpointManager(root)
	checkpoint, err := manager.Create(ctx, "turn-1")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	writeFile(t, filepath.Join(root, "tracked.txt"), "mutated\n")
	if err := manager.RecordChangedFiles(ctx, checkpoint.ID, []string{"tracked.txt"}); err != nil {
		t.Fatalf("RecordChangedFiles() error = %v", err)
	}
	if err := manager.Restore(ctx, checkpoint.ID); err != nil {
		t.Fatalf("Restore() error = %v", err)
	}

	got := readFile(t, filepath.Join(root, "tracked.txt"))
	if got != "original\n" {
		t.Fatalf("tracked.txt = %q, want original", got)
	}
}

func TestCheckpointCreateOutsideGitRepoReturnsUnsupported(t *testing.T) {
	ctx := context.Background()
	manager := NewCheckpointManager(t.TempDir())

	_, err := manager.Create(ctx, "turn-1")
	if !errors.Is(err, ErrUnsupportedCheckpoint) {
		t.Fatalf("Create() error = %v, want ErrUnsupportedCheckpoint", err)
	}
	if err == nil || !strings.Contains(err.Error(), "git repository") {
		t.Fatalf("Create() error = %v, want clear git repository message", err)
	}
}

func TestCheckpointRestorePreservesDirtyChangesPresentAtCheckpoint(t *testing.T) {
	ctx := context.Background()
	root := initTestGitRepo(t)
	writeFile(t, filepath.Join(root, "target.txt"), "target original\n")
	writeFile(t, filepath.Join(root, "notes.txt"), "notes original\n")
	runGit(t, root, "add", "target.txt", "notes.txt")
	runGit(t, root, "commit", "-m", "initial")

	writeFile(t, filepath.Join(root, "notes.txt"), "user dirty notes\n")
	manager := NewCheckpointManager(root)
	checkpoint, err := manager.Create(ctx, "turn-1")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	writeFile(t, filepath.Join(root, "target.txt"), "tool mutation\n")
	if err := manager.RecordChangedFiles(ctx, checkpoint.ID, []string{"target.txt"}); err != nil {
		t.Fatalf("RecordChangedFiles() error = %v", err)
	}
	if err := manager.Restore(ctx, checkpoint.ID); err != nil {
		t.Fatalf("Restore() error = %v", err)
	}

	if got := readFile(t, filepath.Join(root, "target.txt")); got != "target original\n" {
		t.Fatalf("target.txt = %q, want target original", got)
	}
	if got := readFile(t, filepath.Join(root, "notes.txt")); got != "user dirty notes\n" {
		t.Fatalf("notes.txt = %q, want pre-checkpoint dirty content", got)
	}
}

func TestCheckpointRestoreOnlyRestoresRecordedScope(t *testing.T) {
	ctx := context.Background()
	root := initTestGitRepo(t)
	writeFile(t, filepath.Join(root, "a.txt"), "a original\n")
	writeFile(t, filepath.Join(root, "b.txt"), "b original\n")
	runGit(t, root, "add", "a.txt", "b.txt")
	runGit(t, root, "commit", "-m", "initial")

	manager := NewCheckpointManager(root)
	checkpoint, err := manager.Create(ctx, "turn-1")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	writeFile(t, filepath.Join(root, "a.txt"), "turn mutation\n")
	writeFile(t, filepath.Join(root, "b.txt"), "unrelated user mutation\n")
	if err := manager.RecordChangedFiles(ctx, checkpoint.ID, []string{"a.txt"}); err != nil {
		t.Fatalf("RecordChangedFiles() error = %v", err)
	}
	if err := manager.Restore(ctx, checkpoint.ID); err != nil {
		t.Fatalf("Restore() error = %v", err)
	}

	if got := readFile(t, filepath.Join(root, "a.txt")); got != "a original\n" {
		t.Fatalf("a.txt = %q, want checkpoint content", got)
	}
	if got := readFile(t, filepath.Join(root, "b.txt")); got != "unrelated user mutation\n" {
		t.Fatalf("b.txt = %q, want unrelated post-checkpoint content", got)
	}
}

func TestCheckpointRestoreWithoutScopeRejectsAmbiguousChanges(t *testing.T) {
	ctx := context.Background()
	root := initTestGitRepo(t)
	writeFile(t, filepath.Join(root, "a.txt"), "a original\n")
	writeFile(t, filepath.Join(root, "b.txt"), "b original\n")
	runGit(t, root, "add", "a.txt", "b.txt")
	runGit(t, root, "commit", "-m", "initial")

	manager := NewCheckpointManager(root)
	checkpoint, err := manager.Create(ctx, "turn-1")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	writeFile(t, filepath.Join(root, "a.txt"), "turn mutation\n")
	writeFile(t, filepath.Join(root, "b.txt"), "unrelated user mutation\n")
	err = manager.Restore(ctx, checkpoint.ID)
	if err == nil || !strings.Contains(err.Error(), "scope required") {
		t.Fatalf("Restore() error = %v, want scope required", err)
	}
	if got := readFile(t, filepath.Join(root, "a.txt")); got != "turn mutation\n" {
		t.Fatalf("a.txt = %q, want preserved after failed restore", got)
	}
	if got := readFile(t, filepath.Join(root, "b.txt")); got != "unrelated user mutation\n" {
		t.Fatalf("b.txt = %q, want preserved after failed restore", got)
	}
}

func TestCheckpointRestoreWithoutScopeRejectsSingleChange(t *testing.T) {
	ctx := context.Background()
	root := initTestGitRepo(t)
	writeFile(t, filepath.Join(root, "tracked.txt"), "original\n")
	runGit(t, root, "add", "tracked.txt")
	runGit(t, root, "commit", "-m", "initial")

	manager := NewCheckpointManager(root)
	checkpoint, err := manager.Create(ctx, "turn-1")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	writeFile(t, filepath.Join(root, "tracked.txt"), "user mutation\n")
	err = manager.Restore(ctx, checkpoint.ID)
	if !errors.Is(err, ErrCheckpointScopeRequired) {
		t.Fatalf("Restore() error = %v, want ErrCheckpointScopeRequired", err)
	}
	if got := readFile(t, filepath.Join(root, "tracked.txt")); got != "user mutation\n" {
		t.Fatalf("tracked.txt = %q, want preserved after failed restore", got)
	}
}

func TestCheckpointInvalidIDIsRejected(t *testing.T) {
	ctx := context.Background()
	root := initTestGitRepo(t)
	writeFile(t, filepath.Join(root, "tracked.txt"), "original\n")
	runGit(t, root, "add", "tracked.txt")
	runGit(t, root, "commit", "-m", "initial")

	manager := NewCheckpointManager(root)
	if _, err := manager.Create(ctx, "turn-1"); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	for _, id := range []string{"../turn-1", "bad/id", `bad\\id`} {
		err := manager.Restore(ctx, id)
		if err == nil || !strings.Contains(err.Error(), "invalid checkpoint ID") {
			t.Fatalf("Restore(%q) error = %v, want invalid checkpoint ID", id, err)
		}
	}
}

func initTestGitRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	runGit(t, root, "init")
	runGit(t, root, "config", "user.email", "test@example.com")
	runGit(t, root, "config", "user.name", "Test User")
	return root
}

func runGit(t *testing.T, root string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s failed: %v\n%s", strings.Join(args, " "), err, out)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}

func checkpointStore(t *testing.T, root string) string {
	t.Helper()
	dir, err := checkpointStoreDir(context.Background(), root)
	if err != nil {
		t.Fatalf("checkpointStoreDir() error = %v", err)
	}
	return dir
}

func countFiles(t *testing.T, dir, suffix string) int {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return 0
		}
		t.Fatalf("ReadDir(%s) error = %v", dir, err)
	}
	n := 0
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), suffix) {
			n++
		}
	}
	return n
}

// TestCheckpointPatchesAreDeduplicated pins the disk-growth fix: repeated
// checkpoints of an unchanged tree must share one patch file rather than
// writing a fresh copy of the whole working-tree diff per turn.
func TestCheckpointPatchesAreDeduplicated(t *testing.T) {
	ctx := context.Background()
	root := initTestGitRepo(t)
	writeFile(t, filepath.Join(root, "tracked.txt"), "original\n")
	runGit(t, root, "add", "tracked.txt")
	runGit(t, root, "commit", "-m", "initial")
	writeFile(t, filepath.Join(root, "tracked.txt"), "mutated\n")

	manager := NewCheckpointManager(root)
	first, err := manager.Create(ctx, "turn-1")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	second, err := manager.Create(ctx, "turn-2")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	if first.PatchHash == "" || first.PatchHash != second.PatchHash {
		t.Fatalf("identical trees should share a patch hash: %q vs %q", first.PatchHash, second.PatchHash)
	}
	patchDir := filepath.Join(checkpointStore(t, root), "patches")
	if n := countFiles(t, patchDir, ".patch"); n != 1 {
		t.Fatalf("patch files = %d, want 1 shared copy", n)
	}

	// A changed tree must still get its own patch.
	writeFile(t, filepath.Join(root, "tracked.txt"), "changed again\n")
	third, err := manager.Create(ctx, "turn-3")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if third.PatchHash == first.PatchHash {
		t.Fatal("different trees must not share a patch")
	}
	if n := countFiles(t, patchDir, ".patch"); n != 2 {
		t.Fatalf("patch files = %d, want 2", n)
	}
}

// TestCheckpointRetentionCapsStore verifies old checkpoints are dropped and
// their now-unreferenced patches garbage collected.
func TestCheckpointRetentionCapsStore(t *testing.T) {
	ctx := context.Background()
	root := initTestGitRepo(t)
	writeFile(t, filepath.Join(root, "tracked.txt"), "original\n")
	runGit(t, root, "add", "tracked.txt")
	runGit(t, root, "commit", "-m", "initial")

	manager := NewCheckpointManager(root)
	storeDir := checkpointStore(t, root)

	// Each checkpoint gets a distinct tree, so none of them dedupe away.
	total := maxCheckpoints + 25
	for i := range total {
		writeFile(t, filepath.Join(root, "tracked.txt"), fmt.Sprintf("content %d\n", i))
		if _, err := manager.Create(ctx, fmt.Sprintf("turn-%d", i)); err != nil {
			t.Fatalf("Create() error = %v", err)
		}
	}

	if n := countFiles(t, storeDir, ".json"); n > maxCheckpoints {
		t.Fatalf("metadata files = %d, want <= %d", n, maxCheckpoints)
	}
	patchDir := filepath.Join(storeDir, "patches")
	if n := countFiles(t, patchDir, ".patch"); n > maxCheckpoints {
		t.Fatalf("patch files = %d, want <= %d (orphans not collected)", n, maxCheckpoints)
	}
}

// TestCheckpointRestoreReadsLegacyPatchLayout keeps stores written before
// content-addressed patches restorable.
func TestCheckpointRestoreReadsLegacyPatchLayout(t *testing.T) {
	ctx := context.Background()
	root := initTestGitRepo(t)
	writeFile(t, filepath.Join(root, "tracked.txt"), "original\n")
	runGit(t, root, "add", "tracked.txt")
	runGit(t, root, "commit", "-m", "initial")

	manager := NewCheckpointManager(root)
	writeFile(t, filepath.Join(root, "tracked.txt"), "mutated\n")
	checkpoint, err := manager.Create(ctx, "turn-1")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if err := manager.RecordChangedFiles(ctx, checkpoint.ID, []string{"tracked.txt"}); err != nil {
		t.Fatalf("RecordChangedFiles() error = %v", err)
	}

	// Rewrite the store in the pre-dedupe shape: <id>.patch, no patch_hash.
	storeDir := checkpointStore(t, root)
	shared := filepath.Join(storeDir, "patches", checkpoint.PatchHash+".patch")
	patch, err := os.ReadFile(shared)
	if err != nil {
		t.Fatalf("read shared patch: %v", err)
	}
	if err := os.WriteFile(filepath.Join(storeDir, checkpoint.ID+".patch"), patch, 0o600); err != nil {
		t.Fatalf("write legacy patch: %v", err)
	}
	if err := os.RemoveAll(filepath.Join(storeDir, "patches")); err != nil {
		t.Fatalf("remove patch dir: %v", err)
	}
	legacy, metaPath, err := manager.loadCheckpoint(ctx, root, checkpoint.ID)
	if err != nil {
		t.Fatalf("loadCheckpoint() error = %v", err)
	}
	legacy.PatchHash = ""
	meta, err := json.Marshal(legacy)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(metaPath, meta, 0o600); err != nil {
		t.Fatalf("write legacy metadata: %v", err)
	}

	writeFile(t, filepath.Join(root, "tracked.txt"), "mutated again\n")
	if err := manager.Restore(ctx, checkpoint.ID); err != nil {
		t.Fatalf("Restore() with legacy layout error = %v", err)
	}
}
