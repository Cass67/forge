package react

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var (
	newRepoPhrasePattern          = regexp.MustCompile(`(?i)\b(?:new repo|new repository|create repo|create repository|make repo|make repository|set up repo|setup repo)\b`)
	workspaceRootAfterRepoPattern = regexp.MustCompile(`(?i)\b(?:in|at|under)\s+(\S+)`)
)

func deriveActiveWorkspaceRoot(text string) string {
	normalized := normalizeToolIntentText(text)
	if !inputSuggestsRepoSetupCommandWork(normalized) {
		return ""
	}
	repoLoc := newRepoPhrasePattern.FindStringIndex(text)
	if repoLoc == nil {
		return ""
	}
	match := workspaceRootAfterRepoPattern.FindStringSubmatch(text[repoLoc[1]:])
	if len(match) != 2 {
		return ""
	}
	root := trimWorkspaceRootToken(match[1])
	if root == "" || root == string(os.PathSeparator) || hasParentPathSegment(root) {
		return ""
	}
	if root != "~" && !strings.HasPrefix(root, "~/") && !filepath.IsAbs(root) {
		return ""
	}
	if root == "~" || strings.HasPrefix(root, "~/") {
		home, err := os.UserHomeDir()
		if err != nil || home == "" {
			return ""
		}
		if root == "~" {
			root = home
		} else {
			root = filepath.Join(home, root[2:])
		}
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return ""
	}
	abs = filepath.Clean(abs)
	if abs == string(os.PathSeparator) {
		return ""
	}
	return abs
}

func trimWorkspaceRootToken(root string) string {
	root = strings.TrimSpace(root)
	root = strings.Trim(root, "`'\"()[]{}<>")
	return strings.TrimRight(root, ".,;:!?")
}

func hasParentPathSegment(path string) bool {
	if path == ".." || strings.HasPrefix(path, "../") || strings.HasSuffix(path, "/..") || strings.Contains(path, "/../") {
		return true
	}
	return false
}
