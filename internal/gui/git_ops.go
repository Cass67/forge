package gui

// Staging, committing, branching and history. Paths arriving from the
// frontend are workspace-relative and validated through workspacePath before
// they reach git, so a crafted "../" cannot stage outside the workspace.

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const gitLogPageSize = 50

var errNoPaths = errors.New("no paths given")

// relPaths validates each incoming path against the workspace root. Untracked
// or deleted files do not exist on disk (or no longer do), so the check is
// lexical rather than workspacePath's symlink-resolving form.
func (s *Service) relPaths(paths []string) (string, []string, error) {
	root, err := s.workspaceRoot()
	if err != nil {
		return "", nil, err
	}
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		clean := filepath.Clean(strings.TrimSpace(p))
		if clean == "" || clean == "." {
			continue
		}
		if filepath.IsAbs(clean) {
			rel, err := filepath.Rel(root, clean)
			if err != nil {
				return "", nil, errors.New("path outside the workspace")
			}
			clean = rel
		}
		if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
			return "", nil, errors.New("path escapes the workspace")
		}
		out = append(out, clean)
	}
	if len(out) == 0 {
		return "", nil, errNoPaths
	}
	return root, out, nil
}

// ---- diffs ---------------------------------------------------------------

// GitDiff returns the unified diff for one path. staged selects the index
// against HEAD; otherwise the working tree against the index. Untracked files
// have no diff of their own, so they are rendered against /dev/null.
func (s *Service) GitDiff(path string, staged bool) (string, error) {
	root, paths, err := s.relPaths([]string{path})
	if err != nil {
		return "", err
	}
	rel := paths[0]
	args := []string{"diff", "--no-color", "--no-ext-diff", "--find-renames"}
	if staged {
		args = append(args, "--cached")
	}
	args = append(args, "--", rel)
	out, err := runGit(root, args...)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(out) == "" && !staged {
		return untrackedDiff(root, rel)
	}
	return out, nil
}

// untrackedDiff synthesises an all-additions diff for a file git does not
// track yet, so a new file reads the same as any other change in the viewer.
// `git diff --no-index` would also do it, but it exits non-zero whenever it
// finds differences, which is every call here.
func untrackedDiff(root, rel string) (string, error) {
	data, err := os.ReadFile(filepath.Join(root, rel))
	if err != nil {
		return "", err
	}
	if isBinary(data) {
		return fmt.Sprintf("diff --git a/%s b/%s\nBinary file\n", rel, rel), nil
	}
	lines := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	var b strings.Builder
	fmt.Fprintf(&b, "diff --git a/%s b/%s\n--- /dev/null\n+++ b/%s\n@@ -0,0 +1,%d @@\n", rel, rel, rel, len(lines))
	for _, l := range lines {
		b.WriteString("+" + l + "\n")
	}
	return b.String(), nil
}

func isBinary(data []byte) bool {
	limit := min(len(data), 8000)
	for i := 0; i < limit; i++ {
		if data[i] == 0 {
			return true
		}
	}
	return false
}

// GitDiffScope returns the whole diff for a review scope: "worktree",
// "staged", "all" (worktree + index), or "branch" (everything on this branch
// since it forked from base).
func (s *Service) GitDiffScope(scope, base string) (string, error) {
	root, err := s.workspaceRoot()
	if err != nil {
		return "", err
	}
	common := []string{"diff", "--no-color", "--no-ext-diff", "--find-renames"}
	switch scope {
	case "staged":
		return runGit(root, append(common, "--cached")...)
	case "worktree":
		return runGit(root, common...)
	case "branch":
		ref, err := mergeBase(root, base)
		if err != nil {
			return "", err
		}
		return runGit(root, append(common, ref)...)
	default: // "all"
		return runGit(root, append(common, "HEAD")...)
	}
}

