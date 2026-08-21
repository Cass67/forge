package gui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// gitRepo builds a throwaway repository with one commit, one staged edit, one
// unstaged edit and one untracked file, which is every case GitStatus groups.
func gitRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	// macOS /var is a symlink to /private/var; the service resolves the
	// workspace root, so the fixture must too or every path compare fails.
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		root = resolved
	}
	for _, args := range [][]string{
		{"init", "-b", "main"},
		{"config", "user.email", "test@example.com"},
		{"config", "user.name", "Test"},
	} {
		if _, err := runGit(root, args...); err != nil {
			t.Fatalf("git %v: %v", args, err)
		}
	}
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("kept.txt", "one\ntwo\nthree\n")
	write("edited.txt", "alpha\n")
	if _, err := runGit(root, "add", "."); err != nil {
		t.Fatal(err)
	}
	if _, err := runGit(root, "commit", "-m", "initial"); err != nil {
		t.Fatal(err)
	}
	write("edited.txt", "alpha\nbeta\n") // staged
	if _, err := runGit(root, "add", "edited.txt"); err != nil {
		t.Fatal(err)
	}
	write("kept.txt", "one\ntwo\nthree\nfour\n") // unstaged
	write("fresh.txt", "brand new\n")            // untracked
	return root
}

func statusFor(t *testing.T, root string) GitStatusResult {
	t.Helper()
	svc := &Service{ready: true}
	svc.cfg.WorkDir = root
	got, err := svc.GitStatus()
	if err != nil {
		t.Fatalf("GitStatus: %v", err)
	}
	return got
}

func TestGitStatusGroupsChanges(t *testing.T) {
	root := gitRepo(t)
	got := statusFor(t, root)

	if !got.Repository || got.Branch != "main" {
		t.Fatalf("repository=%v branch=%q, want true/main", got.Repository, got.Branch)
	}
	byPath := map[string]GitFileStatus{}
	for _, f := range got.Files {
		byPath[f.Path] = f
	}
	if len(byPath) != 3 {
		t.Fatalf("got %d files (%v), want 3", len(byPath), byPath)
	}
	if f := byPath["edited.txt"]; !f.Staged || f.Unstaged {
		t.Errorf("edited.txt staged=%v unstaged=%v, want true/false", f.Staged, f.Unstaged)
	}
	if f := byPath["kept.txt"]; f.Staged || !f.Unstaged {
		t.Errorf("kept.txt staged=%v unstaged=%v, want false/true", f.Staged, f.Unstaged)
	}
	if f := byPath["fresh.txt"]; !f.Untracked {
		t.Errorf("fresh.txt untracked=%v, want true", f.Untracked)
	}
	// One line added to each tracked file, counted across index and worktree.
	if f := byPath["edited.txt"]; f.Adds != 1 || f.Dels != 0 {
		t.Errorf("edited.txt +%d-%d, want +1-0", f.Adds, f.Dels)
	}
	if f := byPath["kept.txt"]; f.Adds != 1 || f.Dels != 0 {
		t.Errorf("kept.txt +%d-%d, want +1-0", f.Adds, f.Dels)
	}
}

func TestGitStatusOutsideRepoIsNotAnError(t *testing.T) {
	root := t.TempDir()
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		root = resolved
	}
	got := statusFor(t, root)
	if got.Repository || len(got.Files) != 0 {
		t.Fatalf("got %+v, want an empty non-repository result", got)
	}
}

func TestGitDiffCoversStagedAndUntracked(t *testing.T) {
	root := gitRepo(t)
	svc := &Service{ready: true}
	svc.cfg.WorkDir = root

	staged, err := svc.GitDiff("edited.txt", true)
	if err != nil {
		t.Fatalf("GitDiff staged: %v", err)
	}
	if !strings.Contains(staged, "+beta") {
		t.Errorf("staged diff missing the staged line:\n%s", staged)
	}
	// An untracked file has no diff of its own; it must still render as one.
	fresh, err := svc.GitDiff("fresh.txt", false)
	if err != nil {
		t.Fatalf("GitDiff untracked: %v", err)
	}
	if !strings.Contains(fresh, "+brand new") || !strings.Contains(fresh, "--- /dev/null") {
		t.Errorf("untracked diff not synthesised:\n%s", fresh)
	}
}

func TestGitStageUnstageDiscardRoundTrip(t *testing.T) {
	root := gitRepo(t)
	svc := &Service{ready: true}
	svc.cfg.WorkDir = root

	after, err := svc.GitStage([]string{"kept.txt"})
	if err != nil {
		t.Fatalf("GitStage: %v", err)
	}
	if !fileIn(after, "kept.txt").Staged {
		t.Error("kept.txt did not become staged")
	}
	if after, err = svc.GitUnstage([]string{"kept.txt"}); err != nil {
		t.Fatalf("GitUnstage: %v", err)
	}
	if fileIn(after, "kept.txt").Staged {
		t.Error("kept.txt is still staged after unstage")
	}
	// Discard drops the working-tree edit and deletes the untracked file.
	if after, err = svc.GitDiscard([]string{"kept.txt", "fresh.txt"}); err != nil {
		t.Fatalf("GitDiscard: %v", err)
	}
	if fileIn(after, "kept.txt").Path != "" || fileIn(after, "fresh.txt").Path != "" {
		t.Errorf("discard left changes behind: %+v", after.Files)
	}
	if _, err := os.Stat(filepath.Join(root, "fresh.txt")); !os.IsNotExist(err) {
		t.Error("untracked file survived discard")
	}
}

