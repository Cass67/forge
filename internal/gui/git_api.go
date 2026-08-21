package gui

// Git surface for the GUI. Everything here shells out to the `git` binary in
// the workspace root: forge already requires git on PATH, and porcelain output
// is a stabler contract than any Go reimplementation of the index.

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

var errNotRepo = errors.New("not a git repository")

// GitFileStatus is one changed path. Index and Work are the raw porcelain
// codes; the booleans are what the panel actually groups on.
type GitFileStatus struct {
	Path      string `json:"path"`
	Status    string `json:"status"`
	Orig      string `json:"orig,omitempty"`
	Index     string `json:"index"`
	Work      string `json:"work"`
	Staged    bool   `json:"staged"`
	Unstaged  bool   `json:"unstaged"`
	Untracked bool   `json:"untracked"`
	Conflict  bool   `json:"conflict"`
	Adds      int    `json:"adds"`
	Dels      int    `json:"dels"`
}

type GitStatusResult struct {
	Repository bool            `json:"repository"`
	Branch     string          `json:"branch"`
	Upstream   string          `json:"upstream,omitempty"`
	Ahead      int             `json:"ahead"`
	Behind     int             `json:"behind"`
	Detached   bool            `json:"detached"`
	Files      []GitFileStatus `json:"files"`
	Root       string          `json:"root,omitempty"`
	State      string          `json:"state,omitempty"`
}

type GitBranch struct {
	Name     string `json:"name"`
	Current  bool   `json:"current"`
	Remote   bool   `json:"remote"`
	Upstream string `json:"upstream,omitempty"`
	Ahead    int    `json:"ahead"`
	Behind   int    `json:"behind"`
	Subject  string `json:"subject,omitempty"`
	When     string `json:"when,omitempty"`
}

type GitCommit struct {
	SHA     string `json:"sha"`
	Short   string `json:"short"`
	Author  string `json:"author"`
	When    string `json:"when"`
	Subject string `json:"subject"`
	Body    string `json:"body,omitempty"`
	Refs    string `json:"refs,omitempty"`
}

type GitStash struct {
	Index   int    `json:"index"`
	Ref     string `json:"ref"`
	Subject string `json:"subject"`
}

// ---- command plumbing ----------------------------------------------------

func (s *Service) git(args ...string) (string, error) {
	root, err := s.workspaceRoot()
	if err != nil {
		return "", err
	}
	return runGit(root, args...)
}

func runGit(root string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = root
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = strings.TrimSpace(stdout.String())
		}
		if strings.Contains(strings.ToLower(msg), "not a git repository") {
			return "", errNotRepo
		}
		if msg == "" {
			return "", err
		}
		return "", fmt.Errorf("git %s: %s", args[0], msg)
	}
	return stdout.String(), nil
}

// gitLines splits NUL-delimited porcelain output.
func gitLines(out string) []string {
	parts := strings.Split(out, "\x00")
	kept := parts[:0]
	for _, p := range parts {
		if p != "" {
			kept = append(kept, p)
		}
	}
	return kept
}

// ---- status --------------------------------------------------------------

func (s *Service) GitStatus() (GitStatusResult, error) {
	root, err := s.workspaceRoot()
	if err != nil {
		return GitStatusResult{}, err
	}
	out, err := runGit(root, "status", "--porcelain=v2", "-z", "--branch", "--untracked-files=all")
	if err != nil {
		if errors.Is(err, errNotRepo) {
			return GitStatusResult{Files: []GitFileStatus{}}, nil
		}
		return GitStatusResult{}, err
	}
	result := GitStatusResult{Repository: true, Files: []GitFileStatus{}}
	if top, err := runGit(root, "rev-parse", "--show-toplevel"); err == nil {
		result.Root = strings.TrimSpace(top)
	}
	result.State = repoState(root)

	fields := strings.Split(out, "\x00")
	for i := 0; i < len(fields); i++ {
		line := fields[i]
		if line == "" {
			continue
		}
		switch {
		case strings.HasPrefix(line, "# branch.head "):
			head := strings.TrimPrefix(line, "# branch.head ")
			if head == "(detached)" {
				result.Detached = true
			}
			result.Branch = head
		case strings.HasPrefix(line, "# branch.upstream "):
			result.Upstream = strings.TrimPrefix(line, "# branch.upstream ")
		case strings.HasPrefix(line, "# branch.ab "):
			ahead, behind := parseAheadBehind(strings.TrimPrefix(line, "# branch.ab "))
			result.Ahead, result.Behind = ahead, behind
		case strings.HasPrefix(line, "1 "), strings.HasPrefix(line, "2 "):
			f, extra := parseChangedEntry(line)
			if f.Path == "" {
				continue
			}
			// A rename entry ("2 ") is followed by its origin path.
			if extra && i+1 < len(fields) {
				i++
				f.Orig = fields[i]
			}
			result.Files = append(result.Files, f)
		case strings.HasPrefix(line, "u "):
			// Unmerged: "u <XY> <sub> <m1> <m2> <m3> <mW> <h1> <h2> <h3> <path>"
			cols := strings.SplitN(line, " ", 11)
			if len(cols) < 11 {
				continue
			}
			xy := cols[1]
			result.Files = append(result.Files, GitFileStatus{
				Path: cols[10], Status: xy, Index: xy[:1], Work: xy[1:2],
				Unstaged: true, Conflict: true,
			})
		case strings.HasPrefix(line, "? "):
			result.Files = append(result.Files, GitFileStatus{
				Path: strings.TrimPrefix(line, "? "), Status: "??",
				Index: "?", Work: "?", Unstaged: true, Untracked: true,
			})
		}
	}
	applyNumstat(root, result.Files)
	return result, nil
}

