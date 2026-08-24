package gui

// Worktree sessions: each one is a separate checkout of the repository on its
// own branch, so several agents can work in parallel without fighting over
// one working tree. Integrate merges a worktree's branch back.

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type GitWorktree struct {
	Path     string `json:"path"`
	Name     string `json:"name"`
	Branch   string `json:"branch,omitempty"`
	Head     string `json:"head,omitempty"`
	Detached bool   `json:"detached"`
	Bare     bool   `json:"bare"`
	Locked   bool   `json:"locked"`
	Prunable bool   `json:"prunable"`
	Missing  bool   `json:"missing"`
	Current  bool   `json:"current"`
	Main     bool   `json:"main"`
	Ahead    int    `json:"ahead"`
	Behind   int    `json:"behind"`
	Dirty    bool   `json:"dirty"`
}

// IntegrateResult reports what a merge did. Conflicts is non-empty when the
// merge stopped and needs a human — or the agent — to finish it.
type IntegrateResult struct {
	Merged    bool     `json:"merged"`
	Into      string   `json:"into"`
	From      string   `json:"from"`
	Conflicts []string `json:"conflicts"`
	Message   string   `json:"message"`
}

// GitWorktrees lists every checkout of this repository, newest git first.
func (s *Service) GitWorktrees() ([]GitWorktree, error) {
	root, err := s.workspaceRoot()
	if err != nil {
		return nil, err
	}
	out, err := runGit(root, "worktree", "list", "--porcelain")
	if err != nil {
		if errors.Is(err, errNotRepo) {
			return []GitWorktree{}, nil
		}
		return nil, err
	}
	trees := []GitWorktree{}
	var cur *GitWorktree
	flush := func() {
		if cur != nil {
			trees = append(trees, *cur)
			cur = nil
		}
	}
	for _, line := range strings.Split(out, "\n") {
		switch {
		case strings.HasPrefix(line, "worktree "):
			flush()
			path := strings.TrimPrefix(line, "worktree ")
			cur = &GitWorktree{Path: path, Name: filepath.Base(path)}
		case cur == nil:
			continue
		case strings.HasPrefix(line, "HEAD "):
			cur.Head = strings.TrimPrefix(line, "HEAD ")
		case strings.HasPrefix(line, "branch "):
			cur.Branch = strings.TrimPrefix(strings.TrimPrefix(line, "branch "), "refs/heads/")
		case line == "detached":
			cur.Detached = true
		case line == "bare":
			cur.Bare = true
		case strings.HasPrefix(line, "locked"):
			cur.Locked = true
		case strings.HasPrefix(line, "prunable"):
			cur.Prunable = true
		}
	}
	flush()

	base := defaultBranch(root)
	for i := range trees {
		t := &trees[i]
		t.Main = i == 0
		if resolved, err := filepath.EvalSymlinks(t.Path); err == nil {
			t.Current = resolved == root
		}
		if _, err := os.Stat(t.Path); err != nil {
			t.Missing = true
			continue
		}
		if t.Branch != "" && t.Branch != base {
			t.Ahead, t.Behind = countRange(root, base, t.Branch)
		}
		if out, err := runGit(t.Path, "status", "--porcelain", "--untracked-files=no"); err == nil {
			t.Dirty = strings.TrimSpace(out) != ""
		}
	}
	return trees, nil
}

// countRange reports how far branch is ahead of and behind base.
func countRange(root, base, branch string) (int, int) {
	out, err := runGit(root, "rev-list", "--left-right", "--count", base+"..."+branch)
	if err != nil {
		return 0, 0
	}
	fields := strings.Fields(out)
	if len(fields) != 2 {
		return 0, 0
	}
	behind, ahead := atoi(fields[0]), atoi(fields[1])
	return ahead, behind
}

func atoi(s string) int {
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return n
		}
		n = n*10 + int(r-'0')
	}
	return n
}

// GitAddWorktree creates a worktree. An empty path puts it next to the
// repository as "<repo>-<branch>", which keeps it out of the tree git is
// tracking. When newBranch is set, branch is created from base.
func (s *Service) GitAddWorktree(branch, path, base string, newBranch bool) (GitWorktree, error) {
	if _, err := s.mutableRoot(); err != nil {
		return GitWorktree{}, err
	}
	root, err := s.workspaceRoot()
	if err != nil {
		return GitWorktree{}, err
	}
	branch = strings.TrimSpace(branch)
	if branch == "" {
		return GitWorktree{}, errors.New("branch name is empty")
	}
	path = strings.TrimSpace(path)
	if path == "" {
		path = defaultWorktreePath(root, branch)
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(filepath.Dir(root), path)
	}
	if _, err := os.Stat(path); err == nil {
		return GitWorktree{}, fmt.Errorf("%s already exists", path)
	}
	args := []string{"worktree", "add"}
	if newBranch {
		args = append(args, "-b", branch, path)
		if base = strings.TrimSpace(base); base != "" {
			args = append(args, base)
		}
	} else {
		args = append(args, path, branch)
	}
	if _, err := runGit(root, args...); err != nil {
		return GitWorktree{}, err
	}
	trees, err := s.GitWorktrees()
	if err != nil {
		return GitWorktree{}, err
	}
	for _, t := range trees {
		if sameDir(t.Path, path) {
			return t, nil
		}
	}
	return GitWorktree{Path: path, Name: filepath.Base(path), Branch: branch}, nil
}