// mergeBase resolves where the current branch forked from base. An explicit
// base wins; otherwise the repo's default branch is guessed from origin/HEAD
// and then from the usual names.
func mergeBase(root, base string) (string, error) {
	if strings.TrimSpace(base) == "" {
		base = defaultBranch(root)
	}
	out, err := runGit(root, "merge-base", "HEAD", base)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

func defaultBranch(root string) string {
	if out, err := runGit(root, "symbolic-ref", "--quiet", "--short", "refs/remotes/origin/HEAD"); err == nil {
		if name := strings.TrimSpace(out); name != "" {
			return name
		}
	}
	for _, name := range []string{"main", "master", "develop"} {
		if _, err := runGit(root, "rev-parse", "--verify", "--quiet", name); err == nil {
			return name
		}
	}
	return "HEAD~1"
}

// GitDefaultBranch is what the walkthrough and integrate dialogs prefill.
func (s *Service) GitDefaultBranch() (string, error) {
	root, err := s.workspaceRoot()
	if err != nil {
		return "", err
	}
	return defaultBranch(root), nil
}

// ---- staging -------------------------------------------------------------

func (s *Service) GitStage(paths []string) (GitStatusResult, error) {
	if _, err := s.mutableRoot(); err != nil {
		return GitStatusResult{}, err
	}
	root, rel, err := s.relPaths(paths)
	if err != nil {
		return GitStatusResult{}, err
	}
	if _, err := runGit(root, append([]string{"add", "--"}, rel...)...); err != nil {
		return GitStatusResult{}, err
	}
	return s.GitStatus()
}

func (s *Service) GitUnstage(paths []string) (GitStatusResult, error) {
	if _, err := s.mutableRoot(); err != nil {
		return GitStatusResult{}, err
	}
	root, rel, err := s.relPaths(paths)
	if err != nil {
		return GitStatusResult{}, err
	}
	// restore --staged fails on a repo with no commits yet; rm --cached is the
	// equivalent there.
	args := append([]string{"restore", "--staged", "--"}, rel...)
	if !hasHEAD(root) {
		args = append([]string{"rm", "--cached", "-r", "--"}, rel...)
	}
	if _, err := runGit(root, args...); err != nil {
		return GitStatusResult{}, err
	}
	return s.GitStatus()
}

// GitDiscard throws away working-tree changes. Untracked files are deleted,
// which is why this is the one git call the GUI confirms before sending.
func (s *Service) GitDiscard(paths []string) (GitStatusResult, error) {
	if _, err := s.mutableRoot(); err != nil {
		return GitStatusResult{}, err
	}
	root, rel, err := s.relPaths(paths)
	if err != nil {
		return GitStatusResult{}, err
	}
	status, err := s.GitStatus()
	if err != nil {
		return GitStatusResult{}, err
	}
	untracked := map[string]bool{}
	for _, f := range status.Files {
		if f.Untracked {
			untracked[f.Path] = true
		}
	}
	var tracked, fresh []string
	for _, p := range rel {
		if untracked[p] {
			fresh = append(fresh, p)
		} else {
			tracked = append(tracked, p)
		}
	}
	if len(tracked) > 0 {
		if _, err := runGit(root, append([]string{"restore", "--worktree", "--"}, tracked...)...); err != nil {
			return GitStatusResult{}, err
		}
	}
	if len(fresh) > 0 {
		if _, err := runGit(root, append([]string{"clean", "-fdq", "--"}, fresh...)...); err != nil {
			return GitStatusResult{}, err
		}
	}
	return s.GitStatus()
}

func hasHEAD(root string) bool {
	_, err := runGit(root, "rev-parse", "--verify", "--quiet", "HEAD")
	return err == nil
}

// ---- commit --------------------------------------------------------------

// GitCommit commits the index. With amend it rewrites the previous commit;
// an empty message then keeps the existing one.
func (s *Service) GitCommit(message string, amend bool) (GitStatusResult, error) {
	if _, err := s.mutableRoot(); err != nil {
		return GitStatusResult{}, err
	}
	root, err := s.workspaceRoot()
	if err != nil {
		return GitStatusResult{}, err
	}
	message = strings.TrimSpace(message)
	args := []string{"commit"}
	if amend {
		args = append(args, "--amend")
		if message == "" {
			args = append(args, "--no-edit")
		}
	}
	if message != "" {
		args = append(args, "-m", message)
	} else if !amend {
		return GitStatusResult{}, errors.New("commit message is empty")
	}
	if _, err := runGit(root, args...); err != nil {
		return GitStatusResult{}, err
	}
	return s.GitStatus()
}

// ---- branches ------------------------------------------------------------

func (s *Service) GitBranches(includeRemote bool) ([]GitBranch, error) {
	root, err := s.workspaceRoot()
	if err != nil {
		return nil, err
	}
	const format = "%(refname:short)%09%(HEAD)%09%(upstream:short)%09%(upstream:track)%09%(contents:subject)%09%(committerdate:relative)"
	// Local and remote refs are read in separate passes: the ref namespace is
	// the only reliable way to tell "origin/main" from a local branch that
	// happens to be named with a slash.
	namespaces := []struct {
		ref    string
		remote bool
	}{{"refs/heads", false}}
	if includeRemote {
		namespaces = append(namespaces, struct {
			ref    string
			remote bool
		}{"refs/remotes", true})
	}
	branches := []GitBranch{}
	for _, ns := range namespaces {
		out, err := runGit(root, "for-each-ref", "--sort=-committerdate", "--format="+format, ns.ref)
		if err != nil {
			if errors.Is(err, errNotRepo) {
				return []GitBranch{}, nil
			}
			return nil, err
		}
		for _, line := range strings.Split(out, "\n") {
			if strings.TrimSpace(line) == "" {
				continue
			}
			cols := strings.Split(line, "\t")
			if len(cols) < 6 || strings.HasSuffix(cols[0], "/HEAD") {
				continue
			}
			ahead, behind := parseTrack(cols[3])
			branches = append(branches, GitBranch{
				Name:     cols[0],
				Current:  cols[1] == "*",
				Remote:   ns.remote,
				Upstream: cols[2],
				Ahead:    ahead,
				Behind:   behind,
				Subject:  cols[4],
				When:     cols[5],
			})
		}
	}
	return branches, nil
}

// parseTrack reads for-each-ref's "[ahead 2, behind 1]" tracking field.
func parseTrack(s string) (int, int) {
	var ahead, behind int
	s = strings.Trim(s, "[]")
	for _, part := range strings.Split(s, ",") {
		fields := strings.Fields(part)
		if len(fields) != 2 {
			continue
		}
		n, err := strconv.Atoi(fields[1])
		if err != nil {
			continue
		}
		switch fields[0] {
		case "ahead":
			ahead = n
		case "behind":
			behind = n
		}
	}
	return ahead, behind
}

func (s *Service) GitCheckout(name string) (GitStatusResult, error) {
	if _, err := s.mutableRoot(); err != nil {
		return GitStatusResult{}, err
	}
	if strings.TrimSpace(name) == "" {
		return GitStatusResult{}, errors.New("branch name is empty")
	}
	if _, err := s.git("checkout", name); err != nil {
		return GitStatusResult{}, err
	}
	return s.GitStatus()
}

func (s *Service) GitCreateBranch(name, base string, checkout bool) (GitStatusResult, error) {
	if _, err := s.mutableRoot(); err != nil {
		return GitStatusResult{}, err
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return GitStatusResult{}, errors.New("branch name is empty")
	}
	args := []string{"branch", name}
	if checkout {
		args = []string{"checkout", "-b", name}
	}
	if base = strings.TrimSpace(base); base != "" {
		args = append(args, base)
	}
	if _, err := s.git(args...); err != nil {
		return GitStatusResult{}, err
	}
	return s.GitStatus()
}

func (s *Service) GitRenameBranch(from, to string) (GitStatusResult, error) {
	if _, err := s.mutableRoot(); err != nil {
		return GitStatusResult{}, err
	}
	if strings.TrimSpace(to) == "" {
		return GitStatusResult{}, errors.New("branch name is empty")
	}
	if _, err := s.git("branch", "-m", from, to); err != nil {
		return GitStatusResult{}, err
	}
	return s.GitStatus()
}

func (s *Service) GitDeleteBranch(name string, force bool) (GitStatusResult, error) {
	if _, err := s.mutableRoot(); err != nil {
		return GitStatusResult{}, err
	}
	flag := "-d"
	if force {
		flag = "-D"
	}
	if _, err := s.git("branch", flag, name); err != nil {
		return GitStatusResult{}, err
	}
	return s.GitStatus()
}

// ---- remote --------------------------------------------------------------

func (s *Service) GitFetch() (GitStatusResult, error) {
	if _, err := s.mutableRoot(); err != nil {
		return GitStatusResult{}, err
	}
	if _, err := s.git("fetch", "--prune"); err != nil {
		return GitStatusResult{}, err
	}
	return s.GitStatus()
}

func (s *Service) GitPull(rebase bool) (GitStatusResult, error) {
	if _, err := s.mutableRoot(); err != nil {
		return GitStatusResult{}, err
	}
	args := []string{"pull"}
	if rebase {
		args = append(args, "--rebase")
	}
	if _, err := s.git(args...); err != nil {
		return GitStatusResult{}, err
	}
	return s.GitStatus()
}

// GitPush publishes the current branch, setting an upstream the first time so
// a freshly created branch does not need a separate command.
func (s *Service) GitPush(force bool) (GitStatusResult, error) {
	if _, err := s.mutableRoot(); err != nil {
		return GitStatusResult{}, err
	}
	root, err := s.workspaceRoot()
	if err != nil {
		return GitStatusResult{}, err
	}
	status, err := s.GitStatus()
	if err != nil {
		return GitStatusResult{}, err
	}
	args := []string{"push"}
	if force {
		args = append(args, "--force-with-lease")
	}
	if status.Upstream == "" && status.Branch != "" && !status.Detached {
		args = append(args, "--set-upstream", "origin", status.Branch)
	}
	if _, err := runGit(root, args...); err != nil {
		return GitStatusResult{}, err
	}
	return s.GitStatus()
}

// ---- stash ---------------------------------------------------------------

func (s *Service) GitStash(message string, includeUntracked bool) (GitStatusResult, error) {
	if _, err := s.mutableRoot(); err != nil {
		return GitStatusResult{}, err
	}
	args := []string{"stash", "push"}
	if includeUntracked {
		args = append(args, "--include-untracked")
	}
	if m := strings.TrimSpace(message); m != "" {
		args = append(args, "-m", m)
	}
	if _, err := s.git(args...); err != nil {
		return GitStatusResult{}, err
	}
	return s.GitStatus()
}

func (s *Service) GitStashList() ([]GitStash, error) {
	out, err := s.git("stash", "list", "--format=%gd%x09%gs")
	if err != nil {
		if errors.Is(err, errNotRepo) {
			return []GitStash{}, nil
		}
		return nil, err
	}
	stashes := []GitStash{}
	for i, line := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		if line == "" {
			continue
		}
		cols := strings.SplitN(line, "\t", 2)
		st := GitStash{Index: i, Ref: cols[0]}
		if len(cols) > 1 {
			st.Subject = cols[1]
		}
		stashes = append(stashes, st)
	}
	return stashes, nil
}

