package output

import (
	"encoding/json"
	"errors"
	"fmt"
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

// AppendAgentTranscript appends one completed agent turn to a markdown log.
func (w *Writer) AppendAgentTranscript(agent string, pass, round int, content string) error {
	name := transcriptFilename(agent)
	if name == "" {
		return fmt.Errorf("unknown agent %q", agent)
	}
	path := filepath.Join(w.dir, name)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()

	var sb strings.Builder
	if info, statErr := f.Stat(); statErr == nil && info.Size() == 0 {
		sb.WriteString("# " + transcriptTitle(agent) + "\n\n")
	}
	sb.WriteString(fmt.Sprintf("## Pass %d Round %d\n\n", pass, round))
	sb.WriteString(content)
	if !strings.HasSuffix(content, "\n") {
		sb.WriteByte('\n')
	}
	sb.WriteString("\n")
	_, err = f.WriteString(sb.String())
	return err
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

// SeedFrom copies all files from srcCodeDir into this writer's code/ subdirectory.
// If srcCodeDir does not exist the call is a no-op.
func (w *Writer) SeedFrom(srcCodeDir string) error {
	if _, err := os.Stat(srcCodeDir); errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return filepath.WalkDir(srcCodeDir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(srcCodeDir, path)
		dst := filepath.Join(w.dir, "code", rel)
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(dst, data, 0o644)
	})
}

func transcriptFilename(agent string) string {
	switch agent {
	case "writer":
		return "AI-1.md"
	case "auditor":
		return "AI-2.md"
	default:
		return ""
	}
}

func transcriptTitle(agent string) string {
	switch agent {
	case "writer":
		return "AI-1 Transcript"
	case "auditor":
		return "AI-2 Transcript"
	default:
		return "Transcript"
	}
}
