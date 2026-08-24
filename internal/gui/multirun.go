package gui

// Multi-run: launch the same prompt against several models at once. Forge
// binds one chat runtime to one window, so each run is its own forge-gui
// process rather than a tab — optionally in its own worktree so the runs
// cannot overwrite each other's edits.

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

const maxRuns = 5

// RunSpec is the multi-run form's submission.
type RunSpec struct {
	Group   string   `json:"group"`
	Prompt  string   `json:"prompt"`
	Models  []string `json:"models"`
	Isolate bool     `json:"isolate"`
	Base    string   `json:"base,omitempty"`
	Yolo    bool     `json:"yolo,omitempty"`
}

// RunLaunch reports one run's outcome. A run that fails to start does not stop
// the others: partial results are more useful than none.
type RunLaunch struct {
	Model    string `json:"model"`
	Dir      string `json:"dir"`
	Branch   string `json:"branch,omitempty"`
	Started  bool   `json:"started"`
	Error    string `json:"error,omitempty"`
	Worktree bool   `json:"worktree"`
}

var slugUnsafe = regexp.MustCompile(`[^a-zA-Z0-9._-]+`)

func slug(s string) string {
	s = slugUnsafe.ReplaceAllString(strings.TrimSpace(s), "-")
	s = strings.Trim(strings.ToLower(s), "-")
	if len(s) > 40 {
		s = strings.Trim(s[:40], "-")
	}
	return s
}

// StartRuns launches one window per model and returns what happened to each.
func (s *Service) StartRuns(spec RunSpec) ([]RunLaunch, error) {
	if _, err := s.mutableRoot(); err != nil {
		return nil, err
	}
	root, err := s.workspaceRoot()
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(spec.Prompt) == "" {
		return nil, errors.New("the prompt is empty")
	}
	models := dedupe(spec.Models)
	if len(models) == 0 {
		return nil, errors.New("pick at least one model")
	}
	if len(models) > maxRuns {
		return nil, fmt.Errorf("at most %d runs at once", maxRuns)
	}
	self, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("cannot find the forge binary: %w", err)
	}
	if spec.Isolate {
		if status, err := s.GitStatus(); err != nil || !status.Repository {
			return nil, errors.New("isolated runs need a git repository")
		}
	}
	group := slug(spec.Group)
	if group == "" {
		group = "run-" + time.Now().Format("0102-1504")
	}

	launches := make([]RunLaunch, 0, len(models))
	for i, model := range models {
		launch := RunLaunch{Model: model, Dir: root, Worktree: spec.Isolate}
		if spec.Isolate {
			branch := fmt.Sprintf("%s/%d-%s", group, i+1, slug(model))
			tree, err := s.GitAddWorktree(branch, "", spec.Base, true)
			if err != nil {
				launch.Error = err.Error()
				launches = append(launches, launch)
				continue
			}
			launch.Dir, launch.Branch = tree.Path, tree.Branch
		}
		if err := spawnRun(self, launch.Dir, model, spec.Prompt, spec.Yolo); err != nil {
			launch.Error = err.Error()
		} else {
			launch.Started = true
		}
		launches = append(launches, launch)
	}
	return launches, nil
}

func spawnRun(binary, dir, model, prompt string, yolo bool) error {
	args := []string{"-C", dir, "-model", model, "-prompt", prompt}
	if yolo {
		args = append(args, "-yolo")
	}
	cmd := exec.Command(binary, args...)
	cmd.Dir = dir
	// Detached: the new window outlives whichever window launched it.
	cmd.Stdin, cmd.Stdout, cmd.Stderr = nil, nil, nil
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }()
	return nil
}

func dedupe(in []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, v := range in {
		v = strings.TrimSpace(v)
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	return out
}
