package output_test

import (
	"testing"
	"forge/internal/output"
)

func TestParseEmpty(t *testing.T) {
	blocks := output.ParseCodeBlocks("no fenced blocks here")
	if len(blocks) != 0 {
		t.Fatalf("expected 0 blocks, got %d", len(blocks))
	}
}

func TestParseSingleBlock(t *testing.T) {
	text := "```go:main.go\npackage main\n```"
	blocks := output.ParseCodeBlocks(text)
	if len(blocks) != 1 {
		t.Fatalf("expected 1 block, got %d", len(blocks))
	}
	if blocks[0].Filename != "main.go" {
		t.Errorf("unexpected filename: %s", blocks[0].Filename)
	}
	if blocks[0].Content != "package main\n" {
		t.Errorf("unexpected content: %q", blocks[0].Content)
	}
}

func TestParseMultipleBlocks(t *testing.T) {
	text := "```go:a.go\nfunc a(){}\n```\nsome prose\n```go:b.go\nfunc b(){}\n```"
	blocks := output.ParseCodeBlocks(text)
	if len(blocks) != 2 {
		t.Fatalf("expected 2, got %d", len(blocks))
	}
	if blocks[1].Filename != "b.go" {
		t.Errorf("unexpected: %s", blocks[1].Filename)
	}
}

func TestBlockWithoutFilenameIgnored(t *testing.T) {
	text := "```go\nno filename\n```"
	blocks := output.ParseCodeBlocks(text)
	if len(blocks) != 0 {
		t.Fatalf("expected 0, got %d", len(blocks))
	}
}

func TestPathTraversalRejected(t *testing.T) {
	text := "```go:../../etc/passwd\nbad\n```"
	blocks := output.ParseCodeBlocks(text)
	if len(blocks) != 0 {
		t.Fatalf("expected path traversal to be rejected, got %d blocks", len(blocks))
	}
}
