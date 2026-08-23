package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLineAnchorIgnoresTrailingWhitespace(t *testing.T) {
	if lineAnchor("func main() {") != lineAnchor("func main() {   ") {
		t.Error("trailing whitespace changed the anchor")
	}
	if lineAnchor("a") == lineAnchor("b") {
		t.Error("distinct lines collided")
	}
}

func TestResolveAnchorSpan(t *testing.T) {
	lines := []string{"one", "two", "three", "two", "four"}
	one, two, three := lineAnchor("one"), lineAnchor("two"), lineAnchor("three")

	span, err := resolveAnchorSpan(lines, one, three, 0)
	if err != nil {
		t.Fatal(err)
	}
	if span.start != 1 || span.end != 3 {
		t.Fatalf("got %+v, want 1-3", span)
	}

	// A single anchor addresses one line.
	span, err = resolveAnchorSpan(lines, three, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if span.start != 3 || span.end != 3 {
		t.Fatalf("got %+v, want 3-3", span)
	}

	// "two" occurs twice: ambiguous without a hint, resolvable with one.
	if _, err := resolveAnchorSpan(lines, two, "", 0); err == nil {
		t.Error("expected ambiguity error")
	}
	span, err = resolveAnchorSpan(lines, two, "", 4)
	if err != nil {
		t.Fatal(err)
	}
	if span.start != 4 {
		t.Fatalf("hint ignored: %+v", span)
	}

	if _, err := resolveAnchorSpan(lines, "dead", "", 0); err == nil || !strings.Contains(err.Error(), "stale") {
		t.Errorf("expected stale-anchor error, got %v", err)
	}
}

func TestReplaceSpan(t *testing.T) {
	lines := []string{"a", "b", "c", "d"}
	got := strings.Join(replaceSpan(lines, anchorSpan{2, 3}, "X\nY\n"), "\n")
	if got != "a\nX\nY\nd" {
		t.Fatalf("got %q", got)
	}
	got = strings.Join(replaceSpan(lines, anchorSpan{2, 3}, ""), "\n")
	if got != "a\nd" {
		t.Fatalf("delete got %q", got)
	}
}

func TestEditFileByAnchor(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.go")
	original := "package main\n\nfunc main() {\n\tprintln(\"hi\")\n}\n"
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := NewEditFile(dir, func(Action) (bool, error) { return true, nil })
	out, err := tool.Execute(context.Background(), map[string]any{
		"path":         "main.go",
		"start_anchor": lineAnchor("\tprintln(\"hi\")"),
		"new_text":     "\tprintln(\"bye\")\n",
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "failed") {
		t.Fatalf("edit failed: %s", out)
	}
	data, _ := os.ReadFile(path)
	if string(data) != "package main\n\nfunc main() {\n\tprintln(\"bye\")\n}\n" {
		t.Fatalf("got %q", data)
	}

	// A stale anchor must not corrupt the file.
	out, err = tool.Execute(context.Background(), map[string]any{
		"path":         "main.go",
		"start_anchor": lineAnchor("\tprintln(\"hi\")"),
		"new_text":     "nope\n",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "stale") {
		t.Fatalf("expected stale rejection, got %s", out)
	}
	data, _ = os.ReadFile(path)
	if !strings.Contains(string(data), "bye") {
		t.Fatal("file was modified by a stale anchor")
	}
}
