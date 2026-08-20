package react

import (
	"encoding/json"
	"fmt"
	"strings"

	"forge/internal/llm"
)

const (
	// toolResultMaxLines is the line budget passed to truncateToolResults in BuildMessages.
	// Results at or below this threshold are sent to the LLM unmodified.
	toolResultMaxLines = 200
	// Keep these limits generous: every truncated arg puts an "<omitted N
	// chars>" marker into the model's view of its own past calls, and weaker
	// models copy the marker back into new calls.
	toolCallArgSoftStringLimit = 1200
	toolCallArgHardStringLimit = 4000
)

// authoredContentTools carry content the model composed itself. Truncating
// their arguments puts an "<omitted N chars>" marker into the model's view of
// its own past calls, and validation rejects any new call carrying that
// marker, so a file large enough to be truncated became one the model could no
// longer rewrite by reusing its earlier call. Span compaction already bounds
// what old calls cost, so truncating here buys little and traps the model.
// artifact_write is deliberately absent: artifacts are write-once outputs
// that are rarely reused as a template, and they carry the largest payloads,
// so the context saving is worth more there than the risk.
var authoredContentTools = map[string]struct{}{
	"write_file":  {},
	"edit_file":   {},
	"apply_patch": {},
}

var bulkyToolArgKeys = map[string]struct{}{
	"body":        {},
	"content":     {},
	"data":        {},
	"diff":        {},
	"html":        {},
	"input":       {},
	"markdown":    {},
	"new_text":    {},
	"old_text":    {},
	"patch":       {},
	"payload":     {},
	"prompt":      {},
	"replacement": {},
	"script":      {},
	"text":        {},
}

// truncateToolResults returns a copy of messages where any RoleTool message
// whose content exceeds maxLines lines is replaced with a head+tail summary.
// Trailing tool messages (the current turn's freshest results) are exempt so
// the model always sees the full output of what it just ran; older results
// are clipped with a marker that explains how to recover the omitted range.
// The returned slice is a new allocation. String fields (Content, ToolCallID)
// on modified messages are replaced with new values. Reference fields
// (ToolCalls) are shallow-copied.
func truncateToolResults(messages []llm.Message, maxLines int) []llm.Message {
	out := make([]llm.Message, len(messages))
	copy(out, messages)
	// Freshest batch: contiguous RoleTool messages at the end of the sequence.
	exemptFrom := len(out)
	for exemptFrom > 0 && out[exemptFrom-1].Role == llm.RoleTool {
		exemptFrom--
	}
	for i, msg := range out {
		if msg.Role != llm.RoleTool || i >= exemptFrom {
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
		parts = append(parts, fmt.Sprintf("... (%d lines omitted from this older result: lines %d-%d. Re-running the identical call returns the same clipped view; to see the omitted range, read the specific line range with read_file start_line/end_line or narrow the command.)", omitted, head+1, len(lines)-tail))
		parts = append(parts, lines[len(lines)-tail:]...)
		out[i].Content = strings.Join(parts, "\n")
	}
	return out
}

func truncateAssistantToolCalls(messages []llm.Message) []llm.Message {
	var out []llm.Message
	for i, msg := range messages {
		if len(msg.ToolCalls) == 0 {
			continue
		}
		truncatedCalls, changed := truncateNativeToolCalls(msg.ToolCalls)
		if !changed {
			continue
		}
		if out == nil {
			out = make([]llm.Message, len(messages))
			copy(out, messages)
		}
		out[i].ToolCalls = truncatedCalls
	}
	if out == nil {
		return messages
	}
	return out
}

func truncateNativeToolCalls(calls []llm.NativeToolCall) ([]llm.NativeToolCall, bool) {
	out := make([]llm.NativeToolCall, len(calls))
	copy(out, calls)
	changed := false
	for i, call := range out {
		if _, ok := authoredContentTools[strings.ToLower(strings.TrimSpace(call.Name))]; ok {
			continue
		}
		argsJSON, truncated := truncateToolCallArgsJSON(call.ArgsJSON)
		if !truncated {
			continue
		}
		out[i].ArgsJSON = argsJSON
		changed = true
	}
	if !changed {
		return calls, false
	}
	return out, true
}

func truncateToolCallArgsJSON(raw string) (string, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return raw, false
	}
	var payload any
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		if len(raw) <= toolCallArgHardStringLimit {
			return raw, false
		}
		return fmt.Sprintf(`{"_truncated_args":"%d chars omitted"}`, len(raw)), true
	}
	truncated, changed := truncateToolCallArgValue("", payload)
	if !changed {
		return raw, false
	}
	encoded, err := json.Marshal(truncated)
	if err != nil {
		return raw, false
	}
	return string(encoded), true
}

func truncateToolCallArgValue(key string, value any) (any, bool) {
	switch typed := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		changed := false
		for childKey, childValue := range typed {
			next, childChanged := truncateToolCallArgValue(childKey, childValue)
			out[childKey] = next
			changed = changed || childChanged
		}
		return out, changed
	case []any:
		out := make([]any, len(typed))
		changed := false
		for i, childValue := range typed {
			next, childChanged := truncateToolCallArgValue(key, childValue)
			out[i] = next
			changed = changed || childChanged
		}
		return out, changed
	case string:
		if !shouldTruncateToolCallString(key, typed) {
			return typed, false
		}
		return fmt.Sprintf("<omitted %d chars>", len(typed)), true
	default:
		return value, false
	}
}

func shouldTruncateToolCallString(key, value string) bool {
	if len(value) > toolCallArgHardStringLimit {
		return true
	}
	if len(value) <= toolCallArgSoftStringLimit {
		return false
	}
	if _, ok := bulkyToolArgKeys[strings.ToLower(strings.TrimSpace(key))]; ok {
		return true
	}
	return strings.Count(value, "\n") >= 5
}
