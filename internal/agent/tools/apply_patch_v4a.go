package tools

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// V4A is the "*** Begin Patch" envelope used by OpenAI-style apply_patch tools.
// Models emit it constantly for a tool named apply_patch, and git apply rejects
// it outright, so we parse and apply it ourselves.

type v4aOp int

const (
	v4aUpdate v4aOp = iota
	v4aAdd
	v4aDelete
)

type v4aChange struct {
	op       v4aOp
	path     string
	movePath string
	content  string
}

func isV4APatch(patch string) bool {
	for _, line := range strings.Split(patch, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		return strings.HasPrefix(line, "*** Begin Patch") ||
			strings.HasPrefix(line, "*** Update File:") ||
			strings.HasPrefix(line, "*** Add File:") ||
			strings.HasPrefix(line, "*** Delete File:")
	}
	return false
}

func parseV4A(patch, workDir string) ([]v4aChange, error) {
	lines := strings.Split(strings.ReplaceAll(patch, "\r\n", "\n"), "\n")
	var changes []v4aChange
	i := 0
	for ; i < len(lines); i++ {
		if strings.HasPrefix(strings.TrimSpace(lines[i]), "*** Begin Patch") {
			i++
			break
		}
		if strings.HasPrefix(lines[i], "*** ") {
			break
		}
	}
	for i < len(lines) {
		line := lines[i]
		switch {
		case strings.TrimSpace(line) == "":
			i++
		case strings.HasPrefix(line, "*** End Patch"):
			i = len(lines)
		case strings.HasPrefix(line, "*** Add File:"):
			path := strings.TrimSpace(strings.TrimPrefix(line, "*** Add File:"))
			i++
			var body []string
			for i < len(lines) && !strings.HasPrefix(lines[i], "*** ") {
				body = append(body, strings.TrimPrefix(lines[i], "+"))
				i++
			}
			content := strings.Join(body, "\n")
			if content != "" && !strings.HasSuffix(content, "\n") {
				content += "\n"
			}
			changes = append(changes, v4aChange{op: v4aAdd, path: path, content: content})
		case strings.HasPrefix(line, "*** Delete File:"):
			changes = append(changes, v4aChange{op: v4aDelete, path: strings.TrimSpace(strings.TrimPrefix(line, "*** Delete File:"))})
			i++
		case strings.HasPrefix(line, "*** Update File:"):
			path := strings.TrimSpace(strings.TrimPrefix(line, "*** Update File:"))
			i++
			movePath := ""
			if i < len(lines) && strings.HasPrefix(lines[i], "*** Move to:") {
				movePath = strings.TrimSpace(strings.TrimPrefix(lines[i], "*** Move to:"))
				i++
			}
			var body []string
			for i < len(lines) && !strings.HasPrefix(lines[i], "*** ") {
				body = append(body, lines[i])
				i++
			}
			resolved, err := ResolvePath(workDir, path)
			if err != nil {
				return nil, err
			}
			data, err := os.ReadFile(resolved)
			if err != nil {
				return nil, fmt.Errorf("cannot update %s: %v", path, err)
			}
			updated, err := applyV4AHunks(string(data), body)
			if err != nil {
				return nil, fmt.Errorf("%s: %v", path, err)
			}
			changes = append(changes, v4aChange{op: v4aUpdate, path: path, movePath: movePath, content: updated})
		default:
			return nil, fmt.Errorf("unexpected line outside a file section: %q", line)
		}
	}
	if len(changes) == 0 {
		return nil, fmt.Errorf("patch contains no file sections")
	}
	return changes, nil
}

// applyV4AHunks replays context/-/+ hunks against content by locating each
// hunk's context block, since V4A carries no line numbers.
func applyV4AHunks(content string, body []string) (string, error) {
	lines := strings.Split(content, "\n")
	cursor := 0
	var hunk []string
	flush := func() error {
		if len(hunk) == 0 {
			return nil
		}
		var oldBlock, newBlock []string
		for _, l := range hunk {
			switch {
			case strings.HasPrefix(l, "-"):
				oldBlock = append(oldBlock, l[1:])
			case strings.HasPrefix(l, "+"):
				newBlock = append(newBlock, l[1:])
			case strings.HasPrefix(l, " "):
				oldBlock = append(oldBlock, l[1:])
				newBlock = append(newBlock, l[1:])
			default:
				oldBlock = append(oldBlock, l)
				newBlock = append(newBlock, l)
			}
		}
		hunk = nil
		if len(oldBlock) == 0 {
			return fmt.Errorf("hunk has no context or removed lines to anchor on")
		}
		idx := indexLines(lines, oldBlock, cursor)
		if idx < 0 {
			return fmt.Errorf("context not found in file:\n%s", strings.Join(oldBlock, "\n"))
		}
		lines = append(lines[:idx], append(append([]string{}, newBlock...), lines[idx+len(oldBlock):]...)...)
		cursor = idx + len(newBlock)
		return nil
	}
	for _, l := range body {
		if strings.HasPrefix(l, "@@") {
			if err := flush(); err != nil {
				return "", err
			}
			if marker := strings.TrimSpace(strings.TrimPrefix(l, "@@")); marker != "" {
				if idx := indexLines(lines, []string{marker}, cursor); idx >= 0 {
					cursor = idx
				}
			}
			continue
		}
		hunk = append(hunk, l)
	}
	if err := flush(); err != nil {
		return "", err
	}
	return strings.Join(lines, "\n"), nil
}

func indexLines(haystack, needle []string, from int) int {
	if len(needle) == 0 || from < 0 {
		return -1
	}
	for i := from; i+len(needle) <= len(haystack); i++ {
		match := true
		for j := range needle {
			if strings.TrimRight(haystack[i+j], " \t") != strings.TrimRight(needle[j], " \t") {
				match = false
				break
			}
		}
		if match {
			return i
		}
	}
	if from > 0 {
		return indexLines(haystack, needle, 0)
	}
	return -1
}

// applyV4AChanges writes the computed contents to disk and returns a unified
// diff of everything it touched.
func applyV4AChanges(changes []v4aChange, workDir string) (string, error) {
	var diff strings.Builder
	for _, c := range changes {
		resolved, err := ResolvePath(workDir, c.path)
		if err != nil {
			return "", err
		}
		switch c.op {
		case v4aDelete:
			old, _ := os.ReadFile(resolved)
			if err := os.Remove(resolved); err != nil {
				return "", err
			}
			diff.WriteString(simpleDiff(string(old), "", c.path))
		case v4aAdd:
			if err := os.MkdirAll(filepath.Dir(resolved), 0o755); err != nil {
				return "", err
			}
			if err := os.WriteFile(resolved, []byte(c.content), 0o644); err != nil {
				return "", err
			}
			diff.WriteString(simpleDiff("", c.content, c.path))
		case v4aUpdate:
			old, _ := os.ReadFile(resolved)
			target, targetPath := resolved, c.path
			if c.movePath != "" {
				target, err = ResolvePath(workDir, c.movePath)
				if err != nil {
					return "", err
				}
				targetPath = c.movePath
				if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
					return "", err
				}
				if err := os.Remove(resolved); err != nil {
					return "", err
				}
			}
			if err := os.WriteFile(target, []byte(c.content), 0o644); err != nil {
				return "", err
			}
			diff.WriteString(simpleDiff(string(old), c.content, targetPath))
		}
	}
	return diff.String(), nil
}
