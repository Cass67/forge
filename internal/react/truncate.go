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
