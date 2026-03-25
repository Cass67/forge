package agent

import (
	"encoding/json"
	"strings"
)

type ToolCall struct {
	Name string
	Args map[string]any
}

// toolCallOpeners are XML-style tags the LLM may use to wrap tool calls.
// We handle both our instructed format (<tool_call>) and formats that leak
// through from the model's training (<function_calls>, <tool_calls>).
var toolCallOpeners = []string{"<tool_call>", "<function_calls>", "<tool_calls>"}
var toolCallClosers = []string{"</tool_call>", "</function_calls>", "</tool_calls>"}

type inlineToolCallParse struct {
	calls   []ToolCall
	visible string
	ok      bool
}

func isToolCallOpen(line string) (after string, ok bool) {
	for _, tag := range toolCallOpeners {
		if strings.Contains(line, tag) {
			parts := strings.SplitN(line, tag, 2)
			return strings.TrimSpace(parts[1]), true
		}
	}
	return "", false
}

func isToolCallClose(line string) (before string, ok bool) {
	for _, tag := range toolCallClosers {
		if strings.Contains(line, tag) {
			parts := strings.SplitN(line, tag, 2)
			return strings.TrimSpace(parts[0]), true
		}
	}
	return "", false
}

func ParseToolCalls(text string) ([]ToolCall, string) {
	var calls []ToolCall
	var textParts []string

	lines := strings.Split(text, "\n")
	i := 0
	inCodeFence := false

	for i < len(lines) {
		line := lines[i]

		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			if inCodeFence {
				inCodeFence = false
				textParts = append(textParts, line)
				i++
				continue
			}
			inCodeFence = true
			textParts = append(textParts, line)
			i++
			continue
		}

		if inCodeFence {
			textParts = append(textParts, line)
			i++
			continue
		}

		if inline := parseInlineToolCallsLine(line); inline.ok {
			calls = append(calls, inline.calls...)
			if inline.visible != "" {
				textParts = append(textParts, inline.visible)
			}
			i++
			continue
		}

		lineTrimmed := strings.TrimSpace(line)
		if after, ok := isToolCallOpen(lineTrimmed); ok {
			i++
			var block strings.Builder
			if after != "" {
				block.WriteString(after)
				block.WriteByte('\n')
			}
			for i < len(lines) {
				lt := strings.TrimSpace(lines[i])
				if _, ok := isToolCallClose(lt); ok {
					before, _ := isToolCallClose(lt)
					if before != "" {
						block.WriteString(before)
						block.WriteByte('\n')
					}
					i++
					break
				}
				block.WriteString(lines[i])
				block.WriteByte('\n')
				i++
			}

			parsed := parseBlock(block.String())
			calls = append(calls, parsed...)
			continue
		}
		if call, remainder, ok := parseLooseToolCallLine(lineTrimmed); ok {
			calls = append(calls, call)
			if remainder != "" {
				textParts = append(textParts, remainder)
			}
			i++
			continue
		}

		textParts = append(textParts, line)
		i++
	}

	return calls, strings.Join(textParts, "\n")
}

func parseInlineToolCallsLine(line string) inlineToolCallParse {
	remaining := line
	var calls []ToolCall
	var visible strings.Builder
	parsedAny := false

	for {
		startIdx, openerIdx := nextToolCallOpener(remaining)
		if openerIdx < 0 {
			if parsedAny {
				visible.WriteString(remaining)
				return inlineToolCallParse{calls: calls, visible: visible.String(), ok: true}
			}
			return inlineToolCallParse{}
		}
		opener := toolCallOpeners[openerIdx]
		closer := toolCallClosers[openerIdx]
		openerStart := startIdx
		openerEnd := openerStart + len(opener)
		closeRel := strings.Index(remaining[openerEnd:], closer)
		if closeRel < 0 {
			return inlineToolCallParse{}
		}
		closeStart := openerEnd + closeRel
		closeEnd := closeStart + len(closer)

		visible.WriteString(remaining[:openerStart])
		raw := strings.TrimSpace(remaining[openerEnd:closeStart])
		calls = append(calls, parseBlock(raw)...)
		remaining = remaining[closeEnd:]
		parsedAny = true
	}
}

func nextToolCallOpener(line string) (int, int) {
	bestPos := -1
	bestIdx := -1
	for i, opener := range toolCallOpeners {
		pos := strings.Index(line, opener)
		if pos < 0 {
			continue
		}
		if bestPos < 0 || pos < bestPos {
			bestPos = pos
			bestIdx = i
		}
	}
	return bestPos, bestIdx
}

