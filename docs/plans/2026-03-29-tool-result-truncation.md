# Tool Result Truncation Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Truncate large tool results in the message history before sending to the LLM, keeping context small without mutating stored history.

**Architecture:** Add `truncateToolResults(messages []llm.Message, maxLines int) []llm.Message` called inside `BuildMessages()` in `internal/react/prompt.go`. The function copies the slice, replaces oversized `RoleTool` message content with a head+tail summary, and returns the copy. Stored history is never touched.

**Tech Stack:** Go, existing `internal/react` and `internal/llm` packages.

---

## Task 1: Write failing tests for truncateToolResults

**Files:**
- Create: `internal/react/truncate_test.go`

**Step 1: Write the failing tests**

```go
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
```

**Step 2: Run tests to confirm they fail**

```
go test ./internal/react/ -run TestTruncateToolResults -v -count=1
```

Expected: compile error — `truncateToolResults` undefined.

---

## Task 2: Implement truncateToolResults

**Files:**
- Create: `internal/react/truncate.go`

**Step 1: Write minimal implementation**

```go
package react

import (
	"fmt"
	"strings"

	"forge/internal/llm"
)

const (
	toolResultMaxLines  = 40
	toolResultHeadLines = 20
	toolResultTailLines = 10
)

// truncateToolResults returns a copy of messages where any RoleTool message
// whose content exceeds maxLines lines is replaced with a head+tail summary.
// The original slice and its messages are never mutated.
func truncateToolResults(messages []llm.Message, maxLines int) []llm.Message {
	out := make([]llm.Message, len(messages))
	copy(out, messages)
	for i, msg := range out {
		if msg.Role != llm.RoleTool {
			continue
		}
		lines := strings.Split(msg.Content, "\n")
		if len(lines) <= maxLines {
			continue
		}
		head := toolResultHeadLines
		tail := toolResultTailLines
		if head+tail >= len(lines) {
			continue
		}
		omitted := len(lines) - head - tail
		parts := make([]string, 0, head+tail+1)
		parts = append(parts, lines[:head]...)
		parts = append(parts, fmt.Sprintf("... (%d lines truncated)", omitted))
		parts = append(parts, lines[len(lines)-tail:]...)
		out[i].Content = strings.Join(parts, "\n")
	}
	return out
}
```

**Step 2: Run tests to verify they pass**

```
go test ./internal/react/ -run TestTruncateToolResults -v -count=1
```

Expected: all 6 tests PASS.

**Step 3: Commit**

```bash
git add internal/react/truncate.go internal/react/truncate_test.go
git commit -m "react: add truncateToolResults — head+tail truncation for large tool results"
```

---

## Task 3: Wire truncation into BuildMessages

**Files:**
- Modify: `internal/react/prompt.go:42-43` (return statement)
- Modify: `internal/react/prompt_test.go` (add integration test)

**Step 1: Write the failing integration test**

Add to `internal/react/prompt_test.go` (create the file if it does not exist — check first with `ls internal/react/prompt_test.go`):

```go
func TestBuildMessages_LargeToolResultTruncated(t *testing.T) {
	lines := make([]string, 100)
	for i := range lines {
		lines[i] = fmt.Sprintf("output line %d", i+1)
	}
	bigResult := strings.Join(lines, "\n")

	snap := SessionSnapshot{
		History: []llm.Message{
			{Role: llm.RoleUser, Content: "run something"},
			{Role: llm.RoleAssistant, ToolCalls: []llm.NativeToolCall{{ID: "c1", Name: "run_command", ArgsJSON: `{}`}}},
			{Role: llm.RoleTool, ToolCallID: "c1", Content: bigResult},
		},
	}
	msgs := BuildMessages("sys", snap)

	var toolMsg *llm.Message
	for i := range msgs {
		if msgs[i].Role == llm.RoleTool {
			toolMsg = &msgs[i]
			break
		}
	}
	if toolMsg == nil {
		t.Fatal("no tool message in output")
	}
	if strings.Contains(toolMsg.Content, "output line 50") {
		t.Error("middle lines should be truncated from LLM context")
	}
	if !strings.Contains(toolMsg.Content, "truncated") {
		t.Error("truncation marker should be present")
	}
	// Original snapshot must be untouched
	if !strings.Contains(snap.History[2].Content, "output line 50") {
		t.Error("original snapshot history must not be mutated")
	}
}
```

**Step 2: Run test to confirm it fails**

```
go test ./internal/react/ -run TestBuildMessages_LargeToolResultTruncated -v -count=1
```

Expected: FAIL — tool message still contains "output line 50".

**Step 3: Wire truncation into BuildMessages**

In `internal/react/prompt.go`, replace the final `return messages` with:

```go
return truncateToolResults(messages, toolResultMaxLines)
```

The full updated function (lines 16–44):

```go
func BuildMessages(systemPrompt string, snapshot SessionSnapshot) []llm.Message {
	var messages []llm.Message

	systemPrompt = strings.TrimSpace(systemPrompt)
	if systemPrompt != "" {
		messages = append(messages, llm.Message{Role: llm.RoleSystem, Content: systemPrompt})
	}

	if summary := compactionContext(snapshot); summary != "" {
		messages = append(messages, llm.Message{Role: llm.RoleSystem, Content: summary})
	}

	for _, msg := range snapshot.History {
		if msg.Role == llm.RoleTool || len(msg.ToolCalls) > 0 {
			messages = append(messages, msg)
			continue
		}
		content := strings.TrimSpace(msg.Content)
		if content == "" {
			continue
		}
		messages = append(messages, llm.Message{Role: msg.Role, Content: content})
	}

	return truncateToolResults(messages, toolResultMaxLines)
}
```

**Step 4: Run the integration test**

```
go test ./internal/react/ -run TestBuildMessages_LargeToolResultTruncated -v -count=1
```

Expected: PASS.

**Step 5: Run all react tests**

```
go test ./internal/react/ -v -count=1 -timeout 60s
```

Expected: all PASS, no regressions.

**Step 6: Commit**

```bash
git add internal/react/prompt.go internal/react/prompt_test.go
git commit -m "react: truncate large tool results in BuildMessages before LLM dispatch"
```

---

## Task 4: Full test suite + verification

**Step 1: Run full suite**

```
go test ./... -count=1 -timeout 120s
```

Expected: all packages PASS.

**Step 2: Commit if any fixups were needed, then push**

```bash
git push
```
