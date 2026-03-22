package agent

import (
	"encoding/json"
	"strings"
)

type ToolCall struct {
	Name string
	Args map[string]any
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

		lineTrimmed := strings.TrimSpace(line)
		if strings.Contains(lineTrimmed, "<tool_call>") {
			// Handle <tool_call> with JSON on the same line
			after := strings.SplitN(lineTrimmed, "<tool_call>", 2)[1]
			after = strings.TrimSpace(after)
			i++
			var block strings.Builder
			if after != "" {
				block.WriteString(after)
				block.WriteByte('\n')
			}
			for i < len(lines) {
				lt := strings.TrimSpace(lines[i])
				if strings.Contains(lt, "</tool_call>") {
					before := strings.SplitN(lt, "</tool_call>", 2)[0]
					before = strings.TrimSpace(before)
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

			call := parseCallJSON(block.String())
			if call.Name != "" {
				calls = append(calls, call)
			}
			continue
		}

		textParts = append(textParts, line)
		i++
	}

	return calls, strings.Join(textParts, "\n")
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
