package agent

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"forge/internal/llm"
)

func TestRendererAgentText(t *testing.T) {
	var buf bytes.Buffer
	r := NewRenderer(&buf, 80, true)
	r.AgentText("hello world\n")
	if !strings.Contains(buf.String(), "hello world") {
		t.Errorf("output = %q", buf.String())
	}
	if !strings.Contains(buf.String(), "│") {
		t.Error("missing accent border")
	}
}

func TestRendererToolResult(t *testing.T) {
	var buf bytes.Buffer
	r := NewRenderer(&buf, 80, true)
	r.ToolResult("read_file", "main.go (128 lines)", "", false)
	out := buf.String()
	if !strings.Contains(out, "read_file") {
		t.Errorf("missing tool name in: %q", out)
	}
	if !strings.Contains(out, "┌") {
		t.Error("missing box border")
	}
}

func TestRendererToolResultWithDiff(t *testing.T) {
	var buf bytes.Buffer
	r := NewRenderer(&buf, 80, true)
	r.ToolResult("edit_file", "edited main.go", "-old line\n+new line\n", false)
	out := buf.String()
	if !strings.Contains(out, "old line") || !strings.Contains(out, "new line") {
		t.Errorf("diff not rendered: %q", out)
	}
}

func TestRendererStats(t *testing.T) {
	var buf bytes.Buffer
	r := NewRenderer(&buf, 80, true)
	r.Stats(2300*time.Millisecond, llm.Usage{InputTokens: 1200, OutputTokens: 340})
	out := buf.String()
	if !strings.Contains(out, "2.3s") {
		t.Errorf("missing duration: %q", out)
	}
}

func TestRendererNoColor(t *testing.T) {
	var buf bytes.Buffer
	r := NewRenderer(&buf, 80, false)
	r.AgentText("plain text\n")
	out := buf.String()
	if strings.Contains(out, "\033[") {
		t.Error("should not contain ANSI codes when colors disabled")
	}
}

func TestRendererExpand(t *testing.T) {
	var buf bytes.Buffer
	r := NewRenderer(&buf, 80, false)
	// Create a long diff that will be truncated
	var longDiff strings.Builder
	for i := 0; i < 30; i++ {
		longDiff.WriteString("+line " + string(rune('A'+i)) + "\n")
	}
	r.ToolResult("edit_file", "edited main.go", longDiff.String(), false)
	if r.LastExpandable() == "" {
		t.Error("expected lastExpandable to be set for long diff")
	}
	r.ClearExpandable()
	if r.LastExpandable() != "" {
		t.Error("expected lastExpandable to be cleared")
	}
}