// GitStashApply restores a stash. drop removes it from the list (pop).
func (s *Service) GitStashApply(index int, drop bool) (GitStatusResult, error) {
	if _, err := s.mutableRoot(); err != nil {
		return GitStatusResult{}, err
	}
	verb := "apply"
	if drop {
		verb = "pop"
	}
	if _, err := s.git("stash", verb, fmt.Sprintf("stash@{%d}", index)); err != nil {
		return GitStatusResult{}, err
	}
	return s.GitStatus()
}

func (s *Service) GitStashDrop(index int) ([]GitStash, error) {
	if _, err := s.mutableRoot(); err != nil {
		return nil, err
	}
	if _, err := s.git("stash", "drop", fmt.Sprintf("stash@{%d}", index)); err != nil {
		return nil, err
	}
	return s.GitStashList()
}

// ---- history -------------------------------------------------------------

// GitLog returns one page of history. Records are NUL-delimited so subjects
// and bodies containing newlines survive intact.
func (s *Service) GitLog(limit, skip int, ref string) ([]GitCommit, error) {
	root, err := s.workspaceRoot()
	if err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = gitLogPageSize
	}
	const format = "--format=%H%x1f%h%x1f%an%x1f%ar%x1f%s%x1f%b%x1f%D%x00"
	args := []string{"log", format, "--max-count=" + strconv.Itoa(limit)}
	if skip > 0 {
		args = append(args, "--skip="+strconv.Itoa(skip))
	}
	if ref = strings.TrimSpace(ref); ref != "" {
		args = append(args, ref)
	}
	out, err := runGit(root, args...)
	if err != nil {
		if errors.Is(err, errNotRepo) {
			return []GitCommit{}, nil
		}
		return nil, err
	}
	commits := []GitCommit{}
	for _, rec := range gitLines(out) {
		cols := strings.Split(strings.TrimLeft(rec, "\n"), "\x1f")
		if len(cols) < 7 {
			continue
		}
		commits = append(commits, GitCommit{
			SHA: cols[0], Short: cols[1], Author: cols[2], When: cols[3],
			Subject: cols[4], Body: strings.TrimSpace(cols[5]), Refs: cols[6],
		})
	}
	return commits, nil
}

