package format

import (
	"strings"
	"testing"
	"time"
)

func TestAgentLine(t *testing.T) {
	line := AgentLine("hello world")
	if len(line.Spans) != 2 {
		t.Fatalf("expected 2 spans, got %d", len(line.Spans))
	}
	if line.Spans[0].Style != StyleAccentBorder {
		t.Errorf("expected accent border style")
	}
	if line.Spans[0].Text != " │ " {
		t.Errorf("expected ' │ ', got %q", line.Spans[0].Text)
	}
	if line.Spans[1].Text != "hello world" {
		t.Errorf("expected 'hello world', got %q", line.Spans[1].Text)
	}
}

func TestLineToANSI(t *testing.T) {
	line := Line{Spans: []Span{
		{Text: "hello", Style: StyleNormal},
		{Text: " world", Style: StyleSuccess},
	}}
	result := LineToANSI(line, true)
	if !strings.Contains(result, "hello") {
		t.Error("missing hello")
	}
	if !strings.Contains(result, "\033[32m") {
		t.Error("missing green ANSI code")
	}
}

func TestLineToANSINoColor(t *testing.T) {
	line := Line{Spans: []Span{
		{Text: "hello", Style: StyleSuccess},
	}}
	result := LineToANSI(line, false)
	if strings.Contains(result, "\033[") {
		t.Error("should not contain ANSI codes")
	}
	if result != "hello" {
		t.Errorf("expected 'hello', got %q", result)
	}
}

func TestDiff(t *testing.T) {
	diff := "--- a/foo.go\n+++ b/foo.go\n-old line\n+new line\n same line\n"
	lines := Diff(diff)
	if len(lines) == 0 {
		t.Fatal("expected lines")
	}
	foundAdd := false
	foundRemove := false
	for _, line := range lines {
		for _, span := range line.Spans {
			if span.Style == StyleDiffAdd {
				foundAdd = true
			}
			if span.Style == StyleDiffRemove {
				foundRemove = true
			}
		}
	}
	if !foundAdd {
		t.Error("expected StyleDiffAdd span")
	}
	if !foundRemove {
		t.Error("expected StyleDiffRemove span")
	}
}

func TestToolBoxCompact(t *testing.T) {
	lines := ToolBox("read_file", "main.go", "", StatusSuccess, 60)
	ansi := ToANSI(lines, false)
	if !strings.Contains(ansi, "read_file") {
		t.Error("missing tool name")
	}
	if !strings.Contains(ansi, "main.go") {
		t.Error("missing summary")
	}
	if !strings.Contains(ansi, "┌") {
		t.Error("missing top border")
	}
	if !strings.Contains(ansi, "└") {
		t.Error("missing bottom border")
	}
}

func TestToolBoxExpanded(t *testing.T) {
	diff := "-old\n+new\n"
	lines := ToolBox("edit_file", "main.go", diff, StatusSuccess, 60)
	ansi := ToANSI(lines, false)
	if !strings.Contains(ansi, "edit_file") {
		t.Error("missing tool name")
	}
	if !strings.Contains(ansi, "old") {
		t.Error("missing diff content")
	}
}

func TestToolBoxRunCommandFail(t *testing.T) {
	lines := ToolBox("run_command", "go test", "FAIL: TestFoo", StatusError, 60)
	ansi := ToANSI(lines, false)
	if !strings.Contains(ansi, "✗") {
		t.Error("missing error indicator")
	}
}

func TestToolBoxCompactNoDetail(t *testing.T) {
	lines := ToolBox("search", "pattern", "3 matches", StatusSuccess, 60)
	ansi := ToANSI(lines, false)
	// search is not an expanded tool, so detail should not appear
	if strings.Contains(ansi, "3 matches") {
		t.Error("compact tool should not show detail")
	}
}

func TestTruncate(t *testing.T) {
	input := "line1\nline2\nline3\nline4\nline5\n"
	truncated, wasTruncated := Truncate(input, 3)
	if !wasTruncated {
		t.Error("expected truncation")
	}
	if !strings.Contains(truncated, "line1") {
		t.Error("missing line1")
	}
	if strings.Contains(truncated, "/expand") {
		t.Errorf("unexpected /expand hint in %q", truncated)
	}
	if !strings.Contains(truncated, "3 more lines") {
		t.Errorf("missing truncation summary in %q", truncated)
	}
}

func TestTruncateNoOp(t *testing.T) {
	input := "line1\nline2\n"
	truncated, wasTruncated := Truncate(input, 5)
	if wasTruncated {
		t.Error("should not truncate")
	}
	if truncated != input {
		t.Error("expected unchanged input")
	}
}

func TestStats(t *testing.T) {
	line := Stats(2300*time.Millisecond, 1200, 340)
	ansi := LineToANSI(line, false)
	if !strings.Contains(ansi, "2.3s") {
		t.Errorf("missing duration, got %q", ansi)
	}
	if !strings.Contains(ansi, "1.2k") {
		t.Error("missing input tokens")
	}
}

func TestStatsNoTokens(t *testing.T) {
	line := Stats(5*time.Second, 0, 0)
	ansi := LineToANSI(line, false)
	if !strings.Contains(ansi, "5.0s") {
		t.Error("missing duration")
	}
	if strings.Contains(ansi, "↑") {
		t.Error("should not show tokens when zero")
	}
}

func TestApproval(t *testing.T) {
	line := Approval("apply changes", "foo.go")
	ansi := LineToANSI(line, false)
	if !strings.Contains(ansi, "apply changes?") {
		t.Error("missing action text")
	}
	if !strings.Contains(ansi, "[y]es") {
		t.Error("missing yes option")
	}
}

func TestToolStyle(t *testing.T) {
	if ToolStyle("edit_file") != StyleToolPurple {
		t.Error("edit_file should be purple")
	}
	if ToolStyle("write_file") != StyleToolOrange {
		t.Error("write_file should be orange")
	}
	if ToolStyle("run_command") != StyleToolCyan {
		t.Error("run_command should be cyan")
	}
	if ToolStyle("read_file") != StyleToolBlue {
		t.Error("read_file should be blue")
	}
}
