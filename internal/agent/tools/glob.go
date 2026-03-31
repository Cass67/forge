package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
)

func NewGlob(workDir string, ignoreDirs []string) Tool {
	ignoreSet := make(map[string]bool)
	for _, d := range ignoreDirs {
		ignoreSet[d] = true
	}

	return Tool{
		Name:        "glob",
		Description: "Find files matching a glob pattern. Supports ** for recursive matching.",
		Parameters: []ParameterDef{
			{Name: "pattern", Type: "string", Description: "glob pattern (e.g. \"**/*.go\", \"src/**/*.ts\")", Required: true},
			{Name: "path", Type: "string", Description: "base directory (default \".\")", Required: false},
		},
		AutoApprove: true,
		Execute: func(ctx context.Context, args map[string]any) (string, error) {
			pattern, _ := args["pattern"].(string)
			basePath := "."
			if p, ok := args["path"].(string); ok && p != "" {
				basePath = p
			}

			resolved, err := ResolvePathAllowEscape(workDir, basePath)
			if err != nil {
				return fmt.Sprintf("error: %v", err), nil
			}

			var matches []string
			maxEntries := 500

			fsys := os.DirFS(resolved)
			_ = doublestar.GlobWalk(fsys, pattern, func(path string, d os.DirEntry) error {
				// Check if any path component is in the ignore set
				for _, part := range strings.Split(filepath.Dir(path), string(os.PathSeparator)) {
					if ignoreSet[part] {
						return doublestar.SkipDir
					}
				}
				if d.IsDir() && ignoreSet[d.Name()] {
					return doublestar.SkipDir
				}
				if d.IsDir() {
					return nil
				}
				if len(matches) >= maxEntries {
					return doublestar.SkipDir
				}
				matches = append(matches, path)
				return nil
			})

			if len(matches) == 0 {
				return "no matches found", nil
			}

			var sb strings.Builder
			for _, m := range matches {
				if basePath != "." {
					rel, _ := filepath.Rel(workDir, filepath.Join(resolved, m))
					sb.WriteString(rel + "\n")
				} else {
					sb.WriteString(m + "\n")
				}
			}
			if len(matches) >= maxEntries {
				fmt.Fprintf(&sb, "... truncated at %d entries\n", maxEntries)
			}
			return sb.String(), nil
		},
	}
}
