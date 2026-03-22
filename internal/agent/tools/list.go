package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func NewListDir(workDir string, ignoreDirs []string) Tool {
	ignoreSet := make(map[string]bool)
	for _, d := range ignoreDirs {
		ignoreSet[d] = true
	}

	return Tool{
		Name:        "list_dir",
		Description: "List directory contents.",
		Parameters: []ParameterDef{
			{Name: "path", Type: "string", Description: "directory path (default \".\")", Required: false},
			{Name: "recursive", Type: "bool", Description: "list recursively (default false)", Required: false},
		},
		AutoApprove: true,
		Execute: func(ctx context.Context, args map[string]any) (string, error) {
			path := "."
			if p, ok := args["path"].(string); ok && p != "" {
				path = p
			}
			recursive, _ := args["recursive"].(bool)

			resolved, err := ResolvePath(workDir, path)
			if err != nil {
				return "", err
			}

			if recursive {
				return listRecursive(resolved, workDir, ignoreSet)
			}
			return listFlat(resolved, ignoreSet)
		},
	}
}

func listFlat(dir string, ignore map[string]bool) (string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Sprintf("error: %v", err), nil
	}
	var sb strings.Builder
	for _, e := range entries {
		if ignore[e.Name()] {
			continue
		}
		name := e.Name()
		if e.IsDir() {
			name += "/"
		}
		sb.WriteString(name + "\n")
	}
	return sb.String(), nil
}

func listRecursive(dir, workDir string, ignore map[string]bool) (string, error) {
	var sb strings.Builder
	count := 0
	maxEntries := 500

	filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() && ignore[d.Name()] {
			return filepath.SkipDir
		}
		if count >= maxEntries {
			return filepath.SkipAll
		}

		rel, _ := filepath.Rel(workDir, path)
		if rel == "." {
			return nil
		}

		name := rel
		if d.IsDir() {
			name += "/"
		}
		sb.WriteString(name + "\n")
		count++
		return nil
	})

	if count >= maxEntries {
		sb.WriteString(fmt.Sprintf("... truncated at %d entries\n", maxEntries))
	}
	return sb.String(), nil
}
