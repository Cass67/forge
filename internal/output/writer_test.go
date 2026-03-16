package output_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
	"forge/internal/output"
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
