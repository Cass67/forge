package react

import (
	"fmt"
	"strings"

	"forge/internal/llm"
)

const (
	// toolResultMaxLines is the line budget passed to truncateToolResults in BuildMessages.
	// Results at or below this threshold are sent to the LLM unmodified.
	toolResultMaxLines = 40
)

// truncateToolResults returns a copy of messages where any RoleTool message
// whose content exceeds maxLines lines is replaced with a head+tail summary.
// The returned slice is a new allocation. String fields (Content, ToolCallID)
// on modified messages are replaced with new values. Reference fields
// (ToolCalls) are shallow-copied.
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
		// Allocate 2/3 of the budget to head, 1/3 to tail.
		head := maxLines * 2 / 3
		tail := maxLines - head
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