func parseLooseToolCallLine(line string) (ToolCall, string, bool) {
	line = strings.TrimSpace(line)
	if line == "" {
		return ToolCall{}, "", false
	}
	for _, tag := range toolCallClosers {
		if strings.Contains(line, tag) {
			line = strings.TrimSpace(strings.ReplaceAll(line, tag, ""))
		}
	}
	for _, tag := range toolCallOpeners {
		if strings.Contains(line, tag) {
			line = strings.TrimSpace(strings.ReplaceAll(line, tag, ""))
		}
	}
	if line == "" {
		return ToolCall{}, "", false
	}
	call := parseCallJSON(line)
	if call.Name == "" {
		call, remainder, ok := parseLeadingToolCallJSON(line)
		if !ok {
			return ToolCall{}, "", false
		}
		return call, remainder, true
	}
	return call, "", true
}

func parseLeadingToolCallJSON(line string) (ToolCall, string, bool) {
	if !strings.HasPrefix(strings.TrimSpace(line), "{") {
		return ToolCall{}, "", false
	}
	var parsed struct {
		Name string         `json:"name"`
		Args map[string]any `json:"args"`
	}
	dec := json.NewDecoder(strings.NewReader(line))
	if err := dec.Decode(&parsed); err != nil {
		return ToolCall{}, "", false
	}
	if strings.TrimSpace(parsed.Name) == "" {
		return ToolCall{}, "", false
	}
	if parsed.Args == nil {
		parsed.Args = make(map[string]any)
	}
	offset := int(dec.InputOffset())
	if offset < 0 || offset > len(line) {
		offset = len(line)
	}
	return ToolCall{Name: parsed.Name, Args: parsed.Args}, strings.TrimSpace(line[offset:]), true
}

// parseBlock tries to parse the inner content of a tool call block.
// Supports single JSON object, JSON array, and <invoke> XML format.
func parseBlock(raw string) []ToolCall {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}

	// Try single JSON object: {"name": "...", "args": {...}}
	if strings.HasPrefix(raw, "{") {
		if call := parseCallJSON(raw); call.Name != "" {
			return []ToolCall{call}
		}
	}

	// Try JSON array: [{"name": "...", "args": {...}}, ...]
	if strings.HasPrefix(raw, "[") {
		var arr []json.RawMessage
		if err := json.Unmarshal([]byte(raw), &arr); err == nil {
			var calls []ToolCall
			for _, item := range arr {
				if call := parseCallJSON(string(item)); call.Name != "" {
					calls = append(calls, call)
				}
			}
			if len(calls) > 0 {
				return calls
			}
		}
	}

	// Try <invoke> XML format:
	//   <invoke name="tool_name">
	//   <parameter name="key">value</parameter>
	//   </invoke>
	if strings.Contains(raw, "<invoke") {
		return parseInvokeXML(raw)
	}

	return nil
}

func parseCallJSON(raw string) ToolCall {
	raw = strings.TrimSpace(raw)
	var parsed struct {
		Name string         `json:"name"`
		Args map[string]any `json:"args"`
	}
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		return ToolCall{}
	}
	if parsed.Args == nil {
		parsed.Args = make(map[string]any)
	}
	return ToolCall{Name: parsed.Name, Args: parsed.Args}
}

func parseInvokeXML(raw string) []ToolCall {
	var calls []ToolCall
	remaining := raw

	for {
		// Find <invoke name="...">
		idx := strings.Index(remaining, "<invoke")
		if idx < 0 {
			break
		}
		remaining = remaining[idx:]

		// Extract tool name from name="..."
		nameStart := strings.Index(remaining, `name="`)
		if nameStart < 0 {
			break
		}
		nameStart += len(`name="`)
		nameEnd := strings.Index(remaining[nameStart:], `"`)
		if nameEnd < 0 {
			break
		}
		toolName := remaining[nameStart : nameStart+nameEnd]

		// Find closing </invoke>
		closeIdx := strings.Index(remaining, "</invoke>")
		if closeIdx < 0 {
			break
		}

		// Extract parameters between > and </invoke>
		bodyStart := strings.Index(remaining, ">")
		if bodyStart < 0 || bodyStart >= closeIdx {
			break
		}
		body := remaining[bodyStart+1 : closeIdx]

		args := parseInvokeParams(body)
		if toolName != "" {
			calls = append(calls, ToolCall{Name: toolName, Args: args})
		}

		remaining = remaining[closeIdx+len("</invoke>"):]
	}

	return calls
}

func parseInvokeParams(body string) map[string]any {
	args := make(map[string]any)
	remaining := body

	for {
		idx := strings.Index(remaining, `<parameter name="`)
		if idx < 0 {
			break
		}
		remaining = remaining[idx+len(`<parameter name="`):]

		nameEnd := strings.Index(remaining, `"`)
		if nameEnd < 0 {
			break
		}
		paramName := remaining[:nameEnd]
		remaining = remaining[nameEnd:]

		// Skip past the >
		gt := strings.Index(remaining, ">")
		if gt < 0 {
			break
		}
		remaining = remaining[gt+1:]

		// Find </parameter>
		closeIdx := strings.Index(remaining, "</parameter>")
		if closeIdx < 0 {
			break
		}
		paramValue := remaining[:closeIdx]
		args[paramName] = paramValue

		remaining = remaining[closeIdx+len("</parameter>"):]
	}

	return args
}
