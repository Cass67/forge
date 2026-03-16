package summarizer

import (
	"fmt"
	"os"
)

// Entry is one round's summary to be appended to the store.
type Entry struct {
	Pass  int
	Round int
	Body  string // formatted markdown body (Writer/Auditor/Decisions/Outstanding)
}

// Store manages appends to summary-store.md.
type Store struct {
	path string
}

func NewStore(path string) *Store {
	return &Store{path: path}
}

// Append writes a new round entry to the store.
func (s *Store) Append(e Entry) error {
	f, err := os.OpenFile(s.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = fmt.Fprintf(f, "\n## Pass %d · Round %d\n%s\n", e.Pass, e.Round, e.Body)
	return err
}

// AppendPlaceholder writes a failure placeholder entry.
func (s *Store) AppendPlaceholder(pass, round int) error {
	return s.Append(Entry{
		Pass:  pass,
		Round: round,
		Body:  "**[summarizer failed — entry unavailable]**",
	})
}

// ReadAll returns the full contents of the store.
func (s *Store) ReadAll() (string, error) {
	data, err := os.ReadFile(s.path)
	if os.IsNotExist(err) {
		return "", nil
	}
	return string(data), err
}
