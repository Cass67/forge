package tui

import "testing"

func TestTranscriptRecordSegmentsSplitTextAndCode(t *testing.T) {
	segments := segmentsFromContent("Before\n```go\nfmt.Println(\"hi\")\n```\nAfter")
	if len(segments) != 3 {
		t.Fatalf("segments = %#v", segments)
	}
	if segments[0].Kind != SegmentText || segments[0].Text != "Before\n" {
		t.Fatalf("first segment = %#v", segments[0])
	}
	if segments[1].Kind != SegmentCode || segments[1].Lang != "go" || segments[1].Text != "fmt.Println(\"hi\")\n" {
		t.Fatalf("code segment = %#v", segments[1])
	}
	if segments[2].Kind != SegmentText || segments[2].Text != "\nAfter" {
		t.Fatalf("last segment = %#v", segments[2])
	}
}
