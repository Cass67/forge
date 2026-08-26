package tui

import (
	"strings"
	"testing"
)

func TestCompactDiffForDisplay(t *testing.T) {
	var b strings.Builder
	b.WriteString("--- a/f.go\n+++ b/f.go\n")
	for range 20 {
		b.WriteString(" ctx\n")
	}
	b.WriteString("-old\n+new\n")
	for range 20 {
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

func TestSideBySideDiffBlock(t *testing.T) {
	theme := chatThemeRegistry[0]
	diff := "@@ -10,3 +10,4 @@\n ctx line\n-old text\n+new text\n+added only\n"

	got := sideBySideDiffBlock(diff, 120, theme)
	rows := strings.Split(got, "\n")
	if len(rows) != 4 {
		t.Fatalf("want 4 rows (hunk, ctx, pair, add), got %d:\n%s", len(rows), got)
	}
	if !strings.Contains(rows[2], "old text") || !strings.Contains(rows[2], "new text") {
		t.Fatalf("changed pair not on one row:\n%s", rows[2])
	}
	if !strings.Contains(rows[3], "added only") || strings.Contains(rows[3], "old") {
		t.Fatalf("add-only row wrong:\n%s", rows[3])
	}

	if narrow := enhancedDiffBlock(diff, 80, theme); strings.Contains(strings.Split(narrow, "\n")[2], "new text") && strings.Contains(strings.Split(narrow, "\n")[2], "old text") {
		t.Fatalf("narrow width should fall back to unified")
	}
}
