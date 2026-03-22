package output

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// CodeSnapshot captures all code files at a point in time.
type CodeSnapshot struct {
	Files map[string]string // relative path -> content
}

// TakeSnapshot reads all files under the code/ dir and returns a CodeSnapshot.
func (w *Writer) TakeSnapshot() CodeSnapshot {
	snap := CodeSnapshot{Files: make(map[string]string)}
	codeDir := filepath.Join(w.dir, "code")
	filepath.WalkDir(codeDir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(codeDir, path)
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		snap.Files[rel] = string(data)
		return nil
	})
	return snap
}

// DiffEntry represents changes to one file between snapshots.
type DiffEntry struct {
	Filename string
	Status   string // "added", "modified", "deleted"
	Diff     string // unified-style diff text
}

// DiffSnapshots compares two snapshots and returns the changes.
func DiffSnapshots(before, after CodeSnapshot) []DiffEntry {
	allFiles := make(map[string]bool)
	for f := range before.Files {
		allFiles[f] = true
	}
	for f := range after.Files {
		allFiles[f] = true
	}

	sorted := make([]string, 0, len(allFiles))
	for f := range allFiles {
		sorted = append(sorted, f)
	}
	sort.Strings(sorted)

	var entries []DiffEntry
	for _, f := range sorted {
		old, hadOld := before.Files[f]
		new_, hadNew := after.Files[f]

		switch {
		case !hadOld && hadNew:
			entries = append(entries, DiffEntry{
				Filename: f,
				Status:   "added",
				Diff:     formatUnifiedDiff(f, "", new_),
			})
		case hadOld && !hadNew:
			entries = append(entries, DiffEntry{
				Filename: f,
				Status:   "deleted",
				Diff:     formatUnifiedDiff(f, old, ""),
			})
		case hadOld && hadNew && old != new_:
			entries = append(entries, DiffEntry{
				Filename: f,
				Status:   "modified",
				Diff:     formatUnifiedDiff(f, old, new_),
			})
		}
	}
	return entries
}

// formatUnifiedDiff produces a readable diff with +/- line markers.
func formatUnifiedDiff(filename, old, new_ string) string {
	var oldLines, newLines []string
	if old != "" {
		oldLines = strings.Split(old, "\n")
	}
	if new_ != "" {
		newLines = strings.Split(new_, "\n")
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("--- a/%s\n+++ b/%s\n", filename, filename))

	maxLen := len(oldLines)
	if len(newLines) > maxLen {
		maxLen = len(newLines)
	}

	for i := 0; i < maxLen; i++ {
		haveOld := i < len(oldLines)
		haveNew := i < len(newLines)
		switch {
		case haveOld && haveNew && oldLines[i] == newLines[i]:
			sb.WriteString(" " + oldLines[i] + "\n")
		case haveOld && haveNew:
			sb.WriteString("-" + oldLines[i] + "\n")
			sb.WriteString("+" + newLines[i] + "\n")
		case haveOld:
			sb.WriteString("-" + oldLines[i] + "\n")
		case haveNew:
			sb.WriteString("+" + newLines[i] + "\n")
		}
	}
	return sb.String()
}

// AppendDiffLog writes a round's diff to diff-log.md.
func (w *Writer) AppendDiffLog(pass, round int, diffs []DiffEntry) error {
	if len(diffs) == 0 {
		return nil
	}
	path := filepath.Join(w.dir, "diff-log.md")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()

	var sb strings.Builder
	info, _ := f.Stat()
	if info != nil && info.Size() == 0 {
		sb.WriteString("# Diff Log\n\n")
	}
	sb.WriteString(fmt.Sprintf("## Pass %d Round %d\n\n", pass, round))
	for _, d := range diffs {
		sb.WriteString(fmt.Sprintf("### %s (%s)\n\n", d.Filename, d.Status))
		sb.WriteString("```diff\n")
		sb.WriteString(d.Diff)
		sb.WriteString("```\n\n")
	}
	_, err = f.WriteString(sb.String())
	return err
}