// GitCommitDiff returns what one commit changed. Merge commits get --cc so
// the result is a combined diff rather than nothing at all.
func (s *Service) GitCommitDiff(sha string) (string, error) {
	if strings.TrimSpace(sha) == "" {
		return "", errors.New("no commit given")
	}
	return s.git("show", "--no-color", "--no-ext-diff", "--find-renames", "--format=", "--cc", sha)
}

// ---- conflicts -----------------------------------------------------------

// GitResolve marks a conflicted path resolved, optionally taking one side
// wholesale first. side is "ours", "theirs", or "" to keep the merged file.
func (s *Service) GitResolve(path, side string) (GitStatusResult, error) {
	if _, err := s.mutableRoot(); err != nil {
		return GitStatusResult{}, err
	}
	root, rel, err := s.relPaths([]string{path})
	if err != nil {
		return GitStatusResult{}, err
	}
	switch side {
	case "ours", "theirs":
		if _, err := runGit(root, "checkout", "--"+side, "--", rel[0]); err != nil {
			return GitStatusResult{}, err
		}
	case "":
	default:
		return GitStatusResult{}, fmt.Errorf("unknown side %q", side)
	}
	if _, err := runGit(root, "add", "--", rel[0]); err != nil {
		return GitStatusResult{}, err
	}
	return s.GitStatus()
}

// GitContinue finishes the interrupted operation named by GitStatus.State;
// GitAbort backs it out.
func (s *Service) GitContinue(state string) (GitStatusResult, error) {
	if _, err := s.mutableRoot(); err != nil {
		return GitStatusResult{}, err
	}
	verb, ok := stateVerb(state)
	if !ok {
		return GitStatusResult{}, fmt.Errorf("nothing to continue")
	}
	if _, err := s.git(verb, "--continue"); err != nil {
		return GitStatusResult{}, err
	}
	return s.GitStatus()
}

func (s *Service) GitAbort(state string) (GitStatusResult, error) {
	if _, err := s.mutableRoot(); err != nil {
		return GitStatusResult{}, err
	}
	verb, ok := stateVerb(state)
	if !ok {
		return GitStatusResult{}, fmt.Errorf("nothing to abort")
	}
	if _, err := s.git(verb, "--abort"); err != nil {
		return GitStatusResult{}, err
	}
	return s.GitStatus()
}

func stateVerb(state string) (string, bool) {
	switch state {
	case "rebase", "merge", "cherry-pick", "revert":
		return state, true
	default:
		return "", false
	}
}
