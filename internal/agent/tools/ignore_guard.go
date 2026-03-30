package tools

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
)

// ignoreGuard checks file paths against the patterns in .ignore (and .rgignore)
// in the working directory. It is used by read_file and search to prevent the
// agent from accessing secret-bearing files even when explicitly asked.
type ignoreGuard struct {
	patterns []string
}

func newIgnoreGuard(workDir string) ignoreGuard {
	var patterns []string
	for _, name := range []string{".ignore", ".rgignore"} {
		patterns = append(patterns, loadIgnorePatterns(filepath.Join(workDir, name))...)
	}
	return ignoreGuard{patterns: patterns}
}

func loadIgnorePatterns(path string) []string {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close() //nolint:errcheck

	var patterns []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		patterns = append(patterns, line)
	}
	return patterns
}

// blocked reports whether absPath should be denied based on the ignore patterns.
func (g ignoreGuard) blocked(absPath string) bool {
	name := filepath.Base(absPath)
	for _, pat := range g.patterns {
		// Match against the base filename first (handles patterns like "config.toml", "*.env").
		if ok, _ := doublestar.Match(pat, name); ok {
			return true
		}
		// Also match the full path for directory patterns like ".aws/" or "**/.env".
		if ok, _ := doublestar.Match(pat, absPath); ok {
			return true
		}
	}
	return false
}
