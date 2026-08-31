package react

import (
	"encoding/json"
	"fmt"
	"strings"

	"forge/internal/protocol"
)

const (
	compactedToolMetadataMaxBytes     = 240
	compactedToolMetadataAggregateMax = 480
	// microCompactProtectedTail keeps the most recent messages out of micro
	// compaction so the model does not lose tool results it is actively using
	// (losing them forces re-reads, which look like tool-call loops).
	//
	// A step issues several tool calls, so a tail of 8 covered barely two
	// steps: in one observed session three of seven compacted results were
	// re-read straight afterwards, all files being actively edited. Sized to
	// hold the last few steps instead.
	microCompactProtectedTail = 24
)

// protectedTail bounds how much of a history the tail may shield. The tail
// exists to protect the working set, which cannot be the whole conversation:
// a fixed count larger than the history protected everything, so a stale
// oversized result in a short session could never be compacted.
func protectedTail(historyLen, want int) int {
	if want < 1 || historyLen < 2 {
		return 0
	}
	if half := historyLen / 2; want > half {
		return half
	}
	return want
}

const ()

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
	live := session.liveItemsLocked()
	protectTail = protectedTail(len(live), protectTail)
	protectedFrom := len(live) - protectTail
	var replacements []protocol.CompactionReplacement
	for i, item := range live {
		if protectTail > 0 && i >= protectedFrom {
			break
		}
		if item.Kind != protocol.ItemToolResult || item.ToolResult == nil {
			continue
		}
		content := item.ToolResult.Text
		if len(content) <= maxBytes {
			continue
		}
		summary := compactToolResultSummary(content)
		if len(summary) >= len(content) || len(summary) > maxBytes {
			continue
		}
		replacements = append(replacements, protocol.CompactionReplacement{Ref: item.Ref, Text: summary})
	}
	if len(replacements) == 0 {
		session.mu.Unlock()
		return false
	}
	item := session.appendCompactionLocked(nil, replacements)
	sink := session.durableSink
	session.mu.Unlock()
	_ = session.persistDurableItem(item, sink)
	return true
}

func compactToolResultSummary(content string) string {
	parts := []string{
		"[tool result compacted]",
		fmt.Sprintf("Original size: %d bytes.", len(content)),
	}
	if handle := compactedToolHandleMetadata(content); handle != "" {
		// The content is still retrievable, so say so: a handle on its own
		// reads as a dead end and models abandon the task rather than fetch it.
		parts = append(parts, handle,
			"The full output is still stored. Read it with read_output using the handle above, paging with offset and limit.")
	} else {
		parts = append(parts, "Oversized tool output was dropped to keep context bounded. Re-run the call if you need it again.")
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
	// A truncated handle is useless: read_output rejects it and the model
	// cannot reconstruct the elided middle. Drop the other fields instead.
	if token := handleToken(line); token != "" {
		return "Handle: " + token
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

// handleToken pulls the handle value out of a metadata line such as
// `Handle: session/<sha>. Size: 1234.` or a JSON `"handle": "session/<sha>"`.
func handleToken(line string) string {
	lower := strings.ToLower(line)
	idx := strings.Index(lower, "handle")
	if idx < 0 {
		return ""
	}
	rest := strings.TrimLeft(line[idx+len("handle"):], "\"': \t")
	if end := strings.IndexAny(rest, " \t\","); end >= 0 {
		rest = rest[:end]
	}
	return strings.TrimRight(strings.TrimSpace(rest), ".")
}
