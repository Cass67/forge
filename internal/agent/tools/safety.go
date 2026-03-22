package tools

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func ResolvePath(workDir, path string) (string, error) {
	if path == "" {
		return "", fmt.Errorf("empty path")
	}

	resolvedWorkDir, err := filepath.EvalSymlinks(workDir)
	if err != nil {
		resolvedWorkDir = filepath.Clean(workDir)
	}

	var abs string
	if filepath.IsAbs(path) {
		abs = filepath.Clean(path)
	} else {
		abs = filepath.Clean(filepath.Join(resolvedWorkDir, path))
	}

	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		dir := filepath.Dir(abs)
		resolvedDir, dirErr := filepath.EvalSymlinks(dir)
		if dirErr != nil {
			if !strings.HasPrefix(abs, resolvedWorkDir+string(os.PathSeparator)) && abs != resolvedWorkDir {
				return "", fmt.Errorf("path %q escapes working directory", path)
			}
			return abs, nil
		}
		if !strings.HasPrefix(resolvedDir, resolvedWorkDir+string(os.PathSeparator)) && resolvedDir != resolvedWorkDir {
			return "", fmt.Errorf("path %q escapes working directory", path)
		}
		return abs, nil
	}

	if !strings.HasPrefix(resolved, resolvedWorkDir+string(os.PathSeparator)) && resolved != resolvedWorkDir {
		return "", fmt.Errorf("path %q escapes working directory", path)
	}

	return abs, nil
}

func IsBinary(data []byte) bool {
	check := data
	if len(check) > 8192 {
		check = check[:8192]
	}
	for _, b := range check {
		if b == 0 {
			return true
		}
	}
	return false
}
