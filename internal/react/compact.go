package react

import (
	"encoding/json"
	"fmt"
	"strings"

	"forge/internal/llm"
)

const (
	compactedToolMetadataMaxBytes     = 240
	compactedToolMetadataAggregateMax = 480
	// microCompactProtectedTail keeps the most recent messages out of micro
	// compaction so the model does not lose tool results it is actively using
	// (losing them forces re-reads, which look like tool-call loops).
	microCompactProtectedTail = 8
)

func CompactSessionHistory(session *Session, keep int) bool {
	if session == nil {
		return false
	}
	return session.compact(keep)
}

func MicroCompactLargeToolResults(session *Session, maxBytes, protectTail int) bool {
	if session == nil || maxBytes < 1 {
		return false
	}
	session.mu.Lock()
	defer session.mu.Unlock()
	changed := false
	protectedFrom := len(session.history) - protectTail
	for i, msg := range session.history {
		if protectTail > 0 && i >= protectedFrom {
			break
		}
		if msg.Role != llm.RoleTool || len(msg.Content) <= maxBytes {
			continue
		}
		summary := compactToolResultSummary(msg.Content)
		if len(summary) >= len(msg.Content) || len(summary) > maxBytes {
			continue
		}
		session.history[i].Content = summary
		changed = true
	}
	return changed
}

func compactToolResultSummary(content string) string {
	parts := []string{
		"[tool result compacted]",
		fmt.Sprintf("Original size: %d bytes.", len(content)),
	}
	if handle := compactedToolHandleMetadata(content); handle != "" {
		parts = append(parts, handle)
	} else {
		parts = append(parts, "Oversized tool output omitted to keep context bounded.")
	}
	return strings.Join(parts, "\n")
}

func compactedToolHandleMetadata(content string) string {
	if metadata := compactedToolJSONHandleMetadata(content); metadata != "" {
		return metadata
	}
	var lines []string
	omitted := 0
	used := 0
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		lower := strings.ToLower(line)
		if strings.Contains(lower, "handle:") || strings.Contains(lower, `"handle"`) {
			snippet := compactedToolMetadataSnippet(line)
			cost := len(snippet)
			if len(lines) > 0 {
				cost++
			}
			if used+cost > compactedToolMetadataAggregateMax {
				omitted++
				continue
			}
			lines = append(lines, snippet)
			used += cost
		}
	}
	if omitted > 0 {
		lines = append(lines, fmt.Sprintf("... (%d metadata lines omitted)", omitted))
	}
	return strings.Join(lines, "\n")
}

func compactedToolJSONHandleMetadata(content string) string {
	var payload map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(content)), &payload); err != nil {
		return ""
	}
	handle, ok := payload["handle"].(string)
	if !ok || strings.TrimSpace(handle) == "" {
		return ""
	}
	parts := []string{"Handle: " + strings.TrimSpace(handle)}
	if size, ok := payload["size"]; ok {
		parts = append(parts, fmt.Sprintf("Size: %v", size))
	}
	if sha, ok := payload["sha256"].(string); ok && strings.TrimSpace(sha) != "" {
		parts = append(parts, "SHA256: "+strings.TrimSpace(sha))
	}
	return compactedToolMetadataSnippet(strings.Join(parts, ". "))
}

func compactedToolMetadataSnippet(line string) string {
	if len(line) <= compactedToolMetadataMaxBytes {
		return line
	}
	lower := strings.ToLower(line)
	idx := strings.Index(lower, "handle")
	if idx < 0 {
		idx = 0
	}
	start := idx - compactedToolMetadataMaxBytes/4
	if start < 0 {
		start = 0
	}
	end := start + compactedToolMetadataMaxBytes
	if end > len(line) {
		end = len(line)
		start = end - compactedToolMetadataMaxBytes
		if start < 0 {
			start = 0
		}
	}
	snippet := strings.TrimSpace(line[start:end])
	if start > 0 {
		snippet = "..." + snippet
	}
	if end < len(line) {
		snippet += "..."
	}
	return snippet
}
