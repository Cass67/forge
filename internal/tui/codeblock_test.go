package tui

import (
	"strings"
	"testing"
)

func TestRenderMessageContentRendersFencedCodeBlocks(t *testing.T) {
	theme := lookupThemeForTest(t, "default")
	got := renderMessageContent("Before\n```go\nfmt.Println(\"hi\")\n```\nAfter", 60, theme)
	if !strings.Contains(got, "GO") {
		t.Fatalf("missing code label: %q", got)
	}
	if !strings.Contains(got, "fmt.Println(\"hi\")") {
		t.Fatalf("missing code content: %q", got)
	}
	if !strings.Contains(got, "Before") || !strings.Contains(got, "After") {
		t.Fatalf("missing surrounding prose: %q", got)
	}
}

func TestRenderMessageContentRendersDiffBlocks(t *testing.T) {
	theme := lookupThemeForTest(t, "default")
	got := renderMessageContent("```diff\n+ added line\n- removed line\n```", 60, theme)
	if !strings.Contains(got, "DIFF") {
		t.Fatalf("missing diff label: %q", got)
	}
	if !strings.Contains(got, "+ added line") || !strings.Contains(got, "- removed line") {
		t.Fatalf("missing diff content: %q", got)
	}
}
