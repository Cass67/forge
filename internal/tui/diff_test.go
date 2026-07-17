package tui

import (
	"strings"
	"testing"
)

func TestCompactDiffForDisplay(t *testing.T) {
	var b strings.Builder
	b.WriteString("--- a/f.go\n+++ b/f.go\n")
	for i := 0; i < 20; i++ {
		b.WriteString(" ctx\n")
	}
	b.WriteString("-old\n+new\n")
	for i := 0; i < 20; i++ {
		b.WriteString(" ctx\n")
	}

	got := compactDiffForDisplay(b.String(), 30)
	if !strings.Contains(got, "-old") || !strings.Contains(got, "+new") {
		t.Fatalf("changed lines missing:\n%s", got)
	}
	if strings.Count(got, " ctx") > 4 {
		t.Fatalf("too much context kept:\n%s", got)
	}
	if !strings.Contains(got, "--- a/f.go") {
		t.Fatalf("header missing:\n%s", got)
	}

	long := "--- a/f.go\n+++ b/f.go\n" + strings.Repeat("+add\n", 100)
	got = compactDiffForDisplay(long, 30)
	if lines := strings.Count(got, "\n"); lines > 31 {
		t.Fatalf("cap not applied, %d lines", lines)
	}
	if !strings.Contains(got, "more lines") {
		t.Fatalf("truncation marker missing:\n%s", got)
	}
}
