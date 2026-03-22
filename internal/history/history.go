package history

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"forge/internal/output"
)

// SessionSummary is a lightweight view of a session for listing.
type SessionSummary struct {
	ID        string
	Dir       string
	Prompt    string
	Writer    string
	Auditor   string
	Status    string
	StartedAt string
}

// List scans the output directory for session dirs and returns summaries
// sorted newest-first by directory name (which encodes a timestamp).
func List(outputDir string) ([]SessionSummary, error) {
	entries, err := os.ReadDir(outputDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var sessions []SessionSummary
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(outputDir, e.Name())
		metaPath := filepath.Join(dir, "session.json")
		data, err := os.ReadFile(metaPath)
		if err != nil {
			sessions = append(sessions, SessionSummary{
				ID:     e.Name(),
				Dir:    dir,
				Status: "unknown",
			})
			continue
		}
		var meta output.SessionMeta
		if err := json.Unmarshal(data, &meta); err != nil {
			continue
		}
		prompt := meta.Prompt
		if len(prompt) > 80 {
			prompt = prompt[:77] + "..."
		}
		started := ""
		if !meta.StartedAt.IsZero() {
			started = meta.StartedAt.Format("2006-01-02 15:04")
		}
		sessions = append(sessions, SessionSummary{
			ID:        meta.ID,
			Dir:       dir,
			Prompt:    prompt,
			Writer:    meta.Writer,
			Auditor:   meta.Auditor,
			Status:    meta.Status,
			StartedAt: started,
		})
	}

	sort.Slice(sessions, func(i, j int) bool {
		return sessions[i].ID > sessions[j].ID
	})
	return sessions, nil
}

// SessionDetail holds full metadata and file listings for a single session.
type SessionDetail struct {
	Meta      output.SessionMeta
	Dir       string
	CodeFiles []FileInfo
	Artifacts []FileInfo
}

// FileInfo describes a file inside a session directory.
type FileInfo struct {
	Name string
	Size int64
}

// Show reads the full session metadata and file listings for a session.
// sessionID can be an exact directory name or an unambiguous prefix.
func Show(outputDir, sessionID string) (*SessionDetail, error) {
	dir := filepath.Join(outputDir, sessionID)
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		entries, readErr := os.ReadDir(outputDir)
		if readErr != nil {
			return nil, fmt.Errorf("session %q not found", sessionID)
		}
		found := false
		for _, e := range entries {
			if strings.HasPrefix(e.Name(), sessionID) {
				dir = filepath.Join(outputDir, e.Name())
				found = true
				break
			}
		}
		if !found {
			return nil, fmt.Errorf("session %q not found", sessionID)
		}
	}

	metaPath := filepath.Join(dir, "session.json")
	data, err := os.ReadFile(metaPath)
	if err != nil {
		return nil, fmt.Errorf("session %q not found", sessionID)
	}

	var meta output.SessionMeta
	if err := json.Unmarshal(data, &meta); err != nil {
		return nil, fmt.Errorf("invalid session.json: %w", err)
	}

	detail := &SessionDetail{
		Meta: meta,
		Dir:  dir,
	}

	codeDir := filepath.Join(dir, "code")
	filepath.WalkDir(codeDir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(codeDir, path)
		info, _ := d.Info()
		size := int64(0)
		if info != nil {
			size = info.Size()
		}
		detail.CodeFiles = append(detail.CodeFiles, FileInfo{Name: rel, Size: size})
		return nil
	})

	for _, name := range []string{"AI-1.md", "AI-2.md", "summary-store.md", "audit-log.md", "diff-log.md"} {
		if info, err := os.Stat(filepath.Join(dir, name)); err == nil {
			detail.Artifacts = append(detail.Artifacts, FileInfo{Name: name, Size: info.Size()})
		}
	}

	return detail, nil
}
