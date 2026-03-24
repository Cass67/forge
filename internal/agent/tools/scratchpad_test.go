package tools

import (
	"context"
	"testing"
)

func TestScratchpadReadNormalizesMarkdownTopicSuffix(t *testing.T) {
	dir := t.TempDir()
	writeTool := NewScratchpadWrite(dir)
	readTool := NewScratchpadRead(dir)

	if _, err := writeTool.Execute(context.Background(), map[string]any{
		"topic":   "repo_review_evidence",
		"content": "FINDINGS:\n- docs are thin",
	}); err != nil {
		t.Fatalf("write scratchpad: %v", err)
	}

	got, err := readTool.Execute(context.Background(), map[string]any{
		"topic": "repo_review_evidence.md",
	})
	if err != nil {
		t.Fatalf("read scratchpad: %v", err)
	}
	if got != "FINDINGS:\n- docs are thin" {
		t.Fatalf("scratchpad read = %q", got)
	}
}
