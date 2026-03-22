package output

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestApplyBack(t *testing.T) {
	base := t.TempDir()
	w, err := NewWriter(base, time.Now())
	if err != nil {
		t.Fatal(err)
	}

	// Write some code files
	w.WriteCode(CodeBlock{Filename: "main.go", Content: "package main\n"})
	w.WriteCode(CodeBlock{Filename: "util/helper.go", Content: "package util\n"})

	// Apply back to a target dir
	target := t.TempDir()
	if err := w.ApplyBack(target); err != nil {
		t.Fatal(err)
	}

	// Verify files exist
	data, err := os.ReadFile(filepath.Join(target, "main.go"))
	if err != nil {
		t.Fatal("main.go not copied:", err)
	}
	if string(data) != "package main\n" {
		t.Errorf("unexpected content: %q", string(data))
	}

	data, err = os.ReadFile(filepath.Join(target, "util", "helper.go"))
	if err != nil {
		t.Fatal("util/helper.go not copied:", err)
	}
	if string(data) != "package util\n" {
		t.Errorf("unexpected content: %q", string(data))
	}
}

func TestApplyBackEmpty(t *testing.T) {
	base := t.TempDir()
	w, err := NewWriter(base, time.Now())
	if err != nil {
		t.Fatal(err)
	}

	target := t.TempDir()
	if err := w.ApplyBack(target); err != nil {
		t.Fatal("ApplyBack on empty code dir should not fail:", err)
	}
}
