package workspace

import (
	"context"
	"errors"
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
