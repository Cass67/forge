package output

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// PassRecord is the per-pass entry in session.json.
type PassRecord struct {
	Name            string `json:"name"`
	RoundsCompleted int    `json:"rounds_completed"`
	Status          string `json:"status"` // "running", "complete", "aborted"
}

// SessionMeta is the schema for session.json.
type SessionMeta struct {
	ID            string       `json:"id"`
	Prompt        string       `json:"prompt"`
	LanguageHint  string       `json:"language_hint"`
	Writer        string       `json:"writer"`
	Auditor       string       `json:"auditor"`
	Summarizer    string       `json:"summarizer"`
	RoundsPerPass int          `json:"rounds_per_pass"`
	ContextFiles  []string     `json:"context_files"`
	Passes        []PassRecord `json:"passes"`
	Status        string       `json:"status"`
	AbortReason   string       `json:"abort_reason,omitempty"`
	Warnings      []string     `json:"warnings"`
	StartedAt     time.Time    `json:"started_at"`
	CompletedAt   *time.Time   `json:"completed_at"`
}

// Writer manages a session's output directory.
type Writer struct {
	dir string
}

// NewWriter creates the session directory and code/ subdirectory.
func NewWriter(baseDir string, ts time.Time) (*Writer, error) {
	id := ts.UTC().Format("2006-01-02T15-04-05")
	dir := filepath.Join(baseDir, id)
	if err := os.MkdirAll(filepath.Join(dir, "code"), 0o755); err != nil {
		return nil, err
	}
	return &Writer{dir: dir}, nil
}

func (w *Writer) Dir() string { return w.dir }

// WriteCode writes a parsed code block to code/<filename>, creating parent dirs.
func (w *Writer) WriteCode(block CodeBlock) error {
	path := filepath.Join(w.dir, "code", block.Filename)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(block.Content), 0o644)
}

// WriteSessionJSON atomically writes session.json.
func (w *Writer) WriteSessionJSON(meta SessionMeta) error {
	data, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(w.dir, "session.json"), data, 0o644)
}

// InlineCodeFiles reads all files under code/ and returns them as fenced blocks.
func (w *Writer) InlineCodeFiles() (string, error) {
	codeDir := filepath.Join(w.dir, "code")
	var sb strings.Builder
	err := filepath.WalkDir(codeDir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(codeDir, path)
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		sb.WriteString("```:" + rel + "\n")
		sb.Write(data)
		sb.WriteString("\n```\n\n")
		return nil
	})
	return sb.String(), err
}
