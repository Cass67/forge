package tui

import (
	"strings"
	"testing"
)

func TestRenderTraceOverlayPanelShowsContent(t *testing.T) {
	theme := lookupThemeForTest(t, "default")
	got := renderTraceOverlayPanel(theme, "tool_call read_file\nobserve complete", "", 100, 24)
	if !strings.Contains(got, "Debug trace") {
		t.Fatalf("missing title: %q", got)
	}
	if !strings.Contains(got, "tool_call read_file") {
		t.Fatalf("missing trace content: %q", got)
	}
}

func TestRenderTraceOverlayPanelHandlesEmptyContent(t *testing.T) {
	theme := lookupThemeForTest(t, "default")
	got := renderTraceOverlayPanel(theme, "", "", 100, 24)
	if !strings.Contains(got, "No trace captured yet.") {
		t.Fatalf("missing empty-state text: %q", got)
	}
}

func TestRenderTraceOverlayPanelShowsDebugLogPath(t *testing.T) {
	theme := lookupThemeForTest(t, "default")
	got := renderTraceOverlayPanel(theme, "", "/tmp/forge-chat-debug.jsonl", 100, 24)
	if !strings.Contains(got, "/tmp/forge-chat-debug.jsonl") {
		t.Fatalf("missing debug log path: %q", got)
	}
}
