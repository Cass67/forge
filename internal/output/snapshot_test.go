package output_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"
	"forge/internal/output"
)

func TestSnapshot(t *testing.T) {
	base := t.TempDir()
	w, _ := output.NewWriter(base, time.Now())
	w.WriteCode(output.CodeBlock{Filename: "main.go", Content: "package main\n"})

	snapDir, err := w.Snapshot(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(snapDir, "main.go"))
	if err != nil {
		t.Fatalf("snapshot file not found: %v", err)
	}
	if string(b) != "package main\n" {
		t.Errorf("unexpected content: %q", string(b))
	}
}