func TestRelPathsRejectsEscapes(t *testing.T) {
	root := gitRepo(t)
	svc := &Service{ready: true}
	svc.cfg.WorkDir = root
	if _, _, err := svc.relPaths([]string{"../outside"}); err == nil {
		t.Error("relPaths accepted a path escaping the workspace")
	}
}

func TestWorktreeAddListRemove(t *testing.T) {
	root := gitRepo(t)
	svc := &Service{ready: true}
	svc.cfg.WorkDir = root

	tree, err := svc.GitAddWorktree("feature/spike", "", "main", true)
	if err != nil {
		t.Fatalf("GitAddWorktree: %v", err)
	}
	if tree.Branch != "feature/spike" {
		t.Fatalf("branch %q, want feature/spike", tree.Branch)
	}
	// The slash in the branch must not become a nested directory.
	if strings.Contains(filepath.Base(tree.Path), "/") {
		t.Errorf("worktree path %q kept the branch separator", tree.Path)
	}
	trees, err := svc.GitWorktrees()
	if err != nil {
		t.Fatalf("GitWorktrees: %v", err)
	}
	if len(trees) != 2 || !trees[0].Main || !trees[0].Current {
		t.Fatalf("got %+v, want the main worktree first and current", trees)
	}
	if _, err := svc.GitRemoveWorktree(root, false, false); err == nil {
		t.Error("removing the current worktree was allowed")
	}
	trees, err = svc.GitRemoveWorktree(tree.Path, false, true)
	if err != nil {
		t.Fatalf("GitRemoveWorktree: %v", err)
	}
	if len(trees) != 1 {
		t.Errorf("got %d worktrees after removal, want 1", len(trees))
	}
}

func TestParseWalkthroughToleratesFencedJSON(t *testing.T) {
	raw := "Here you go:\n```json\n" +
		`{"summary":"s","stops":[{"title":"t","tag":"KEY ","files":["a.go"],"explanation":"e"},` +
		`{"title":"u","tag":"nonsense","files":null,"explanation":"e2"}]}` + "\n```"
	walk, err := parseWalkthrough(raw)
	if err != nil {
		t.Fatalf("parseWalkthrough: %v", err)
	}
	if walk.Stops[0].Tag != "key" {
		t.Errorf("tag %q, want key", walk.Stops[0].Tag)
	}
	if walk.Stops[1].Tag != "" {
		t.Errorf("tag %q, want it dropped", walk.Stops[1].Tag)
	}
	if walk.Stops[1].Files == nil {
		t.Error("nil Files must normalise to an empty slice for the frontend")
	}
}

func TestUncoveredFilesFindsWhatTheModelMissed(t *testing.T) {
	diff := "diff --git a/a.go b/a.go\n@@\n+x\n" +
		"diff --git a/b.go b/b.go\n@@\n+y\n" +
		"diff --git a/c.go b/c.go\n@@\n+z\n"
	stops := []WalkStop{{Files: []string{"a.go"}}, {Files: []string{"c.go"}}}
	got := uncoveredFiles(diff, stops)
	if len(got) != 1 || got[0] != "b.go" {
		t.Fatalf("got %v, want [b.go]", got)
	}
}

func fileIn(status GitStatusResult, path string) GitFileStatus {
	for _, f := range status.Files {
		if f.Path == path {
			return f
		}
	}
	return GitFileStatus{}
}

// A worktree's .git is a file pointing at the real git directory, so an
// interrupted merge there is only visible if the git dir is resolved.
func TestRepoStateSeesAMergeInsideAWorktree(t *testing.T) {
	root := gitRepo(t)
	svc := &Service{ready: true}
	svc.cfg.WorkDir = root
	tree, err := svc.GitAddWorktree("side", "", "main", true)
	if err != nil {
		t.Fatalf("GitAddWorktree: %v", err)
	}
	// Fabricate an in-progress merge in the worktree's own git directory.
	out, err := runGit(tree.Path, "rev-parse", "--absolute-git-dir")
	if err != nil {
		t.Fatalf("rev-parse: %v", err)
	}
	gitDir := strings.TrimSpace(out)
	head, err := runGit(tree.Path, "rev-parse", "HEAD")
	if err != nil {
		t.Fatalf("rev-parse HEAD: %v", err)
	}
	if err := os.WriteFile(filepath.Join(gitDir, "MERGE_HEAD"), []byte(head), 0o644); err != nil {
		t.Fatal(err)
	}
	if state := repoState(tree.Path); state != "merge" {
		t.Fatalf("state %q, want merge", state)
	}
}