// parseChangedEntry reads a porcelain-v2 "1"/"2" record. The second return
// reports whether an origin path follows in the NUL stream (renames only).
func parseChangedEntry(line string) (GitFileStatus, bool) {
	rename := strings.HasPrefix(line, "2 ")
	// "1 <XY> <sub> <mH> <mI> <mW> <hH> <hI> <path>"
	// "2 <XY> <sub> <mH> <mI> <mW> <hH> <hI> <X><score> <path>"
	want := 9
	if rename {
		want = 10
	}
	cols := strings.SplitN(line, " ", want)
	if len(cols) < want {
		return GitFileStatus{}, false
	}
	xy := cols[1]
	if len(xy) < 2 {
		return GitFileStatus{}, false
	}
	f := GitFileStatus{
		Path:     cols[want-1],
		Status:   xy,
		Index:    xy[:1],
		Work:     xy[1:2],
		Staged:   xy[0] != '.',
		Unstaged: xy[1] != '.',
	}
	return f, rename
}

func parseAheadBehind(s string) (int, int) {
	var ahead, behind int
	for _, tok := range strings.Fields(s) {
		n, err := strconv.Atoi(strings.TrimLeft(tok, "+-"))
		if err != nil {
			continue
		}
		if strings.HasPrefix(tok, "+") {
			ahead = n
		} else if strings.HasPrefix(tok, "-") {
			behind = n
		}
	}
	return ahead, behind
}

// repoState names an interrupted operation so the panel can offer the right
// continue/abort action instead of a bare conflict list. The git directory is
// asked for rather than assumed: inside a worktree, .git is a file pointing
// elsewhere, so probing <root>/.git/MERGE_HEAD would never find anything.
func repoState(root string) string {
	if root == "" {
		return ""
	}
	out, err := runGit(root, "rev-parse", "--absolute-git-dir")
	if err != nil {
		return ""
	}
	gitDir := strings.TrimSpace(out)
	for _, probe := range []struct{ path, state string }{
		{"rebase-merge", "rebase"},
		{"rebase-apply", "rebase"},
		{"MERGE_HEAD", "merge"},
		{"CHERRY_PICK_HEAD", "cherry-pick"},
		{"REVERT_HEAD", "revert"},
		{"BISECT_LOG", "bisect"},
	} {
		if _, err := os.Stat(filepath.Join(gitDir, probe.path)); err == nil {
			return probe.state
		}
	}
	return ""
}

// applyNumstat fills per-file line counts in one pass rather than one diff per
// row: a large working tree otherwise costs hundreds of git invocations.
func applyNumstat(root string, files []GitFileStatus) {
	counts := map[string][2]int{}
	for _, args := range [][]string{
		{"diff", "--numstat", "-z"},
		{"diff", "--numstat", "-z", "--cached"},
	} {
		out, err := runGit(root, args...)
		if err != nil {
			continue
		}
		mergeNumstat(out, counts)
	}
	for i := range files {
		if c, ok := counts[files[i].Path]; ok {
			files[i].Adds, files[i].Dels = c[0], c[1]
		}
	}
}

// mergeNumstat parses `git diff --numstat -z`. Ordinary records are
// "adds\tdels\tpath\0"; renames put the two paths in their own NUL fields.
func mergeNumstat(out string, into map[string][2]int) {
	fields := strings.Split(out, "\x00")
	for i := 0; i < len(fields); i++ {
		rec := fields[i]
		if rec == "" {
			continue
		}
		cols := strings.Split(rec, "\t")
		if len(cols) < 2 {
			continue
		}
		adds, _ := strconv.Atoi(cols[0])
		dels, _ := strconv.Atoi(cols[1])
		path := ""
		if len(cols) >= 3 && cols[2] != "" {
			path = cols[2]
		} else if i+2 < len(fields) {
			// rename: <old>\0<new>\0
			path = fields[i+2]
			i += 2
		}
		if path == "" {
			continue
		}
		prev := into[path]
		into[path] = [2]int{prev[0] + adds, prev[1] + dels}
	}
}
