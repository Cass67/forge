package tools

import (
	"os/exec"
	"path/filepath"
	"strings"
)

func annotateGitPathState(workDir, displayPath string) string {
	if strings.TrimSpace(displayPath) == "" || strings.Contains(displayPath, " [") {
		return displayPath
	}
	if gitPathIsUntracked(workDir, displayPath) {
		return displayPath + " [untracked]"
	}
	return displayPath
}

func gitPathIsUntracked(workDir, path string) bool {
	workDir = strings.TrimSpace(workDir)
	path = strings.TrimSpace(path)
	if workDir == "" || path == "" || filepath.IsAbs(path) {
		return false
	}
	cmd := exec.Command("git", "-C", workDir, "ls-files", "--others", "--exclude-standard", "--", path)
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if strings.TrimSpace(line) == path {
			return true
		}
	}
	return false
}
