package react

import (
	"fmt"
	"strings"
	"testing"

	"forge/internal/llm"
)

func TestTruncateToolResults_ShortResultUnchanged(t *testing.T) {
	msgs := []llm.Message{
		{Role: llm.RoleTool, ToolCallID: "1", Content: "line1\nline2\nline3"},
	}
	got := truncateToolResults(msgs, 40)
	if got[0].Content != msgs[0].Content {
		t.Errorf("short result should be unchanged, got %q", got[0].Content)
	}
}

func TestTruncateToolResults_LongResultTruncated(t *testing.T) {
	lines := make([]string, 100)
	for i := range lines {
		lines[i] = fmt.Sprintf("line %d", i+1)
	}
	content := strings.Join(lines, "\n")
	msgs := []llm.Message{
		{Role: llm.RoleTool, ToolCallID: "1", Content: content},
	}
	got := truncateToolResults(msgs, 40)
	result := got[0].Content
	if strings.Contains(result, "line 50") {
		t.Error("middle lines should be dropped")
	}
	if !strings.Contains(result, "line 1") {
		t.Error("head lines should be present")
	}
	if !strings.Contains(result, "line 100") {
		t.Error("tail lines should be present")
	}
	if !strings.Contains(result, "truncated") {
		t.Error("truncation marker should be present")
	}
}

func TestTruncateToolResults_NonToolMessageUnchanged(t *testing.T) {
	long := strings.Repeat("x\n", 200)
	msgs := []llm.Message{
		{Role: llm.RoleAssistant, Content: long},
		{Role: llm.RoleUser, Content: long},
	}
	got := truncateToolResults(msgs, 40)
	for i, m := range got {
		if m.Content != msgs[i].Content {
			t.Errorf("msg[%d] role=%s should be unchanged", i, m.Role)
		}
	}
}

func TestTruncateToolResults_DoesNotMutateOriginal(t *testing.T) {
	lines := make([]string, 100)
	for i := range lines {
		lines[i] = fmt.Sprintf("line %d", i+1)
	}
	original := strings.Join(lines, "\n")
	msgs := []llm.Message{
		{Role: llm.RoleTool, ToolCallID: "1", Content: original},
	}
	_ = truncateToolResults(msgs, 40)
	if msgs[0].Content != original {
		t.Error("original slice must not be mutated")
	}
}

func TestTruncateToolResults_ToolCallIDPreserved(t *testing.T) {
	lines := make([]string, 100)
	for i := range lines {
		lines[i] = fmt.Sprintf("line %d", i+1)
	}
	msgs := []llm.Message{
		{Role: llm.RoleTool, ToolCallID: "abc-123", Content: strings.Join(lines, "\n")},
	}
	got := truncateToolResults(msgs, 40)
	if got[0].ToolCallID != "abc-123" {
		t.Errorf("ToolCallID must be preserved, got %q", got[0].ToolCallID)
	}
}

func TestTruncateToolResults_ExactlyAtLimitUnchanged(t *testing.T) {
	lines := make([]string, 40)
	for i := range lines {
		lines[i] = fmt.Sprintf("line %d", i+1)
	}
	content := strings.Join(lines, "\n")
	msgs := []llm.Message{
		{Role: llm.RoleTool, ToolCallID: "1", Content: content},
	}
	got := truncateToolResults(msgs, 40)
	if got[0].Content != content {
		t.Errorf("result at exactly the limit should be unchanged")
	}
}
