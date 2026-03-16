package summarizer_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"forge/internal/summarizer"
)

func TestAppendEntry(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "summary-store.md")
	store := summarizer.NewStore(path)

	entry := summarizer.Entry{
		Pass:  1,
		Round: 1,
		Body:  "**Writer:** did stuff\n**Auditor:** found issues",
	}
	if err := store.Append(entry); err != nil {
		t.Fatal(err)
	}

	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), "Pass 1 · Round 1") {
		t.Errorf("missing heading in: %s", string(data))
	}
}

func TestAppendPlaceholderOnFailure(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "summary-store.md")
	store := summarizer.NewStore(path)
	if err := store.AppendPlaceholder(2, 1); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), "summarizer failed") {
		t.Errorf("missing placeholder in: %s", string(data))
	}
}

func TestReadAll(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "summary-store.md")
	store := summarizer.NewStore(path)
	store.Append(summarizer.Entry{Pass: 1, Round: 1, Body: "content"})

	text, err := store.ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "content") {
		t.Errorf("expected content in: %s", text)
	}
}