// defaultWorktreePath derives a sibling directory name, flattening the slashes
// a branch name may carry so "feat/x" does not become a nested directory.
func defaultWorktreePath(root, branch string) string {
	slug := strings.ReplaceAll(branch, string(filepath.Separator), "-")
	slug = strings.ReplaceAll(slug, "/", "-")
	return filepath.Join(filepath.Dir(root), filepath.Base(root)+"-"+slug)
}

func sameDir(a, b string) bool {
	ra, err := filepath.EvalSymlinks(a)
	if err != nil {
		ra = filepath.Clean(a)
	}
	rb, err := filepath.EvalSymlinks(b)
	if err != nil {
		rb = filepath.Clean(b)
	}
	return ra == rb
}

// GitRemoveWorktree detaches a worktree. The branch survives unless
// deleteBranch is set: nothing is deleted the caller did not ask for.
func (s *Service) GitRemoveWorktree(path string, force, deleteBranch bool) ([]GitWorktree, error) {
	if _, err := s.mutableRoot(); err != nil {
		return nil, err
	}
	root, err := s.workspaceRoot()
	if err != nil {
		return nil, err
	}
	if sameDir(path, root) {
		return nil, errors.New("cannot remove the worktree you are working in")
	}
	branch := ""
	if trees, err := s.GitWorktrees(); err == nil {
		for _, t := range trees {
			if sameDir(t.Path, path) {
				branch = t.Branch
				if t.Main {
					return nil, errors.New("cannot remove the main worktree")
				}
			}
		}
	}
	args := []string{"worktree", "remove"}
	if force {
		args = append(args, "--force")
	}
	if _, err := runGit(root, append(args, path)...); err != nil {
		return nil, err
	}
	if deleteBranch && branch != "" {
		flag := "-d"
		if force {
			flag = "-D"
		}
		if _, err := runGit(root, "branch", flag, branch); err != nil {
			// The worktree is already gone; report the branch failure without
			// pretending the whole call failed.
			trees, listErr := s.GitWorktrees()
			if listErr != nil {
				return nil, err
			}
			return trees, err
		}
	}
	return s.GitWorktrees()
}

// GitIntegrate merges from into the into branch, checking it out first when
// the workspace is sitting on something else. A conflicted merge is left in
// place so it can be resolved in the diff panel or handed to the agent.
func (s *Service) GitIntegrate(from, into string, squash bool) (IntegrateResult, error) {
	if _, err := s.mutableRoot(); err != nil {
		return IntegrateResult{}, err
	}
	root, err := s.workspaceRoot()
	if err != nil {
		return IntegrateResult{}, err
	}
	from, into = strings.TrimSpace(from), strings.TrimSpace(into)
	if from == "" {
		return IntegrateResult{}, errors.New("no source branch")
	}
	if into == "" {
		into = defaultBranch(root)
	}
	if from == into {
		return IntegrateResult{}, errors.New("source and target are the same branch")
	}
	status, err := s.GitStatus()
	if err != nil {
		return IntegrateResult{}, err
	}
	if len(status.Files) > 0 {
		return IntegrateResult{}, errors.New("commit or stash your changes before integrating")
	}
	if status.Branch != into {
		if _, err := runGit(root, "checkout", into); err != nil {
			return IntegrateResult{}, err
		}
	}
	result := IntegrateResult{Into: into, From: from}
	args := []string{"merge", "--no-edit"}
	if squash {
		args = append(args, "--squash")
	}
	if _, err := runGit(root, append(args, from)...); err != nil {
		result.Message = err.Error()
		result.Conflicts = conflictedPaths(root)
		if len(result.Conflicts) == 0 {
			return result, err
		}
		return result, nil
	}
	result.Merged = true
	result.Message = fmt.Sprintf("merged %s into %s", from, into)
	return result, nil
}

func conflictedPaths(root string) []string {
	out, err := runGit(root, "diff", "--name-only", "--diff-filter=U", "-z")
	if err != nil {
		return nil
	}
	return gitLines(out)
}
