package output_test

import (
	"encoding/json"
	"forge/internal/output"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCreateSessionDir(t *testing.T) {
	base := t.TempDir()
	w, err := output.NewWriter(base, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(w.Dir(), "code")); err != nil {
		t.Fatalf("code dir not created: %v", err)
	}
}

func TestWriteCodeFile(t *testing.T) {
	base := t.TempDir()
	w, _ := output.NewWriter(base, time.Now())
	err := w.WriteCode(output.CodeBlock{Filename: "main.go", Content: "package main\n"})
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(w.Dir(), "code", "main.go"))
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "package main\n" {
		t.Errorf("unexpected content: %q", string(b))
	}
}

func TestWriteSessionJSON(t *testing.T) {
	base := t.TempDir()
	w, _ := output.NewWriter(base, time.Now())
	meta := output.SessionMeta{
		ID:            "test-id",
		Prompt:        "build something",
		Writer:        "claude-sonnet-4-6",
		Auditor:       "gpt-4o",
		Summarizer:    "claude-haiku-4-5-20251001",
		RoundsPerPass: 3,
		Status:        "running",
	}
	if err := w.WriteSessionJSON(meta); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(w.Dir(), "session.json"))
	var got output.SessionMeta
	json.Unmarshal(data, &got)
	if got.Prompt != "build something" {
		t.Errorf("unexpected prompt: %s", got.Prompt)
	}
}

func TestAppendAgentTranscript(t *testing.T) {
	base := t.TempDir()
	w, _ := output.NewWriter(base, time.Now())
	if err := w.AppendAgentTranscript("writer", 1, 2, "hello world"); err != nil {
		t.Fatal(err)
	}
	if err := w.AppendAgentTranscript("writer", 1, 3, "next turn"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(w.Dir(), "AI-1.md"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if !strings.Contains(text, "# AI-1 Transcript") {
		t.Fatalf("missing title: %s", text)
	}
	if !strings.Contains(text, "## Pass 1 Round 2") || !strings.Contains(text, "## Pass 1 Round 3") {
		t.Fatalf("missing round headings: %s", text)
	}
	if !strings.Contains(text, "hello world") || !strings.Contains(text, "next turn") {
		t.Fatalf("missing content: %s", text)
	}
}

func TestSeedFrom(t *testing.T) {
	base := t.TempDir()

	// Create source writer with some files
	src, _ := output.NewWriter(base, time.Now())
	src.WriteCode(output.CodeBlock{Filename: "main.go", Content: "package main\n"})
	src.WriteCode(output.CodeBlock{Filename: "sub/util.go", Content: "package sub\n"})

	// Create destination writer and seed it
	dst, _ := output.NewWriter(base, time.Now().Add(time.Second))
	if err := dst.SeedFrom(filepath.Join(src.Dir(), "code")); err != nil {
		t.Fatalf("SeedFrom: %v", err)
	}

	// Both files should appear in dst's code dir
	for _, name := range []string{"main.go", "sub/util.go"} {
		data, err := os.ReadFile(filepath.Join(dst.Dir(), "code", name))
		if err != nil {
			t.Fatalf("expected seeded file %s: %v", name, err)
		}
		if len(data) == 0 {
			t.Errorf("seeded file %s is empty", name)
		}
	}
}

func TestSeedFromMissingDir(t *testing.T) {
	base := t.TempDir()
	dst, _ := output.NewWriter(base, time.Now())
	// Non-existent source dir should return nil (nothing to seed).
	if err := dst.SeedFrom("/no/such/path/code"); err != nil {
		t.Errorf("SeedFrom non-existent dir should be a no-op, got: %v", err)
	}
}
